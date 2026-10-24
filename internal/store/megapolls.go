package store

// Mega polls: the daily question (migration 00021).
//
// The admin queues them, one per day. Days are calendar dates in the site's
// time zone (the web package decides which day it is, and when a day ends);
// here they're plain dates, passed and returned as time.Time at midnight UTC.
//
// Publishing turns the day's queue entry into an ordinary post with a poll:
// written by the admin who queued it, shown as from "Snowball", marked mega
// (results hidden until it closes). The queue row then points at the post,
// for the /polls archive. Until it's published, an entry can be edited or
// deleted; after that, it's a post, with the usual rules.

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrMegaDayTaken  = errors.New("there's already a mega poll for that day")
	ErrMegaPublished = errors.New("that mega poll has been published, so it can't be changed here")
	ErrMegaNotFound  = errors.New("no mega poll for that day")
	ErrMegaTodayLive = errors.New("today's mega poll is still up")
	ErrMegaQueueDry  = errors.New("nothing is queued after today")
	ErrMegaTodayWait = errors.New("today's mega poll hasn't gone up yet")
)

// MegaPoll is one queue entry.
type MegaPoll struct {
	Day       time.Time // the date, at midnight UTC
	Question  string
	Body      string // optional Markdown
	Options   []string
	Topics    string // as typed: matched to tags when published
	Format    string
	Level     string
	CreatedBy int64

	PostID       int64  // set once published (0 before, or if the post was later erased)
	PostPublicID string // likewise
	Problem      string // why publishing didn't work, if it didn't
}

// Published reports whether the entry has become a post.
func (m MegaPoll) Published() bool { return m.PostID != 0 }

const megaColumns = `m.day, m.question, m.body, m.options, m.topics, m.format, m.level, m.created_by,
	coalesce(m.post_id, 0), coalesce(p.public_id, ''), m.publish_problem`

func scanMega(row pgx.Row, m *MegaPoll) error {
	return row.Scan(&m.Day, &m.Question, &m.Body, &m.Options, &m.Topics, &m.Format, &m.Level, &m.CreatedBy,
		&m.PostID, &m.PostPublicID, &m.Problem)
}

// dateOnly keeps just the calendar date, as Postgres's date type does.
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// MegaPollsFrom lists the queue from day on, oldest first (the admin page
// passes a week ago, so recent ones show too).
func (s *Store) MegaPollsFrom(ctx context.Context, day time.Time, limit int) ([]MegaPoll, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+megaColumns+`
		FROM mega_polls m LEFT JOIN posts p ON p.id = m.post_id
		WHERE m.day >= $1
		ORDER BY m.day
		LIMIT $2`, dateOnly(day), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (MegaPoll, error) {
		var m MegaPoll
		err := scanMega(row, &m)
		return m, err
	})
}

// MegaPollFor loads one day's entry.
func (s *Store) MegaPollFor(ctx context.Context, day time.Time) (m MegaPoll, found bool, err error) {
	err = scanMega(s.db.QueryRow(ctx, `
		SELECT `+megaColumns+`
		FROM mega_polls m LEFT JOIN posts p ON p.id = m.post_id
		WHERE m.day = $1`, dateOnly(day)), &m)
	if errors.Is(err, pgx.ErrNoRows) {
		return MegaPoll{}, false, nil
	}
	return m, err == nil, err
}

// SaveMegaPoll adds an entry (oldDay nil) or changes the unpublished one
// queued for *oldDay, possibly moving it to another day.
func (s *Store) SaveMegaPoll(ctx context.Context, oldDay *time.Time, m MegaPoll) error {
	var err error
	if oldDay == nil {
		_, err = s.db.Exec(ctx, `
			INSERT INTO mega_polls (day, question, body, options, topics, format, level, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			dateOnly(m.Day), m.Question, m.Body, m.Options, m.Topics, m.Format, m.Level, m.CreatedBy)
	} else {
		var tag pgconn.CommandTag
		tag, err = s.db.Exec(ctx, `
			UPDATE mega_polls SET day = $2, question = $3, body = $4, options = $5, topics = $6, format = $7,
			                      level = $8, publish_problem = ''
			WHERE day = $1 AND post_id IS NULL`,
			dateOnly(*oldDay), dateOnly(m.Day), m.Question, m.Body, m.Options, m.Topics, m.Format, m.Level)
		if err == nil && tag.RowsAffected() == 0 {
			return s.whyNotChangeable(ctx, *oldDay)
		}
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "mega_polls_pkey" {
		return ErrMegaDayTaken
	}
	return err
}

// DeleteMegaPoll takes an unpublished entry out of the queue.
func (s *Store) DeleteMegaPoll(ctx context.Context, day time.Time) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM mega_polls WHERE day = $1 AND post_id IS NULL`, dateOnly(day))
	if err == nil && tag.RowsAffected() == 0 {
		return s.whyNotChangeable(ctx, day)
	}
	return err
}

// whyNotChangeable explains why an entry couldn't be changed: it's gone, or
// it's been published.
func (s *Store) whyNotChangeable(ctx context.Context, day time.Time) error {
	m, found, err := s.MegaPollFor(ctx, day)
	switch {
	case err != nil:
		return err
	case !found:
		return ErrMegaNotFound
	case m.Published():
		return ErrMegaPublished
	}
	return ErrMegaNotFound
}

// SetMegaProblem records why a day's entry couldn't be published ("" clears it).
func (s *Store) SetMegaProblem(ctx context.Context, day time.Time, problem string) error {
	_, err := s.db.Exec(ctx, `UPDATE mega_polls SET publish_problem = $2 WHERE day = $1 AND post_id IS NULL`,
		dateOnly(day), problem)
	return err
}

// PublishMegaPoll turns day's queue entry into a post, tagged tagIDs, whose
// poll closes at closesAt. It returns the post's public ID, or "" if there
// was nothing to publish (no entry, or already published): safe to call
// from several places, or twice.
func (s *Store) PublishMegaPoll(ctx context.Context, day time.Time, tagIDs []int, closesAt time.Time) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var m MegaPoll
	err = tx.QueryRow(ctx, `
		SELECT question, body, options, created_by FROM mega_polls
		WHERE day = $1 AND post_id IS NULL FOR UPDATE`, dateOnly(day)).
		Scan(&m.Question, &m.Body, &m.Options, &m.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	publicID, err := newPublicID()
	if err != nil {
		return "", err
	}
	postID, err := insertPost(ctx, tx, NewPost{
		AuthorID: m.CreatedBy, Title: m.Question, Body: m.Body, TagIDs: tagIDs, BySite: true,
		Poll: &NewPoll{Options: m.Options, ClosesAt: closesAt, Mega: true},
	}, publicID, nil, ContentHash(m.Question, "", m.Body))
	if err != nil {
		return "", err // an ID collision included: the next attempt rolls a new one
	}
	if _, err := tx.Exec(ctx, `UPDATE mega_polls SET post_id = $2, publish_problem = '' WHERE day = $1`,
		dateOnly(day), postID); err != nil {
		return "", err
	}
	return publicID, tx.Commit(ctx)
}

// MegaPollPost returns the post published for day, if it's still up (not
// removed or deleted): what the front page's box shows.
func (s *Store) MegaPollPost(ctx context.Context, day time.Time) (Post, bool, error) {
	var publicID string
	err := s.db.QueryRow(ctx, `
		SELECT p.public_id FROM mega_polls m JOIN posts p ON p.id = m.post_id
		WHERE m.day = $1 AND p.removed_at IS NULL AND p.deleted_at IS NULL`, dateOnly(day)).Scan(&publicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, false, nil
	}
	if err != nil {
		return Post{}, false, err
	}
	return s.PostByPublicID(ctx, publicID)
}

// ArchivedMegaPoll is one line of the /polls archive.
type ArchivedMegaPoll struct {
	Day          time.Time
	PublicID     string
	Question     string
	ClosesAt     time.Time
	Total        int
	CommentCount int
	Winners      []string // the leading option(s); empty with no votes
	WinnerVotes  int
}

// MegaPollArchive lists published mega polls that are still up, newest day
// first, from before the day before (nil: from the newest). next is the
// cursor for the following page, nil on the last.
func (s *Store) MegaPollArchive(ctx context.Context, before *time.Time, limit int) ([]ArchivedMegaPoll, *time.Time, error) {
	args := []any{limit + 1}
	cond := ""
	if before != nil {
		cond = "AND m.day < $2"
		args = append(args, dateOnly(*before))
	}
	rows, err := s.db.Query(ctx, `
		SELECT m.day, p.public_id, p.title, pl.closes_at, p.comment_count,
		       (SELECT coalesce(sum(votes), 0) FROM poll_options WHERE post_id = p.id),
		       (SELECT coalesce(max(votes), 0) FROM poll_options WHERE post_id = p.id),
		       coalesce((SELECT array_agg(label ORDER BY position) FROM poll_options o
		                 WHERE o.post_id = p.id AND o.votes > 0
		                   AND o.votes = (SELECT max(votes) FROM poll_options WHERE post_id = p.id)), '{}')
		FROM mega_polls m
		JOIN posts p ON p.id = m.post_id
		JOIN polls pl ON pl.post_id = p.id
		WHERE p.removed_at IS NULL AND p.deleted_at IS NULL `+cond+`
		ORDER BY m.day DESC
		LIMIT $1`, args...)
	if err != nil {
		return nil, nil, err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ArchivedMegaPoll, error) {
		var a ArchivedMegaPoll
		err := row.Scan(&a.Day, &a.PublicID, &a.Question, &a.ClosesAt, &a.CommentCount,
			&a.Total, &a.WinnerVotes, &a.Winners)
		return a, err
	})
	if err != nil {
		return nil, nil, err
	}
	var next *time.Time
	if len(list) > limit {
		list = list[:limit]
		d := list[limit-1].Day
		next = &d
	}
	return list, next, nil
}

// RunNextNow fills today with the next queued mega poll, once today's has
// been taken down (removed or deleted), or when there's none: today's entry
// is dropped from the queue (its post stays, removed), and every
// unpublished entry after today moves up a day. The caller then publishes
// the new today's, as usual.
func (s *Store) RunNextNow(ctx context.Context, today time.Time) error {
	today = dateOnly(today)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Lock the whole queue: shifting days must not race with a save.
	if _, err := tx.Exec(ctx, `LOCK TABLE mega_polls IN EXCLUSIVE MODE`); err != nil {
		return err
	}
	var published, live bool
	err = tx.QueryRow(ctx, `
		SELECT m.post_id IS NOT NULL, m.post_id IS NOT NULL AND p.removed_at IS NULL AND p.deleted_at IS NULL
		FROM mega_polls m LEFT JOIN posts p ON p.id = m.post_id
		WHERE m.day = $1`, today).Scan(&published, &live)
	switch {
	case errors.Is(err, pgx.ErrNoRows): // nothing today: fine
	case err != nil:
		return err
	case live:
		return ErrMegaTodayLive
	case !published:
		return ErrMegaTodayWait // waiting to go up (or stuck on its topics): fix it rather than skip it
	}
	var next int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM mega_polls WHERE day > $1 AND post_id IS NULL`, today).Scan(&next); err != nil {
		return err
	}
	if next == 0 {
		return ErrMegaQueueDry
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mega_polls WHERE day = $1`, today); err != nil {
		return err
	}
	// Two steps, because the day is the key: moving each entry back one in
	// a single statement could collide with its neighbour mid-way.
	if _, err := tx.Exec(ctx, `UPDATE mega_polls SET day = day + 100000 WHERE day > $1 AND post_id IS NULL`, today); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE mega_polls SET day = day - 100001 WHERE day > $1::date + 100000`, today); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
