package web

// Request-level tests for suspensions and appeals (moderation slice 2).

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// suspend suspends a test user directly in the store.
func suspend(t *testing.T, u *testUser, reason string, days int) {
	t.Helper()
	if err := testStore.Suspend(ctx, u.id, reason, "Take a break.", days); err != nil {
		t.Fatal(err)
	}
}

func TestSuspendedAccountsAreReadOnly(t *testing.T) {
	s := newSite(t)
	me := s.user(t, "blue-humming-otter")
	other := s.user(t, "red-baking-hare")
	mine := s.post(t, me, "Mine")
	theirs := s.post(t, other, "Theirs")
	cid := s.comment(t, theirs, other, "Their comment")
	suspend(t, me, "harassment", 7)
	p := "/p/" + theirs.PublicID

	// The banner, on every page.
	front := s.get("/", me)
	expectContains(t, "banner", front, "suspended until")
	expectContains(t, "banner", front, "(harassment)")
	expectContains(t, "banner", front, `href="/account/suspension"`)

	// No controls for writing on the pages.
	page := s.get(p, me)
	expectMissing(t, "no report link", page, "/report")
	expectMissing(t, "no reply link", page, "/c/"+cid+"/reply")
	expectContains(t, "no comment form", page, "can't comment for now")
	expectContains(t, "vote arrows explain", page, "Your account is suspended, so it can't vote for now.")

	// And the routes refuse, whatever the page showed.
	expectRedirect(t, "submit page", s.get("/submit", me), "/account/suspension")
	expectRedirect(t, "submit", s.send("/submit", form("title", "x", "topics", "technology", "form_token", s.token(formSubmit)), me), "/account/suspension")
	expectRedirect(t, "comment", s.send(p+"/comment", form("body", "hi", "form_token", s.token(formComment)), me), "/account/suspension")
	expectRedirect(t, "report", s.get(p+"/report", me), "/account/suspension")
	expectRedirect(t, "edit", s.get("/p/"+mine.PublicID+"/edit", me), "/account/suspension")
	expectStatus(t, "vote (htmx)", s.send("/vote", form("kind", "post", "id", theirs.PublicID, "dir", "up",
		"form_token", s.token(formVote)), me, "HX-Request", "true"), http.StatusForbidden)

	// Reading and deleting their own things still work.
	expectStatus(t, "reading", s.get(p, me), http.StatusOK)
	expectRedirect(t, "deleting their own post", s.send("/p/"+mine.PublicID+"/delete",
		form("form_token", s.token(formDeleteItem), "back", "/account/history"), me), "/account/history")

	// Others are unaffected.
	expectContains(t, "others can still comment", s.get(p, other), "<textarea")
	expectMissing(t, "others: no banner", s.get("/", other), "suspended until")
}

func TestSuspensionPageAndAppeal(t *testing.T) {
	s := newSite(t)
	me := s.user(t, "blue-humming-otter")
	boss := admin(t, s.user(t, "silver-sleeping-fox"))

	expectRedirect(t, "not suspended", s.get("/account/suspension", me), "/account")
	suspend(t, me, "spam", 0)
	page := s.get("/account/suspension", me)
	expectStatus(t, "suspension page", page, http.StatusOK)
	expectContains(t, "suspension page", page, "Your account is banned")
	expectContains(t, "suspension page", page, `href="/rules#spam"`)
	expectContains(t, "suspension page", page, "Take a break.")

	appeal := func(text string) *httptest.ResponseRecorder {
		return s.send("/account/suspension", form("appeal", text, "form_token", s.token(formAppeal)), me)
	}
	expectContains(t, "too short", appeal("no"), "Say a little about why")
	expectContains(t, "sent", appeal("I posted my own shop once, I didn't know."), "Your appeal has been sent.")
	expectContains(t, "waiting", s.get("/account/suspension", me), "waiting for the admin")
	expectContains(t, "again", appeal("Please, again."), "waiting for the admin")

	// The admin sees it, counted in the header, and turns it down.
	expectContains(t, "header", s.get("/", boss), "1 to review")
	queue := s.get("/admin/reports", boss)
	expectContains(t, "queue", queue, "I posted my own shop once")
	expectContains(t, "queue", queue, `href="/admin/users/blue-humming-otter"`)
	sus, _ := testStore.ActiveSuspension(ctx, me.id)
	decide := func(action string) *httptest.ResponseRecorder {
		return s.send("/admin/appeals", form("suspension", itoa(sus.ID), "action", action, "form_token", s.token(formModerate)), boss)
	}
	expectStatus(t, "member deciding", s.send("/admin/appeals", form("suspension", itoa(sus.ID), "action", "lift",
		"form_token", s.token(formModerate)), me), http.StatusNotFound)
	expectRedirect(t, "keep", decide("keep"), "/admin/reports?done=kept")
	expectContains(t, "turned down", s.get("/account/suspension", me), "the suspension stands")
	expectContains(t, "deciding twice", decide("keep"), "already been dealt with")
	expectMissing(t, "public log", s.get("/modlog", nil), "Appeal turned down")
	expectContains(t, "public log", s.get("/modlog", nil), "Account banned: spam")
}

func TestAdminSuspendsAndLifts(t *testing.T) {
	s := newSite(t)
	me := s.user(t, "blue-humming-otter")
	boss := admin(t, s.user(t, "silver-sleeping-fox"))
	s.post(t, me, "Something I wrote")
	page := "/admin/users/blue-humming-otter"

	expectStatus(t, "member", s.get(page, me), http.StatusNotFound)
	view := s.get(page, boss)
	expectStatus(t, "admin", view, http.StatusOK)
	expectContains(t, "admin", view, "Something I wrote")
	expectRedirect(t, "lookup form", s.get("/admin/users?name=blue-humming-otter", boss), page)
	expectStatus(t, "no such account", s.get("/admin/users/nobody-here-at-all", boss), http.StatusNotFound)

	send := func(path string, pairs ...string) *httptest.ResponseRecorder {
		return s.send(path, form(append(pairs, "form_token", s.token(formModerate))...), boss)
	}
	expectContains(t, "no length", send(page+"/suspend", "reason", "spam"), "Pick how long")
	expectRedirect(t, "suspend", send(page+"/suspend", "reason", "harassment", "days", "7", "note", "Cool off."), page+"?done=suspended")
	expectContains(t, "suspended", s.get(page, boss), "Suspended now")
	expectContains(t, "they see it", s.get("/", me), "suspended until")
	expectContains(t, "public log", s.get("/modlog", nil), "Account suspended for 7 days: harassment")
	expectContains(t, "the admin can't be", send("/admin/users/silver-sleeping-fox/suspend", "reason", "spam", "days", "1"),
		"The admin account can't be suspended.")
	expectMissing(t, "no suspend form for the admin", s.get("/admin/users/silver-sleeping-fox", boss), `action="/admin/users/silver-sleeping-fox/suspend"`)

	sus, _ := testStore.ActiveSuspension(ctx, me.id)
	expectRedirect(t, "lift", send(page+"/lift", "suspension", itoa(sus.ID)), page+"?done=lifted")
	expectMissing(t, "lifted", s.get("/", me), "suspended until")
	expectContains(t, "public log", s.get("/modlog", nil), "Suspension lifted")
}

func TestRemoveAndSuspendFromTheQueue(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	reader := s.user(t, "red-baking-hare")
	boss := admin(t, s.user(t, "silver-sleeping-fox"))
	post := s.post(t, author, "Buy my watches")
	s.send("/p/"+post.PublicID+"/report", form("reason", "spam", "form_token", s.token(formReport)), reader)

	rec := s.send("/admin/reports", form("post", post.PublicID, "comment", "", "author", "blue-humming-otter",
		"action", "remove", "reason", "spam", "days", "30", "form_token", s.token(formModerate)), boss)
	expectRedirect(t, "remove and suspend", rec, "/admin/reports?done=suspended")
	sus, err := testStore.ActiveSuspension(ctx, author.id)
	if err != nil || sus == nil || sus.Reason != "spam" {
		t.Fatalf("the author should be suspended for spam: %+v, %v", sus, err)
	}
	if p, _, _ := testStore.PostByPublicID(ctx, post.PublicID); !p.Removed {
		t.Error("the post should be removed too")
	}
}
