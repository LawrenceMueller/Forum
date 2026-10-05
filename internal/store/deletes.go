package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Deleting your own content erases it: the text (and a post's link) are
// overwritten, so they're gone from the database itself, not merely hidden.
// The row stays, so threads keep their shape: a deleted post keeps its title
// (Reddit's choice, so the discussion under it still makes sense), and a
// deleted comment with replies holds their place. The database enforces
// that deleted rows are empty (migration 00009).
//
// The row still records which account wrote it. Breaking that link too was
// considered and set aside: it would get in the way of moderation, such as
// cleaning up after a spammer.

// DeletePost erases one of the author's posts. Deleting something already
// deleted (or someone else's) does nothing.
func (s *Store) DeletePost(ctx context.Context, publicID string, authorID int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE posts
		SET body = '', url = NULL, content_hash = '', deleted_at = now()
		WHERE public_id = $1 AND author_id = $2 AND deleted_at IS NULL`,
		publicID, authorID)
	return err
}

// DeleteComment erases one of the author's comments, and takes it off the
// post's visible comment count (unless the admin had already removed it,
// in which case it wasn't being counted).
func (s *Store) DeleteComment(ctx context.Context, publicID string, authorID int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var postID int64
	var wasVisible bool
	err = tx.QueryRow(ctx, `
		UPDATE comments
		SET body = '', content_hash = '', deleted_at = now()
		WHERE public_id = $1 AND author_id = $2 AND deleted_at IS NULL
		RETURNING post_id, removed_at IS NULL`,
		publicID, authorID).Scan(&postID, &wasVisible)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already deleted, or not theirs: nothing to do
	}
	if err != nil {
		return err
	}

	if wasVisible {
		if _, err := tx.Exec(ctx,
			`UPDATE posts SET comment_count = greatest(comment_count - 1, 0) WHERE id = $1`, postID,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// eraseAllContent erases every post and comment an account wrote, inside an
// existing transaction (used when deleting an account with "also delete
// everything I've posted").
func eraseAllContent(ctx context.Context, tx pgx.Tx, authorID int64) error {
	if _, err := tx.Exec(ctx, `
		UPDATE posts
		SET body = '', url = NULL, content_hash = '', deleted_at = now()
		WHERE author_id = $1 AND deleted_at IS NULL`, authorID,
	); err != nil {
		return err
	}

	// One statement erases the comments and adjusts every affected post's
	// count: the WITH part does the erasing and reports which posts lost
	// visible comments; the UPDATE after it subtracts them.
	_, err := tx.Exec(ctx, `
		WITH erased AS (
			UPDATE comments
			SET body = '', content_hash = '', deleted_at = now()
			WHERE author_id = $1 AND deleted_at IS NULL
			RETURNING post_id, removed_at IS NULL AS was_visible
		)
		UPDATE posts p
		SET comment_count = greatest(p.comment_count - lost.n, 0)
		FROM (SELECT post_id, count(*) AS n FROM erased WHERE was_visible GROUP BY post_id) lost
		WHERE p.id = lost.post_id`, authorID)
	return err
}

// ---- the private history page ----

// HistoryPost is one of the viewer's own posts, for their history page.
type HistoryPost struct {
	ID           int64
	PublicID     string
	Title        string
	CreatedAt    time.Time
	Score        int
	CommentCount int
	Removed      bool
}

// HistoryComment is one of the viewer's own comments, with the post it's on.
type HistoryComment struct {
	ID          int64
	PublicID    string
	Body        string
	CreatedAt   time.Time
	Score       int
	Removed     bool
	PostID      string // public ID
	PostTitle   string
	PostRemoved bool
}

// historyCursor marks where a page of history ended. History pages are
// newest first, paged by creation time like the "newest" list.
func historyCursor(createdAt time.Time, id int64) *Cursor {
	return &Cursor{Value: createdAt.UnixMicro(), ID: id}
}

// MyPosts lists an author's own posts that they haven't deleted, newest first.
func (s *Store) MyPosts(ctx context.Context, authorID int64, after *Cursor, limit int) ([]HistoryPost, *Cursor, error) {
	args := []any{authorID, limit + 1}
	cond := ""
	if after != nil {
		cond = "AND (p.created_at, p.id) < ($3, $4)"
		args = append(args, time.UnixMicro(after.Value), after.ID)
	}
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.public_id, p.title, p.created_at, p.score, p.comment_count,
		       p.removed_at IS NOT NULL
		FROM posts p
		WHERE p.author_id = $1 AND p.deleted_at IS NULL `+cond+`
		ORDER BY p.created_at DESC, p.id DESC
		LIMIT $2`, args...)
	if err != nil {
		return nil, nil, err
	}
	posts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (HistoryPost, error) {
		var p HistoryPost
		err := row.Scan(&p.ID, &p.PublicID, &p.Title, &p.CreatedAt, &p.Score, &p.CommentCount, &p.Removed)
		return p, err
	})
	if err != nil {
		return nil, nil, err
	}
	var next *Cursor
	if len(posts) > limit {
		posts = posts[:limit]
		last := posts[limit-1]
		next = historyCursor(last.CreatedAt, last.ID)
	}
	return posts, next, nil
}

// MyComments lists an author's own comments that they haven't deleted, newest first.
func (s *Store) MyComments(ctx context.Context, authorID int64, after *Cursor, limit int) ([]HistoryComment, *Cursor, error) {
	args := []any{authorID, limit + 1}
	cond := ""
	if after != nil {
		cond = "AND (c.created_at, c.id) < ($3, $4)"
		args = append(args, time.UnixMicro(after.Value), after.ID)
	}
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.public_id, c.body, c.created_at, c.upvotes - c.downvotes,
		       c.removed_at IS NOT NULL,
		       p.public_id, p.title, p.removed_at IS NOT NULL
		FROM comments c
		JOIN posts p ON p.id = c.post_id
		WHERE c.author_id = $1 AND c.deleted_at IS NULL `+cond+`
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT $2`, args...)
	if err != nil {
		return nil, nil, err
	}
	comments, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (HistoryComment, error) {
		var c HistoryComment
		err := row.Scan(&c.ID, &c.PublicID, &c.Body, &c.CreatedAt, &c.Score, &c.Removed,
			&c.PostID, &c.PostTitle, &c.PostRemoved)
		return c, err
	})
	if err != nil {
		return nil, nil, err
	}
	var next *Cursor
	if len(comments) > limit {
		comments = comments[:limit]
		last := comments[limit-1]
		next = historyCursor(last.CreatedAt, last.ID)
	}
	return comments, next, nil
}
