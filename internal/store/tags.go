package store

// The tag system's rows. The store only loads and saves them; the rules
// (what a typed name means, which tags imply which) live in internal/tags,
// which the web package builds from these rows.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Tag is one row of the tags table.
type Tag struct {
	ID          int
	Name        string // the slug, used in URLs
	Display     string
	Description string
	Facet       string // "topic", "format" or "level"
}

// TagEdge says Parent is a direct parent of Tag.
type TagEdge struct{ Tag, Parent int }

// TagAlias is another name for a tag.
type TagAlias struct {
	Tag   int
	Alias string
}

// TagData is the whole tag system: every tag, every parent link, every alias.
type TagData struct {
	Tags    []Tag
	Edges   []TagEdge
	Aliases []TagAlias
}

// querier is what reading needs, which both the pool and a transaction
// have: the admin's changes read the tree back inside their transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Tags returns every tag, by slug.
func (s *Store) Tags(ctx context.Context) ([]Tag, error) { return readTags(ctx, s.db) }

func readTags(ctx context.Context, q querier) ([]Tag, error) {
	rows, err := q.Query(ctx,
		`SELECT id, name, display_name, description, facet FROM tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	// CollectRows reads every row into a slice and closes rows for us.
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Tag, error) {
		var t Tag
		err := row.Scan(&t.ID, &t.Name, &t.Display, &t.Description, &t.Facet)
		return t, err
	})
}

// LoadTags reads the whole tag system. It's a few hundred small rows, read
// at startup (and after the admin changes a tag), then kept in memory.
func (s *Store) LoadTags(ctx context.Context) (TagData, error) { return readTagData(ctx, s.db) }

func readTagData(ctx context.Context, q querier) (TagData, error) {
	var d TagData
	var err error
	if d.Tags, err = readTags(ctx, q); err != nil {
		return d, err
	}
	rows, err := q.Query(ctx, `SELECT tag_id, parent_id FROM tag_parents ORDER BY tag_id, parent_id`)
	if err != nil {
		return d, err
	}
	if d.Edges, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (TagEdge, error) {
		var e TagEdge
		err := row.Scan(&e.Tag, &e.Parent)
		return e, err
	}); err != nil {
		return d, err
	}
	rows, err = q.Query(ctx, `SELECT tag_id, alias FROM tag_aliases ORDER BY tag_id, alias`)
	if err != nil {
		return d, err
	}
	d.Aliases, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (TagAlias, error) {
		var a TagAlias
		err := row.Scan(&a.Tag, &a.Alias)
		return a, err
	})
	return d, err
}

// TagPostCounts is how many visible posts sit under each tag, counting
// everything below it too: a sourdough post counts for Sourdough, Bread,
// Baking and Food & drink, once each. Tags with no posts are absent.
//
// It reads every tag link, so it's meant to run once a minute in the
// background (see web/stats.go), never per page load.
func (s *Store) TagPostCounts(ctx context.Context) (map[int]int64, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.ancestor_id, count(DISTINCT pt.post_id)
		FROM tag_closure c
		JOIN post_tags pt ON pt.tag_id = c.descendant_id
		JOIN posts p ON p.id = pt.post_id AND p.removed_at IS NULL AND p.deleted_at IS NULL
		GROUP BY c.ancestor_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[int]int64)
	for rows.Next() {
		var id int
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		counts[id] = n
	}
	return counts, rows.Err()
}
