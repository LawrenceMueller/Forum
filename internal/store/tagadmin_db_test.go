package store

// Database tests for changing the tag tree. They make their own tags
// (slugs starting "zz-test-") under real ones and delete them afterwards,
// since the tag tables aren't emptied between tests (testdb.Tables).

import (
	"errors"
	"slices"
	"testing"
)

// testTags removes every zz-test- tag when the test ends.
func testTags(t *testing.T, st *Store) {
	t.Cleanup(func() {
		exec(t, st, `DELETE FROM post_tags WHERE tag_id IN (SELECT id FROM tags WHERE name LIKE 'zz-test-%')`)
		exec(t, st, `DELETE FROM tags WHERE name LIKE 'zz-test-%'`)
	})
}

// ancestorsOf reads a tag's ancestors from tag_closure, by slug.
func ancestorsOf(t *testing.T, st *Store, id int) []string {
	t.Helper()
	rows, err := st.db.Query(ctx, `
		SELECT a.name FROM tag_closure c JOIN tags a ON a.id = c.ancestor_id
		WHERE c.descendant_id = $1 AND c.depth > 0 ORDER BY a.name`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	return out
}

func mustCreateTag(t *testing.T, st *Store, slug string, parents ...int) int {
	t.Helper()
	id, err := st.CreateTag(ctx, NewTag{Slug: slug, Display: "Test " + slug, Facet: "topic", Parents: parents}, nil)
	if err != nil {
		t.Fatalf("creating %s: %v", slug, err)
	}
	return id
}

func TestCreateTagBuildsTheClosure(t *testing.T) {
	st := freshDB(t)
	testTags(t, st)
	bread := mustTag(t, st, "bread")

	id := mustCreateTag(t, st, "zz-test-rye", bread)
	if got := ancestorsOf(t, st, id); !slices.Equal(got, []string{"baking", "bread", "food-and-drink"}) {
		t.Errorf("ancestors = %v", got)
	}
	if n := count(t, st, `SELECT count(*) FROM tag_closure WHERE ancestor_id = $1 AND descendant_id = $1 AND depth = 0`, id); n != 1 {
		t.Errorf("the tag should be its own depth-0 row, found %d", n)
	}

	// The same slug again is refused with a sentence.
	_, err := st.CreateTag(ctx, NewTag{Slug: "zz-test-rye", Display: "Again", Facet: "topic"}, nil)
	var rejected TagRejected
	if !errors.As(err, &rejected) {
		t.Errorf("a duplicate slug gave %v, want TagRejected", err)
	}
}

func TestACheckThatObjectsUndoesEverything(t *testing.T) {
	st := freshDB(t)
	testTags(t, st)
	before := count(t, st, `SELECT count(*) FROM tag_closure`)

	saw := 0
	_, err := st.CreateTag(ctx, NewTag{Slug: "zz-test-nope", Display: "Nope", Facet: "topic",
		Parents: []int{mustTag(t, st, "bread")}}, func(d TagData) error {
		// The check sees the tree as it would be, new tag included.
		for _, tag := range d.Tags {
			if tag.Name == "zz-test-nope" {
				saw++
			}
		}
		return errors.New("not today")
	})
	if err == nil || err.Error() != "not today" {
		t.Fatalf("err = %v, want the check's own words", err)
	}
	if saw != 1 {
		t.Errorf("the check should see the new tag once, saw it %d times", saw)
	}
	if n := count(t, st, `SELECT count(*) FROM tags WHERE name = 'zz-test-nope'`); n != 0 {
		t.Error("the refused tag was saved anyway")
	}
	if after := count(t, st, `SELECT count(*) FROM tag_closure`); after != before {
		t.Errorf("tag_closure changed from %d to %d rows", before, after)
	}
}

func TestParentsAndAliases(t *testing.T) {
	st := freshDB(t)
	testTags(t, st)
	bread, sports := mustTag(t, st, "bread"), mustTag(t, st, "sports")
	id := mustCreateTag(t, st, "zz-test-thing", bread)

	if err := st.AddTagParent(ctx, id, sports, nil); err != nil {
		t.Fatal(err)
	}
	if got := ancestorsOf(t, st, id); !slices.Equal(got, []string{"baking", "bread", "food-and-drink", "sports"}) {
		t.Errorf("with two parents, ancestors = %v", got)
	}
	if err := st.RemoveTagParent(ctx, id, bread, nil); err != nil {
		t.Fatal(err)
	}
	if got := ancestorsOf(t, st, id); !slices.Equal(got, []string{"sports"}) {
		t.Errorf("after removing bread, ancestors = %v", got)
	}

	if err := st.AddTagAlias(ctx, id, "thingamajig", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.AddTagAlias(ctx, id, "thingamajig", nil); err == nil {
		t.Error("the same alias twice should be refused")
	}
	if err := st.RemoveTagAlias(ctx, id, "thingamajig", nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, st, `SELECT count(*) FROM tag_aliases WHERE tag_id = $1`, id); n != 0 {
		t.Errorf("%d aliases left", n)
	}

	// Renaming keeps the old names as aliases when asked.
	err := st.UpdateTag(ctx, id, TagDetails{Slug: "zz-test-renamed", Display: "Renamed", Description: "now with words",
		AddAliases: []string{"zz-test-thing", "Test zz-test-thing"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, st, `SELECT count(*) FROM tag_aliases WHERE tag_id = $1`, id); n != 2 {
		t.Errorf("after renaming, %d aliases, want the 2 old names", n)
	}
	if err := st.UpdateTag(ctx, -1, TagDetails{Slug: "zz-test-x", Display: "X"}, nil); !errors.Is(err, ErrTagNotFound) {
		t.Errorf("updating a missing tag gave %v", err)
	}
}

func TestMergeTag(t *testing.T) {
	st := freshDB(t)
	testTags(t, st)
	bread := mustTag(t, st, "bread")
	from := mustCreateTag(t, st, "zz-test-from", bread)
	into := mustCreateTag(t, st, "zz-test-into", bread)
	child := mustCreateTag(t, st, "zz-test-child", from)
	exec(t, st, `INSERT INTO tag_aliases (tag_id, alias) VALUES ($1, 'old alias')`, from)

	author := mustUser(t, st, "blue-humming-otter")
	onlyFrom := mustPost(t, st, author, "only from")
	both := mustPost(t, st, author, "both")
	exec(t, st, `INSERT INTO post_tags (post_id, tag_id) SELECT id, $1 FROM posts WHERE public_id = $2`, from, onlyFrom.PublicID)
	exec(t, st, `INSERT INTO post_tags (post_id, tag_id) SELECT id, $1 FROM posts WHERE public_id = $2`, from, both.PublicID)
	exec(t, st, `INSERT INTO post_tags (post_id, tag_id) SELECT id, $1 FROM posts WHERE public_id = $2`, into, both.PublicID)

	if err := st.MergeTag(ctx, from, into, []string{"zz-test-from", "old alias"}, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, st, `SELECT count(*) FROM tags WHERE id = $1`, from); n != 0 {
		t.Error("the merged tag still exists")
	}
	if n := count(t, st, `SELECT count(*) FROM post_tags WHERE tag_id = $1`, into); n != 2 {
		t.Errorf("%d posts carry the merged-into tag, want 2 (the shared one once)", n)
	}
	if got := ancestorsOf(t, st, child); !slices.Contains(got, "zz-test-into") || slices.Contains(got, "zz-test-from") {
		t.Errorf("the child's ancestors are %v; it should have moved under zz-test-into", got)
	}
	if n := count(t, st, `SELECT count(*) FROM tag_aliases WHERE tag_id = $1 AND alias IN ('zz-test-from', 'old alias')`, into); n != 2 {
		t.Errorf("%d of the old names lead to the merged-into tag, want 2", n)
	}
}

func TestDeleteTagOnlyWhenUnused(t *testing.T) {
	st := freshDB(t)
	testTags(t, st)
	parent := mustCreateTag(t, st, "zz-test-parent")
	mustCreateTag(t, st, "zz-test-kid", parent)
	used := mustCreateTag(t, st, "zz-test-used")
	p := mustPost(t, st, mustUser(t, st, "blue-humming-otter"), "a post")
	exec(t, st, `INSERT INTO post_tags (post_id, tag_id) SELECT id, $1 FROM posts WHERE public_id = $2`, used, p.PublicID)

	var rejected TagRejected
	for _, id := range []int{parent, used} {
		if err := st.DeleteTag(ctx, id, nil); !errors.As(err, &rejected) {
			t.Errorf("deleting tag %d (in use) gave %v, want TagRejected", id, err)
		}
	}
	unused := mustCreateTag(t, st, "zz-test-unused")
	if err := st.DeleteTag(ctx, unused, nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.TagPostRows(ctx, used); n != 1 {
		t.Errorf("TagPostRows = %d, want 1", n)
	}
}
