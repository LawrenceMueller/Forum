package store

// Suspensions and appeals (moderation slice 2). A suspension lasts a number
// of days or forever; while it's active the account is read-only (enforced
// in the web package, which learns about it from UserBySession). The
// suspended person gets one appeal, which lands in the admin's queue.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// SuspensionLengths are the lengths the admin can pick, in days; 0 means
// permanent.
var SuspensionLengths = []int{1, 7, 30, 0}

var (
	ErrUserNotFound      = errors.New("no such account")
	ErrCantSuspendAdmin  = errors.New("the admin can't be suspended")
	ErrNotSuspended      = errors.New("that suspension isn't active")
	ErrAlreadyAppealed   = errors.New("this suspension has already been appealed")
	ErrAppealNotPossible = errors.New("there's no appeal to decide on")
)

// Suspension is one suspension of an account.
type Suspension struct {
	ID              int64
	UserID          int64
	Reason          string // one of RemovalReasons
	Note            string // the admin's words to the person, optional
	CreatedAt       time.Time
	EndsAt          *time.Time // nil: permanent
	LiftedAt        *time.Time
	Appeal          string
	AppealedAt      *time.Time
	AppealHandledAt *time.Time
}

// Permanent reports whether it never ends.
func (s Suspension) Permanent() bool { return s.EndsAt == nil }

// Active reports whether it's in force at the given time.
func (s Suspension) Active(now time.Time) bool {
	return s.LiftedAt == nil && (s.EndsAt == nil || s.EndsAt.After(now))
}

// CanAppeal reports whether the person may still appeal it.
func (s Suspension) CanAppeal() bool { return s.AppealedAt == nil }

// lengthText is a suspension's length for the log: "7 days", "permanent".
func lengthText(days int) string {
	switch days {
	case 0:
		return "permanent"
	case 1:
		return "1 day"
	}
	return strconv.Itoa(days) + " days"
}

const suspensionColumns = `id, user_id, reason, note, created_at, ends_at, lifted_at,
	coalesce(appeal, ''), appealed_at, appeal_handled_at`

func scanSuspension(row pgx.Row, s *Suspension) error {
	return row.Scan(&s.ID, &s.UserID, &s.Reason, &s.Note, &s.CreatedAt, &s.EndsAt, &s.LiftedAt,
		&s.Appeal, &s.AppealedAt, &s.AppealHandledAt)
}

// activeSuspensionSQL finds an account's suspension in force, if any; used
// inside UserBySession and below. ($1 is the user ID.)
const activeSuspensionSQL = `
	SELECT ` + suspensionColumns + ` FROM suspensions
	WHERE user_id = $1 AND lifted_at IS NULL AND (ends_at IS NULL OR ends_at > now())
	ORDER BY id DESC LIMIT 1`

// ActiveSuspension returns an account's suspension in force, if any.
func (s *Store) ActiveSuspension(ctx context.Context, userID int64) (*Suspension, error) {
	var sus Suspension
	err := scanSuspension(s.db.QueryRow(ctx, activeSuspensionSQL, userID), &sus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sus, nil
}

// Suspensions returns every suspension of an account, newest first.
func (s *Store) Suspensions(ctx context.Context, userID int64) ([]Suspension, error) {
	rows, err := s.db.Query(ctx, `SELECT `+suspensionColumns+` FROM suspensions
		WHERE user_id = $1 ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Suspension, error) {
		var sus Suspension
		err := scanSuspension(row, &sus)
		return sus, err
	})
}

// Suspend suspends an account for days (0: permanently). A suspension
// already in force is replaced by the new one.
func (s *Store) Suspend(ctx context.Context, userID int64, reason, note string, days int) error {
	if !slices.Contains(RemovalReasons, reason) {
		return fmt.Errorf("unknown suspension reason %q", reason)
	}
	if !slices.Contains(SuspensionLengths, days) {
		return fmt.Errorf("unsupported suspension length %d", days)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var isAdmin, deleted bool
	err = tx.QueryRow(ctx, `SELECT is_admin, deleted_at IS NOT NULL FROM users WHERE id = $1 FOR UPDATE`, userID).
		Scan(&isAdmin, &deleted)
	if errors.Is(err, pgx.ErrNoRows) || deleted {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if isAdmin {
		return ErrCantSuspendAdmin
	}
	if _, err := tx.Exec(ctx, `UPDATE suspensions SET lifted_at = now()
		WHERE user_id = $1 AND lifted_at IS NULL AND (ends_at IS NULL OR ends_at > now())`, userID); err != nil {
		return err
	}
	var ends any // NULL for permanent
	if days > 0 {
		ends = time.Now().Add(time.Duration(days) * 24 * time.Hour)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO suspensions (user_id, reason, note, ends_at) VALUES ($1, $2, $3, $4)`,
		userID, reason, note, ends); err != nil {
		return err
	}
	if err := logAccountAction(ctx, tx, "suspend", reason, lengthText(days)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// logAccountAction writes a log entry about an account. Like the rest of
// the log it names nobody: "an account was suspended for 7 days: spam".
func logAccountAction(ctx context.Context, tx pgx.Tx, action, reason, detail string) error {
	var why, more any
	if reason != "" {
		why = reason
	}
	if detail != "" {
		more = detail
	}
	_, err := tx.Exec(ctx, `INSERT INTO mod_log (action, reason, detail) VALUES ($1, $2, $3)`, action, why, more)
	return err
}

// lockActive loads a suspension that's in force, locking it.
func lockActive(ctx context.Context, tx pgx.Tx, id int64) (Suspension, error) {
	var sus Suspension
	err := scanSuspension(tx.QueryRow(ctx, `SELECT `+suspensionColumns+` FROM suspensions
		WHERE id = $1 AND lifted_at IS NULL AND (ends_at IS NULL OR ends_at > now()) FOR UPDATE`, id), &sus)
	if errors.Is(err, pgx.ErrNoRows) {
		return sus, ErrNotSuspended
	}
	return sus, err
}

// LiftSuspension ends a suspension early (the admin changed their mind, or
// granted an appeal). It also settles any open appeal.
func (s *Store) LiftSuspension(ctx context.Context, id int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	sus, err := lockActive(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE suspensions SET lifted_at = now(),
		appeal_handled_at = CASE WHEN appeal IS NOT NULL THEN coalesce(appeal_handled_at, now()) END
		WHERE id = $1`, id); err != nil {
		return err
	}
	detail := ""
	if sus.AppealedAt != nil && sus.AppealHandledAt == nil {
		detail = "after an appeal"
	}
	if err := logAccountAction(ctx, tx, "lift", sus.Reason, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Appeal files the suspended person's one appeal.
func (s *Store) Appeal(ctx context.Context, userID, id int64, text string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	sus, err := lockActive(ctx, tx, id)
	if err != nil {
		return err
	}
	if sus.UserID != userID {
		return ErrNotSuspended
	}
	if !sus.CanAppeal() {
		return ErrAlreadyAppealed
	}
	if _, err := tx.Exec(ctx, `UPDATE suspensions SET appeal = $2, appealed_at = now() WHERE id = $1`, id, text); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// KeepSuspension turns down an appeal: the suspension stands, and the
// person sees that their appeal was read.
func (s *Store) KeepSuspension(ctx context.Context, id int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	sus, err := lockActive(ctx, tx, id)
	if err != nil {
		return err
	}
	if sus.AppealedAt == nil || sus.AppealHandledAt != nil {
		return ErrAppealNotPossible
	}
	if _, err := tx.Exec(ctx, `UPDATE suspensions SET appeal_handled_at = now() WHERE id = $1`, id); err != nil {
		return err
	}
	if err := logAccountAction(ctx, tx, "keep", sus.Reason, ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// OpenAppeal is an appeal waiting for the admin, with who it's from.
type OpenAppeal struct {
	Suspension
	Username string
}

// OpenAppeals lists appeals waiting for a decision, oldest first.
func (s *Store) OpenAppeals(ctx context.Context) ([]OpenAppeal, error) {
	rows, err := s.db.Query(ctx, `
		SELECT s.id, s.user_id, s.reason, s.note, s.created_at, s.ends_at, s.lifted_at,
		       coalesce(s.appeal, ''), s.appealed_at, s.appeal_handled_at, u.username
		FROM suspensions s JOIN users u ON u.id = s.user_id
		WHERE s.appeal IS NOT NULL AND s.appeal_handled_at IS NULL AND s.lifted_at IS NULL
		  AND (s.ends_at IS NULL OR s.ends_at > now())
		ORDER BY s.appealed_at`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (OpenAppeal, error) {
		var a OpenAppeal
		err := row.Scan(&a.ID, &a.UserID, &a.Reason, &a.Note, &a.CreatedAt, &a.EndsAt, &a.LiftedAt,
			&a.Appeal, &a.AppealedAt, &a.AppealHandledAt, &a.Username)
		return a, err
	})
}

// ---- the admin's view of an account ----

// AccountInfo is what the admin sees about an account.
type AccountInfo struct {
	ID        int64
	Username  string
	IsAdmin   bool
	CreatedAt time.Time
	DeletedAt *time.Time
	Shadowban *time.Time // when it was shadowbanned; nil if not
	Posts     int64      // not deleted by them, removed ones included
	Comments  int64
	Removed   int64 // posts and comments the admin removed
}

// AccountByName looks up an account for the admin, deleted ones included.
func (s *Store) AccountByName(ctx context.Context, name string) (AccountInfo, error) {
	var a AccountInfo
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.username, u.is_admin, u.created_at, u.deleted_at, u.shadowbanned_at,
		       (SELECT count(*) FROM posts WHERE author_id = u.id AND deleted_at IS NULL),
		       (SELECT count(*) FROM comments WHERE author_id = u.id AND deleted_at IS NULL),
		       (SELECT count(*) FROM posts WHERE author_id = u.id AND removed_at IS NOT NULL AND removal_reason IS DISTINCT FROM 'held')
		     + (SELECT count(*) FROM comments WHERE author_id = u.id AND removed_at IS NOT NULL AND removal_reason IS DISTINCT FROM 'held')
		FROM users u WHERE u.username = $1`, name).
		Scan(&a.ID, &a.Username, &a.IsAdmin, &a.CreatedAt, &a.DeletedAt, &a.Shadowban, &a.Posts, &a.Comments, &a.Removed)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrUserNotFound
	}
	return a, err
}
