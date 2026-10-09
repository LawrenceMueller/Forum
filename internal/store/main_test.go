package store

// Database tests: setup shared by every test in this package that talks to
// Postgres.
//
// They run against a separate database, snowball_test, in the same Postgres
// container you develop with. Your real development data (the "snowball"
// database) is never touched. The test database is created automatically
// the first time, and every migration is applied to it before the tests run,
// so tests always see exactly the schema you've built.
//
// Run them with:   go test ./internal/store
// Skip them with:  go test -short ./...   (the quick, database-free tests still run)
//
// To point them at a different database, set SNOWBALL_TEST_DATABASE_URL.
// Its database name must end in "_test": a safety catch, since every test
// starts by emptying the tables.

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // lets goose talk to Postgres through database/sql
	"github.com/pressly/goose/v3"
)

const defaultTestDatabaseURL = "postgres://snowball:snowball-dev-only@localhost:5432/snowball_test?sslmode=disable"

// testStore is shared by every database test. nil when they're skipped (-short).
var testStore *Store

// TestMain runs once, before any test in this package. It prepares the test
// database, runs all the tests (m.Run), then cleans up.
func TestMain(m *testing.M) {
	flag.Parse() // needed before testing.Short() can be read here
	if testing.Short() {
		os.Exit(m.Run()) // database tests will skip themselves
	}

	dbURL := os.Getenv("SNOWBALL_TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = defaultTestDatabaseURL
	}

	st, err := setupTestDatabase(dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\ncan't set up the test database: %v\n"+
			"Is Postgres running? Try: docker compose up -d\n"+
			"Or skip the database tests with: go test -short ./...\n\n", err)
		os.Exit(1)
	}
	testStore = st

	code := m.Run()
	st.Close()
	os.Exit(code)
}

func setupTestDatabase(dbURL string) (*Store, error) {
	ctx := context.Background()

	u, err := url.Parse(dbURL)
	if err != nil {
		return nil, err
	}
	name := strings.TrimPrefix(u.Path, "/")
	if !strings.HasSuffix(name, "_test") {
		return nil, fmt.Errorf("refusing to use database %q: test database names must end in _test, "+
			"because every test empties its tables", name)
	}

	// Create the test database if it doesn't exist yet. That has to be done
	// from a connection to some OTHER database, so connect to "postgres",
	// which always exists.
	admin := *u
	admin.Path = "/postgres"
	conn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		return nil, err
	}
	var exists bool
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name,
	).Scan(&exists); err != nil {
		conn.Close(ctx)
		return nil, err
	}
	if !exists {
		// Database names can't be $1 placeholders; Sanitize quotes it safely.
		if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			conn.Close(ctx)
			return nil, err
		}
	}
	conn.Close(ctx)

	// Apply every migration (goose as a library, rather than the CLI).
	// Tests run with the package folder as the working directory, so the
	// migrations are two levels up.
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		return nil, err
	}
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		db.Close()
		return nil, err
	}
	if err := goose.Up(db, "../../migrations"); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrations: %w", err)
	}
	db.Close()

	return Open(ctx, dbURL)
}

// ---- helpers used by the database tests ----

// freshDB gives a test an empty database: every table emptied except the
// seeded tags. Call it first in every database test, so no test can see
// another's leftovers. It skips the test under -short.
func freshDB(t *testing.T) *Store {
	t.Helper() // failures inside a helper are reported at the caller's line
	if testStore == nil {
		t.Skip("database tests skipped (-short)")
	}
	exec(t, testStore, `TRUNCATE users, sessions, recovery_codes, posts, post_tags,
		comments, post_votes, comment_votes RESTART IDENTITY CASCADE`)
	return testStore
}

var ctx = context.Background()

// exec runs SQL directly, for arranging a scene (like aging a post).
func exec(t *testing.T, st *Store, sql string, args ...any) {
	t.Helper()
	if _, err := st.db.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// count runs a SELECT count(*) (or any single-number query) and returns it.
func count(t *testing.T, st *Store, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

// expectConstraint runs SQL that must be refused by the named constraint.
func expectConstraint(t *testing.T, st *Store, constraint, sql string, args ...any) {
	t.Helper()
	_, err := st.db.Exec(ctx, sql, args...)
	if err == nil {
		t.Fatalf("expected %s to refuse %q, but it was allowed", constraint, sql)
	}
	if !strings.Contains(err.Error(), constraint) {
		t.Fatalf("expected %s to refuse %q, got a different error: %v", constraint, sql, err)
	}
}

// mustUser creates an account with two recovery codes ("code-a", "code-b"
// as their stored hashes) and returns its ID.
func mustUser(t *testing.T, st *Store, name string) int64 {
	t.Helper()
	id, err := st.CreateUser(ctx, name, "hash-of-"+name, []string{"code-a", "code-b"})
	if err != nil {
		t.Fatalf("creating user %s: %v", name, err)
	}
	return id
}

// mustTag returns a seeded tag's ID by name.
func mustTag(t *testing.T, st *Store, name string) int {
	t.Helper()
	tags, err := st.Tags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range tags {
		if tag.Name == name {
			return tag.ID
		}
	}
	t.Fatalf("no seeded tag %q", name)
	return 0
}

// mustPost creates a text post tagged "tech" and returns the loaded post.
func mustPost(t *testing.T, st *Store, author int64, title string) Post {
	t.Helper()
	pid, err := st.CreatePost(ctx, NewPost{
		AuthorID: author, Title: title, Body: "body of " + title,
		TagIDs: []int{mustTag(t, st, "tech")},
	})
	if err != nil {
		t.Fatalf("creating post %q: %v", title, err)
	}
	return mustLoadPost(t, st, pid)
}

func mustLoadPost(t *testing.T, st *Store, publicID string) Post {
	t.Helper()
	p, found, err := st.PostByPublicID(ctx, publicID)
	if err != nil || !found {
		t.Fatalf("loading post %s: found=%v err=%v", publicID, found, err)
	}
	return p
}

// mustComment creates a comment (a reply when parent isn't "") and returns
// its public ID.
func mustComment(t *testing.T, st *Store, post Post, author int64, parent, body string) string {
	t.Helper()
	cid, _, err := st.CreateComment(ctx, NewComment{
		PostID: post.ID, ParentPublicID: parent, AuthorID: author, Body: body,
	})
	if err != nil {
		t.Fatalf("creating comment %q: %v", body, err)
	}
	return cid
}

func mustLoadComment(t *testing.T, st *Store, publicID string) Comment {
	t.Helper()
	c, found, err := st.CommentByPublicID(ctx, publicID)
	if err != nil || !found {
		t.Fatalf("loading comment %s: found=%v err=%v", publicID, found, err)
	}
	return c
}
