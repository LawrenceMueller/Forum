package store

// Reply notifications. A reply to you is a comment directly under one of
// your comments, or a top-level comment on one of your posts, by someone
// else. Nothing about replies is stored apart from the comments themselves:
// the inbox is a query over them. Each account keeps just a counter (for
// the header) and the time it last opened its inbox (to mark what's new).

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// notifyReply counts a new reply for the account it answers, inside the
// transaction that saves the reply. Replying to yourself counts for nothing.
// (Push notifications, if they ever come, would hook in here.)
func notifyReply(ctx context.Context, tx pgx.Tx, to, from int64) error {
	if to == from || to == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE users SET unread_replies = unread_replies + 1
		WHERE id = $1 AND deleted_at IS NULL`, to)
	return err
}

// Reply is one reply to the viewer, for the inbox.
type Reply struct {
	ID            int64
	PublicID      string
	Body          string
	Author        string
	AuthorDeleted bool
	CreatedAt     time.Time
	Depth         int
	ParentID      string // the comment it answers ("" when it's on the post)
	PostID        string // public ID
	PostTitle     string
}

// Replies lists replies to an account, newest first: direct replies to its
// comments and top-level comments on its posts, by others, still up, under
// things it hasn't deleted. Paged by after (nil for the first page).
func (s *Store) Replies(ctx context.Context, userID int64, after *Cursor, limit int) ([]Reply, *Cursor, error) {
	args := []any{userID, limit + 1}
	cond := ""
	if after != nil {
		cond = "WHERE (created_at, id) < ($3, $4)"
		args = append(args, time.UnixMicro(after.Value), after.ID)
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, public_id, body, author, author_deleted, created_at, depth, parent_public_id, post_public_id, post_title
		FROM (
			SELECT c.id, c.public_id, c.body, u.username AS author, u.deleted_at IS NOT NULL AS author_deleted,
			       c.created_at, c.depth, parent.public_id AS parent_public_id,
			       p.public_id AS post_public_id, p.title AS post_title
			FROM comments parent
			JOIN comments c ON c.parent_id = parent.id
			JOIN posts p ON p.id = c.post_id
			JOIN users u ON u.id = c.author_id
			WHERE parent.author_id = $1 AND parent.deleted_at IS NULL
			  AND c.author_id <> $1 AND c.removed_at IS NULL AND c.deleted_at IS NULL
			  AND p.removed_at IS NULL
			UNION ALL
			SELECT c.id, c.public_id, c.body, u.username, u.deleted_at IS NOT NULL,
			       c.created_at, c.depth, '', p.public_id, p.title
			FROM posts p
			JOIN comments c ON c.post_id = p.id AND c.parent_id IS NULL
			JOIN users u ON u.id = c.author_id
			WHERE p.author_id = $1 AND p.deleted_at IS NULL AND p.removed_at IS NULL
			  AND c.author_id <> $1 AND c.removed_at IS NULL AND c.deleted_at IS NULL
		) replies `+cond+`
		ORDER BY created_at DESC, id DESC
		LIMIT $2`, args...)
	if err != nil {
		return nil, nil, err
	}
	replies, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Reply, error) {
		var r Reply
		err := row.Scan(&r.ID, &r.PublicID, &r.Body, &r.Author, &r.AuthorDeleted, &r.CreatedAt, &r.Depth,
			&r.ParentID, &r.PostID, &r.PostTitle)
		return r, err
	})
	if err != nil {
		return nil, nil, err
	}
	var next *Cursor
	if len(replies) > limit {
		replies = replies[:limit]
		last := replies[limit-1]
		next = historyCursor(last.CreatedAt, last.ID)
	}
	return replies, next, nil
}

// MarkRepliesSeen clears the counter and records the time: everything up
// to now has been seen.
func (s *Store) MarkRepliesSeen(ctx context.Context, userID int64) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET unread_replies = 0, replies_seen_at = now() WHERE id = $1`, userID)
	return err
}
