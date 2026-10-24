package store

// Polls: any post can carry one (migration 00020 has the privacy rules).
//
// A vote is stored under a per-poll fingerprint, PollVoter(key, post id),
// where key is the account's poll key (auth.VoterKeys.PollKey). The key
// never reaches the database; the fingerprint is a SHA-256 of the two, so
// the store can work out an account's fingerprint in every poll at once
// (for withdrawing a spammer's votes, or cutting a deleted account's loose)
// in SQL, given only the key.
//
// Votes are final: there's no changing or taking one back, so a pick
// means something. Totals live in poll_options.votes, updated in the same
// transaction as the vote, so reading a poll's results is one small query.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Poll limits. The web package checks them first; the database's
// constraints back up the option ones.
const (
	MinPollOptions   = 2
	MaxPollOptions   = 6
	MaxPollOptionLen = 80
)

var (
	ErrPollNotFound   = errors.New("no such poll")
	ErrPollClosed     = errors.New("this poll is closed")
	ErrAlreadyVoted   = errors.New("you've already voted in this poll")
	ErrNoSuchOption   = errors.New("no such option")
	errPollOptionsBad = errors.New("a poll needs 2 to 6 options")
)

// NewPoll is the poll part of a new post.
type NewPoll struct {
	Options  []string
	ClosesAt time.Time
	Mega     bool // results hidden until it closes
}

// Poll is a poll as the rest of the app sees it.
type Poll struct {
	PostID   int64
	ClosesAt time.Time
	Options  []PollOption // in position order
	Total    int
	Mega     bool // a mega poll: results hidden until it closes
}

// PollOption is one choice and how many picked it.
type PollOption struct {
	Label string
	Votes int
}

// Open reports whether the poll still takes votes.
func (p Poll) Open() bool { return time.Now().Before(p.ClosesAt) }

// PollVoter is an account's fingerprint in one poll. pollFingerprintSQL is
// the same thing in SQL, and the two must always agree (a test checks).
func PollVoter(pollKey string, postID int64) string {
	sum := sha256.Sum256([]byte(pollKey + ":" + strconv.FormatInt(postID, 10)))
	return hex.EncodeToString(sum[:])
}

// pollFingerprintSQL computes PollVoter from $1 (the key) and a post_id
// column, for statements that touch one account's votes in every poll.
const pollFingerprintSQL = `encode(sha256(convert_to($1 || ':' || post_id::text, 'UTF8')), 'hex')`

// insertPoll adds a poll to a post being created (inside its transaction).
func insertPoll(ctx context.Context, tx pgx.Tx, postID int64, p NewPoll) error {
	if len(p.Options) < MinPollOptions || len(p.Options) > MaxPollOptions {
		return errPollOptionsBad
	}
	if _, err := tx.Exec(ctx, `INSERT INTO polls (post_id, closes_at, mega) VALUES ($1, $2, $3)`, postID, p.ClosesAt, p.Mega); err != nil {
		return err
	}
	for i, label := range p.Options {
		if _, err := tx.Exec(ctx, `INSERT INTO poll_options (post_id, position, label) VALUES ($1, $2, $3)`,
			postID, i, label); err != nil {
			return err
		}
	}
	return nil
}

// PollForPost loads a post's poll, or nil if it hasn't got one.
func (s *Store) PollForPost(ctx context.Context, postID int64) (*Poll, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.closes_at, p.mega, o.label, o.votes
		FROM polls p JOIN poll_options o ON o.post_id = p.post_id
		WHERE p.post_id = $1
		ORDER BY o.position`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var poll *Poll
	for rows.Next() {
		var closes time.Time
		var mega bool
		var o PollOption
		if err := rows.Scan(&closes, &mega, &o.Label, &o.Votes); err != nil {
			return nil, err
		}
		if poll == nil {
			poll = &Poll{PostID: postID, ClosesAt: closes, Mega: mega}
		}
		poll.Options = append(poll.Options, o)
		poll.Total += o.Votes
	}
	return poll, rows.Err()
}

// MyPollPick returns the option a fingerprint voted for (voted is false if
// it hasn't). After a poll closes this always says "not voted": the
// fingerprints are gone by then, and the results are shown to everyone.
func (s *Store) MyPollPick(ctx context.Context, postID int64, voter string) (pick int, voted bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT position FROM poll_votes WHERE post_id = $1 AND voter = $2`,
		postID, voter).Scan(&pick)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return pick, err == nil, err
}

// PollVote records a vote for option pick. voter is the account's
// fingerprint in this poll (PollVoter); userID is used only to tag the
// account's comments on this post with its pick (the public part), never
// stored with the vote.
func (s *Store) PollVote(ctx context.Context, postID int64, voter string, userID int64, pick int) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var open, gone bool
	err = tx.QueryRow(ctx, `
		SELECT pl.closes_at > now(), p.removed_at IS NOT NULL OR p.deleted_at IS NOT NULL
		FROM polls pl JOIN posts p ON p.id = pl.post_id
		WHERE pl.post_id = $1`, postID).Scan(&open, &gone)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrPollNotFound
	case err != nil:
		return err
	case !open || gone:
		return ErrPollClosed
	}

	// The option's row is locked by the update, so votes for it take turns
	// and none is lost.
	tag, err := tx.Exec(ctx, `UPDATE poll_options SET votes = votes + 1 WHERE post_id = $1 AND position = $2`,
		postID, pick)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNoSuchOption
	}
	tag, err = tx.Exec(ctx, `INSERT INTO poll_votes (voter, post_id, position) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, voter, postID, pick)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadyVoted // rolls back the count too
	}
	// Comments they already wrote here get their pick now.
	if _, err := tx.Exec(ctx, `UPDATE comments SET poll_pick = $3
		WHERE post_id = $1 AND author_id = $2 AND poll_pick IS NULL`, postID, userID, pick); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ClosePolls deletes the vote fingerprints of every poll that has closed,
// keeping only the totals. It runs hourly (cmd/snowball), and returns how
// many fingerprints went.
func (s *Store) ClosePolls(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM poll_votes v USING polls p
		WHERE p.post_id = v.post_id AND p.closes_at <= now()`)
	return tag.RowsAffected(), err
}

// withdrawPollVotes takes back every vote an account cast in polls still
// open (closed polls have no fingerprints left to find), inside a cleanup
// or shadowban. It returns how many there were.
func withdrawPollVotes(ctx context.Context, tx pgx.Tx, pollKey string) (int, error) {
	if pollKey == "" {
		return 0, nil
	}
	var n int
	err := tx.QueryRow(ctx, `
		WITH gone AS (
			DELETE FROM poll_votes WHERE voter = `+pollFingerprintSQL+`
			RETURNING post_id, position
		), counted AS (
			UPDATE poll_options o SET votes = o.votes - g.n
			FROM (SELECT post_id, position, count(*) AS n FROM gone GROUP BY post_id, position) g
			WHERE o.post_id = g.post_id AND o.position = g.position
		)
		SELECT count(*) FROM gone`, pollKey).Scan(&n)
	return n, err
}

// cutPollVotesLoose keeps a deleted account's poll votes counting but
// swaps their fingerprints for random values, as DeleteAccount does with
// the other votes.
func cutPollVotesLoose(ctx context.Context, tx pgx.Tx, pollKey string) error {
	if pollKey == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE poll_votes SET voter = gen_random_uuid()::text WHERE voter = `+pollFingerprintSQL, pollKey)
	return err
}
