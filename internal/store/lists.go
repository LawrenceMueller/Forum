package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Sort is how a list of posts is ordered.
type Sort int

const (
	SortNew     Sort = iota // newest first
	SortActive              // most recent activity first (bump order)
	SortTop                 // most points in the last 7 days
	SortPopular             // "hot" ranking, among the last 24 hours' posts (not paginated)
)

// hotRank is Reddit's "hot" formula, as SQL. Two parts, added together:
//
//   - the score on a logarithmic scale: going from 1 to 10 points counts as
//     much as going from 10 to 100, so early votes matter most, and a post
//     can't win just by having been around to collect more of them;
//   - the post's age: newer posts start higher. Every 45,000 seconds
//     (12.5 hours) of age is worth the same as a tenfold change in score.
//
// The result: a post taking off now outranks an older one that already
// peaked, even with fewer total points. Negative scores sink below zero.
const hotRank = `(sign(p.score)::float8 * log(greatest(abs(p.score), 1)::float8)
	+ extract(epoch FROM p.created_at)::float8 / 45000)`

// Cursor marks where a page of posts ended, so the next page can start
// right after it. Value is the sort key of the last post shown (a time in
// microseconds, or a score) and ID breaks ties between equal values.
//
// This is "keyset" pagination. Unlike page numbers (?page=3), it stays fast
// no matter how many posts exist, and new posts arriving while someone
// reads never make them skip or repeat anything.
type Cursor struct {
	Value int64
	ID    int64
}

// String is the cursor as it appears in URLs: ?after=1790000000000000.42
func (c Cursor) String() string {
	return strconv.FormatInt(c.Value, 10) + "." + strconv.FormatInt(c.ID, 10)
}

// ParseCursor reads a cursor from a URL. Anything malformed is an error.
func ParseCursor(s string) (Cursor, error) {
	v, id, ok := strings.Cut(s, ".")
	if !ok {
		return Cursor{}, errors.New("bad cursor")
	}
	value, err1 := strconv.ParseInt(v, 10, 64)
	postID, err2 := strconv.ParseInt(id, 10, 64)
	if err1 != nil || err2 != nil || postID < 1 {
		return Cursor{}, errors.New("bad cursor")
	}
	return Cursor{Value: value, ID: postID}, nil
}

// ListOptions picks which posts to list.
type ListOptions struct {
	Sort    Sort
	TagID   int     // only posts under this tag (0 for all), including its descendants
	TagOnly bool    // with TagID: only posts carrying exactly that tag
	After   *Cursor // start after this post; nil for the first page
	Limit   int
}

// ListPosts returns one page of visible posts (never removed or deleted),
// without their bodies. If there's another page after this one, next is
// the cursor to fetch it with.
func (s *Store) ListPosts(ctx context.Context, opt ListOptions) (posts []Post, next *Cursor, err error) {
	var (
		where []string
		args  []any
	)
	// arg adds a value and returns its placeholder ($1, $2, ...). Values
	// always travel as arguments, never pasted into the SQL text.
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	where = append(where, "p.removed_at IS NULL", "p.deleted_at IS NULL")

	switch {
	case opt.TagID != 0 && opt.TagOnly:
		where = append(where, `EXISTS (
			SELECT 1 FROM post_tags pt
			WHERE pt.post_id = p.id AND pt.tag_id = `+arg(opt.TagID)+`)`)
	case opt.TagID != 0:
		// The closure table holds every (ancestor, descendant) pair, so
		// "tagged with this tag or anything below it" is one join.
		where = append(where, `EXISTS (
			SELECT 1 FROM post_tags pt
			JOIN tag_closure c ON c.descendant_id = pt.tag_id
			WHERE pt.post_id = p.id AND c.ancestor_id = `+arg(opt.TagID)+`)`)
	}

	// Each sort has a key column; the cursor compares against the same pair.
	// These strings are fixed in code, never built from input.
	var key string
	switch opt.Sort {
	case SortNew:
		key = "p.created_at"
	case SortActive:
		key = "p.last_activity_at"
	case SortTop:
		key = "p.score"
		where = append(where, "p.created_at > now() - interval '7 days'")
	case SortPopular:
		key = hotRank
		where = append(where, "p.created_at > now() - interval '24 hours'")
		opt.After = nil // a short list, never paginated
	}
	timeKey := opt.Sort == SortNew || opt.Sort == SortActive

	if opt.After != nil {
		var v any = opt.After.Value
		if timeKey {
			v = time.UnixMicro(opt.After.Value)
		}
		// Row comparison: "(key, id) comes after the cursor" in a single test.
		where = append(where, "("+key+", p.id) < ("+arg(v)+", "+arg(opt.After.ID)+")")
	}

	limit := opt.Limit
	if limit < 1 || limit > 100 {
		limit = 30
	}

	query := `
		SELECT p.id, p.public_id, p.title, coalesce(p.url, ''),
		       u.username, u.deleted_at IS NOT NULL,
		       p.created_at, p.last_activity_at, p.edited_at, p.score, p.comment_count,
		       coalesce((SELECT array_agg(t.name ORDER BY t.name)
		                 FROM post_tags pt JOIN tags t ON t.id = pt.tag_id
		                 WHERE pt.post_id = p.id), '{}')
		FROM posts p
		JOIN users u ON u.id = p.author_id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY ` + key + ` DESC, p.id DESC
		LIMIT ` + arg(limit+1) // one extra, to learn whether another page exists

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	posts, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Post, error) {
		var p Post
		err := row.Scan(&p.ID, &p.PublicID, &p.Title, &p.URL,
			&p.Author, &p.AuthorDeleted,
			&p.CreatedAt, &p.LastActivityAt, &p.EditedAt, &p.Score, &p.CommentCount,
			&p.Tags)
		return p, err
	})
	if err != nil {
		return nil, nil, err
	}

	if len(posts) > limit {
		posts = posts[:limit]
		last := posts[limit-1]
		c := Cursor{ID: last.ID}
		switch opt.Sort {
		case SortNew:
			c.Value = last.CreatedAt.UnixMicro()
		case SortActive:
			c.Value = last.LastActivityAt.UnixMicro()
		default:
			c.Value = int64(last.Score)
		}
		next = &c
	}
	return posts, next, nil
}

// CountPosts is how many visible posts exist, for the footer.
func (s *Store) CountPosts(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM posts WHERE removed_at IS NULL AND deleted_at IS NULL`,
	).Scan(&n)
	return n, err
}
