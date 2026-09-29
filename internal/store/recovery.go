package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrRecoveryFailed = errors.New("username and recovery code don't match")

// RecoverAccount spends one recovery code and sets a new password, all in one
// transaction. It also deletes every session the user has, so if someone
// else had got into the account, they're thrown out.
func (s *Store) RecoverAccount(ctx context.Context, username, codeHash, newPasswordHash string) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// Delete the code and learn whose it was, in a single statement.
	// If two requests race with the same code, the database lets only one
	// of them delete the row, so a code can never be spent twice.
	var userID int64
	err = tx.QueryRow(ctx, `
		DELETE FROM recovery_codes rc
		USING users u
		WHERE rc.user_id = u.id
		  AND u.username = $1
		  AND u.deleted_at IS NULL
		  AND rc.code_hash = $2
		RETURNING rc.user_id`,
		username, codeHash,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRecoveryFailed // no such user, or no such code for them
	}
	if err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET password_hash = $1 WHERE id = $2`, newPasswordHash, userID,
	); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return userID, nil
}

func (s *Store) RemainingRecoveryCodes(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM recovery_codes WHERE user_id = $1`, userID,
	).Scan(&n)
	return n, err
}
