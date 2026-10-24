package main

// Writing the world into Postgres.
//
// Rows go in with COPY, Postgres's bulk-loading route: one long stream of
// rows instead of one INSERT statement (and one round trip) per row. It's
// what makes millions of rows take seconds instead of an hour. COPY also
// accepts our own IDs for the "GENERATED ALWAYS" identity columns, which
// plain INSERT refuses; afterwards each table's ID sequence is moved past
// the highest ID, so rows the site creates later don't collide.
//
// Everything happens in one transaction: if anything fails, the database is
// left exactly as it was.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"snowball/internal/auth"
	"snowball/internal/store"
)

func at(t int64) any {
	if t == 0 {
		return nil
	}
	return time.Unix(t, 0)
}

// copyRows streams n rows, made by row(i), into a table.
func copyRows(ctx context.Context, tx pgx.Tx, table string, cols []string, n int, row func(i int) []any) error {
	start := time.Now()
	i := 0
	count, err := tx.CopyFrom(ctx, pgx.Identifier{table}, cols, pgx.CopyFromFunc(func() ([]any, error) {
		if i >= n {
			return nil, nil // no more rows
		}
		values := row(i)
		i++
		return values, nil
	}))
	if err != nil {
		return fmt.Errorf("copying %s: %w", table, err)
	}
	fmt.Printf("  %-14s %10s rows  %6.1fs\n", table, commas(count), time.Since(start).Seconds())
	return nil
}

// copyStream is copyRows for rows that come from a producer calling emit,
// like the votes. It runs the producer in its own goroutine and passes rows
// across a channel, so the rows never all sit in memory at once.
func copyStream(ctx context.Context, tx pgx.Tx, table string, cols []string, produce func(emit func([]any))) error {
	start := time.Now()
	rows := make(chan []any, 4096)
	stop := make(chan struct{})
	go func() {
		defer close(rows)
		produce(func(values []any) {
			select {
			case rows <- values:
			case <-stop: // the copy failed; just drain away
			}
		})
	}()
	count, err := tx.CopyFrom(ctx, pgx.Identifier{table}, cols, pgx.CopyFromFunc(func() ([]any, error) {
		values, ok := <-rows
		if !ok {
			return nil, nil
		}
		return values, nil
	}))
	close(stop)
	for range rows { // let the producer finish
	}
	if err != nil {
		return fmt.Errorf("copying %s: %w", table, err)
	}
	fmt.Printf("  %-14s %10s rows  %6.1fs\n", table, commas(count), time.Since(start).Seconds())
	return nil
}

// fingerprints gives each account's vote fingerprint, the way the site makes
// them, so logging in as a seeded account shows its votes highlighted.
// Deleted accounts' votes get random-looking fingerprints instead, as
// DeleteAccount does, so they can never be traced back.
type fingerprints struct {
	w     *world
	keys  *auth.VoterKeys
	cache []string
}

func (f *fingerprints) of(u int32, kind string, item int64) string {
	if f.w.users[u].deleted != 0 {
		sum := sha256.Sum256(fmt.Appendf(nil, "%d:%s:%d:%d", f.w.seed, kind, u, item))
		return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
	}
	if f.cache[u] == "" {
		f.cache[u] = f.keys.Voter(f.w.users[u].id)
	}
	return f.cache[u]
}

func (w *world) postBody(p *post) string {
	wr := newWriter(w.rng(saltPostText, p.id), p.tag)
	switch p.kind {
	case kindMarkdown:
		return markdownShowcase
	case kindUnicode:
		return unicodeBody
	case kindLongBody:
		return wr.longBody()
	case kindUnbroken:
		return "The same word in the body: " + unbrokenWord + "\n\nAnd the link above is very long too."
	case kindBrandNew:
		return "Posted a few minutes ago, so it shows how a brand-new post looks."
	case kindMegathread:
		return "Share whatever you're making, fixing, reading, or cooking. Hundreds of replies, so it checks how a huge thread loads."
	}
	return wr.postBody(p.url != "")
}

func write(ctx context.Context, conn *pgx.Conn, w *world, keys *auth.VoterKeys, passwordHash string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Start from empty tables (the seeded tags stay) and IDs from 1.
	if _, err := tx.Exec(ctx, `TRUNCATE users, sessions, recovery_codes, posts, post_tags, tag_posts, reports, mod_log, suspensions, admin_notes,
		comments, post_votes, comment_votes, polls, poll_options, poll_votes, mega_polls RESTART IDENTITY CASCADE`); err != nil {
		return err
	}

	err = copyRows(ctx, tx, "users",
		[]string{"id", "username", "password_hash", "created_at", "deleted_at", "is_admin"},
		len(w.users), func(i int) []any {
			u := &w.users[i]
			var hash any = passwordHash
			if u.deleted != 0 {
				hash = nil // deleted accounts keep no password
			}
			return []any{u.id, u.name, hash, at(u.created), at(u.deleted), u.admin}
		})
	if err != nil {
		return err
	}

	err = copyRows(ctx, tx, "posts",
		[]string{"id", "public_id", "author_id", "title", "url", "body", "created_at", "edited_at",
			"last_activity_at", "score", "upvotes", "downvotes", "comment_count", "content_hash",
			"deleted_at", "removed_at"},
		len(w.posts), func(i int) []any {
			p := &w.posts[i]
			var url any
			body, hash := "", ""
			if p.deleted == 0 {
				body = w.postBody(p)
				if p.url != "" {
					url = p.url
				}
				hash = store.ContentHash(p.title, p.url, body)
			}
			return []any{p.id, p.publicID, w.users[p.author].id, p.title, url, body,
				at(p.created), at(p.edited), at(p.lastActivity),
				p.up - p.down, p.up, p.down, p.visible, hash, at(p.deleted), at(p.removed)}
		})
	if err != nil {
		return err
	}

	err = copyStream(ctx, tx, "post_tags", []string{"post_id", "tag_id"}, func(emit func([]any)) {
		for i := range w.posts {
			for _, t := range w.posts[i].tags {
				emit([]any{w.posts[i].id, w.tags[t].id})
			}
		}
	})
	if err != nil {
		return err
	}

	if err := writeMegaPolls(ctx, tx, w, keys); err != nil {
		return fmt.Errorf("writing mega polls: %w", err)
	}

	// tag_posts lists each post under every tag it shows up under (what
	// tag pages read); the site keeps it up to date as posts are written,
	// so here it's filled in one go, like migration 00012 does.
	t := time.Now()
	tag, err := tx.Exec(ctx, `
		INSERT INTO tag_posts (tag_id, post_id, created_at, direct)
		SELECT c.ancestor_id, p.id, p.created_at, bool_or(c.depth = 0)
		FROM post_tags pt
		JOIN tag_closure c ON c.descendant_id = pt.tag_id
		JOIN posts p ON p.id = pt.post_id
		GROUP BY c.ancestor_id, p.id, p.created_at`)
	if err != nil {
		return fmt.Errorf("filling tag_posts: %w", err)
	}
	fmt.Printf("  %-14s %10s rows  %6.1fs\n", "tag_posts", commas(tag.RowsAffected()), time.Since(t).Seconds())

	polls := w.makePolls()
	pollsByPost := map[int32]*seedPoll{}
	for _, sp := range polls {
		pollsByPost[sp.post] = sp
	}
	if err := writePolls(ctx, tx, w, polls, keys); err != nil {
		return fmt.Errorf("writing polls: %w", err)
	}

	err = copyRows(ctx, tx, "comments",
		[]string{"id", "public_id", "post_id", "parent_id", "author_id", "depth", "body",
			"created_at", "edited_at", "upvotes", "downvotes", "content_hash", "deleted_at", "removed_at", "poll_pick"},
		len(w.comments), func(i int) []any {
			c := &w.comments[i]
			p := &w.posts[c.post]
			var parent any
			if c.parent != 0 {
				parent = c.parent
			}
			body, hash := "", ""
			if c.deleted == 0 {
				body = newWriter(w.rng(saltCommentText, c.id), p.tag).comment()
				hash = store.ContentHash("", "", body)
			}
			return []any{c.id, c.publicID, p.id, parent, w.users[c.author].id, int32(c.depth), body,
				at(c.created), at(c.edited), c.up, c.down, hash, at(c.deleted), at(c.removed), pollPick(pollsByPost, c)}
		})
	if err != nil {
		return err
	}

	fp := &fingerprints{w: w, keys: keys, cache: make([]string, len(w.users))}
	err = copyStream(ctx, tx, "post_votes", []string{"voter", "post_id", "value"}, func(emit func([]any)) {
		for i := range w.posts {
			p := &w.posts[i]
			w.postVotes(p, func(v int32, value int16) {
				emit([]any{fp.of(v, "post", p.id), p.id, value})
			})
		}
	})
	if err != nil {
		return err
	}
	err = copyStream(ctx, tx, "comment_votes", []string{"voter", "comment_id", "value"}, func(emit func([]any)) {
		for i := range w.comments {
			c := &w.comments[i]
			w.commentVotes(c, func(v int32, value int16) {
				emit([]any{fp.of(v, "comment", c.id), c.id, value})
			})
		}
	})
	if err != nil {
		return err
	}

	// A note from the admin, so the front page shows the box.
	if _, err := tx.Exec(ctx, `INSERT INTO admin_notes (body, created_at) VALUES ($1, now() - interval '2 days')`,
		"This is a made-up site from the seed program: every account, post and vote is invented. "+
			"Log in as anyone with the seed password; older notes would be listed under *notes* at the bottom of the page."); err != nil {
		return err
	}

	// Move each ID sequence past the highest ID, so the next row the site
	// inserts gets a fresh one. (Fixed table names, so building the SQL
	// from them is safe.)
	for _, table := range []string{"users", "posts", "comments"} {
		if _, err := tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence('`+table+`', 'id'),
			coalesce(max(id), 0) + 1, false) FROM `+table); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// verify checks the seeded data adds up the way the site keeps it adding
// up: every counter matches the rows it counts, and threads hang together.
func verify(ctx context.Context, conn *pgx.Conn) error {
	checks := []struct{ what, sql string }{
		{"open polls' totals match their votes", `
			SELECT count(*) FROM poll_options o JOIN polls p ON p.post_id = o.post_id
			LEFT JOIN (SELECT post_id, position, count(*) AS n FROM poll_votes GROUP BY post_id, position) v
			       ON v.post_id = o.post_id AND v.position = o.position
			WHERE p.closes_at > now() AND o.votes <> coalesce(v.n, 0)`},
		{"closed polls have no fingerprints left", `
			SELECT count(*) FROM poll_votes v JOIN polls p ON p.post_id = v.post_id WHERE p.closes_at <= now()`},
		{"post vote counters match post_votes", `
			SELECT count(*) FROM posts p
			LEFT JOIN (SELECT post_id, count(*) FILTER (WHERE value = 1) AS up,
			                  count(*) FILTER (WHERE value = -1) AS down
			           FROM post_votes GROUP BY post_id) v ON v.post_id = p.id
			WHERE p.upvotes <> coalesce(v.up, 0) OR p.downvotes <> coalesce(v.down, 0)`},
		{"comment vote counters match comment_votes", `
			SELECT count(*) FROM comments c
			LEFT JOIN (SELECT comment_id, count(*) FILTER (WHERE value = 1) AS up,
			                  count(*) FILTER (WHERE value = -1) AS down
			           FROM comment_votes GROUP BY comment_id) v ON v.comment_id = c.id
			WHERE c.upvotes <> coalesce(v.up, 0) OR c.downvotes <> coalesce(v.down, 0)`},
		{"comment counts match visible comments", `
			SELECT count(*) FROM posts p
			LEFT JOIN (SELECT post_id, count(*) AS n FROM comments
			           WHERE deleted_at IS NULL AND removed_at IS NULL GROUP BY post_id) c ON c.post_id = p.id
			WHERE p.comment_count <> coalesce(c.n, 0)`},
		{"replies are on their parent's post, one level deeper", `
			SELECT count(*) FROM comments c JOIN comments parent ON parent.id = c.parent_id
			WHERE parent.post_id <> c.post_id OR c.depth <> parent.depth + 1`},
		{"last activity is the latest comment (or the post itself)", `
			SELECT count(*) FROM posts p
			LEFT JOIN (SELECT post_id, max(created_at) AS latest FROM comments GROUP BY post_id) c ON c.post_id = p.id
			WHERE p.last_activity_at <> greatest(p.created_at, coalesce(c.latest, p.created_at))`},
		{"nothing written by an account after it was deleted", `
			SELECT (SELECT count(*) FROM posts p JOIN users u ON u.id = p.author_id WHERE p.created_at > u.deleted_at)
			     + (SELECT count(*) FROM comments c JOIN users u ON u.id = c.author_id WHERE c.created_at > u.deleted_at)`},
		{"every post has 1 to 3 topics", `
			SELECT count(*) FROM posts p
			WHERE (SELECT count(*) FROM post_tags pt JOIN tags t ON t.id = pt.tag_id
			       WHERE pt.post_id = p.id AND t.facet = 'topic') NOT BETWEEN 1 AND 3`},
		{"tag_posts matches the posts' tags and the tree", store.TagPostsMismatchesSQL},
		{"nothing written before its author's account existed", `
			SELECT (SELECT count(*) FROM posts p JOIN users u ON u.id = p.author_id WHERE p.created_at < u.created_at)
			     + (SELECT count(*) FROM comments c JOIN users u ON u.id = c.author_id WHERE c.created_at < u.created_at)`},
	}
	failed := 0
	for _, c := range checks {
		var bad int64
		if err := conn.QueryRow(ctx, c.sql).Scan(&bad); err != nil {
			return fmt.Errorf("check %q: %w", c.what, err)
		}
		mark := "ok"
		if bad != 0 {
			mark = fmt.Sprintf("FAILED (%d rows)", bad)
			failed++
		}
		fmt.Printf("  %-58s %s\n", c.what, mark)
	}
	if failed > 0 {
		return fmt.Errorf("%d checks failed", failed)
	}
	return nil
}
