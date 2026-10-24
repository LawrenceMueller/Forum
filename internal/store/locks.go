package store

// Admin controls for a thread getting out of hand (migration 00022):
//
//   - Locking a post stops new comments and comment edits on it; everything
//     stays readable, and votes, reports and deleting your own comment still
//     work. Unlocking undoes it.
//   - Closing a poll early ends the voting now: everyone sees the results,
//     and the vote fingerprints go at once, as they would at the usual close.
//
// Both are logged in the public moderation log.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var (
	ErrCommentsLocked = errors.New("this post is locked, so it takes no new comments")
	ErrPollNotOpen    = errors.New("this poll has already closed")
)

// SetLocked locks (on) or unlocks a post. Asking for what's already so does
// nothing, and logs nothing.
func (s *Store) SetLocked(ctx context.Context, publicID string, on bool) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var was bool
	err = tx.QueryRow(ctx, `SELECT locked_at IS NOT NULL FROM posts WHERE public_id = $1 FOR UPDATE`, publicID).Scan(&was)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrItemNotFound
	}
	if err != nil {
		return err
	}
	if was == on {
		return nil
	}
	action := "unlock"
	if on {
		action = "lock"
		_, err = tx.Exec(ctx, `UPDATE posts SET locked_at = now() WHERE public_id = $1`, publicID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE posts SET locked_at = NULL WHERE public_id = $1`, publicID)
	}
	if err != nil {
		return err
	}
	if err := logAction(ctx, tx, action, Item{PostID: publicID}, "", 0); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ClosePollNow ends a post's poll straight away.
func (s *Store) ClosePollNow(ctx context.Context, publicID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var postID int64
	var open bool
	err = tx.QueryRow(ctx, `
		SELECT p.id, pl.closes_at > now()
		FROM posts p JOIN polls pl ON pl.post_id = p.id
		WHERE p.public_id = $1 FOR UPDATE OF pl`, publicID).Scan(&postID, &open)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrPollNotFound
	case err != nil:
		return err
	case !open:
		return ErrPollNotOpen
	}
	if _, err := tx.Exec(ctx, `UPDATE polls SET closes_at = now() WHERE post_id = $1`, postID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM poll_votes WHERE post_id = $1`, postID); err != nil {
		return err
	}
	if err := logAction(ctx, tx, "close", Item{PostID: publicID}, "", 0); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
