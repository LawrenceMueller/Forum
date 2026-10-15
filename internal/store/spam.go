package store

// Content checks (moderation slice 4): the domain blocklist, and finding
// duplicates by content hash. The rules that use them live in
// web/spamchecks.go.

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDomainBlocked means the domain is already on the list.
var ErrDomainBlocked = errors.New("that site is already blocked")

// BlockedDomain is one site links may not point to (or any of its subdomains).
type BlockedDomain struct {
	Domain    string
	Note      string // the admin's reminder of why
	CreatedAt time.Time
}

// BlockedDomains returns the blocklist, alphabetically.
func (s *Store) BlockedDomains(ctx context.Context) ([]BlockedDomain, error) {
	rows, err := s.db.Query(ctx, `SELECT domain, note, created_at FROM blocked_domains ORDER BY domain`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (BlockedDomain, error) {
		var d BlockedDomain
		err := row.Scan(&d.Domain, &d.Note, &d.CreatedAt)
		return d, err
	})
}

// BlockDomain adds a site to the blocklist. domain must already be
// normalized (lowercase, no scheme, path or "www.").
func (s *Store) BlockDomain(ctx context.Context, domain, note string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO blocked_domains (domain, note) VALUES ($1, $2)`, domain, note)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrDomainBlocked
	}
	return err
}

// UnblockDomain takes a site off the blocklist.
func (s *Store) UnblockDomain(ctx context.Context, domain string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM blocked_domains WHERE domain = $1`, domain)
	return err
}

// Duplicates says how a new post or comment's text compares with recent
// ones: whether its own author posted the same within window, and how many
// OTHER accounts posted the same within window. (hash is ContentHash.)
func (s *Store) Duplicates(ctx context.Context, comment bool, authorID int64, hash string, window time.Duration) (mine bool, others int, err error) {
	table := "posts"
	if comment {
		table = "comments"
	}
	// The table name is one of two fixed strings, never input.
	err = s.db.QueryRow(ctx, `
		SELECT coalesce(bool_or(author_id = $1), false),
		       count(DISTINCT author_id) FILTER (WHERE author_id <> $1)
		FROM `+table+`
		WHERE content_hash = $2 AND created_at > now() - $3 * interval '1 second' AND deleted_at IS NULL`,
		authorID, hash, int64(window.Seconds())).Scan(&mine, &others)
	return mine, others, err
}
