// Package dbsetup creates a Postgres database if it doesn't exist yet and
// applies every migration to it. Development tools share it: the tests
// (through internal/testdb) and the seed program (cmd/seed). The running
// site never uses it; in production, migrations are run deliberately with
// the goose CLI.
package dbsetup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // lets goose talk to Postgres through database/sql
	"github.com/pressly/goose/v3"
)

// Root finds the project root (the folder holding go.mod), wherever the
// program or test happens to be running from.
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

// Ensure makes sure the named database exists on the server that serverURL
// points to, applies every migration, and returns the database's own URL.
// Only the server and credentials are taken from serverURL; its database
// name is replaced with name.
func Ensure(ctx context.Context, serverURL, name string) (string, error) {
	u, err := url.Parse(serverURL)
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
	if err := goose.UpContext(ctx, db, filepath.Join(root, "migrations")); err != nil {
		return "", fmt.Errorf("migrations: %w", err)
	}
	return dbURL, nil
}
