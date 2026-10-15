package store

// Moderation: reports from members, the admin's queue, removing (with a
// reason) and restoring, and the moderation log. Design in section 11.2 of
// docs/PROJECT_CONTEXT.md.
//
// Every action happens in one transaction with its log entry and with the
// reports it settles, so the queue, the items and the log always agree.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ReportReasons are what a member can report something for (the database
// checks the same list).
var ReportReasons = []string{"spam", "harassment", "personal", "illegal", "tags", "other"}

// RemovalReasons are what the admin can remove something for. "held" isn't
// one: it's set automatically by reports, never chosen.
var RemovalReasons = []string{"spam", "harassment", "personal", "illegal", "rules"}

// SeriousReasons can hide an item before the admin looks: harm that can't
// wait until morning. Every other reason only waits in the queue.
var SeriousReasons = []string{"illegal", "personal"}

const (
	// HoldAfter is how many people must report something for a serious
	// reason before it's hidden automatically.
	HoldAfter = 3
	// ReportsPerDay is how many reports one account may file in 24 hours.
	ReportsPerDay = 10
	// heldReason marks something hidden by reports, pending review.
	heldReason = "held"
)

var (
	ErrAlreadyReported = errors.New("you've already reported this")
	ErrReportLimit     = errors.New("too many reports today")
	ErrItemNotFound    = errors.New("no such post or comment")
)

// Item names one post, or one comment on it, by public IDs.
type Item struct {
	PostID    string // the post's public ID
	CommentID string // the comment's public ID; "" for the post itself
}

// item is an Item as it stands in the database, locked for an action.
type item struct {
	postID, commentID int64 // commentID 0 for a post
	removed, deleted  bool
	reason            string
}

// lockItem finds an item and locks its row until the transaction ends, so
// two actions on it can't interleave. A comment can be named by its own ID
// alone; lockItem fills in its post's.
func lockItem(ctx context.Context, tx pgx.Tx, it *Item) (item, error) {
	var x item
	var err error
	if it.CommentID == "" {
		err = tx.QueryRow(ctx, `
			SELECT id, 0, removed_at IS NOT NULL, deleted_at IS NOT NULL, coalesce(removal_reason, '')
			FROM posts WHERE public_id = $1 FOR UPDATE`, it.PostID).
			Scan(&x.postID, &x.commentID, &x.removed, &x.deleted, &x.reason)
	} else {
		err = tx.QueryRow(ctx, `
			SELECT p.public_id, c.post_id, c.id, c.removed_at IS NOT NULL, c.deleted_at IS NOT NULL, coalesce(c.removal_reason, '')
			FROM comments c JOIN posts p ON p.id = c.post_id
			WHERE c.public_id = $1 AND ($2 = '' OR p.public_id = $2) FOR UPDATE OF c`, it.CommentID, it.PostID).
			Scan(&it.PostID, &x.postID, &x.commentID, &x.removed, &x.deleted, &x.reason)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrItemNotFound
	}
	return x, err
}

// hide removes an item (or changes why it's removed: a held item that the
// admin then removes keeps its place, with the real reason). A comment
// leaving view takes one off its post's visible count.
func hide(ctx context.Context, tx pgx.Tx, x item, reason string) error {
	if x.commentID == 0 {
		_, err := tx.Exec(ctx, `UPDATE posts SET removed_at = coalesce(removed_at, now()), removal_reason = $2
			WHERE id = $1`, x.postID, reason)
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE comments SET removed_at = coalesce(removed_at, now()), removal_reason = $2
		WHERE id = $1`, x.commentID, reason); err != nil {
		return err
	}
	if !x.removed && !x.deleted {
		_, err := tx.Exec(ctx, `UPDATE posts SET comment_count = greatest(comment_count - 1, 0) WHERE id = $1`, x.postID)
		return err
	}
	return nil
}

// unhide puts back an item that reports had hidden.
func unhide(ctx context.Context, tx pgx.Tx, x item) error {
	if x.commentID == 0 {
		_, err := tx.Exec(ctx, `UPDATE posts SET removed_at = NULL, removal_reason = NULL WHERE id = $1`, x.postID)
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE comments SET removed_at = NULL, removal_reason = NULL WHERE id = $1`, x.commentID); err != nil {
		return err
	}
	if !x.deleted {
		_, err := tx.Exec(ctx, `UPDATE posts SET comment_count = comment_count + 1 WHERE id = $1`, x.postID)
		return err
	}
	return nil
}

// settle marks an item's open reports handled, returning how many.
func settle(ctx context.Context, tx pgx.Tx, x item) (int, error) {
	tag, err := tx.Exec(ctx, `UPDATE reports SET handled_at = now()
		WHERE post_id = $1 AND comment_id IS NOT DISTINCT FROM $2 AND handled_at IS NULL`, x.postID, nullID(x.commentID))
	return int(tag.RowsAffected()), err
}

// logAction writes one moderation log entry.
func logAction(ctx context.Context, tx pgx.Tx, action string, it Item, reason string, reports int) error {
	var comment, why any
	if it.CommentID != "" {
		comment = it.CommentID
	}
	if reason != "" {
		why = reason
	}
	_, err := tx.Exec(ctx, `INSERT INTO mod_log (action, post_public_id, comment_public_id, reason, reports)
		VALUES ($1, $2, $3, $4, $5)`, action, it.PostID, comment, why, reports)
	return err
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// ---- reports ----

// NewReport is one member's report.
type NewReport struct {
	Item
	Reporter string // auth.VoterKeys.Reporter of the account
	Reason   string // one of ReportReasons
	Note     string // optional, at most 200 characters
}

// Report files a report. held is true when this report was the one that
// hid the item (see HoldAfter).
func (s *Store) Report(ctx context.Context, r NewReport) (held bool, err error) {
	if !slices.Contains(ReportReasons, r.Reason) {
		return false, fmt.Errorf("unknown report reason %q", r.Reason)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	x, err := lockItem(ctx, tx, &r.Item)
	if err != nil {
		return false, err
	}
	var today int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM reports WHERE reporter = $1 AND created_at > now() - interval '1 day'`,
		r.Reporter).Scan(&today); err != nil {
		return false, err
	}
	if today >= ReportsPerDay {
		return false, ErrReportLimit
	}
	_, err = tx.Exec(ctx, `INSERT INTO reports (post_id, comment_id, reporter, reason, note) VALUES ($1, $2, $3, $4, $5)`,
		x.postID, nullID(x.commentID), r.Reporter, r.Reason, r.Note)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "one_report_per_person" {
		return false, ErrAlreadyReported
	}
	if err != nil {
		return false, err
	}

	// Enough people reporting serious harm hides it until the admin looks.
	if slices.Contains(SeriousReasons, r.Reason) && !x.removed && !x.deleted {
		var serious int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM reports
			WHERE post_id = $1 AND comment_id IS NOT DISTINCT FROM $2 AND handled_at IS NULL AND reason = ANY($3)`,
			x.postID, nullID(x.commentID), SeriousReasons).Scan(&serious); err != nil {
			return false, err
		}
		if serious >= HoldAfter {
			if err := hide(ctx, tx, x, heldReason); err != nil {
				return false, err
			}
			if err := logAction(ctx, tx, "hold", r.Item, "", serious); err != nil {
				return false, err
			}
			held = true
		}
	}
	return held, tx.Commit(ctx)
}

// ---- the admin's actions ----

// Remove is the admin removing a post or comment for a reason. The row
// stays (a removed post keeps its thread; a removed comment keeps its
// replies in place), hidden from everyone but the admin; its open reports
// are settled. Removing something already removed just updates the reason.
func (s *Store) Remove(ctx context.Context, it Item, reason string) error {
	if !slices.Contains(RemovalReasons, reason) {
		return fmt.Errorf("unknown removal reason %q", reason)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	x, err := lockItem(ctx, tx, &it)
	if err != nil {
		return err
	}
	if err := hide(ctx, tx, x, reason); err != nil {
		return err
	}
	n, err := settle(ctx, tx, x)
	if err != nil {
		return err
	}
	if err := logAction(ctx, tx, "remove", it, reason, n); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Dismiss closes an item's reports without removing it; an item that the
// reports had hidden comes back.
func (s *Store) Dismiss(ctx context.Context, it Item) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	x, err := lockItem(ctx, tx, &it)
	if err != nil {
		return err
	}
	n, err := settle(ctx, tx, x)
	if err != nil {
		return err
	}
	if err := logAction(ctx, tx, "dismiss", it, "", n); err != nil {
		return err
	}
	if x.removed && x.reason == heldReason {
		if err := unhide(ctx, tx, x); err != nil {
			return err
		}
		if err := logAction(ctx, tx, "restore", it, "", 0); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ---- the queue ----

// QueueItem is one reported post or comment, with its open reports.
type QueueItem struct {
	Item
	PostTitle     string
	Body          string // the post's or comment's text
	URL           string // a link post's link
	Author        string
	AuthorDeleted bool
	CreatedAt     time.Time
	Removed       bool
	RemovalReason string
	Deleted       bool

	Reports int            // open reports
	Reasons map[string]int // how many of each reason
	Notes   []string       // reporters' notes, newest first
	Latest  time.Time
	Serious bool // any serious reason among them
}

// ReportQueue returns the items with open reports, most urgent first:
// serious reasons, then the most reported, then the most recent. At most
// limit items.
func (s *Store) ReportQueue(ctx context.Context, limit int) ([]QueueItem, error) {
	rows, err := s.db.Query(ctx, `
		WITH open AS (
			SELECT post_id, comment_id,
			       count(*) AS n,
			       max(created_at) AS latest,
			       bool_or(reason = ANY($1)) AS serious,
			       array_agg(reason) AS reasons,
			       array_agg(note ORDER BY created_at DESC) FILTER (WHERE note <> '') AS notes
			FROM reports WHERE handled_at IS NULL
			GROUP BY post_id, comment_id
			ORDER BY bool_or(reason = ANY($1)) DESC, count(*) DESC, max(created_at) DESC
			LIMIT $2
		)
		SELECT p.public_id, coalesce(c.public_id, ''), p.title,
		       coalesce(c.body, p.body), CASE WHEN c.id IS NULL THEN coalesce(p.url, '') ELSE '' END,
		       u.username, u.deleted_at IS NOT NULL, coalesce(c.created_at, p.created_at),
		       coalesce(c.removed_at, p.removed_at) IS NOT NULL,
		       coalesce(CASE WHEN c.id IS NULL THEN p.removal_reason ELSE c.removal_reason END, ''),
		       coalesce(c.deleted_at, p.deleted_at) IS NOT NULL,
		       o.n, o.reasons, coalesce(o.notes, '{}'), o.latest, o.serious
		FROM open o
		JOIN posts p ON p.id = o.post_id
		LEFT JOIN comments c ON c.id = o.comment_id
		JOIN users u ON u.id = coalesce(c.author_id, p.author_id)
		ORDER BY o.serious DESC, o.n DESC, o.latest DESC`, SeriousReasons, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (QueueItem, error) {
		var q QueueItem
		var reasons []string
		err := row.Scan(&q.PostID, &q.CommentID, &q.PostTitle, &q.Body, &q.URL,
			&q.Author, &q.AuthorDeleted, &q.CreatedAt, &q.Removed, &q.RemovalReason, &q.Deleted,
			&q.Reports, &reasons, &q.Notes, &q.Latest, &q.Serious)
		q.Reasons = map[string]int{}
		for _, r := range reasons {
			q.Reasons[r]++
		}
		return q, err
	})
}

// OpenAppealCount counts appeals waiting for a decision.
func (s *Store) OpenAppealCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM suspensions
		WHERE appeal IS NOT NULL AND appeal_handled_at IS NULL AND lifted_at IS NULL
		  AND (ends_at IS NULL OR ends_at > now())`).Scan(&n)
	return n, err
}

// OpenReportItems is how many posts and comments have open reports.
func (s *Store) OpenReportItems(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM (
		SELECT DISTINCT post_id, comment_id FROM reports WHERE handled_at IS NULL) open`).Scan(&n)
	return n, err
}

// ---- the log ----

// LogEntry is one moderation action.
type LogEntry struct {
	ID        int64
	Action    string // remove, hold, restore, dismiss
	Item             // what it was done to
	Reason    string // for removals and suspensions
	Detail    string // for suspensions: "7 days", "permanent"; for lifts: "after an appeal"
	Reports   int    // open reports it settled
	CreatedAt time.Time
}

// ModLog returns moderation actions, newest first, starting below the
// given ID (0 for the newest). withDismissals includes closed reports with
// no action taken and turned-down appeals, which only the admin's view shows.
func (s *Store) ModLog(ctx context.Context, before int64, limit int, withDismissals bool) ([]LogEntry, error) {
	if before <= 0 {
		before = 1<<63 - 1
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, action, coalesce(post_public_id, ''), coalesce(comment_public_id, ''), coalesce(reason, ''),
		       coalesce(detail, ''), reports, created_at
		FROM mod_log
		WHERE id < $1 AND ($3 OR action NOT IN ('dismiss', 'keep'))
		ORDER BY id DESC LIMIT $2`, before, limit, withDismissals)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (LogEntry, error) {
		var e LogEntry
		err := row.Scan(&e.ID, &e.Action, &e.PostID, &e.CommentID, &e.Reason, &e.Detail, &e.Reports, &e.CreatedAt)
		return e, err
	})
}
