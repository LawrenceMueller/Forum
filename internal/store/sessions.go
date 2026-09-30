package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// CreateSession stores a session under the hash of its token.
func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash string, expires time.Time) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expires,
	)
	return err
}

// DeleteExpiredSessions removes every session past its expiry, for everyone,
// and reports how many went. Expired sessions already can't log anyone in;
// this just stops them piling up.
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// UserBySession finds who a session belongs to, if it's valid and unexpired.
func (s *Store) UserBySession(ctx context.Context, tokenHash string) (User, bool, error) {
	var u User
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.username, u.is_admin
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND u.deleted_at IS NULL`,
		tokenHash,
	).Scan(&u.ID, &u.Username, &u.IsAdmin)

	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	return u, true, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// DeleteUserSessions logs a user out everywhere. On Snowball, every
// log out is a log out of everything.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}
