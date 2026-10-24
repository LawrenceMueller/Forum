package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// SchemaVersion is the migration this code expects the database to be at:
// the number of the newest file in migrations/. A test in internal/testdb
// fails if the two ever disagree, so adding a migration means bumping this.
const SchemaVersion = 22

// ErrSchemaBehind means the database is missing migrations this code needs.
var ErrSchemaBehind = errors.New("database is missing migrations")

// CheckSchema compares the database's migration version (from goose's own
// table) with SchemaVersion. A database that's behind gets ErrSchemaBehind
// with what to do; one that's ahead (newer migrations than this code knows)
// gets a plain error, which the caller can choose to only warn about.
func (s *Store) CheckSchema(ctx context.Context) (have int64, err error) {
	err = s.db.QueryRow(ctx,
		`SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&have)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42P01" { // no goose table: never migrated
		have, err = 0, nil
	}
	if err != nil {
		return 0, err
	}
	switch {
	case have < SchemaVersion:
		return have, fmt.Errorf("%w: it's at %05d, this code needs %05d (run: goose -dir migrations postgres <the same database URL> up)",
			ErrSchemaBehind, have, SchemaVersion)
	case have > SchemaVersion:
		return have, fmt.Errorf("database is at migration %05d, newer than this code expects (%05d)", have, SchemaVersion)
	}
	return have, nil
}
