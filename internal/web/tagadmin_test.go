package web

// Request-level tests for the admin tag page. They make their own tags
// (slugs starting "zz-test-") and delete them afterwards, since the tag
// tables aren't emptied between tests.

import (
	"net/http"
	"strings"
	"testing"
)

// cleanTestTags removes every zz-test- tag when the test ends.
func cleanTestTags(t *testing.T) {
	t.Cleanup(func() {
		sqlExec(t, `DELETE FROM post_tags WHERE tag_id IN (SELECT id FROM tags WHERE name LIKE 'zz-test-%')`)
		sqlExec(t, `DELETE FROM tags WHERE name LIKE 'zz-test-%'`)
	})
}

// createTag makes a tag through the admin page.
func (s *site) createTag(t *testing.T, boss *testUser, display, slug, parents string) {
	t.Helper()
	rec := s.send("/admin/tags", form("display", display, "slug", slug, "facet", "topic", "parents", parents,
		"form_token", s.token(formTagAdmin)), boss)
	expectRedirect(t, "create "+slug, rec, "/admin/tags/"+slug+"?done=created")
}

func TestTagAdminIsForAdminsOnly(t *testing.T) {
	s := newSite(t)
	cleanTestTags(t)
	author := s.user(t, "blue-humming-otter")

	create := form("display", "Zz test", "slug", "zz-test-sneaky", "facet", "topic", "form_token", s.token(formTagAdmin))
	for _, u := range []*testUser{nil, author} {
		expectStatus(t, "index", s.get("/admin/tags", u), http.StatusNotFound)
		expectStatus(t, "one tag", s.get("/admin/tags/bread", u), http.StatusNotFound)
		expectStatus(t, "create", s.send("/admin/tags", create, u), http.StatusNotFound)
		expectStatus(t, "rename", s.send("/admin/tags/bread/details",
			form("display", "Hacked", "slug", "hacked", "form_token", s.token(formTagAdmin)), u), http.StatusNotFound)
	}
	if _, ok := s.srv.tree().BySlug("zz-test-sneaky"); ok {
		t.Fatal("a non-admin created a tag")
	}
	// No admin links for them either.
	expectMissing(t, "account", s.get("/account", author), "/admin/tags")
	expectMissing(t, "tag page", s.get("/t/bread", author), "edit this tag")
}

func TestTagAdminCreateRenameAndReparent(t *testing.T) {
	s := newSite(t)
	cleanTestTags(t)
	boss := admin(t, s.user(t, "green-napping-puffin"))

	expectContains(t, "account link", s.get("/account", boss), `href="/admin/tags"`)
	expectContains(t, "edit link", s.get("/t/bread", boss), `href="/admin/tags/bread">edit this tag`)
	index := s.get("/admin/tags", boss)
	expectStatus(t, "index", index, http.StatusOK)
	expectContains(t, "index", index, `href="/admin/tags/sourdough">Sourdough</a>`)
	expectContains(t, "filter", s.get("/admin/tags?q=wild+yeast", boss), `href="/admin/tags/sourdough"`)

	// A new tag is live at once: its page, its place in the tree, the
	// type-ahead.
	s.createTag(t, boss, "Zz test rye", "zz-test-rye", "bread")
	page := s.get("/admin/tags/zz-test-rye?done=created", boss)
	expectContains(t, "created", page, "Tag created.")
	expectContains(t, "created", page, `href="/admin/tags/bread">Bread</a>`)
	expectContains(t, "public page", s.get("/t/zz-test-rye", nil), `<a href="/t/bread">Bread</a>`)
	expectContains(t, "type-ahead", s.get("/tags/suggest?topics=zz+test+ry", nil), "choose=zz-test-rye")

	// Renaming, keeping the old names: old links still arrive.
	rec := s.send("/admin/tags/zz-test-rye/details", form("display", "Zz test rye bread", "slug", "zz-test-rye-bread",
		"description", "Dark and dense", "keep", "1", "form_token", s.token(formTagAdmin)), boss)
	expectRedirect(t, "rename", rec, "/admin/tags/zz-test-rye-bread?done=saved")
	if loc := s.get("/t/zz-test-rye", nil).Header().Get("Location"); loc != "/t/zz-test-rye-bread" {
		t.Errorf("the old slug redirects to %q", loc)
	}
	expectRedirect(t, "old admin address", s.get("/admin/tags/zz-test-rye", boss), "/admin/tags/zz-test-rye-bread")

	// A second parent, then back to one.
	rec = s.send("/admin/tags/zz-test-rye-bread/parents", form("action", "add", "name", "Sports",
		"form_token", s.token(formTagAdmin)), boss)
	expectRedirect(t, "add parent", rec, "/admin/tags/zz-test-rye-bread?done=parents")
	tag, _ := s.srv.tree().BySlug("zz-test-rye-bread")
	sports, _ := s.srv.tree().BySlug("sports")
	if !s.srv.tree().IsAncestor(sports.ID, tag.ID) {
		t.Error("sports should now be above it")
	}
	rec = s.send("/admin/tags/zz-test-rye-bread/parents", form("action", "remove", "parent", "sports",
		"form_token", s.token(formTagAdmin)), boss)
	expectRedirect(t, "remove parent", rec, "/admin/tags/zz-test-rye-bread?done=parents")
	if s.srv.tree().IsAncestor(sports.ID, tag.ID) {
		t.Error("sports should be gone from above it")
	}

	// Aliases.
	rec = s.send("/admin/tags/zz-test-rye-bread/aliases", form("action", "add", "alias", "zz pumpernickel",
		"form_token", s.token(formTagAdmin)), boss)
	expectRedirect(t, "add alias", rec, "/admin/tags/zz-test-rye-bread?done=aliases")
	if found := s.srv.tree().Lookup("ZZ Pumpernickel"); len(found) != 1 || found[0].Slug != "zz-test-rye-bread" {
		t.Errorf("the new alias finds %v", found)
	}
}

func TestTagAdminRefusals(t *testing.T) {
	s := newSite(t)
	cleanTestTags(t)
	boss := admin(t, s.user(t, "green-napping-puffin"))
	s.createTag(t, boss, "Zz test top", "zz-test-top", "")
	s.createTag(t, boss, "Zz test under", "zz-test-under", "zz-test-top")
	tok := s.token(formTagAdmin)

	for _, c := range []struct {
		what, path string
		values     []string
		want       string
	}{
		{"a slug that's taken", "/admin/tags", []string{"display", "Bread again", "slug", "bread", "facet", "topic"},
			"There's already a tag with that slug."},
		{"a bad slug", "/admin/tags", []string{"display", "Zz", "slug", "Zz Test", "facet", "topic"},
			"The slug can only have lowercase letters"},
		{"an unknown parent", "/admin/tags", []string{"display", "Zz test x", "slug", "zz-test-x", "facet", "topic", "parents", "nonsense-tag"},
			"There's no topic called “nonsense-tag”."},
		{"a format with parents", "/admin/tags", []string{"display", "Zz test f", "slug", "zz-test-f", "facet", "format", "parents", "bread"},
			"Only topics have parents."},
		{"a name another tag already has", "/admin/tags", []string{"display", "Sour dough", "slug", "zz-test-sd", "facet", "topic"},
			"have the same name"},
		{"a loop", "/admin/tags/zz-test-top/parents", []string{"action", "add", "name", "zz-test-under"},
			"Zz test under is underneath Zz test top, so it can't also be above it."},
		{"a pointless parent", "/admin/tags/zz-test-under/parents", []string{"action", "add", "name", "zz-test-top"},
			"Zz test top is already a parent."},
		{"a punctuation alias", "/admin/tags/zz-test-top/aliases", []string{"action", "add", "alias", ".zz"},
			"starts with punctuation"},
		{"an alias that's another tag's name", "/admin/tags/zz-test-top/aliases", []string{"action", "add", "alias", "Sourdough"},
			"is sourdough's own name"},
		{"an alias it already answers to", "/admin/tags/zz-test-top/aliases", []string{"action", "add", "alias", "ZZ test top"},
			"already leads here"},
		{"a merge without the box ticked", "/admin/tags/zz-test-under/merge", []string{"into", "zz-test-top"},
			"Tick the box to confirm the merge"},
		{"a merge into what's underneath", "/admin/tags/zz-test-top/merge", []string{"into", "zz-test-under", "confirm", "1"},
			"Move it out first"},
		{"a merge across facets", "/admin/tags/zz-test-top/merge", []string{"into", "question", "confirm", "1"},
			"There's no topic called “question”."},
		{"deleting a tag with tags underneath", "/admin/tags/zz-test-top/delete", []string{"confirm", "1"},
			"Only a tag with no posts and nothing under it can be deleted."},
	} {
		rec := s.send(c.path, form(append(c.values, "form_token", tok)...), boss)
		expectStatus(t, c.what, rec, http.StatusOK)
		expectContains(t, c.what, rec, c.want)
	}

	// None of it changed anything.
	tree := s.srv.tree()
	for _, slug := range []string{"zz-test-x", "zz-test-f", "zz-test-sd"} {
		if _, ok := tree.BySlug(slug); ok {
			t.Errorf("%s was created anyway", slug)
		}
	}
	top, _ := tree.BySlug("zz-test-top")
	if len(top.Parents) != 0 || len(top.Aliases) != 0 {
		t.Errorf("zz-test-top changed: parents %v, aliases %v", top.Parents, top.Aliases)
	}
}

func TestTagAdminMergeAndDelete(t *testing.T) {
	s := newSite(t)
	cleanTestTags(t)
	boss := admin(t, s.user(t, "green-napping-puffin"))
	me := s.user(t, "blue-humming-otter")
	s.createTag(t, boss, "Zz test dupe", "zz-test-dupe", "bread")
	s.createTag(t, boss, "Zz test keeper", "zz-test-keeper", "bread")
	s.createTag(t, boss, "Zz test spare", "zz-test-spare", "")
	s.postTagged(t, me, "Filed under the duplicate", "zz-test-dupe")

	rec := s.send("/admin/tags/zz-test-dupe/merge", form("into", "zz-test-keeper", "confirm", "1",
		"form_token", s.token(formTagAdmin)), boss)
	expectRedirect(t, "merge", rec, "/admin/tags/zz-test-keeper?done=merged")

	// The post moved, the old address leads to the keeper, and its old
	// display name does too.
	expectContains(t, "keeper's page", s.get("/t/zz-test-keeper", nil), "Filed under the duplicate")
	if loc := s.get("/t/zz-test-dupe", nil).Header().Get("Location"); loc != "/t/zz-test-keeper" {
		t.Errorf("the merged slug redirects to %q", loc)
	}
	if found := s.srv.tree().Lookup("Zz test dupe"); len(found) != 1 || found[0].Slug != "zz-test-keeper" {
		t.Errorf("the merged name finds %v", found)
	}

	// A tag in use can't be deleted; an unused one can.
	keeper := s.get("/admin/tags/zz-test-keeper", boss)
	expectContains(t, "in use", keeper, "Merge it instead")
	expectMissing(t, "in use", keeper, "/admin/tags/zz-test-keeper/delete")
	rec = s.send("/admin/tags/zz-test-spare/delete", form("confirm", "1", "form_token", s.token(formTagAdmin)), boss)
	expectRedirect(t, "delete", rec, "/admin/tags?done=deleted")
	if _, ok := s.srv.tree().BySlug("zz-test-spare"); ok {
		t.Error("the deleted tag is still in the tree")
	}
	expectStatus(t, "deleted tag's page", s.get("/t/zz-test-spare", nil), http.StatusNotFound)

	if !strings.Contains(s.get("/admin/tags?done=deleted", boss).Body.String(), "Tag deleted.") {
		t.Error("no notice after deleting")
	}
}
