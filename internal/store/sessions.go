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
	var sus Suspension
	var susID *int64
	// The suspension in force rides along in the same query (LEFT JOIN
	// LATERAL: "and, for this user, the latest active suspension, if any"),
	// so knowing it costs no extra trip to the database.
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.username, u.is_admin, u.created_at,
		       a.id, coalesce(a.reason, ''), coalesce(a.note, ''), coalesce(a.created_at, now()),
		       a.ends_at, coalesce(a.appeal, ''), a.appealed_at, a.appeal_handled_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		LEFT JOIN LATERAL (
			SELECT * FROM suspensions
			WHERE user_id = u.id AND lifted_at IS NULL AND (ends_at IS NULL OR ends_at > now())
			ORDER BY id DESC LIMIT 1
		) a ON true
		WHERE s.token_hash = $1 AND s.expires_at > now() AND u.deleted_at IS NULL`,
		tokenHash,
	).Scan(&u.ID, &u.Username, &u.IsAdmin, &u.CreatedAt,
		&susID, &sus.Reason, &sus.Note, &sus.CreatedAt, &sus.EndsAt, &sus.Appeal, &sus.AppealedAt, &sus.AppealHandledAt)
	if err == nil && susID != nil {
		sus.ID, sus.UserID = *susID, u.ID
		u.Suspension = &sus
	}

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
