// Package store is the only place Snowball talks to Postgres. Every SQL
// query lives here, so a schema change touches one package.
//
// It deals in already-hashed values: it never sees a password or a raw
// token, and doesn't need to know how the hashes were made.
package store

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db *pgxpool.Pool
}

// Open connects to Postgres and checks the connection actually works.
//
// A pool keeps several connections open and hands them out to requests as
// needed. Opening a fresh connection per request would be slow, and sharing
// one connection across concurrent requests isn't safe.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("bad database config: %w", err)
	}

	// pgxpool.New doesn't connect right away, so ping to find out now
	// rather than on the first page load. Give up after 5 seconds.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("can't reach the database: %w", err)
	}

	var version string
	if err := pool.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		pool.Close()
		return nil, fmt.Errorf("first query failed: %w", err)
	}
	log.Printf("connected: %s", version)

	return &Store{db: pool}, nil
}

func (s *Store) Close() {
	s.db.Close()
}
