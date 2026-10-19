package web

// Request-level tests for moderation, slice 1: reporting, auto-hiding,
// the admin's queue, removal reasons, the public log and the rules page.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"snowball/internal/store"
)

func TestWhoSeesReportLinks(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	reader := s.user(t, "red-baking-hare")
	newbie := young(t, s.user(t, "green-napping-puffin"))
	post := s.post(t, author, "A post")
	cid := s.comment(t, post, reader, "A comment")
	p := "/p/" + post.PublicID

	page := s.get(p, reader)
	expectContains(t, "reader, the post", page, `href="`+p+`/report"`)
	expectMissing(t, "reader, own comment", page, p+"/c/"+cid+"/report")
	expectContains(t, "author, the comment", s.get(p, author), p+"/c/"+cid+"/report")
	expectMissing(t, "author, own post", s.get(p, author), `href="`+p+`/report"`)
	expectMissing(t, "logged out", s.get(p, nil), "/report")
	expectMissing(t, "a brand-new account", s.get(p, newbie), "/report")
}

func TestReporting(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	reader := s.user(t, "red-baking-hare")
	post := s.post(t, author, "Cheap watches here")
	path := "/p/" + post.PublicID + "/report"

	page := s.get(path, reader)
	expectStatus(t, "report page", page, http.StatusOK)
	expectContains(t, "report page", page, "Cheap watches here")
	expectContains(t, "report page", page, "Nobody can see who reported what")

	send := func(who *testUser, pairs ...string) *httptest.ResponseRecorder {
		return s.send(path, form(append(pairs, "form_token", s.token(formReport))...), who)
	}
	expectContains(t, "no reason", send(reader), "Pick a reason.")
	expectContains(t, "other, no note", send(reader, "reason", "other"), "Say what's wrong in the note.")
	expectContains(t, "too long a note", send(reader, "reason", "spam", "note", strings.Repeat("x", 201)), "200 characters")

	done := send(reader, "reason", "spam", "note", "selling watches")
	expectStatus(t, "report", done, http.StatusOK)
	expectContains(t, "report", done, "Thanks, the admin will look at it.")
	expectContains(t, "again", send(reader, "reason", "spam"), "You've already reported this.")

	// What can't be reported.
	expectStatus(t, "own post", s.get(path, author), http.StatusBadRequest)
	expectStatus(t, "brand-new account", s.get(path, young(t, s.user(t, "green-napping-puffin"))), http.StatusForbidden)
	expectRedirect(t, "logged out", s.get(path, nil), "/login")

	// The report is filed under a fingerprint, never the account.
	var reporter string
	if err := rawDB.QueryRow(ctx, `SELECT reporter FROM reports`).Scan(&reporter); err != nil {
		t.Fatal(err)
	}
	if reporter != s.srv.voters.Reporter(reader.id) || strings.Contains(reporter, "red-baking-hare") {
		t.Errorf("stored reporter %q isn't the reader's reporter fingerprint", reporter)
	}
}

func TestSeriousReportsHideUntilReviewed(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	post := s.post(t, author, "A thread")
	cid := s.comment(t, post, author, "Their address is 12 Example Street")
	s.reply(t, post, cid, author, "A reply, which keeps the comment's place in the thread")
	p := "/p/" + post.PublicID

	for i, name := range []string{"red-baking-hare", "green-napping-puffin", "purple-reading-owl"} {
		rec := s.send(p+"/c/"+cid+"/report", form("reason", "personal", "form_token", s.token(formReport)), s.user(t, name))
		if i == 2 {
			expectContains(t, "third report", rec, "now hidden")
		}
	}
	page := s.get(p, nil)
	expectContains(t, "held comment", page, "[hidden while reports are reviewed]")
	expectMissing(t, "held comment", page, "12 Example Street")
	expectContains(t, "public log", s.get("/modlog", nil), "Comment hidden until reviewed")
}

func TestReportQueue(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	reader := s.user(t, "red-baking-hare")
	boss := admin(t, s.user(t, "silver-sleeping-fox"))
	post := s.post(t, author, "Buy my watches")
	kept := s.post(t, author, "A fine post")
	for _, p := range []string{post.PublicID, kept.PublicID} {
		s.send("/p/"+p+"/report", form("reason", "spam", "note", "ads", "form_token", s.token(formReport)), reader)
	}

	// Only the admin has a queue, and a count in the header.
	expectStatus(t, "queue, member", s.get("/admin/reports", reader), http.StatusNotFound)
	expectMissing(t, "header, member", s.get("/", reader), "to review</a>")
	expectContains(t, "header, admin", s.get("/", boss), `href="/admin/reports">2 to review</a>`)
	queue := s.get("/admin/reports", boss)
	expectStatus(t, "queue", queue, http.StatusOK)
	expectContains(t, "queue", queue, "Buy my watches")
	expectContains(t, "queue", queue, "spam × 1")
	expectContains(t, "queue", queue, "“ads”")

	act := func(p string, pairs ...string) *httptest.ResponseRecorder {
		return s.send("/admin/reports", form(append([]string{"post", p, "comment", ""}, append(pairs, "form_token", s.token(formModerate))...)...), boss)
	}
	expectContains(t, "remove without a reason", act(post.PublicID, "action", "remove"), "Pick a reason for removing it.")
	expectRedirect(t, "remove", act(post.PublicID, "action", "remove", "reason", "spam"), "/admin/reports?done=removed")
	expectRedirect(t, "dismiss", act(kept.PublicID, "action", "dismiss"), "/admin/reports?done=dismissed")
	expectContains(t, "queue empty", s.get("/admin/reports", boss), "Nothing reported.")
	expectMissing(t, "header count gone", s.get("/", boss), "to review</a>")

	// The removal shows its reason, and the log lists it (without the dismissal, publicly).
	expectContains(t, "removed post", s.get("/p/"+post.PublicID, nil), "[removed: spam]")
	expectContains(t, "kept post", s.get("/p/"+kept.PublicID, nil), "A fine post")
	public := s.get("/modlog", nil)
	expectContains(t, "public log", public, "Post removed: spam")
	expectContains(t, "public log", public, "after 1 report")
	expectMissing(t, "public log", public, "dismissed")
	expectMissing(t, "public log", public, "Buy my watches")
	expectContains(t, "admin's log", s.get("/modlog", boss), "Reports on a post dismissed")
}

func TestRemovalNeedsAReason(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	boss := admin(t, s.user(t, "silver-sleeping-fox"))
	post := s.post(t, author, "A post")
	cid := s.comment(t, post, author, "A comment")
	s.reply(t, post, cid, author, "A reply, which keeps the comment's place in the thread")
	p := "/p/" + post.PublicID

	expectStatus(t, "post, no reason", s.send(p+"/remove", form("form_token", s.token(formRemovePost)), boss), http.StatusBadRequest)
	expectStatus(t, "comment, made-up reason", s.send(p+"/c/"+cid+"/remove",
		form("reason", "boring", "form_token", s.token(formRemoveComment)), boss), http.StatusBadRequest)
	expectRedirect(t, "comment", s.send(p+"/c/"+cid+"/remove",
		form("reason", "harassment", "form_token", s.token(formRemoveComment)), boss), p)
	expectContains(t, "placeholder", s.get(p, nil), "[removed: harassment]")
	expectContains(t, "admin's view", s.get(p, boss), "removed: harassment: only you, as admin")
}

func TestRulesPage(t *testing.T) {
	s := newSite(t)
	page := s.get("/rules", nil)
	expectStatus(t, "rules", page, http.StatusOK)
	for _, anchor := range []string{`id="spam"`, `id="harassment"`, `id="personal"`, `id="illegal"`, `id="rules"`} {
		expectContains(t, "rules", page, anchor)
	}
	expectContains(t, "footer", s.get("/", nil), `href="/modlog"`)
}

// reply adds a reply under a comment. (A removed comment with no replies
// leaves the thread entirely; one with replies shows its placeholder.)
func (s *site) reply(t *testing.T, p store.Post, parent string, author *testUser, body string) string {
	t.Helper()
	cid, _, err := testStore.CreateComment(ctx, store.NewComment{PostID: p.ID, ParentPublicID: parent, AuthorID: author.id, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return cid
}

func TestErasedItemsForTheAdmin(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	boss := admin(t, s.user(t, "silver-sleeping-fox"))
	post := s.post(t, author, "Old spam")
	cid := s.comment(t, post, author, "old spam comment")
	s.reply(t, post, cid, author, "a reply that keeps it in place")
	for _, it := range []store.Item{{PostID: post.PublicID, CommentID: cid}, {PostID: post.PublicID}} {
		if err := testStore.Remove(ctx, it, "spam"); err != nil {
			t.Fatal(err)
		}
	}
	sqlExec(t, `UPDATE posts SET removed_at = now() - interval '31 days'`)
	sqlExec(t, `UPDATE comments SET removed_at = now() - interval '31 days' WHERE public_id = $1`, cid)
	if _, err := testStore.Purge(ctx); err != nil {
		t.Fatal(err)
	}

	page := s.get("/p/"+post.PublicID, boss)
	expectContains(t, "admin, post", page, "Its text was erased 30 days after removal.")
	expectContains(t, "admin, comment", page, "removed: spam, text erased after 30 days")
	expectMissing(t, "admin", page, "Old spam")
	expectMissing(t, "admin", page, "old spam comment")
	expectContains(t, "visitor", s.get("/p/"+post.PublicID, nil), "[removed: spam]")
	expectContains(t, "rules", s.get("/rules", nil), "keeps its text for 30 days")
}

func TestAboutPage(t *testing.T) {
	s := newSite(t)
	page := s.get("/about", nil)
	expectStatus(t, "about", page, http.StatusOK)
	expectContains(t, "about", page, "What Snowball knows about you")
	expectContains(t, "sidebar link works", s.get("/", nil), `href="/about"`)
}
