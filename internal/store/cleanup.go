package store

// Cleanup and shadowbans (moderation slice 3).
//
// Cleanup removes everything an account posted, with one reason, and
// withdraws every vote it cast, correcting the counts. Votes are stored
// under a fingerprint (auth.VoterKeys.Voter), never the account; the web
// package computes the fingerprint with the secret and passes it in, so
// nothing new about who voted is ever stored.
//
// A shadowban (for obvious spam bots only) makes an account's new posts and
// comments arrive already removed, with the reason "shadow": their author
// still sees them as if they were up (web/posts.go, web/comments.go);
// nobody else does, and they count for nothing. The account's votes and
// reports are dropped without a word.

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

// shadowReason marks content from a shadowbanned account.
const shadowReason = "shadow"

// CleanupResult says what a cleanup did.
type CleanupResult struct {
	Posts, Comments int // newly removed (or held ones given the reason)
	Votes           int // withdrawn
}

func (c CleanupResult) String() string {
	return fmt.Sprintf("%d posts, %d comments, %d votes withdrawn", c.Posts, c.Comments, c.Votes)
}

// Cleanup removes everything an account posted for reason, closes the
// reports on it, and withdraws the votes cast under voter (the account's
// vote fingerprint) and, in polls still open, under pollKey (its poll key).
func (s *Store) Cleanup(ctx context.Context, userID int64, voter, pollKey, reason string) (CleanupResult, error) {
	var res CleanupResult
	if !slices.Contains(RemovalReasons, reason) {
		return res, fmt.Errorf("unknown removal reason %q", reason)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)

	var isAdmin bool
	err = tx.QueryRow(ctx, `SELECT is_admin FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&isAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, ErrUserNotFound
	}
	if err != nil {
		return res, err
	}
	if isAdmin {
		return res, ErrCantSuspendAdmin
	}

	// Comments first: each one leaving view takes one off its post's count.
	if _, err := tx.Exec(ctx, `
		UPDATE posts p SET comment_count = greatest(p.comment_count - c.n, 0)
		FROM (SELECT post_id, count(*) AS n FROM comments
		      WHERE author_id = $1 AND removed_at IS NULL AND deleted_at IS NULL
		      GROUP BY post_id) c
		WHERE p.id = c.post_id`, userID); err != nil {
		return res, err
	}
	// "Not removed yet, or removed only provisionally" (held by reports, or
	// shadowed): those get the real reason now.
	const open = `deleted_at IS NULL AND (removed_at IS NULL OR removal_reason IN ('held', 'shadow'))`
	tag, err := tx.Exec(ctx, `UPDATE comments SET removed_at = coalesce(removed_at, now()), removal_reason = $2
		WHERE author_id = $1 AND `+open, userID, reason)
	if err != nil {
		return res, err
	}
	res.Comments = int(tag.RowsAffected())
	tag, err = tx.Exec(ctx, `UPDATE posts SET removed_at = coalesce(removed_at, now()), removal_reason = $2
		WHERE author_id = $1 AND `+open, userID, reason)
	if err != nil {
		return res, err
	}
	res.Posts = int(tag.RowsAffected())

	// Their reports are settled: nothing left to decide.
	if _, err := tx.Exec(ctx, `
		UPDATE reports SET handled_at = now()
		WHERE handled_at IS NULL AND (
		      comment_id IN (SELECT id FROM comments WHERE author_id = $1)
		   OR (comment_id IS NULL AND post_id IN (SELECT id FROM posts WHERE author_id = $1)))`, userID); err != nil {
		return res, err
	}

	n, err := withdrawVotes(ctx, tx, voter)
	if err != nil {
		return res, err
	}
	m, err := withdrawPollVotes(ctx, tx, pollKey)
	if err != nil {
		return res, err
	}
	res.Votes = n + m

	if err := logAccountAction(ctx, tx, "cleanup", reason, res.String()); err != nil {
		return res, err
	}
	return res, tx.Commit(ctx)
}

// withdrawVotes deletes every vote cast under one fingerprint and takes
// them back out of the counts, returning how many there were.
func withdrawVotes(ctx context.Context, tx pgx.Tx, voter string) (int, error) {
	if _, err := tx.Exec(ctx, `
		UPDATE posts p SET upvotes = p.upvotes - v.up, downvotes = p.downvotes - v.down,
		                   score = p.score - (v.up - v.down)
		FROM (SELECT post_id, count(*) FILTER (WHERE value = 1) AS up, count(*) FILTER (WHERE value = -1) AS down
		      FROM post_votes WHERE voter = $1 GROUP BY post_id) v
		WHERE p.id = v.post_id`, voter); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE comments c SET upvotes = c.upvotes - v.up, downvotes = c.downvotes - v.down
		FROM (SELECT comment_id, count(*) FILTER (WHERE value = 1) AS up, count(*) FILTER (WHERE value = -1) AS down
		      FROM comment_votes WHERE voter = $1 GROUP BY comment_id) v
		WHERE c.id = v.comment_id`, voter); err != nil {
		return 0, err
	}
	a, err := tx.Exec(ctx, `DELETE FROM post_votes WHERE voter = $1`, voter)
	if err != nil {
		return 0, err
	}
	b, err := tx.Exec(ctx, `DELETE FROM comment_votes WHERE voter = $1`, voter)
	if err != nil {
		return 0, err
	}
	return int(a.RowsAffected() + b.RowsAffected()), nil
}

// SetShadowban turns an account's shadowban on or off. Turning it on also
// withdraws the votes it has already cast (under voter, and pollKey in open
// polls); what it already posted is left for a cleanup to decide.
func (s *Store) SetShadowban(ctx context.Context, userID int64, voter, pollKey string, on bool) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var isAdmin, was bool
	err = tx.QueryRow(ctx, `SELECT is_admin, shadowbanned_at IS NOT NULL FROM users WHERE id = $1 FOR UPDATE`, userID).
		Scan(&isAdmin, &was)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if isAdmin {
		return ErrCantSuspendAdmin
	}
	if was == on {
		return nil // nothing to change
	}
	if on {
		if _, err := tx.Exec(ctx, `UPDATE users SET shadowbanned_at = now() WHERE id = $1`, userID); err != nil {
			return err
		}
		if _, err := withdrawVotes(ctx, tx, voter); err != nil {
			return err
		}
		if _, err := withdrawPollVotes(ctx, tx, pollKey); err != nil {
			return err
		}
		err = logAccountAction(ctx, tx, "shadowban", "spam", "")
	} else {
		if _, err := tx.Exec(ctx, `UPDATE users SET shadowbanned_at = NULL WHERE id = $1`, userID); err != nil {
			return err
		}
		err = logAccountAction(ctx, tx, "unshadowban", "", "")
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// shadowbanned reports whether an account is shadowbanned, inside a
// transaction that's about to write something of theirs.
func shadowbanned(ctx context.Context, tx pgx.Tx, userID int64) (bool, error) {
	var on bool
	err := tx.QueryRow(ctx, `SELECT shadowbanned_at IS NOT NULL FROM users WHERE id = $1`, userID).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return on, err
}
