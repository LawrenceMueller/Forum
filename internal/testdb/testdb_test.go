package testdb

// The migration round trip: apply every migration, roll every one back,
// apply them all again, and check the schema came out identical.
//
// Down sections only ever run in an emergency (rolling back a bad deploy),
// which is exactly when you don't want to discover one is broken. This test
// runs them all on every CI run instead.
//
// It uses its own database, snowball_migrations_test, which never holds any
// data: some Down sections deliberately refuse to run when certain data
// exists (00005 with deleted accounts, 00009 with erased comments).

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"snowball/internal/store"
)

func TestMigrationsRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("database test skipped (-short)")
	}

	dbURL, err := Prepare("snowball_migrations_test") // creates it and migrates up
	if err != nil {
		t.Fatalf("can't set up the database: %v (is Postgres running? try: docker compose up -d)", err)
	}
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "migrations")

	before := schema(t, db)
	if len(before) == 0 {
		t.Fatal("the migrated schema is empty, so there's nothing to compare")
	}

	// All the way down.
	if err := goose.DownTo(db, dir, 0); err != nil {
		t.Fatalf("rolling back every migration: %v", err)
	}
	if left := schema(t, db); len(left) != 0 {
		t.Errorf("after rolling everything back, these remain (a Down misses something):\n  %s",
			strings.Join(left, "\n  "))
	}

	// And up again.
	if err := goose.Up(db, dir); err != nil {
		t.Fatalf("re-applying the migrations: %v", err)
	}
	after := schema(t, db)

	if !slices.Equal(before, after) {
		for _, line := range before {
			if !slices.Contains(after, line) {
				t.Errorf("lost in the round trip:   %s", line)
			}
		}
		for _, line := range after {
			if !slices.Contains(before, line) {
				t.Errorf("gained in the round trip: %s", line)
			}
		}
	}
}

// schema describes every column, constraint, and index in the database as
// sorted lines of text, so two snapshots can be compared. goose's own
// bookkeeping table is left out.
func schema(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT 'column ' || table_name || '.' || column_name || ' ' || data_type
		       || ' nullable=' || is_nullable || ' default=' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name <> 'goose_db_version'
		UNION ALL
		SELECT 'constraint ' || conrelid::regclass || ' ' || conname || ' ' || pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE connamespace = 'public'::regnamespace AND conrelid::regclass::text <> 'goose_db_version'
		UNION ALL
		SELECT 'index ' || indexname || ' ' || indexdef
		FROM pg_indexes
		WHERE schemaname = 'public' AND tablename <> 'goose_db_version'
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

// TestSchemaVersionMatchesMigrations keeps store.SchemaVersion (what the
// server checks at startup) in step with the newest migration file, and
// checks a fully migrated database passes that check.
func TestSchemaVersionMatchesMigrations(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(root, "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	var newest int64
	for _, f := range files {
		n, err := strconv.ParseInt(strings.SplitN(filepath.Base(f), "_", 2)[0], 10, 64)
		if err != nil {
			t.Fatalf("%s doesn't start with a number", filepath.Base(f))
		}
		newest = max(newest, n)
	}
	if newest != store.SchemaVersion {
		t.Fatalf("the newest migration is %05d but store.SchemaVersion is %05d: bump it in internal/store/schema.go",
			newest, store.SchemaVersion)
	}

	if testing.Short() {
		return
	}
	dbURL, err := Prepare("snowball_migrations_test")
	if err != nil {
		t.Fatalf("can't set up the database: %v", err)
	}
	st, err := store.Open(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if have, err := st.CheckSchema(context.Background()); err != nil || have != store.SchemaVersion {
		t.Errorf("CheckSchema on a migrated database: %d, %v", have, err)
	}
}
