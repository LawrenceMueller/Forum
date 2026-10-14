package store

// Changing the tag tree, for the admin tag page.
//
// Every change runs the same way (changeTags):
//
//  1. In one transaction, take a lock so two changes can't interleave.
//  2. Make the change with plain SQL.
//  3. Read the whole tree back, inside the transaction, and hand it to the
//     caller's check. The rules live in internal/tags, which the store
//     doesn't know about: the web package passes a check that builds a
//     tags.Tree (refusing loops) and looks for misleading names.
//  4. If the check objects, roll everything back. Otherwise rebuild
//     tag_closure from the parent links, refresh tag_posts for the posts
//     whose place in the tree moved, and commit.
//
// So a change that would break the tree never reaches the database, however
// it was made, and the closure table always matches the parents it was
// built from.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrTagNotFound means the tag to change doesn't exist (any more).
var ErrTagNotFound = errors.New("no such tag")

// TagRejected is a change refused for a reason the admin should read.
type TagRejected struct{ Reason string }

func (e TagRejected) Error() string { return e.Reason }

// TagCheck looks over the tree as it would be after a change; an error
// refuses the change, and its text is shown to the admin.
type TagCheck func(TagData) error

// tagLock is an arbitrary number naming the advisory lock that changes to
// tags take. (Advisory locks are named by numbers; this one means "tags".)
const tagLock = 0x7461_6773 // "tags" in ASCII

// changeTags runs one change: see the top of this file.
//
// under names a tag whose posts' tag_posts rows need rebuilding afterwards,
// because the tree above them changed (0 for none).
func (s *Store) changeTags(ctx context.Context, check TagCheck, under int, change func(tx pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // does nothing once committed

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, tagLock); err != nil {
		return err
	}
	if err := change(tx); err != nil {
		return tagError(err)
	}
	data, err := readTagData(ctx, tx)
	if err != nil {
		return err
	}
	if check != nil {
		if err := check(data); err != nil {
			var rejected TagRejected
			if errors.As(err, &rejected) {
				return rejected
			}
			return TagRejected{err.Error()}
		}
	}
	if err := rebuildClosure(ctx, tx); err != nil {
		return err
	}
	if under != 0 {
		if err := refreshTagPostsUnder(ctx, tx, under); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// rebuildClosure recomputes every (ancestor, descendant) pair from the
// parent links: the same query migration 00010 used. A few hundred tags
// make a few thousand rows, which takes a few milliseconds.
func rebuildClosure(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `DELETE FROM tag_closure`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO tag_closure (ancestor_id, descendant_id, depth)
		WITH RECURSIVE walk (ancestor_id, descendant_id, depth) AS (
			SELECT id, id, 0 FROM tags
			UNION ALL
			SELECT w.ancestor_id, tp.tag_id, w.depth + 1
			FROM walk w JOIN tag_parents tp ON tp.parent_id = w.descendant_id
			WHERE w.depth < 32
		)
		SELECT ancestor_id, descendant_id, min(depth) FROM walk GROUP BY ancestor_id, descendant_id`)
	return err
}

// tagError turns the database's refusals into sentences. The web package
// checks most of these first; this is the backstop.
func tagError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "23505": // unique violation
		if pgErr.ConstraintName == "tags_name_key" {
			return TagRejected{"There's already a tag with that slug."}
		}
		return TagRejected{"That's already there."}
	case "23514": // check violation
		return TagRejected{fmt.Sprintf("That breaks a rule for tags (%s).", pgErr.ConstraintName)}
	case "23503": // foreign key violation
		return TagRejected{"That tag is still in use."}
	}
	return err
}

// ---- the changes ----

// NewTag is a tag to create.
type NewTag struct {
	Slug, Display, Description, Facet string
	Parents                           []int
}

// CreateTag adds a tag, returning its ID.
func (s *Store) CreateTag(ctx context.Context, t NewTag, check TagCheck) (int, error) {
	var id int
	err := s.changeTags(ctx, check, 0, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO tags (name, display_name, description, facet) VALUES ($1, $2, $3, $4)
			RETURNING id`, t.Slug, t.Display, t.Description, t.Facet).Scan(&id); err != nil {
			return err
		}
		for _, p := range t.Parents {
			if _, err := tx.Exec(ctx, `INSERT INTO tag_parents (tag_id, parent_id) VALUES ($1, $2)`, id, p); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

// TagDetails are a tag's names and description, as edited.
type TagDetails struct {
	Slug, Display, Description string
	// AddAliases are added along with the change: the old slug and display
	// name, when the admin keeps them, so old links and habits still work.
	AddAliases []string
}

// UpdateTag changes a tag's slug, display name and description.
func (s *Store) UpdateTag(ctx context.Context, id int, d TagDetails, check TagCheck) error {
	return s.changeTags(ctx, check, 0, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE tags SET name = $2, display_name = $3, description = $4 WHERE id = $1`,
			id, d.Slug, d.Display, d.Description)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTagNotFound
		}
		return addAliases(ctx, tx, id, d.AddAliases)
	})
}

func addAliases(ctx context.Context, tx pgx.Tx, id int, aliases []string) error {
	for _, a := range aliases {
		if _, err := tx.Exec(ctx, `INSERT INTO tag_aliases (tag_id, alias) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, id, a); err != nil {
			return err
		}
	}
	return nil
}

// AddTagParent puts a tag under another one as well.
func (s *Store) AddTagParent(ctx context.Context, id, parent int, check TagCheck) error {
	return s.changeTags(ctx, check, id, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO tag_parents (tag_id, parent_id) VALUES ($1, $2)`, id, parent)
		return err
	})
}

// RemoveTagParent takes a tag out from under one of its parents. (Without
// parents left, it becomes a top-level tag.)
func (s *Store) RemoveTagParent(ctx context.Context, id, parent int, check TagCheck) error {
	return s.changeTags(ctx, check, id, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM tag_parents WHERE tag_id = $1 AND parent_id = $2`, id, parent)
		return err
	})
}

// AddTagAlias gives a tag another name.
func (s *Store) AddTagAlias(ctx context.Context, id int, alias string, check TagCheck) error {
	return s.changeTags(ctx, check, 0, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO tag_aliases (tag_id, alias) VALUES ($1, $2)`, id, alias)
		return err
	})
}

// RemoveTagAlias takes one of a tag's other names away.
func (s *Store) RemoveTagAlias(ctx context.Context, id int, alias string, check TagCheck) error {
	return s.changeTags(ctx, check, 0, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM tag_aliases WHERE tag_id = $1 AND alias = $2`, id, alias)
		return err
	})
}

// MergeTag folds one tag into another, for good: its posts get the other
// tag instead, its children move under the other tag, names (passed in as
// aliases: its slug, display name and aliases) lead to the other tag, and
// the tag itself is deleted.
func (s *Store) MergeTag(ctx context.Context, from, into int, aliases []string, check TagCheck) error {
	return s.changeTags(ctx, check, into, func(tx pgx.Tx) error {
		// Posts. A post that had both keeps one row (ON CONFLICT).
		if _, err := tx.Exec(ctx, `
			INSERT INTO post_tags (post_id, tag_id)
			SELECT post_id, $2 FROM post_tags WHERE tag_id = $1
			ON CONFLICT DO NOTHING`, from, into); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM post_tags WHERE tag_id = $1`, from); err != nil {
			return err
		}
		// Children move up to the tag it's merged into. (If "into" was one of
		// its children, it simply loses that parent.)
		if _, err := tx.Exec(ctx, `
			INSERT INTO tag_parents (tag_id, parent_id)
			SELECT tag_id, $2 FROM tag_parents WHERE parent_id = $1 AND tag_id <> $2
			ON CONFLICT DO NOTHING`, from, into); err != nil {
			return err
		}
		if err := addAliases(ctx, tx, into, aliases); err != nil {
			return err
		}
		// Deleting the tag takes its parent links, aliases and closure rows
		// with it (ON DELETE CASCADE).
		tag, err := tx.Exec(ctx, `DELETE FROM tags WHERE id = $1`, from)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTagNotFound
		}
		return nil
	})
}

// DeleteTag removes a tag that nothing uses: no posts (not even removed
// ones) and no children. Anything in use is merged instead, so its posts
// and old links have somewhere to go.
func (s *Store) DeleteTag(ctx context.Context, id int, check TagCheck) error {
	return s.changeTags(ctx, check, 0, func(tx pgx.Tx) error {
		var posts, children int64
		if err := tx.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM post_tags WHERE tag_id = $1),
			       (SELECT count(*) FROM tag_parents WHERE parent_id = $1)`, id).Scan(&posts, &children); err != nil {
			return err
		}
		if posts > 0 || children > 0 {
			return TagRejected{"Only a tag with no posts and nothing under it can be deleted. Merge it into another instead."}
		}
		tag, err := tx.Exec(ctx, `DELETE FROM tags WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTagNotFound
		}
		return nil
	})
}

// TagPostRows is how many posts carry exactly this tag, removed and deleted
// ones included: the count that decides whether a tag can be deleted.
func (s *Store) TagPostRows(ctx context.Context, id int) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM post_tags WHERE tag_id = $1`, id).Scan(&n)
	return n, err
}
