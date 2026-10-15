package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrPostNotFound = errors.New("no such post")
	ErrPostLocked   = errors.New("post is removed or deleted, so it can't take new comments")
	ErrParentGone   = errors.New("the comment being replied to doesn't exist or can't take replies")
)

// Comment is one comment as the rest of the app sees it.
type Comment struct {
	ID            int64
	ParentID      int64 // 0 for a top-level comment
	PublicID      string
	PostID        int64
	Author        string
	AuthorDeleted bool
	Body          string
	CreatedAt     time.Time
	EditedAt      *time.Time
	Upvotes       int
	Downvotes     int
	Depth         int
	Deleted       bool
	Removed       bool   // by the admin, or held by reports
	RemovalReason string // why, when removed ("" if unrecorded)
}

// maxCommentsLoaded caps how many comments one page load reads. Far above
// any thread Snowball is likely to see; paging can come if it's ever hit.
const maxCommentsLoaded = 2000

const commentColumns = `
	c.id, coalesce(c.parent_id, 0), c.public_id, c.post_id,
	u.username, u.deleted_at IS NOT NULL,
	c.body, c.created_at, c.edited_at, c.upvotes, c.downvotes, c.depth,
	c.deleted_at IS NOT NULL, c.removed_at IS NOT NULL, coalesce(c.removal_reason, '')`

func scanComment(row pgx.Row, c *Comment) error {
	return row.Scan(&c.ID, &c.ParentID, &c.PublicID, &c.PostID,
		&c.Author, &c.AuthorDeleted,
		&c.Body, &c.CreatedAt, &c.EditedAt, &c.Upvotes, &c.Downvotes, &c.Depth,
		&c.Deleted, &c.Removed, &c.RemovalReason)
}

// CommentsForPost loads every comment on a post, oldest first. The tree is
// assembled in Go (see web/comments.go), which makes sorting siblings by a
// changing value like "best" simple.
func (s *Store) CommentsForPost(ctx context.Context, postID int64) ([]Comment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+commentColumns+`
		FROM comments c
		JOIN users u ON u.id = c.author_id
		WHERE c.post_id = $1
		ORDER BY c.id
		LIMIT $2`, postID, maxCommentsLoaded)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Comment, error) {
		var c Comment
		err := scanComment(row, &c)
		return c, err
	})
}

// CommentByPublicID loads one comment. found is false if there's no such comment.
func (s *Store) CommentByPublicID(ctx context.Context, publicID string) (c Comment, found bool, err error) {
	err = scanComment(s.db.QueryRow(ctx, `
		SELECT `+commentColumns+`
		FROM comments c
		JOIN users u ON u.id = c.author_id
		WHERE c.public_id = $1`, publicID), &c)
	if errors.Is(err, pgx.ErrNoRows) {
		return Comment{}, false, nil
	}
	if err != nil {
		return Comment{}, false, err
	}
	return c, true, nil
}

// NewComment is what the comment form hands over.
type NewComment struct {
	PostID         int64
	ParentPublicID string // "" for a top-level comment
	AuthorID       int64
	Body           string
}

// CreateComment saves a comment, returning its public ID and depth.
// In the same transaction it bumps the post's comment count and activity
// time, which is what moves the post up the "active" list.
func (s *Store) CreateComment(ctx context.Context, c NewComment) (publicID string, depth int, err error) {
	hash := ContentHash("", "", c.Body)

	for range 5 {
		publicID, err = newPublicID()
		if err != nil {
			return "", 0, err
		}
		depth, err = s.createCommentOnce(ctx, c, publicID, hash)

		// A failed statement aborts a Postgres transaction, so an ID
		// collision retries the whole transaction with a new ID.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "comments_public_id_key" {
			continue
		}
		if err != nil {
			return "", 0, err
		}
		return publicID, depth, nil
	}
	return "", 0, errors.New("could not find a free public ID")
}

func (s *Store) createCommentOnce(ctx context.Context, c NewComment, publicID, hash string) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// FOR UPDATE locks the post row until we commit, so two comments
	// arriving at once can't both read the same count and lose one.
	var locked bool
	err = tx.QueryRow(ctx,
		`SELECT removed_at IS NOT NULL OR deleted_at IS NOT NULL FROM posts WHERE id = $1 FOR UPDATE`,
		c.PostID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrPostNotFound
	}
	if err != nil {
		return 0, err
	}
	if locked {
		return 0, ErrPostLocked
	}

	var parentID *int64 // NULL for top-level
	depth := 0
	if c.ParentPublicID != "" {
		var id, postID int64
		var parentDepth int
		var gone bool
		err := tx.QueryRow(ctx, `
			SELECT id, post_id, depth, removed_at IS NOT NULL OR deleted_at IS NOT NULL
			FROM comments WHERE public_id = $1`, c.ParentPublicID,
		).Scan(&id, &postID, &parentDepth, &gone)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (postID != c.PostID || gone)) {
			return 0, ErrParentGone
		}
		if err != nil {
			return 0, err
		}
		parentID = &id
		depth = parentDepth + 1
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO comments (public_id, post_id, parent_id, author_id, depth, body, content_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		publicID, c.PostID, parentID, c.AuthorID, depth, c.Body, hash,
	); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE posts SET comment_count = comment_count + 1, last_activity_at = now()
		WHERE id = $1`, c.PostID,
	); err != nil {
		return 0, err
	}
	return depth, tx.Commit(ctx)
}
