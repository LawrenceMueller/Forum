package web

// Tests for moderation slice 4: probation, duplicates, the domain blocklist.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// veteran makes a test user old enough to be past probation.
func veteran(t *testing.T, u *testUser) *testUser {
	t.Helper()
	sqlExec(t, `UPDATE users SET created_at = now() - interval '4 days' WHERE id = $1`, u.id)
	return u
}

func TestNormalizeDomain(t *testing.T) {
	for typed, want := range map[string]string{
		"example.com":                   "example.com",
		"  https://www.Example.COM/a/b": "example.com",
		"http://sub.spam.example:8080":  "sub.spam.example",
		"xn--bcher-kva.example":         "xn--bcher-kva.example",
		"localhost":                     "",
		"not a domain":                  "",
		"":                              "",
		"javascript:alert(1)":           "",
	} {
		if got := normalizeDomain(typed); got != want {
			t.Errorf("normalizeDomain(%q) = %q, want %q", typed, got, want)
		}
	}
}

func TestProbationKeepsLinksOut(t *testing.T) {
	s := newSite(t)
	newbie := s.user(t, "blue-humming-otter") // two days old: still on probation
	old := veteran(t, s.user(t, "red-baking-hare"))

	submit := func(u *testUser, pairs ...string) *httptest.ResponseRecorder {
		return s.send("/submit", form(append(pairs, "topics", "technology", "form_token", s.token(formSubmit))...), u)
	}
	expectContains(t, "submit page hint", s.get("/submit", newbie), "links (here and in the text) unlock on")
	expectMissing(t, "no hint later", s.get("/submit", old), "unlock on")

	for name, pairs := range map[string][]string{
		"link field":    {"title", "A link", "url", "https://example.org/page"},
		"link in text":  {"title", "Text", "body", "Read https://example.org/page"},
		"bare www link": {"title", "Text", "body", "Go to www.example.org now"},
		"markdown link": {"title", "Text", "body", "[click](https://example.org)"},
	} {
		expectContains(t, name, submit(newbie, pairs...), "New accounts can share links after their first 3 days")
	}
	expectRedirect(t, "plain text is fine", submit(newbie, "title", "Just words", "body", "No links here."), "/p/")
	expectRedirect(t, "old enough", submit(old, "title", "A link", "url", "https://example.org/page"), "/p/")

	// Comments too.
	post := s.post(t, old, "A thread")
	rec := s.send("/p/"+post.PublicID+"/comment", form("body", "see https://example.org", "form_token", s.token(formComment)), newbie)
	expectContains(t, "comment link", rec, "New accounts can share links")
}

func TestProbationPostsMoreSlowly(t *testing.T) {
	s := newSite(t)
	newbie := s.user(t, "blue-humming-otter")
	for i, title := range []string{"First", "Second", "Third"} {
		rec := s.send("/submit", form("title", title, "topics", "technology", "form_token", s.token(formSubmit)), newbie)
		if i < 2 {
			expectRedirect(t, title, rec, "/p/")
		} else {
			expectContains(t, title, rec, "You've posted a lot recently.")
		}
	}
}

func TestDuplicates(t *testing.T) {
	s := newSite(t)
	me := veteran(t, s.user(t, "blue-humming-otter"))
	post := s.post(t, me, "A thread")

	submit := func() *httptest.ResponseRecorder {
		return s.send("/submit", form("title", "Same title", "body", "Same text", "topics", "technology",
			"form_token", s.token(formSubmit)), me)
	}
	expectRedirect(t, "first post", submit(), "/p/")
	expectContains(t, "same post again", submit(), "You've already posted this in the last week")

	comment := func(u *testUser, body string) *httptest.ResponseRecorder {
		return s.send("/p/"+post.PublicID+"/comment", form("body", body, "form_token", s.token(formComment)), u)
	}
	long := "This is a long enough comment to count as a duplicate when repeated."
	expectRedirect(t, "comment", comment(me, long), "/p/")
	expectContains(t, "same comment again", comment(me, long), "You've already posted that comment today.")
	expectRedirect(t, "short comment", comment(me, "Thanks!"), "/p/")
	expectRedirect(t, "short comment again", comment(me, "Thanks!"), "/p/")

	// A wave: the same long text from several accounts.
	wave := "Visit my totally real store for the cheapest watches anywhere on earth!!"
	expectRedirect(t, "wave 1", comment(veteran(t, s.user(t, "red-baking-hare")), wave), "/p/")
	expectRedirect(t, "wave 2", comment(veteran(t, s.user(t, "green-napping-puffin")), wave), "/p/")
	expectContains(t, "wave 3", comment(veteran(t, s.user(t, "purple-reading-owl")), wave), "looks like spam")
}

func TestDomainBlocklist(t *testing.T) {
	s := newSite(t)
	me := veteran(t, s.user(t, "blue-humming-otter"))
	boss := admin(t, s.user(t, "silver-sleeping-fox"))
	post := s.post(t, me, "A thread")

	expectStatus(t, "member", s.get("/admin/domains", me), http.StatusNotFound)
	change := func(pairs ...string) *httptest.ResponseRecorder {
		return s.send("/admin/domains", form(append(pairs, "form_token", s.token(formModerate))...), boss)
	}
	expectContains(t, "not a domain", change("action", "add", "domain", "nonsense"), "doesn't look like a site's address")
	expectRedirect(t, "block", change("action", "add", "domain", "https://www.Spam.example/x", "note", "watch spam"), "/admin/domains?done=added")
	expectContains(t, "already", change("action", "add", "domain", "spam.example"), "spam.example is already blocked.")
	page := s.get("/admin/domains", boss)
	expectContains(t, "list", page, "<strong>spam.example</strong>")
	expectContains(t, "list", page, "watch spam")

	submit := s.send("/submit", form("title", "Deals", "url", "https://shop.spam.example/deal", "topics", "technology",
		"form_token", s.token(formSubmit)), me)
	expectContains(t, "subdomain link", submit, "Links to spam.example aren't allowed here.")
	rec := s.send("/p/"+post.PublicID+"/comment", form("body", "[deal](http://spam.example)", "form_token", s.token(formComment)), me)
	expectContains(t, "comment link", rec, "Links to spam.example aren't allowed here.")
	// Lookalikes aren't caught.
	rec = s.send("/p/"+post.PublicID+"/comment", form("body", "see https://notspam.example", "form_token", s.token(formComment)), me)
	expectRedirect(t, "a different site", rec, "/p/")

	expectRedirect(t, "unblock", change("action", "remove", "domain", "spam.example"), "/admin/domains?done=removed")
	submit = s.send("/submit", form("title", "Deals", "url", "https://shop.spam.example/deal", "topics", "technology",
		"form_token", s.token(formSubmit)), me)
	expectRedirect(t, "after unblocking", submit, "/p/")
	if !strings.Contains(s.get("/account", boss).Body.String(), `href="/admin/domains"`) {
		t.Error("the admin's account page should link to the blocklist")
	}
}
