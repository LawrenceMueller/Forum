// Command seed fills a development database with a made-up but realistic
// Snowball: accounts, posts, threads, and votes, shaped like a real forum.
// It's for seeing what a lived-in site looks like, and for measuring how the
// queries cope with a lot of data. It is not a test: it checks nothing about
// the site's behavior (the test suite does that).
//
// Run it from the project root:
//
//	go run ./cmd/seed                  a small site, in snowball_dev
//	go run ./cmd/seed -size large      a year of a busy forum (a minute or two)
//	go run ./cmd/seed -seed 7          a different site; the same seed always builds the same one
//
// Then start Snowball against that database (PowerShell):
//
//	$env:DATABASE_URL = "postgres://snowball:snowball-dev-only@localhost:5432/snowball_dev?sslmode=disable"
//	go run ./cmd/snowball
//	Remove-Item Env:DATABASE_URL      # afterwards, back to your usual dev database
//
// It EMPTIES the target database first, so it only accepts databases whose
// names end in _dev, on this computer. Your usual dev database (snowball)
// and the test databases (*_test) are never touched.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"snowball/internal/auth"
	"snowball/internal/dbsetup"
)

const defaultURL = "postgres://snowball:snowball-dev-only@localhost:5432/snowball_dev?sslmode=disable"

// Every seeded account has this password, so you can log in as anyone.
// (It's only ever used on your own machine.)
const seedPassword = "Seeded-Otter-42!"

// Must match devVoteSecret in cmd/snowball/main.go, so the site recognizes
// seeded accounts' votes as theirs. SNOWBALL_VOTE_SECRET overrides both.
const devVoteSecret = "snowball-dev-only-vote-secret-do-not-use-in-production"

func voteSecret() string {
	if s := os.Getenv("SNOWBALL_VOTE_SECRET"); s != "" {
		return s
	}
	return devVoteSecret
}

func main() {
	sizeName := flag.String("size", "small", "how big a site: small or large")
	seed := flag.Uint64("seed", 1, "random seed: the same seed builds the same site")
	dbURL := flag.String("db", defaultURL, "database to fill (its name must end in _dev)")
	flag.Parse()

	if err := run(*sizeName, *seed, *dbURL); err != nil {
		fmt.Fprintf(os.Stderr, "\nseed: %v\n", err)
		os.Exit(1)
	}
}

func run(sizeName string, seed uint64, dbURL string) error {
	sz, ok := sizes[sizeName]
	if !ok {
		return fmt.Errorf("unknown size %q (use small or large)", sizeName)
	}
	name, err := checkTarget(dbURL)
	if err != nil {
		return err
	}
	if _, err := dbsetup.Root(); err != nil {
		return fmt.Errorf("%w\n(run this from the project folder, where go.mod is)", err)
	}
	ctx := context.Background()
	began := time.Now()

	fmt.Printf("Preparing %s (creating it and applying migrations if needed)...\n", name)
	dbURL, err = dbsetup.Ensure(ctx, dbURL, name)
	if err != nil {
		return fmt.Errorf("%w\n(is Postgres running? try: docker compose up -d)", err)
	}
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	tags, err := loadTags(ctx, conn)
	if err != nil {
		return err
	}
	keys, err := auth.NewVoterKeys(voteSecret())
	if err != nil {
		return err
	}
	// One real argon2id hash, shared by every account: hashing thousands
	// would take minutes, since argon2 is slow on purpose.
	hash, err := auth.HashPassword(seedPassword)
	if err != nil {
		return err
	}

	fmt.Printf("Building a %s site (seed %d)...\n", sizeName, seed)
	t := time.Now()
	w := newWorld(seed, sz, time.Now(), tags)
	fmt.Printf("  made up %s accounts, %s posts, %s comments in %.1fs\n",
		commas(int64(len(w.users))), commas(int64(len(w.posts))), commas(int64(len(w.comments))),
		time.Since(t).Seconds())

	fmt.Println("Writing (in one transaction, so a failure changes nothing)...")
	if err := write(ctx, conn, w, keys, hash); err != nil {
		return err
	}

	// VACUUM ANALYZE refreshes the statistics the query planner decides with
	// (without it, Postgres would plan as if the tables were still empty),
	// and marks pages as all-visible, which index-only scans need.
	fmt.Println("Updating the planner's statistics (VACUUM ANALYZE)...")
	t = time.Now()
	if _, err := conn.Exec(ctx, "VACUUM ANALYZE"); err != nil {
		return err
	}
	fmt.Printf("  done in %.1fs\n", time.Since(t).Seconds())

	fmt.Println("Checking everything adds up...")
	if err := verify(ctx, conn); err != nil {
		return err
	}

	var dbSize string
	if err := conn.QueryRow(ctx, `SELECT pg_size_pretty(pg_database_size(current_database()))`).Scan(&dbSize); err != nil {
		return err
	}
	fmt.Printf("\nDone in %.0fs. %s is now %s.\n\n", time.Since(began).Seconds(), name, dbSize)
	printLogins(w)
	fmt.Printf(`
To browse it, start Snowball against it (PowerShell):

  $env:DATABASE_URL = "%s"
  go run ./cmd/snowball

and afterwards, to go back to your usual dev database:

  Remove-Item Env:DATABASE_URL
`, dbURL)
	return nil
}

// checkTarget refuses anything but a local database named *_dev, because
// seeding starts by emptying every table.
func checkTarget(dbURL string) (string, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return "", fmt.Errorf("bad -db URL: %w", err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if !strings.HasSuffix(name, "_dev") {
		return "", fmt.Errorf("refusing to seed %q: seeding empties the database first, so its name must end in _dev", name)
	}
	if h := u.Hostname(); h != "localhost" && h != "127.0.0.1" && h != "::1" {
		return "", fmt.Errorf("refusing to seed a database on %q: only databases on this computer can be seeded", h)
	}
	return name, nil
}

func loadTags(ctx context.Context, conn *pgx.Conn) ([]tagInfo, error) {
	rows, err := conn.Query(ctx, `SELECT id, name, facet FROM tags ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var tags []tagInfo
	index := map[int32]int32{}
	for rows.Next() {
		var t tagInfo
		if err := rows.Scan(&t.id, &t.name, &t.facet); err != nil {
			return nil, err
		}
		index[t.id] = int32(len(tags))
		tags = append(tags, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return nil, fmt.Errorf("no tags in the database (the 00010 migration loads them)")
	}

	// Every top-level topic, and everything below it (from the closure table).
	rows, err = conn.Query(ctx, `
		SELECT c.ancestor_id, c.descendant_id
		FROM tag_closure c
		JOIN tags a ON a.id = c.ancestor_id
		WHERE a.facet = 'topic'
		  AND NOT EXISTS (SELECT 1 FROM tag_parents tp WHERE tp.tag_id = a.id)
		ORDER BY c.ancestor_id, c.descendant_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var root, below int32
		if err := rows.Scan(&root, &below); err != nil {
			return nil, err
		}
		t := &tags[index[root]]
		if t.weight == 0 {
			t.weight = 3
			if w, ok := tagWeights[t.name]; ok {
				t.weight = w
			}
		}
		if below != root {
			t.below = append(t.below, index[below])
		}
	}
	return tags, rows.Err()
}

// printLogins suggests accounts worth logging in as.
func printLogins(w *world) {
	var admin string
	type busy struct {
		name  string
		posts int
	}
	counts := make([]int, len(w.users))
	for _, p := range w.posts {
		counts[p.author]++
	}
	var people []busy
	for i, u := range w.users {
		if u.admin {
			admin = u.name
		} else if u.deleted == 0 {
			people = append(people, busy{u.name, counts[i]})
		}
	}
	slices.SortFunc(people, func(a, b busy) int { return b.posts - a.posts })

	fmt.Printf("Every account's password is %s. Some to try:\n", seedPassword)
	fmt.Printf("  %-28s the admin\n", admin)
	for _, p := range people[:min(2, len(people))] {
		fmt.Printf("  %-28s one of the busiest (%d posts)\n", p.name, p.posts)
	}
	if n := len(people); n > 0 {
		fmt.Printf("  %-28s a quiet one (%d posts)\n", people[n-1].name, people[n-1].posts)
	}
}

// commas formats 1234567 as "1,234,567".
func commas(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
