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
// To use a different Postgres server, set SNOWBALL_TEST_DATABASE_URL (see
// internal/testdb). The web package's tests use their own database,
// snowball_web_test, so the two packages never empty each other's tables.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"snowball/internal/testdb"
)

// testStore is shared by every database test. nil when they're skipped (-short).
var testStore *Store

// TestMain runs once, before any test in this package. It prepares the test
// database (internal/testdb creates it and applies the migrations), runs all
// the tests (m.Run), then cleans up.
func TestMain(m *testing.M) {
	flag.Parse() // needed before testing.Short() can be read here
	if testing.Short() {
		os.Exit(m.Run()) // database tests will skip themselves
	}

	dbURL, err := testdb.Prepare("snowball_test")
	if err == nil {
		testStore, err = Open(context.Background(), dbURL)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\ncan't set up the test database: %v\n"+
			"Is Postgres running? Try: docker compose up -d\n"+
			"Or skip the database tests with: go test -short ./...\n\n", err)
		os.Exit(1)
	}

	code := m.Run()
	testStore.Close()
	os.Exit(code)
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
	exec(t, testStore, `TRUNCATE `+testdb.Tables+` RESTART IDENTITY CASCADE`)
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

// mustPost creates a text post tagged "technology" and returns the loaded post.
func mustPost(t *testing.T, st *Store, author int64, title string) Post {
	t.Helper()
	pid, err := st.CreatePost(ctx, NewPost{
		AuthorID: author, Title: title, Body: "body of " + title,
		TagIDs: []int{mustTag(t, st, "technology")},
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
