package web

// The smoke test: load every page, as every kind of visitor, and check each
// one renders. Template mistakes (a misspelled field, a bad pipeline) only
// show up when a page is actually rendered, so without this, the first to
// find a broken page would be whoever visits it.

import (
	"net/http"
	"strings"
	"testing"

	"snowball/internal/store"
)

// Headers every response must carry, whatever it is (see headers.go).
var requiredHeaders = []string{
	"Content-Security-Policy",
	"X-Content-Type-Options",
	"X-Frame-Options",
	"Referrer-Policy",
	"X-Robots-Tag",
}

func TestSmokeEveryPage(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	reader := s.user(t, "red-baking-hare")
	boss := admin(t, s.user(t, "green-napping-puffin"))

	post := s.post(t, author, "A post worth reading")
	cid := s.comment(t, post, reader, "A comment worth reading")
	removed := s.post(t, author, "A removed post")
	if err := testStore.Remove(ctx, store.Item{PostID: removed.PublicID}, "spam"); err != nil {
		t.Fatal(err)
	}

	// Votes in both directions, so the voted history views have arrows to draw.
	if _, _, err := testStore.Vote(ctx, store.VotePost, post.PublicID, s.srv.voters.Voter(reader.id), reader.id, 1); err != nil {
		t.Fatal(err)
	}
	disliked := s.post(t, author, "A post worth disliking")
	if _, _, err := testStore.Vote(ctx, store.VotePost, disliked.PublicID, s.srv.voters.Voter(reader.id), reader.id, -1); err != nil {
		t.Fatal(err)
	}

	p := "/p/" + post.PublicID
	c := p + "/c/" + cid

	pages := []struct {
		path   string
		who    *testUser
		status int
		kind   string // "page" (full HTML page), "fragment" (htmx piece), or "other"
	}{
		// Logged out.
		{"/", nil, 200, "page"},
		{"/new", nil, 200, "page"},
		{"/active", nil, 200, "page"},
		{"/top", nil, 200, "page"},
		{"/tags", nil, 200, "page"},
		{"/t/technology", nil, 200, "page"},
		{"/t/bread?only=1", nil, 200, "page"},
		{"/t/question", nil, 200, "page"},
		{"/t/football", nil, 200, "page"}, // means two tags: asks which
		{"/t/food", nil, 301, "other"},    // an old name: goes to food-and-drink
		{p, nil, 200, "page"},
		{p + "?sort=new", nil, 200, "page"},
		{c, nil, 200, "page"},
		{"/p/" + removed.PublicID, nil, 200, "page"},
		{"/signup", nil, 200, "page"},
		{"/login", nil, 200, "page"},
		{"/login?notice=password-changed", nil, 200, "page"},
		{"/recover", nil, 200, "page"},
		{"/goodbye", nil, 200, "page"},
		{"/rules", nil, 200, "page"},
		{"/notes", nil, 200, "page"},
		{"/modlog", nil, 200, "page"},
		{"/robots.txt", nil, 200, "other"},
		{"/static/css/style.css", nil, 200, "other"},

		// Logged in.
		{"/", reader, 200, "page"},
		{p, reader, 200, "page"},
		{"/submit", reader, 200, "page"},
		{"/account", reader, 200, "page"},
		{"/account/history", reader, 200, "page"},
		{"/account/replies", reader, 200, "page"},
		{"/account/history?show=comments", reader, 200, "page"},
		{"/account/history?show=upvoted", reader, 200, "page"},
		{"/account/history?show=downvoted", reader, 200, "page"},
		{"/recovered", reader, 200, "page"},
		{c + "/reply", reader, 200, "page"},
		{c + "/edit", reader, 200, "page"}, // reader wrote the comment
		{p + "/edit", author, 200, "page"},
		{p + "/report", reader, 200, "page"},
		{c + "/report", author, 200, "page"},

		// The admin's views, including what only the admin sees.
		{p, boss, 200, "page"},
		{"/p/" + removed.PublicID, boss, 200, "page"},
		{"/account", boss, 200, "page"},
		{"/admin/tags", boss, 200, "page"},
		{"/admin/reports", boss, 200, "page"},
		{"/admin/domains", boss, 200, "page"},
		{"/admin/notes", boss, 200, "page"},
		{"/admin/users/blue-humming-otter", boss, 200, "page"},
		{"/account/suspension", reader, 303, "other"}, // not suspended: back to the account page
		{"/modlog", boss, 200, "page"},
		{"/admin/tags?q=bread", boss, 200, "page"},
		{"/admin/tags/bread", boss, 200, "page"},
		{"/admin/tags/question", boss, 200, "page"}, // a format: no parents section
		{"/admin/tags/food", boss, 303, "other"},    // an old name: to food-and-drink's page

		// Admin pages don't exist for anyone else.
		{"/admin/tags", reader, 404, "other"},
		{"/admin/reports", reader, 404, "other"},
		{"/admin/domains", reader, 404, "other"},
		{"/admin/users/blue-humming-otter", reader, 404, "other"},
		{"/admin/tags", nil, 404, "other"},

		// Logged out visitors are sent to log in.
		{"/submit", nil, 303, "other"},
		{"/account", nil, 303, "other"},
		{"/account/history", nil, 303, "other"},
		{"/account/replies", nil, 303, "other"},
		{"/recovered", nil, 303, "other"},

		// Logged-in visitors don't need these.
		{"/signup", reader, 303, "other"},
		{"/login", reader, 303, "other"},

		// Things that don't exist.
		{"/p/zzzzzzzz", nil, 404, "other"},
		{"/p/NOT-AN-ID", nil, 404, "other"},
		{"/t/no-such-tag", nil, 404, "other"},
		{"/no/such/page", nil, 404, "other"},
	}

	for _, pg := range pages {
		who := "logged out"
		if pg.who != nil {
			who = pg.who.name
		}
		t.Run(pg.path+" as "+who, func(t *testing.T) {
			rec := s.get(pg.path, pg.who)
			expectStatus(t, "GET "+pg.path, rec, pg.status)

			for _, h := range requiredHeaders {
				if rec.Header().Get(h) == "" {
					t.Errorf("missing security header %s", h)
				}
			}

			if pg.kind == "page" {
				body := rec.Body.String()
				if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
					t.Errorf("Content-Type %q, want text/html", ct)
				}
				if !strings.Contains(body, "</html>") {
					t.Error("the page stops before </html>: a template probably failed partway")
				}
				if strings.Contains(body, "something broke") {
					t.Error("the page shows a server error")
				}
			}

			// Pages for a logged-in user carry their name, so must never be cached.
			if pg.who != nil && !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
				t.Error("a logged-in response must have Cache-Control: no-store")
			}
		})
	}

	// A removed post shows its title only to the admin.
	expectMissing(t, "removed post, logged out", s.get("/p/"+removed.PublicID, nil), "A removed post")
	expectContains(t, "removed post, logged out", s.get("/p/"+removed.PublicID, nil), "[removed: spam]")
	expectContains(t, "removed post, admin", s.get("/p/"+removed.PublicID, boss), "A removed post")
}

// htmx endpoints return a piece of a page, never a whole one.
func TestSmokeFragments(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	reader := s.user(t, "red-baking-hare")
	post := s.post(t, author, "A post")
	cid := s.comment(t, post, reader, "A comment")

	for _, f := range []struct {
		path string
		who  *testUser
		want string
	}{
		{"/p/" + post.PublicID + "/c/" + cid + "/reply", reader, "<textarea"},
		{"/p/" + post.PublicID + "/c/" + cid + "/edit", reader, "<textarea"},
		{"/signup/name", nil, `name="sig"`},
		{"/tags/suggest?topics=sour", nil, "choose=sourdough"},
		{"/tags/pick?choose=sourdough&topics=sour", nil, `id="topics"`},
	} {
		rec := s.get(f.path, f.who, "HX-Request", "true")
		expectStatus(t, "htmx GET "+f.path, rec, http.StatusOK)
		if body := rec.Body.String(); strings.Contains(body, "<html") || !strings.Contains(body, f.want) {
			t.Errorf("htmx GET %s should return just a fragment containing %s", f.path, f.want)
		}
	}
}
