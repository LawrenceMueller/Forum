// Package testdb prepares Postgres databases for tests. It's used only by
// test code (store and web), never by the running site.
//
// Each package that needs a database gets its OWN one (snowball_test,
// snowball_web_test, ...). "go test ./..." runs packages in parallel, and
// every test starts by emptying the tables, so two packages sharing one
// database would wipe each other's data mid-test.
package testdb

import (
	"context"
	"fmt"
	"os"
	"strings"

	"snowball/internal/dbsetup"
)

// DefaultURL matches compose.yaml and .env. Set SNOWBALL_TEST_DATABASE_URL to
// use a different server (CI does); only the server and credentials are taken
// from it, since each package picks its own database name.
const DefaultURL = "postgres://snowball:snowball-dev-only@localhost:5432/snowball_test?sslmode=disable"

// Root finds the project root (the folder holding go.mod), wherever the test
// happens to be running from.
func Root() (string, error) { return dbsetup.Root() }

// Prepare makes sure the named database exists and has every migration
// applied, and returns its connection URL. The name must end in "_test":
// a safety catch, since tests empty its tables.
func Prepare(name string) (string, error) {
	if !strings.HasSuffix(name, "_test") {
		return "", fmt.Errorf("refusing to use database %q: test database names must end in _test", name)
	}
	base := os.Getenv("SNOWBALL_TEST_DATABASE_URL")
	if base == "" {
		base = DefaultURL
	}
	return dbsetup.Ensure(context.Background(), base, name)
}

// Tables is every table a test may write to. Emptying these resets the
// database; the tag tree's tables (tags, tag_parents, tag_aliases, tag_closure)
// hold the starter tree from the migrations and are deliberately left alone.
const Tables = `users, sessions, recovery_codes, posts, post_tags, tag_posts, reports, mod_log, suspensions, blocked_domains, admin_notes,
	comments, post_votes, comment_votes, polls, poll_options, poll_votes, mega_polls`
