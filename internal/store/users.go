package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrUsernameTaken = errors.New("username already taken")

// User is an account as the rest of the app sees it.
type User struct {
	ID       int64
	Username string
}

// UsernameTaken is the courtesy check used when offering a name.
func (s *Store) UsernameTaken(ctx context.Context, name string) (bool, error) {
	var taken bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE username = $1)`, name,
	).Scan(&taken)
	return taken, err
}

// CreateUser saves a new account and its recovery code hashes in one
// transaction: if anything fails partway, nothing is saved, so there can
// never be a user without codes or codes without a user.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash string, codeHashes []string) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	// Undoes everything unless Commit runs first; after Commit it does nothing.
	defer tx.Rollback(ctx)

	// $1 and $2 are placeholders. Values are sent separately from the SQL,
	// so a malicious value can never be run as SQL (no SQL injection).
	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id`,
		username, passwordHash,
	).Scan(&id)
	if err != nil {
		// 23505 is Postgres's code for a unique-constraint violation:
		// someone else got this name between the offer and now.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return 0, ErrUsernameTaken
		}
		return 0, err
	}

	for _, h := range codeHashes {
		if _, err := tx.Exec(ctx,
			`INSERT INTO recovery_codes (user_id, code_hash) VALUES ($1, $2)`, id, h,
		); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return id, nil
}

// Credentials returns what login needs to check a password.
// found is false if there's no such user, or the account was deleted.
func (s *Store) Credentials(ctx context.Context, username string) (id int64, passwordHash string, found bool, err error) {
	err = s.db.QueryRow(ctx,
		`SELECT id, password_hash FROM users WHERE username = $1 AND deleted_at IS NULL`, username,
	).Scan(&id, &passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return id, passwordHash, true, nil
}

// PasswordHash returns a live account's password hash, for re-checking the
// password before sensitive changes.
func (s *Store) PasswordHash(ctx context.Context, userID int64) (string, error) {
	var hash string
	err := s.db.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = $1 AND deleted_at IS NULL`, userID,
	).Scan(&hash)
	return hash, err
}

// ChangePassword saves a new password hash and deletes every session for
// the account, so it's logged out everywhere, including where the change
// was made.
func (s *Store) ChangePassword(ctx context.Context, userID int64, newHash string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE users SET password_hash = $1 WHERE id = $2 AND deleted_at IS NULL`, newHash, userID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteAccount soft-deletes a user in one transaction: the password hash is
// wiped and the row marked deleted (so the username stays taken forever),
// and the recovery codes and sessions are removed outright.
func (s *Store) DeleteAccount(ctx context.Context, userID int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE users SET password_hash = NULL, deleted_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`, userID,
	); err != nil {
		return err
	}
	// Soft delete keeps the row, so ON DELETE CASCADE never fires:
	// the codes and sessions have to go explicitly.
	if _, err := tx.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
