package store

// Notes from the admin: short Markdown updates shown on the front page
// (the newest visible one) and at /notes (all visible ones).

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNoteNotFound means there's no such note.
var ErrNoteNotFound = errors.New("no such note")

// Note is one note from the admin.
type Note struct {
	ID        int64
	Body      string // Markdown
	CreatedAt time.Time
	EditedAt  *time.Time
	Hidden    bool
}

const noteColumns = `id, body, created_at, edited_at, hidden_at IS NOT NULL`

func scanNote(row pgx.Row, n *Note) error {
	return row.Scan(&n.ID, &n.Body, &n.CreatedAt, &n.EditedAt, &n.Hidden)
}

// Notes returns notes newest first: only visible ones, unless withHidden.
func (s *Store) Notes(ctx context.Context, withHidden bool, limit int) ([]Note, error) {
	rows, err := s.db.Query(ctx, `SELECT `+noteColumns+` FROM admin_notes
		WHERE $1 OR hidden_at IS NULL ORDER BY created_at DESC, id DESC LIMIT $2`, withHidden, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Note, error) {
		var n Note
		err := scanNote(row, &n)
		return n, err
	})
}

// LatestNote returns the newest visible note, or nil if there's none.
func (s *Store) LatestNote(ctx context.Context) (*Note, error) {
	notes, err := s.Notes(ctx, false, 1)
	if err != nil || len(notes) == 0 {
		return nil, err
	}
	return &notes[0], nil
}

// AddNote posts a note.
func (s *Store) AddNote(ctx context.Context, body string) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO admin_notes (body) VALUES ($1) RETURNING id`, body).Scan(&id)
	return id, err
}

// EditNote replaces a note's text.
func (s *Store) EditNote(ctx context.Context, id int64, body string) error {
	return noteChanged(s.db.Exec(ctx, `UPDATE admin_notes SET body = $2, edited_at = now() WHERE id = $1`, id, body))
}

// SetNoteHidden hides a note from everywhere public, or shows it again.
func (s *Store) SetNoteHidden(ctx context.Context, id int64, hidden bool) error {
	return noteChanged(s.db.Exec(ctx, `UPDATE admin_notes
		SET hidden_at = CASE WHEN $2 THEN coalesce(hidden_at, now()) END WHERE id = $1`, id, hidden))
}

// DeleteNote removes a note for good.
func (s *Store) DeleteNote(ctx context.Context, id int64) error {
	return noteChanged(s.db.Exec(ctx, `DELETE FROM admin_notes WHERE id = $1`, id))
}

func noteChanged(tag interface{ RowsAffected() int64 }, err error) error {
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNoteNotFound
	}
	return nil
}
