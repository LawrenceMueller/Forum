package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// VoteTarget is what's being voted on.
type VoteTarget int

const (
	VotePost VoteTarget = iota
	VoteComment
)

var (
	ErrVoteNotFound = errors.New("nothing to vote on")
	ErrVoteLocked   = errors.New("removed or deleted content can't be voted on")
	ErrOwnContent   = errors.New("you can't vote on your own posts or comments")
)

// voteSQL holds the statements for one target. They're fixed strings,
// chosen by the target, never built from input.
type voteSQL struct {
	lock, current, insert, update, remove, count, mine string
}

var voteStatements = map[VoteTarget]voteSQL{
	VotePost: {
		lock: `SELECT id, author_id, removed_at IS NOT NULL OR deleted_at IS NOT NULL
		       FROM posts WHERE public_id = $1 FOR UPDATE`,
		current: `SELECT value FROM post_votes WHERE voter = $1 AND post_id = $2`,
		insert:  `INSERT INTO post_votes (voter, post_id, value) VALUES ($1, $2, $3)`,
		update:  `UPDATE post_votes SET value = $3 WHERE voter = $1 AND post_id = $2`,
		remove:  `DELETE FROM post_votes WHERE voter = $1 AND post_id = $2`,
		count: `UPDATE posts SET upvotes = upvotes + $1, downvotes = downvotes + $2,
		                         score = score + $1 - $2
		        WHERE id = $3 RETURNING score`,
		mine: `SELECT post_id, value FROM post_votes WHERE voter = $1 AND post_id = ANY($2)`,
	},
	VoteComment: {
		// A comment is closed to votes if it, or the post it's on, is gone.
		// FOR UPDATE OF c locks only the comment row.
		lock: `SELECT c.id, c.author_id,
		              c.removed_at IS NOT NULL OR c.deleted_at IS NOT NULL
		              OR p.removed_at IS NOT NULL OR p.deleted_at IS NOT NULL
		       FROM comments c JOIN posts p ON p.id = c.post_id
		       WHERE c.public_id = $1 FOR UPDATE OF c`,
		current: `SELECT value FROM comment_votes WHERE voter = $1 AND comment_id = $2`,
		insert:  `INSERT INTO comment_votes (voter, comment_id, value) VALUES ($1, $2, $3)`,
		update:  `UPDATE comment_votes SET value = $3 WHERE voter = $1 AND comment_id = $2`,
		remove:  `DELETE FROM comment_votes WHERE voter = $1 AND comment_id = $2`,
		count: `UPDATE comments SET upvotes = upvotes + $1, downvotes = downvotes + $2
		        WHERE id = $3 RETURNING upvotes - downvotes`,
		mine: `SELECT comment_id, value FROM comment_votes WHERE voter = $1 AND comment_id = ANY($2)`,
	},
}

// voteChange works out a click. Clicking the arrow you already chose takes
// the vote back; clicking the other one switches. It returns the new vote
// (1, -1, or 0 for none) and how the up and down counts change.
func voteChange(old, dir int) (next, upDelta, downDelta int) {
	next = dir
	if old == dir {
		next = 0
	}
	is := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	upDelta = is(next == 1) - is(old == 1)
	downDelta = is(next == -1) - is(old == -1)
	return next, upDelta, downDelta
}

// Vote records a click on an arrow (dir is 1 for up, -1 for down) and
// returns the item's new score and the voter's resulting vote.
//
// voter is the account's fingerprint (auth.VoterKeys); voterID is used only
// to stop people voting on their own content, and is never stored.
func (s *Store) Vote(ctx context.Context, t VoteTarget, publicID, voter string, voterID int64, dir int) (score, mine int, err error) {
	if dir != 1 && dir != -1 {
		return 0, 0, errors.New("a vote is up or down")
	}
	q := voteStatements[t]

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)

	// Locking the item makes votes on it take turns, so two clicks at the
	// same moment can't both read the old counts and lose an update.
	var itemID, authorID int64
	var locked bool
	err = tx.QueryRow(ctx, q.lock, publicID).Scan(&itemID, &authorID, &locked)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, 0, ErrVoteNotFound
	case err != nil:
		return 0, 0, err
	case locked:
		return 0, 0, ErrVoteLocked
	case authorID == voterID:
		return 0, 0, ErrOwnContent
	}

	var old int
	err = tx.QueryRow(ctx, q.current, voter, itemID).Scan(&old)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, err
	}

	next, up, down := voteChange(old, dir)
	switch {
	case next == 0:
		_, err = tx.Exec(ctx, q.remove, voter, itemID)
	case old == 0:
		_, err = tx.Exec(ctx, q.insert, voter, itemID, next)
	default:
		_, err = tx.Exec(ctx, q.update, voter, itemID, next)
	}
	if err != nil {
		return 0, 0, err
	}

	if err := tx.QueryRow(ctx, q.count, up, down, itemID).Scan(&score); err != nil {
		return 0, 0, err
	}
	return score, next, tx.Commit(ctx)
}

// MyVotes returns one voter's votes on a set of items (by internal ID),
// so a page can highlight the arrows they've already clicked. Items they
// haven't voted on are simply absent.
func (s *Store) MyVotes(ctx context.Context, t VoteTarget, voter string, ids []int64) (map[int64]int, error) {
	mine := make(map[int64]int)
	if len(ids) == 0 {
		return mine, nil
	}
	rows, err := s.db.Query(ctx, voteStatements[t].mine, voter, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var value int
		if err := rows.Scan(&id, &value); err != nil {
			return nil, err
		}
		mine[id] = value
	}
	return mine, rows.Err()
}

// VoteState is an item's current score and one voter's vote on it, for
// redrawing the arrows when a vote couldn't be recorded.
func (s *Store) VoteState(ctx context.Context, t VoteTarget, publicID, voter string) (score, mine int, err error) {
	var q string
	if t == VoteComment {
		q = `SELECT c.upvotes - c.downvotes, coalesce(v.value, 0)
		     FROM comments c LEFT JOIN comment_votes v ON v.comment_id = c.id AND v.voter = $2
		     WHERE c.public_id = $1`
	} else {
		q = `SELECT p.score, coalesce(v.value, 0)
		     FROM posts p LEFT JOIN post_votes v ON v.post_id = p.id AND v.voter = $2
		     WHERE p.public_id = $1`
	}
	err = s.db.QueryRow(ctx, q, publicID, voter).Scan(&score, &mine)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrVoteNotFound
	}
	return score, mine, err
}

// VoteAuthor returns who wrote a post or comment, and the voter's current
// vote on it (0 if none), so the vote limits can be checked before a vote is
// recorded. The author ID is only used in memory, never stored with the vote.
func (s *Store) VoteAuthor(ctx context.Context, t VoteTarget, publicID, voter string) (authorID int64, mine int, err error) {
	q := `SELECT p.author_id, coalesce(v.value, 0)
	      FROM posts p LEFT JOIN post_votes v ON v.post_id = p.id AND v.voter = $2
	      WHERE p.public_id = $1`
	if t == VoteComment {
		q = `SELECT c.author_id, coalesce(v.value, 0)
		     FROM comments c LEFT JOIN comment_votes v ON v.comment_id = c.id AND v.voter = $2
		     WHERE c.public_id = $1`
	}
	err = s.db.QueryRow(ctx, q, publicID, voter).Scan(&authorID, &mine)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrVoteNotFound
	}
	return authorID, mine, err
}
