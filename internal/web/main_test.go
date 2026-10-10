package web

// Request-level tests: setup and helpers.
//
// These drive the COMPLETE site handler (security headers, CSRF protection,
// sessions, rate limits, then the route) with fake requests from Go's
// httptest package. No browser, no network: a request goes in, a recorded
// response comes out.
//
// They use their own database, snowball_web_test, so they can run alongside
// the store package's tests without emptying each other's tables.
//
// Run them with:  go test ./internal/web
// Skip them with: go test -short ./...

import (
	"context"
	"flag"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"snowball/internal/auth"
	"snowball/internal/store"
	"snowball/internal/testdb"
)

var (
	testStore *store.Store  // what the site under test uses
	rawDB     *pgxpool.Pool // for arranging scenes directly in SQL
	ctx       = context.Background()

	// Every test user gets this password, hashed once up front, since
	// argon2 is deliberately slow.
	testPassword     = "Correct-Horse-7!"
	testPasswordHash string
)

func TestMain(m *testing.M) {
	flag.Parse()

	// Templates and static files are found relative to the project root,
	// as when the server runs, so start there.
	root, err := testdb.Root()
	if err == nil {
		err = os.Chdir(root)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "can't find the project root: %v\n", err)
		os.Exit(1)
	}

	if testing.Short() {
		os.Exit(m.Run()) // request-level tests skip themselves
	}

	if err := setup(); err != nil {
		fmt.Fprintf(os.Stderr, "\ncan't set up the web test database: %v\n"+
			"Is Postgres running? Try: docker compose up -d\n"+
			"Or skip the database tests with: go test -short ./...\n\n", err)
		os.Exit(1)
	}
	code := m.Run()
	testStore.Close()
	rawDB.Close()
	os.Exit(code)
}

func setup() error {
	dbURL, err := testdb.Prepare("snowball_web_test")
	if err != nil {
		return err
	}
	if testStore, err = store.Open(ctx, dbURL); err != nil {
		return err
	}
	if rawDB, err = pgxpool.New(ctx, dbURL); err != nil {
		return err
	}
	testPasswordHash, err = auth.HashPassword(testPassword)
	return err
}

// ---- a fresh site per test ----

// site is a running copy of Snowball, minus the network.
type site struct {
	srv *Server
	h   http.Handler
}

// newSite empties the database and builds a brand-new server, so each test
// starts with no data and untouched rate limits.
func newSite(t *testing.T) *site {
	t.Helper()
	if testStore == nil {
		t.Skip("request-level tests skipped (-short)")
	}
	sqlExec(t, `TRUNCATE `+testdb.Tables+` RESTART IDENTITY CASCADE`)
	srv, err := New(testStore, Config{VoteSecret: strings.Repeat("test-vote-secret-", 3)})
	if err != nil {
		t.Fatal(err)
	}
	return &site{srv: srv, h: srv.Handler()}
}

func sqlExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := rawDB.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("sql %q: %v", sql, err)
	}
}

// ---- users ----

type testUser struct {
	id     int64
	name   string
	cookie *http.Cookie
}

// user creates an account and logs it in by creating a session directly
// (the login flow itself has its own tests). Accounts start a day old, so
// they can vote; use young() for a brand-new one.
func (s *site) user(t *testing.T, name string) *testUser {
	t.Helper()
	id, err := testStore.CreateUser(ctx, name, testPasswordHash,
		[]string{auth.HashRecoveryCode("AAAA-BBBB-CCCC")})
	if err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	sqlExec(t, `UPDATE users SET created_at = now() - interval '2 days' WHERE id = $1`, id)
	return &testUser{id: id, name: name, cookie: s.session(t, id)}
}

// session logs a user in on another "device", returning its cookie.
func (s *site) session(t *testing.T, userID int64) *http.Cookie {
	t.Helper()
	token, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := testStore.CreateSession(ctx, userID, auth.HashToken(token), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: token}
}

func young(t *testing.T, u *testUser) *testUser {
	t.Helper()
	sqlExec(t, `UPDATE users SET created_at = now() WHERE id = $1`, u.id)
	return u
}

func admin(t *testing.T, u *testUser) *testUser {
	t.Helper()
	sqlExec(t, `UPDATE users SET is_admin = true WHERE id = $1`, u.id)
	return u
}

// ---- content ----

func (s *site) post(t *testing.T, author *testUser, title string) store.Post {
	t.Helper()
	tags, err := testStore.Tags(ctx)
	if err != nil || len(tags) == 0 {
		t.Fatalf("no tags: %v", err)
	}
	pid, err := testStore.CreatePost(ctx, store.NewPost{
		AuthorID: author.id, Title: title, Body: "Body of " + title, TagIDs: []int{tags[0].ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := testStore.PostByPublicID(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (s *site) comment(t *testing.T, p store.Post, author *testUser, body string) string {
	t.Helper()
	cid, _, err := testStore.CreateComment(ctx, store.NewComment{PostID: p.ID, AuthorID: author.id, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return cid
}

// ---- requests ----

// token is a valid form token for a form, issued an hour ago: a patient
// visitor, so the "submitted too fast" check never gets in the way.
func (s *site) token(f formRule) string {
	return s.srv.forms.Issue(f.name, time.Now().Add(-time.Hour))
}

// form builds form values from pairs: form("title", "Hi", "tag", "1").
func form(pairs ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		v.Add(pairs[i], pairs[i+1])
	}
	return v
}

// do sends one request through the whole site. u may be nil (logged out).
// headers are extra name/value pairs, like "HX-Request", "true".
func (s *site) do(method, path string, values url.Values, u *testUser, headers ...string) *httptest.ResponseRecorder {
	var body *strings.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	} else {
		body = strings.NewReader("")
	}
	// An https URL marks the request as arriving over TLS, as in production.
	r := httptest.NewRequest(method, "https://snowball.test"+path, body)
	if values != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if u != nil {
		r.AddCookie(u.cookie)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	return rec
}

func (s *site) get(path string, u *testUser, headers ...string) *httptest.ResponseRecorder {
	return s.do("GET", path, nil, u, headers...)
}

func (s *site) send(path string, values url.Values, u *testUser, headers ...string) *httptest.ResponseRecorder {
	return s.do("POST", path, values, u, headers...)
}

// ---- checks ----

// text is the response body with HTML escapes undone ("don&#39;t" -> "don't"),
// for searching for messages.
func text(rec *httptest.ResponseRecorder) string {
	return html.UnescapeString(rec.Body.String())
}

func expectStatus(t *testing.T, what string, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		snippet := rec.Body.String()
		if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}
		t.Fatalf("%s: status %d, want %d\nbody: %s", what, rec.Code, want, snippet)
	}
}

func expectRedirect(t *testing.T, what string, rec *httptest.ResponseRecorder, wantPrefix string) {
	t.Helper()
	expectStatus(t, what, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, wantPrefix) {
		t.Fatalf("%s: redirected to %q, want it to start with %q", what, loc, wantPrefix)
	}
}

func expectContains(t *testing.T, what string, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if !strings.Contains(text(rec), want) {
		t.Fatalf("%s: page doesn't contain %q", what, want)
	}
}

func expectMissing(t *testing.T, what string, rec *httptest.ResponseRecorder, unwanted string) {
	t.Helper()
	if strings.Contains(text(rec), unwanted) {
		t.Fatalf("%s: page contains %q, which it mustn't", what, unwanted)
	}
}

// sessionCookieFrom returns the session cookie a response set, if any.
func sessionCookieFrom(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return c
		}
	}
	return nil
}

func itoa(n int) string { return strconv.Itoa(n) }
