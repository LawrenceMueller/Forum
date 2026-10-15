package store

// Retention (moderation slice 5): what moderation keeps, it keeps for 30
// days, then lets go. Purge runs hourly from the server (cmd/snowball):
//
//   - posts and comments removed by the admin more than 30 days ago have
//     their words erased, as a deletion would (a post's title becomes
//     "[removed]"); the rows stay, so threads keep their shape. Items still
//     held by reports, waiting for the admin, are left until decided.
//   - reports handled more than 30 days ago are deleted;
//   - a suspension that ended (or was lifted) more than 30 days ago keeps
//     its dates and reason, but its note and appeal are emptied.
//
// The moderation log keeps its entries: they hold no content.

import (
	"context"
	"fmt"
	"time"
)

// Retention is how long moderation keeps text around.
const Retention = 30 * 24 * time.Hour

// PurgeResult says what one sweep did.
type PurgeResult struct {
	Posts, Comments, Reports, Suspensions int
}

func (p PurgeResult) String() string {
	return fmt.Sprintf("erased %d posts and %d comments, deleted %d handled reports, emptied %d old suspensions",
		p.Posts, p.Comments, p.Reports, p.Suspensions)
}

// Any reports whether the sweep found anything to do.
func (p PurgeResult) Any() bool { return p.Posts+p.Comments+p.Reports+p.Suspensions > 0 }

// Purge erases and deletes whatever has been kept past Retention.
func (s *Store) Purge(ctx context.Context) (PurgeResult, error) {
	var res PurgeResult
	secs := int64(Retention.Seconds())
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE posts SET title = '[removed]', body = '', url = NULL, content_hash = '', erased_at = now()
		WHERE removed_at < now() - $1 * interval '1 second' AND erased_at IS NULL
		  AND removal_reason IS DISTINCT FROM 'held'`, secs)
	if err != nil {
		return res, err
	}
	res.Posts = int(tag.RowsAffected())

	tag, err = tx.Exec(ctx, `
		UPDATE comments SET body = '', content_hash = '', erased_at = now()
		WHERE removed_at < now() - $1 * interval '1 second' AND erased_at IS NULL
		  AND removal_reason IS DISTINCT FROM 'held'`, secs)
	if err != nil {
		return res, err
	}
	res.Comments = int(tag.RowsAffected())

	tag, err = tx.Exec(ctx, `DELETE FROM reports WHERE handled_at < now() - $1 * interval '1 second'`, secs)
	if err != nil {
		return res, err
	}
	res.Reports = int(tag.RowsAffected())

	tag, err = tx.Exec(ctx, `
		UPDATE suspensions SET note = '', appeal = CASE WHEN appeal IS NOT NULL THEN '' END
		WHERE coalesce(lifted_at, ends_at) < now() - $1 * interval '1 second'
		  AND (note <> '' OR coalesce(appeal, '') <> '')`, secs)
	if err != nil {
		return res, err
	}
	res.Suspensions = int(tag.RowsAffected())

	return res, tx.Commit(ctx)
}
