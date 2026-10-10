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
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // lets goose talk to Postgres through database/sql
	"github.com/pressly/goose/v3"
)

// DefaultURL matches compose.yaml and .env. Set SNOWBALL_TEST_DATABASE_URL to
// use a different server (CI does); only the server and credentials are taken
// from it, since each package picks its own database name.
const DefaultURL = "postgres://snowball:snowball-dev-only@localhost:5432/snowball_test?sslmode=disable"

// Root finds the project root (the folder holding go.mod), wherever the test
// happens to be running from.
func Root() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("couldn't find go.mod above the current folder")
		}
		dir = parent
	}
}

// Prepare makes sure the named database exists and has every migration
// applied, and returns its connection URL. The name must end in "_test":
// a safety catch, since tests empty its tables.
func Prepare(name string) (string, error) {
	if !strings.HasSuffix(name, "_test") {
		return "", fmt.Errorf("refusing to use database %q: test database names must end in _test", name)
	}
	ctx := context.Background()

	base := os.Getenv("SNOWBALL_TEST_DATABASE_URL")
	if base == "" {
		base = DefaultURL
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	dbURL := u.String()

	// Create the database if needed. That has to happen from a connection to
	// some OTHER database, so connect to "postgres", which always exists.
	admin := *u
	admin.Path = "/postgres"
	conn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)

	var exists bool
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name,
	).Scan(&exists); err != nil {
		return "", err
	}
	if !exists {
		// Database names can't be $1 placeholders; Sanitize quotes it safely.
		if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			return "", err
		}
	}

	// Apply every migration, with goose as a library rather than the CLI.
	root, err := Root()
	if err != nil {
		return "", err
	}
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		return "", err
	}
	defer db.Close()
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return "", err
	}
	if err := goose.Up(db, filepath.Join(root, "migrations")); err != nil {
		return "", fmt.Errorf("migrations: %w", err)
	}
	return dbURL, nil
}

// Tables is every table a test may write to. Emptying these resets the
// database; the seeded tags table is deliberately left alone.
const Tables = `users, sessions, recovery_codes, posts, post_tags,
	comments, post_votes, comment_votes`
