// Command bench times Snowball's pages against a running server, to find
// slow pages and to check later changes didn't make one slower. Use it with
// the large seed, so the numbers mean something:
//
//	go run ./cmd/seed -size large
//	(start Snowball against snowball_dev, as the seed program explains)
//	go run ./cmd/bench                  time every kind of page
//	go run ./cmd/bench -load 30s -c 50  then 50 visitors at once, for 30 seconds
//
// It picks real examples from the database (the busiest thread, a busy
// account, a deep page of /new...), logs in as a seeded account (the seed
// password), and requests each page several times. The server must run
// against the same database the -db flag names.
//
// Times are measured here, around each whole request; on this computer
// that's almost all server time. They're best compared with each other and
// with earlier runs, not read as what visitors will see on the internet.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	defaultDB    = "postgres://snowball:snowball-dev-only@localhost:5432/snowball_dev?sslmode=disable"
	seedPassword = "Seeded-Otter-42!" // cmd/seed's password for every account
)

func main() {
	base := flag.String("base", "http://localhost:8080", "the running server")
	dbURL := flag.String("db", defaultDB, "the database the server runs against (to pick examples)")
	runs := flag.Int("n", 15, "requests per page")
	load := flag.Duration("load", 0, "after timing pages, run a load test this long (e.g. 30s)")
	conc := flag.Int("c", 50, "visitors at once during the load test")
	flag.Parse()

	if err := run(*base, *dbURL, *runs, *load, *conc); err != nil {
		fmt.Fprintf(os.Stderr, "bench: %v\n", err)
		os.Exit(1)
	}
}

// examples are the real things worth timing, picked from the database.
type examples struct {
	bigThread, typicalPost, deepComment string // /p/... addresses
	wideTag, narrowTag                  string
	busyUser, admin                     string
	samplePosts                         []string // for the load test
}

func pick(ctx context.Context, dbURL string) (examples, error) {
	var ex examples
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return ex, fmt.Errorf("%w (is Postgres running, and is -db right?)", err)
	}
	defer conn.Close(ctx)

	queries := []struct {
		sql  string
		dest *string
	}{
		{`SELECT public_id FROM posts WHERE removed_at IS NULL AND deleted_at IS NULL
		  ORDER BY comment_count DESC LIMIT 1`, &ex.bigThread},
		// A post with a middling number of comments: what most visits look like.
		{`SELECT public_id FROM posts WHERE removed_at IS NULL AND deleted_at IS NULL AND comment_count BETWEEN 10 AND 30
		  ORDER BY id LIMIT 1`, &ex.typicalPost},
		{`SELECT p.public_id || '/c/' || c.public_id FROM comments c JOIN posts p ON p.id = c.post_id
		  WHERE c.depth >= 4 AND c.removed_at IS NULL AND c.deleted_at IS NULL AND p.removed_at IS NULL
		  ORDER BY c.id LIMIT 1`, &ex.deepComment},
		// The top-level topic with the most posts underneath, and a busy leaf.
		{`SELECT t.name FROM tags t JOIN tag_closure c ON c.ancestor_id = t.id JOIN post_tags pt ON pt.tag_id = c.descendant_id
		  WHERE NOT EXISTS (SELECT 1 FROM tag_parents WHERE tag_id = t.id) AND t.facet = 'topic'
		  GROUP BY t.name ORDER BY count(*) DESC LIMIT 1`, &ex.wideTag},
		{`SELECT t.name FROM tags t JOIN post_tags pt ON pt.tag_id = t.id
		  WHERE t.facet = 'topic' AND NOT EXISTS (SELECT 1 FROM tag_parents WHERE parent_id = t.id)
		  GROUP BY t.name ORDER BY count(*) DESC LIMIT 1`, &ex.narrowTag},
		{`SELECT u.username FROM users u JOIN posts p ON p.author_id = u.id
		  WHERE u.deleted_at IS NULL AND NOT u.is_admin GROUP BY u.username ORDER BY count(*) DESC LIMIT 1`, &ex.busyUser},
		{`SELECT username FROM users WHERE is_admin AND deleted_at IS NULL LIMIT 1`, &ex.admin},
	}
	for _, q := range queries {
		if err := conn.QueryRow(ctx, q.sql).Scan(q.dest); err != nil {
			return ex, fmt.Errorf("picking examples (is this a seeded database?): %w", err)
		}
	}
	rows, err := conn.Query(ctx, `SELECT public_id FROM posts WHERE removed_at IS NULL AND deleted_at IS NULL
		ORDER BY random() LIMIT 300`)
	if err != nil {
		return ex, err
	}
	ex.samplePosts, err = pgx.CollectRows(rows, pgx.RowTo[string])
	return ex, err
}

// visitor is one browser: its own cookies, so it can be logged in.
type visitor struct {
	base   string
	client *http.Client
}

func newVisitor(base string) *visitor {
	jar, _ := cookiejar.New(nil)
	return &visitor{base: base, client: &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		// Report redirects instead of following them.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// get fetches a page and returns its status, body and time taken.
func (v *visitor) get(path string) (int, string, time.Duration, error) {
	start := time.Now()
	resp, err := v.client.Get(v.base + path)
	if err != nil {
		return 0, "", 0, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, string(body), time.Since(start), err
}

var tokenField = regexp.MustCompile(`name="form_token" value="([^"]+)"`)

// login signs the visitor in as name, through the real login form.
func (v *visitor) login(name string) error {
	_, page, _, err := v.get("/login")
	if err != nil {
		return err
	}
	m := tokenField.FindStringSubmatch(page)
	if m == nil {
		return fmt.Errorf("no form token on /login")
	}
	resp, err := v.client.PostForm(v.base+"/login", url.Values{
		"username": {name}, "password": {seedPassword}, "form_token": {m[1]},
	})
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Set-Cookie"), "=") {
		return fmt.Errorf("logging in as %s failed (status %d): was the database seeded with the seed program?", name, resp.StatusCode)
	}
	return nil
}

var olderLink = regexp.MustCompile(`href="(/new\?after=[^"]+)"`)

// deepPage follows "older posts" from /new, depth pages in.
func (v *visitor) deepPage(depth int) (string, error) {
	path := "/new"
	for range depth {
		_, body, _, err := v.get(path)
		if err != nil {
			return "", err
		}
		m := olderLink.FindStringSubmatch(body)
		if m == nil {
			return "", fmt.Errorf("no older-posts link on %s", path)
		}
		path = strings.ReplaceAll(m[1], "&amp;", "&")
	}
	return path, nil
}

type page struct {
	label, path string
	who         *visitor
}

func run(base, dbURL string, runs int, load time.Duration, conc int) error {
	ctx := context.Background()
	ex, err := pick(ctx, dbURL)
	if err != nil {
		return err
	}
	anon, member, admin := newVisitor(base), newVisitor(base), newVisitor(base)
	if _, _, _, err := anon.get("/"); err != nil {
		return fmt.Errorf("%w (is Snowball running at %s?)", err, base)
	}
	if err := member.login(ex.busyUser); err != nil {
		return err
	}
	if err := admin.login(ex.admin); err != nil {
		return err
	}
	deep, err := anon.deepPage(30)
	if err != nil {
		return err
	}

	pages := []page{
		{"front page", "/", anon},
		{"/new", "/new", anon},
		{"/new, 30 pages in", deep, anon},
		{"/active", "/active", anon},
		{"/top", "/top", anon},
		{"/tags", "/tags", anon},
		{"wide tag (" + ex.wideTag + ")", "/t/" + ex.wideTag, anon},
		{"wide tag, only", "/t/" + ex.wideTag + "?only=1", anon},
		{"narrow tag (" + ex.narrowTag + ")", "/t/" + ex.narrowTag, anon},
		{"format tag (question)", "/t/question", anon},
		{"typical post", "/p/" + ex.typicalPost, anon},
		{"biggest thread", "/p/" + ex.bigThread, anon},
		{"biggest thread, newest", "/p/" + ex.bigThread + "?sort=new", anon},
		{"deep comment thread", "/p/" + ex.deepComment, anon},
		{"type-ahead", "/tags/suggest?topics=sour", anon},
		{"front page, logged in", "/", member},
		{"typical post, logged in", "/p/" + ex.typicalPost, member},
		{"biggest thread, logged in", "/p/" + ex.bigThread, member},
		{"submit page", "/submit", member},
		{"account", "/account", member},
		{"history: posts", "/account/history", member},
		{"history: comments", "/account/history?show=comments", member},
		{"history: upvoted", "/account/history?show=upvoted", member},
		{"history: downvoted", "/account/history?show=downvoted", member},
		{"admin: tags", "/admin/tags", admin},
		{"admin: one tag", "/admin/tags/" + ex.wideTag, admin},
	}

	fmt.Printf("Timing %d pages, %d requests each (after one warm-up)...\n\n", len(pages), runs)
	fmt.Printf("%-34s %8s %8s %8s %9s\n", "page", "median", "p95", "max", "size")
	for _, p := range pages {
		if status, _, _, err := p.who.get(p.path); err != nil || status != http.StatusOK {
			fmt.Printf("%-34s  failed: status %d, %v (%s)\n", p.label, status, err, p.path)
			continue
		}
		var times []time.Duration
		size := 0
		for range runs {
			_, body, took, err := p.who.get(p.path)
			if err != nil {
				return err
			}
			times = append(times, took)
			size = len(body)
		}
		slices.Sort(times)
		fmt.Printf("%-34s %8s %8s %8s %8dK\n", p.label, ms(percentile(times, 50)), ms(percentile(times, 95)),
			ms(times[len(times)-1]), size/1024)
	}

	if load > 0 {
		return loadTest(base, ex, deep, load, conc)
	}
	return nil
}

// loadTest has conc logged-out visitors browse at once, as fast as they
// can: mostly lists and posts, like real reading.
func loadTest(base string, ex examples, deep string, d time.Duration, conc int) error {
	fmt.Printf("\nLoad test: %d visitors at once for %s...\n", conc, d)
	paths := []string{"/", "/", "/", "/new", "/active", "/top", "/tags", "/t/" + ex.wideTag, "/t/" + ex.narrowTag, deep}
	for range 10 { // posts: about half the requests
		paths = append(paths, "")
	}

	var (
		mu       sync.Mutex
		times    []time.Duration
		failures atomic.Int64
		wg       sync.WaitGroup
	)
	stop := time.Now().Add(d)
	for range conc {
		wg.Go(func() {
			v := newVisitor(base)
			var mine []time.Duration
			for time.Now().Before(stop) {
				path := paths[rand.IntN(len(paths))]
				if path == "" {
					path = "/p/" + ex.samplePosts[rand.IntN(len(ex.samplePosts))]
				}
				status, _, took, err := v.get(path)
				if err != nil || status != http.StatusOK {
					failures.Add(1)
					continue
				}
				mine = append(mine, took)
			}
			mu.Lock()
			times = append(times, mine...)
			mu.Unlock()
		})
	}
	wg.Wait()
	if len(times) == 0 {
		return fmt.Errorf("no request succeeded")
	}
	slices.Sort(times)
	fmt.Printf("  %d pages in %s: %.0f pages a second\n", len(times), d, float64(len(times))/d.Seconds())
	fmt.Printf("  median %s, p95 %s, p99 %s, slowest %s\n", ms(percentile(times, 50)), ms(percentile(times, 95)),
		ms(percentile(times, 99)), ms(times[len(times)-1]))
	if n := failures.Load(); n > 0 {
		fmt.Printf("  %d requests failed or weren't 200\n", n)
	}
	return nil
}

func percentile(sorted []time.Duration, p int) time.Duration {
	i := (len(sorted) - 1) * p / 100
	return sorted[i]
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000) }
