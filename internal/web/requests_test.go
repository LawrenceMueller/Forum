package web

// Request-level tests of the flows and, above all, the permission
// boundaries: the things someone probing the site would try.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- logging in and out ----

func TestLoginFlow(t *testing.T) {
	s := newSite(t)
	s.user(t, "blue-humming-otter")
	tok := s.token(formLogin)

	wrong := s.send("/login", form("username", "blue-humming-otter", "password", "nope", "form_token", tok), nil)
	expectStatus(t, "wrong password", wrong, http.StatusOK)
	expectContains(t, "wrong password", wrong, "That name and password don't match.")
	if sessionCookieFrom(wrong) != nil {
		t.Fatal("a wrong password must not log anyone in")
	}

	// Names are forgiving about case and spaces.
	ok := s.send("/login", form("username", " Blue-Humming-Otter ", "password", testPassword, "form_token", tok), nil)
	expectRedirect(t, "right password", ok, "/")
	cookie := sessionCookieFrom(ok)
	if cookie == nil {
		t.Fatal("logging in should set the session cookie")
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.Path != "/" {
		t.Errorf("session cookie flags: HttpOnly=%v Secure=%v Path=%q", cookie.HttpOnly, cookie.Secure, cookie.Path)
	}

	// The new session works.
	me := &testUser{name: "blue-humming-otter", cookie: cookie}
	expectStatus(t, "account page with the new session", s.get("/account", me), http.StatusOK)
}

func TestLoginBotChecks(t *testing.T) {
	s := newSite(t)
	s.user(t, "blue-humming-otter")

	honeypot := s.send("/login", form("username", "blue-humming-otter", "password", testPassword,
		"form_token", s.token(formLogin), "homepage", "http://spam.example"), nil)
	expectRedirect(t, "honeypot filled", honeypot, "/login")

	noToken := s.send("/login", form("username", "blue-humming-otter", "password", testPassword), nil)
	expectRedirect(t, "no form token", noToken, "/login")

	wrongForm := s.send("/login", form("username", "blue-humming-otter", "password", testPassword,
		"form_token", s.token(formSignup)), nil)
	expectContains(t, "a token from another form", wrongForm, "This form expired")

	if sessionCookieFrom(honeypot) != nil || sessionCookieFrom(noToken) != nil || sessionCookieFrom(wrongForm) != nil {
		t.Fatal("bot-like submissions must never log anyone in, even with the right password")
	}
}

func TestLogoutEndsEverySession(t *testing.T) {
	s := newSite(t)
	me := s.user(t, "blue-humming-otter")
	phone := &testUser{name: me.name, cookie: s.session(t, me.id)}

	expectRedirect(t, "log out", s.send("/logout", form(), me), "/")
	expectRedirect(t, "the other device afterwards", s.get("/account", phone), "/login")
}

// ---- signing up ----

func TestSignupFlow(t *testing.T) {
	s := newSite(t)
	name := "green-napping-puffin"

	rec := s.send("/signup", form(
		"username", name, "sig", s.srv.names.Sign(name),
		"password", testPassword, "confirm", testPassword,
		"ack_recovery", "yes", "ack_username", "yes",
		"form_token", s.token(formSignup),
	), nil)
	expectStatus(t, "sign up", rec, http.StatusOK)
	if n := strings.Count(rec.Body.String(), "<code>"); n != 8 {
		t.Errorf("the welcome page should show 8 recovery codes, found %d", n)
	}
	if taken, _ := testStore.UsernameTaken(ctx, name); !taken {
		t.Error("the account wasn't created")
	}
}

func TestSignupRefusesAForgedName(t *testing.T) {
	s := newSite(t)
	rec := s.send("/signup", form(
		"username", "red-sailing-ocelot", "sig", "a-signature-i-made-up",
		"password", testPassword, "confirm", testPassword,
		"ack_recovery", "yes", "ack_username", "yes",
		"form_token", s.token(formSignup),
	), nil)
	expectContains(t, "forged name", rec, "That name didn't come from us")
	if taken, _ := testStore.UsernameTaken(ctx, "red-sailing-ocelot"); taken {
		t.Error("a name with a forged signature must not become an account")
	}
}

// ---- the doors that should be closed ----

func TestLoggedOutVisitorsAreSentToLogin(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	post := s.post(t, author, "A post")

	for _, path := range []string{"/submit", "/p/" + post.PublicID + "/comment", "/account/password",
		"/account/delete", "/vote"} {
		expectRedirect(t, "POST "+path+" logged out", s.send(path, form(), nil), "/login")
	}

	// htmx can't follow an ordinary redirect for a whole page, so it's told
	// to load the login page with a header instead.
	rec := s.send("/vote", form(), nil, "HX-Request", "true")
	if got := rec.Header().Get("HX-Redirect"); got != "/login" {
		t.Errorf("htmx vote while logged out: HX-Redirect = %q, want /login", got)
	}
}

func TestCrossSiteFormsAreRefused(t *testing.T) {
	s := newSite(t)
	s.user(t, "blue-humming-otter")
	values := form("username", "blue-humming-otter", "password", testPassword, "form_token", s.token(formLogin))

	// A modern browser says where a form came from...
	expectStatus(t, "cross-site POST (Sec-Fetch-Site)", s.send("/login", values, nil, "Sec-Fetch-Site", "cross-site"), http.StatusForbidden)
	// ...and older ones send the Origin of the page that sent it.
	expectStatus(t, "cross-site POST (Origin)", s.send("/login", values, nil, "Origin", "https://evil.example"), http.StatusForbidden)
	// The same form from Snowball itself is fine.
	expectRedirect(t, "same-site POST", s.send("/login", values, nil, "Sec-Fetch-Site", "same-origin"), "/")
}

func TestNobodyEditsOrDeletesSomeoneElsesWork(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	other := s.user(t, "red-baking-hare")
	post := s.post(t, author, "Mine")
	cid := s.comment(t, post, author, "My comment")
	p := "/p/" + post.PublicID
	c := p + "/c/" + cid

	// Their edit pages don't even admit to existing.
	expectStatus(t, "edit page, someone else's post", s.get(p+"/edit", other), http.StatusNotFound)
	expectStatus(t, "edit page, someone else's comment", s.get(c+"/edit", other), http.StatusNotFound)

	expectStatus(t, "edit someone else's post", s.send(p+"/edit",
		form("body", "hijacked", "tag", "1", "form_token", s.token(formEditPost)), other), http.StatusNotFound)
	expectStatus(t, "edit someone else's comment", s.send(c+"/edit",
		form("body", "hijacked", "form_token", s.token(formEditComment)), other), http.StatusNotFound)
	expectStatus(t, "delete someone else's post", s.send(p+"/delete",
		form("form_token", s.token(formDeleteItem), "back", "/"), other), http.StatusNotFound)
	expectStatus(t, "delete someone else's comment", s.send(c+"/delete",
		form("form_token", s.token(formDeleteItem), "back", "/"), other), http.StatusNotFound)

	after, _, _ := testStore.PostByPublicID(ctx, post.PublicID)
	if after.Body != "Body of Mine" || after.Deleted {
		t.Errorf("the post changed: body %q, deleted %v", after.Body, after.Deleted)
	}
	expectContains(t, "the comment", s.get(p, nil), "My comment")

	// The author can, though.
	expectRedirect(t, "delete my own post", s.send(p+"/delete",
		form("form_token", s.token(formDeleteItem), "back", "/account/history"), author), "/account/history")
}

func TestAdminEndpointsHideFromEveryoneElse(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	boss := admin(t, s.user(t, "green-napping-puffin"))
	post := s.post(t, author, "Remove me")
	cid := s.comment(t, post, author, "Remove me too")
	p := "/p/" + post.PublicID

	postForm := form("form_token", s.token(formRemovePost))
	commentForm := form("form_token", s.token(formRemoveComment))

	// To anyone but the admin, these addresses are simply not there.
	for _, u := range []*testUser{nil, author} {
		expectStatus(t, "remove post (not admin)", s.send(p+"/remove", postForm, u), http.StatusNotFound)
		expectStatus(t, "remove comment (not admin)", s.send(p+"/c/"+cid+"/remove", commentForm, u), http.StatusNotFound)
	}
	if p, _, _ := testStore.PostByPublicID(ctx, post.PublicID); p.Removed {
		t.Fatal("a non-admin removed a post")
	}

	expectRedirect(t, "admin removes the comment", s.send(p+"/c/"+cid+"/remove", commentForm, boss), p)
	expectRedirect(t, "admin removes the post", s.send(p+"/remove", postForm, boss), p)
	if p, _, _ := testStore.PostByPublicID(ctx, post.PublicID); !p.Removed {
		t.Fatal("the admin's removal didn't take")
	}
}

// ---- posting, commenting, voting ----

func TestSubmitAPost(t *testing.T) {
	s := newSite(t)
	me := s.user(t, "blue-humming-otter")
	tags, _ := testStore.Tags(ctx)

	noTag := s.send("/submit", form("title", "Untagged", "body", "hi", "form_token", s.token(formSubmit)), me)
	expectContains(t, "no tag", noTag, "Pick at least one tag.")

	rec := s.send("/submit", form("title", "A brand new post", "body", "**bold** words",
		"tag", itoa(tags[0].ID), "form_token", s.token(formSubmit)), me)
	expectRedirect(t, "submit", rec, "/p/")

	page := s.get(rec.Header().Get("Location"), nil)
	expectStatus(t, "the new post", page, http.StatusOK)
	expectContains(t, "the new post", page, "A brand new post")
	if !strings.Contains(page.Body.String(), "<strong>bold</strong>") {
		t.Error("the body's Markdown should be rendered")
	}
}

func TestCommentAndReply(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	me := s.user(t, "red-baking-hare")
	post := s.post(t, author, "Talk about this")
	p := "/p/" + post.PublicID

	empty := s.send(p+"/comment", form("body", "   ", "form_token", s.token(formComment)), me)
	expectContains(t, "empty comment", empty, "Write something first.")

	rec := s.send(p+"/comment", form("body", "First!", "form_token", s.token(formComment)), me)
	expectRedirect(t, "comment", rec, p+"#c-")
	parent := strings.TrimPrefix(rec.Header().Get("Location"), p+"#c-")

	reply := s.send(p+"/comment", form("body", "A reply", "parent", parent, "form_token", s.token(formComment)), author)
	expectRedirect(t, "reply", reply, p+"#c-")

	page := s.get(p, nil)
	expectContains(t, "thread", page, "First!")
	expectContains(t, "thread", page, "A reply")
}

func TestVoting(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	voter := s.user(t, "red-baking-hare")
	newbie := young(t, s.user(t, "green-napping-puffin"))
	post := s.post(t, author, "Vote on me")

	vote := func(u *testUser, back string, headers ...string) *httptest.ResponseRecorder {
		return s.send("/vote", form("kind", "post", "id", post.PublicID, "dir", "up",
			"back", back, "form_token", s.token(formVote)), u, headers...)
	}

	// With htmx: back comes just the arrows, with the new score.
	rec := vote(voter, "/", "HX-Request", "true")
	expectStatus(t, "htmx vote", rec, http.StatusOK)
	if body := rec.Body.String(); strings.Contains(body, "<html") || !strings.Contains(body, `class="score">1<`) {
		t.Errorf("htmx vote should return just the arrows with score 1, got: %s", body)
	}

	// The rules, explained under the arrows.
	expectContains(t, "own post", vote(author, "/", "HX-Request", "true"), "You can't vote on your own.")
	expectContains(t, "brand-new account", vote(newbie, "/", "HX-Request", "true"), "New accounts can vote after their first day.")
	if p, _, _ := testStore.PostByPublicID(ctx, post.PublicID); p.Score != 1 {
		t.Errorf("score %d, want 1: refused votes must not count", p.Score)
	}

	// Without JavaScript: back to the page it came from...
	expectRedirect(t, "plain vote", vote(voter, "/new"), "/new")
	// ...but never to another site, however the address is disguised.
	for _, sneaky := range []string{"https://evil.example", "//evil.example", "/\t/evil.example", `/\evil.example`} {
		rec := vote(voter, sneaky)
		if loc := rec.Header().Get("Location"); loc != "/" {
			t.Errorf("back=%q redirected to %q, want /", sneaky, loc)
		}
	}
}
