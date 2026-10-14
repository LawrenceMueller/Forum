package store

// tag_posts (migration 00012) lists every post under every tag it shows up
// under, so tag pages can walk an index instead of sorting every match.
// It's derived from post_tags and tag_closure, and these functions keep it
// in step: whatever changes a post's tags, or the tree above them, calls
// one of them in the same transaction.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// refreshTagPosts rebuilds the tag_posts rows of the given posts.
func refreshTagPosts(ctx context.Context, tx pgx.Tx, postIDs []int64) error {
	if len(postIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM tag_posts WHERE post_id = ANY($1)`, postIDs); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO tag_posts (tag_id, post_id, created_at, direct)
		SELECT c.ancestor_id, p.id, p.created_at, bool_or(c.depth = 0)
		FROM post_tags pt
		JOIN tag_closure c ON c.descendant_id = pt.tag_id
		JOIN posts p ON p.id = pt.post_id
		WHERE pt.post_id = ANY($1)
		GROUP BY c.ancestor_id, p.id, p.created_at`, postIDs)
	return err
}

// refreshTagPostsUnder rebuilds the rows of every post under a tag (as the
// tree is now): after the tree changes around that tag. Run it after
// tag_closure is rebuilt.
func refreshTagPostsUnder(ctx context.Context, tx pgx.Tx, tagID int) error {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT pt.post_id
		FROM post_tags pt JOIN tag_closure c ON c.descendant_id = pt.tag_id
		WHERE c.ancestor_id = $1`, tagID)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	return refreshTagPosts(ctx, tx, ids)
}

// TagPostsMismatches counts rows where tag_posts disagrees with post_tags
// and the tree: missing, extra, or with the wrong time or "direct" flag.
// Zero means it's right. (For tests and the seed program's checks.)
func (s *Store) TagPostsMismatches(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, TagPostsMismatchesSQL).Scan(&n)
	return n, err
}

// TagPostsMismatchesSQL is the query behind TagPostsMismatches, shared with
// the seed program.
const TagPostsMismatchesSQL = `
	WITH expected AS (
		SELECT c.ancestor_id AS tag_id, p.id AS post_id, p.created_at, bool_or(c.depth = 0) AS direct
		FROM post_tags pt
		JOIN tag_closure c ON c.descendant_id = pt.tag_id
		JOIN posts p ON p.id = pt.post_id
		GROUP BY c.ancestor_id, p.id, p.created_at
	)
	SELECT (SELECT count(*) FROM (SELECT * FROM expected EXCEPT SELECT tag_id, post_id, created_at, direct FROM tag_posts) a)
	     + (SELECT count(*) FROM (SELECT tag_id, post_id, created_at, direct FROM tag_posts EXCEPT SELECT * FROM expected) b)`
