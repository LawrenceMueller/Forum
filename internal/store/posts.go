package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Tag is one entry in the curated tag list.
type Tag struct {
	ID   int
	Name string
}

// Tags returns the full tag list, alphabetically.
func (s *Store) Tags(ctx context.Context) ([]Tag, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name FROM tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	// CollectRows reads every row into a slice and closes rows for us.
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Tag, error) {
		var t Tag
		err := row.Scan(&t.ID, &t.Name)
		return t, err
	})
}

// NewPost is what the submit form hands over. Validation (lengths, URL
// format, 1-3 tags) happens before this; the database double-checks lengths.
type NewPost struct {
	AuthorID int64
	Title    string
	URL      string // empty for a text-only post
	Body     string // Markdown source, may be empty
	TagIDs   []int
}

// Post is a post as the rest of the app sees it.
type Post struct {
	ID             int64
	PublicID       string
	Title          string
	URL            string
	Body           string
	Author         string
	AuthorDeleted  bool
	CreatedAt      time.Time
	LastActivityAt time.Time  // creation, or the latest comment (bump order)
	EditedAt       *time.Time // nil if never edited (after the grace period)
	Score          int
	CommentCount   int
	Tags           []string
	Deleted        bool // by its author
	Removed        bool // by the admin
}

// Public IDs are 8 characters of lowercase base32 (a-z, 2-7): 40 random
// bits, about a trillion possibilities, so guessing one is hopeless.
var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newPublicID() (string, error) {
	b := make([]byte, 5) // 5 bytes = 40 bits = exactly 8 base32 characters
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(idEncoding.EncodeToString(b)), nil
}

// contentHash fingerprints a post's content for duplicate detection later:
// the same text posted again, by anyone, is almost always spam.
func contentHash(title, url, body string) string {
	norm := strings.ToLower(strings.TrimSpace(title)) + "\x00" +
		strings.TrimSpace(url) + "\x00" +
		strings.ToLower(strings.Join(strings.Fields(body), " "))
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}

// CreatePost saves a post and its tags in one transaction, returning the
// post's public ID. If the random ID happens to collide with an existing one
// (astronomically unlikely), it simply tries another.
func (s *Store) CreatePost(ctx context.Context, p NewPost) (string, error) {
	var url *string // NULL for text posts
	if p.URL != "" {
		url = &p.URL
	}
	hash := contentHash(p.Title, p.URL, p.Body)

	for range 5 {
		publicID, err := newPublicID()
		if err != nil {
			return "", err
		}

		err = s.createPostWithID(ctx, p, publicID, url, hash)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "posts_public_id_key" {
			continue // ID collision: roll again
		}
		if err != nil {
			return "", err
		}
		return publicID, nil
	}
	return "", errors.New("could not find a free public ID")
}

func (s *Store) createPostWithID(ctx context.Context, p NewPost, publicID string, url *string, hash string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var postID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO posts (public_id, author_id, title, url, body, content_hash)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		publicID, p.AuthorID, p.Title, url, p.Body, hash,
	).Scan(&postID)
	if err != nil {
		return err
	}

	for _, tagID := range p.TagIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO post_tags (post_id, tag_id) VALUES ($1, $2)`, postID, tagID,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// PostByPublicID loads one post with its author and tags.
// found is false if there's no such post.
func (s *Store) PostByPublicID(ctx context.Context, publicID string) (p Post, found bool, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT p.id, p.public_id, p.title, coalesce(p.url, ''), p.body,
		       u.username, u.deleted_at IS NOT NULL,
		       p.created_at, p.edited_at, p.score, p.comment_count,
		       coalesce(array_agg(t.name ORDER BY t.name) FILTER (WHERE t.name IS NOT NULL), '{}'),
		       p.deleted_at IS NOT NULL, p.removed_at IS NOT NULL
		FROM posts p
		JOIN users u ON u.id = p.author_id
		LEFT JOIN post_tags pt ON pt.post_id = p.id
		LEFT JOIN tags t ON t.id = pt.tag_id
		WHERE p.public_id = $1
		GROUP BY p.id, u.id`,
		publicID,
	).Scan(&p.ID, &p.PublicID, &p.Title, &p.URL, &p.Body,
		&p.Author, &p.AuthorDeleted,
		&p.CreatedAt, &p.EditedAt, &p.Score, &p.CommentCount,
		&p.Tags, &p.Deleted, &p.Removed)

	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, false, nil
	}
	if err != nil {
		return Post{}, false, err
	}
	return p, true, nil
}

// RemovePost is the admin's removal: the post stays in the database (so the
// thread and its comments survive) but is hidden from everyone else.
func (s *Store) RemovePost(ctx context.Context, publicID string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE posts SET removed_at = now() WHERE public_id = $1 AND removed_at IS NULL`, publicID)
	return err
}
