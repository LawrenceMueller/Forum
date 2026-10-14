package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// The editing rules, shared by posts and comments:
//
//   - authors can edit for EditWindow after posting, then it's final;
//   - edits within EditGrace are silent (for fixing a typo right away);
//     later ones set edited_at, which shows an "edited" marker;
//   - no revisions are kept: the row is simply updated.
//
// The window is checked by the DATABASE's clock, inside the UPDATE itself,
// so a form held open past the deadline can't sneak an edit in, and
// neither the browser's clock nor the app server's can be gamed.
const (
	EditWindow = 10 * time.Minute
	EditGrace  = 2 * time.Minute
)

// ErrEditClosed covers every reason an edit can't happen: the window has
// passed, it isn't the editor's own content, or it was removed or deleted.
var ErrEditClosed = errors.New("this can no longer be edited")

// The window and grace travel as whole seconds, and Postgres turns them
// into intervals with "* interval '1 second'".
var (
	windowSecs = int(EditWindow / time.Second)
	graceSecs  = int(EditGrace / time.Second)
)

// EditPost replaces a post's text and tags. The title and link can never be
// edited: swapping a link after a post has collected votes is a classic spam
// trick, and changing a title can change what people voted on.
//
// title and url are the post's existing ones, needed to update its content
// hash; they're not changed.
func (s *Store) EditPost(ctx context.Context, publicID string, authorID int64, title, url, body string, tagIDs []int) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var postID int64
	err = tx.QueryRow(ctx, `
		UPDATE posts
		SET body = $1,
		    content_hash = $2,
		    edited_at = CASE WHEN now() - created_at > $3 * interval '1 second'
		                     THEN now() ELSE edited_at END
		WHERE public_id = $4
		  AND author_id = $5
		  AND removed_at IS NULL AND deleted_at IS NULL
		  AND created_at > now() - $6 * interval '1 second'
		RETURNING id`,
		body, ContentHash(title, url, body), graceSecs, publicID, authorID, windowSecs,
	).Scan(&postID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrEditClosed
	}
	if err != nil {
		return err
	}

	// Replace the tags wholesale: simpler than working out which changed.
	if _, err := tx.Exec(ctx, `DELETE FROM post_tags WHERE post_id = $1`, postID); err != nil {
		return err
	}
	for _, tagID := range tagIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO post_tags (post_id, tag_id) VALUES ($1, $2)`, postID, tagID,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// EditComment replaces a comment's text and returns the updated comment.
func (s *Store) EditComment(ctx context.Context, publicID string, authorID int64, body string) (Comment, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE comments
		SET body = $1,
		    content_hash = $2,
		    edited_at = CASE WHEN now() - created_at > $3 * interval '1 second'
		                     THEN now() ELSE edited_at END
		WHERE public_id = $4
		  AND author_id = $5
		  AND removed_at IS NULL AND deleted_at IS NULL
		  AND created_at > now() - $6 * interval '1 second'`,
		body, ContentHash("", "", body), graceSecs, publicID, authorID, windowSecs,
	)
	if err != nil {
		return Comment{}, err
	}
	if tag.RowsAffected() == 0 {
		return Comment{}, ErrEditClosed
	}

	c, _, err := s.CommentByPublicID(ctx, publicID)
	return c, err
}
