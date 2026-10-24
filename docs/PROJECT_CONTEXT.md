# Snowball: Project Context

This document is the single place to understand Snowball: what it is, how it is built, what exists today and where it lives in the code, and what is planned. Someone who has never seen the project should be able to read it top to bottom and start working on tickets.

Last updated: after the admin's thread controls (locking comments, closing a poll early, taking today's mega poll down and running the next one; section 7.24, migration 00022), and before that mega polls and the `/admin` hub (section 7.23, migration 00021), which finished the polls chapter begun with polls on posts (section 7.22, migration 00020). Before that: the `/about` page and reply notifications (section 7.21, migration 00019), notes from the admin (section 7.20, migration 00018) and deleting from the post page (section 7.15), the first finishing touches, which followed the moderation chapter, all five slices (reports and the queue, suspensions and appeals, cleanup and spam shadowbans, probation and content checks, 30-day erasure; section 7.19, migrations 00013 to 00017; design and decisions in section 11.2), with the startup check that the database has every migration. Before that: the performance pass (section 10, "Performance"), the admin tag page and topic type-ahead (tags v2 Phase 1, finished), tags v2 Phase 1 part A, the tag system v2 design, the voted history views, the seed program and the testing chapter. Next up, to choose with the owner: images (after researching the legal duties, section 11.2), tags Phase 2 (members asking for tags), or launch preparation (section 11.4).

---

## 1. What Snowball is

Snowball is a small, anonymous, Reddit-style forum, run by one person. Four values drive nearly every decision, and a ticket that conflicts with one of them should be questioned before it is built.

**Anonymity.** Accounts are not tied to real identities. There is no email, no real name, and users cannot choose their username: the site generates one (like `blue-humming-otter`). Account history exists, but it never traces back to a person.

**Store as little as possible.** No email addresses, no IP addresses, no user agents, no device lists, no last-login times, no tracking, no analytics, no ads. Anything not stored can never leak. Rate limiting uses IPs only in memory, briefly, and never writes them anywhere. Votes are stored against keyed fingerprints, not account IDs, and with no timestamps. When a feature seems to need personal data, the default answer is to find a design that doesn't.

**Manageable by one person.** Moderation surface and operational load are kept small. That is why there are tags instead of per-community boards, and why defenses are automated.

**Hand-built, old-web character.** The site should feel like something a person made: dense, text-first, honest browser defaults (underlined links, visited colors), one deliberate accent color, informative footer, copy written in the owner's voice. It is not a retro costume; it is modern, responsive, and fast.

The project is also a learning vehicle for in-demand skills (Go, PostgreSQL, Docker, security engineering), so clarity and good practice in the code matter as much as features.

---

## 2. Tech stack

| Layer | Choice | Notes |
|---|---|---|
| Language | Go 1.25 or newer (developed on 1.27.2) | 1.25 is required for `http.NewCrossOriginProtection` (CSRF defense). Keep up with patch releases: govulncheck flags standard-library holes they fix. |
| Web layer | Go standard library `net/http` | No framework. Method-and-path routing (`"GET /p/{id}"`). |
| Templates | Go `html/template` | Auto-escapes all output. Server-rendered HTML. |
| Interactivity | htmx 2.x (self-hosted) | In-place updates: username reroll, reply forms, vote arrows. |
| Site JavaScript | `static/js/snowball.js` | Small, plain JS. No inline scripts anywhere (CSP forbids them). |
| Markdown | `github.com/yuin/goldmark` + `github.com/microcosm-cc/bluemonday` | Render with HTML off, then sanitize against an allowlist. |
| Database | PostgreSQL 18 (Docker) | Accessed through `pgx` v5 with a connection pool. |
| Migrations | goose (CLI) | SQL files in `migrations/`, applied in order. Tests also use goose as a Go library (`internal/testdb`). |
| Password hashing | argon2id (`golang.org/x/crypto/argon2`) | Wrapped in `internal/auth`. |
| Unicode normalization | `golang.org/x/text/unicode/norm` | NFKC for passwords. |
| Rate limiting | `golang.org/x/time/rate` | Token buckets, in memory. |
| Local infrastructure | Docker Compose | Postgres only, for now. |
| Checks | gofmt, go vet, staticcheck, govulncheck, the race detector | All run by `scripts/check.ps1`, locally and in CI (section 10). |
| CI | GitHub Actions | `.github/workflows/ci.yml`: Postgres 18 service, then `scripts/check.ps1`. |
| Planned production | VPS + Docker Compose + Caddy | Caddy for HTTPS; optionally Anubis in front of the app. |

Go module name: `snowball` (the first line of `go.mod` must be `module snowball`, because internal imports are written as `snowball/internal/...`).

---

## 3. Running the project locally

### Prerequisites

Go 1.25+, Docker Desktop (on Windows, with WSL2), Git, and the goose CLI:

```
go install github.com/pressly/goose/v3/cmd/goose@latest
```

New clone: `cp .env.example .env` (it holds development values only).

### Everyday commands (run from the project root)

| Task | Command |
|---|---|
| Start Postgres | `docker compose up -d` |
| Check Postgres is healthy | `docker compose ps` |
| Apply migrations | `goose up` |
| See migration status | `goose status` |
| Undo the latest migration | `goose down` |
| Sync dependencies | `go mod tidy` (prints nothing on success) |
| Format code | `go fmt ./...` |
| Check code | `go vet ./...` |
| Run every check (what CI runs) | `.\scripts\check.ps1` (section 10) |
| Quick check while working | `.\scripts\check.ps1 -Quick` (no database tests, no staticcheck/govulncheck) |
| Run tests only | `go test ./...` (add `-v` for names, `-cover` for coverage, `-short` to skip database tests) |
| Run the site | `go run ./cmd/snowball` then open http://localhost:8080 |
| Database shell | `docker compose exec db psql -U snowball` |
| Database shell (from any folder) | `docker exec -it snowball-db-1 psql -U snowball` |
| Stop Postgres | `docker compose down` |
| Wipe the database completely | `docker compose down -v` (destroys all data) |
| Fill `snowball_dev` with a made-up site | `go run ./cmd/seed` (or `-size large`; see "Seeded data" below) |
| Time every page (server running against `snowball_dev`) | `go run ./cmd/bench` (add `-load 30s -c 50` for a load test; section 10, "Performance") |

Before every commit: `go fmt ./...`, then `.\scripts\check.ps1`, then `git status` to check `.env` is not staged and new migrations are.

The server must be started from the project root, because templates and static files are found relative to the current folder. If goose prints its help text instead of running, it couldn't read its settings from `.env` (missing `GOOSE_` lines, or the file saved as UTF-16).

### Configuration

`.env` (never committed; `.env.example` is the committed template) holds local development settings. Docker Compose and goose both read it automatically.

| Variable | Used by | Purpose |
|---|---|---|
| `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` | Compose | Creates the database user and database on first start. Dev values: `snowball` / `snowball-dev-only` / `snowball`. |
| `GOOSE_DRIVER`, `GOOSE_DBSTRING`, `GOOSE_MIGRATION_DIR` | goose | Where migrations run. |

Environment variables read by the Go app (not from `.env`; set in the shell or deployment):

| Variable | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | Local dev URL (`devDatabaseURL` in `cmd/snowball/main.go`) | Postgres connection string. The fallback is logged when used. |
| `SNOWBALL_VOTE_SECRET` | A development secret (`devVoteSecret` in main.go), logged when used | Keys the fingerprints stored with votes (section 7.13). At least 32 characters or the app refuses to start. **In production it must be set, kept stable forever, and backed up separately from the database.** Changing it makes everyone's past votes unrecognized as theirs. Generate with `openssl rand -hex 32`. |
| `SNOWBALL_TRUST_PROXY` | unset | Set to `1` only when running behind Caddy (or another reverse proxy). Makes the app read the visitor IP from the last `X-Forwarded-For` entry. Never set without a proxy. |

### Docker notes

`compose.yaml` starts with `name: snowball`, so the Compose project, container (`snowball-db-1`), and volume names stay the same whatever folder the project is in (without it, Compose names everything after the folder, and a renamed folder looks like a different, stopped project). It runs `postgres:18`, binds port `5432` to `127.0.0.1` only, stores data in the named volume `pgdata` mounted at `/var/lib/postgresql` (where Postgres 18 images expect it), and has a `pg_isready` healthcheck.

### Seeded data

`go run ./cmd/seed` builds a made-up but realistic site in its own database, `snowball_dev`, for seeing what a lived-in Snowball looks like and for measuring queries against real volume. It is not a test (nothing is checked about the site's behavior). It empties `snowball_dev` first, so it refuses any database not named `*_dev` or not on this computer; the usual dev database (`snowball`) and the test databases are never touched.

| Size | Contents | Time |
|---|---|---|
| `small` (default) | 120 accounts, about 410 posts over 30 days, 4,500 comments, 22,000 votes | about a second |
| `large` | 20,000 accounts, 100,000 posts over a year, 1.2 million comments, 5.8 million votes (about 2 GB) | a minute or two |

Browse it by pointing the site at it, then switch back:

```powershell
$env:DATABASE_URL = "postgres://snowball:snowball-dev-only@localhost:5432/snowball_dev?sslmode=disable"
go run ./cmd/snowball
Remove-Item Env:DATABASE_URL
```

Every seeded account's password is `Seeded-Otter-42!`; the program prints the admin's name and a few others worth logging in as. Votes use the real fingerprints (the dev vote secret, or `SNOWBALL_VOTE_SECRET` if set), so a seeded account sees its own votes highlighted.

How it's built (`cmd/seed`): `generate.go` makes up the world with realistic shapes: activity per account follows a power law (a few people post and vote most), post "quality" is lognormal (most posts are ordinary, a few take off), recent posts are still collecting votes and comments, replies cluster around recent comments, and some content is edited, deleted, or removed. `text.go` writes the words from templates and per-tag word lists (nothing real; links only to reserved example domains). `write.go` loads everything with Postgres `COPY` in one transaction, sets the ID sequences past the highest ID, and `main.go` then runs `VACUUM ANALYZE` (so the planner has statistics) and checks every counter matches its rows. Votes are never held in memory: each item's votes come from their own seeded generator, produced once to count and again to write. The same `-seed` builds the same site (times are relative to the run).

Every seeded site also gets fixtures near the top of "new" for checking the layout: a brand-new post, the longest allowed title, an unbroken 100-character word with a very long link, Unicode and emoji, every Markdown feature, a nearly 40,000-character post, a 600-comment (large: 2,500, past the 2,000-comment load cap) megathread, a reply chain 14 levels deep (past the continue-this-thread depth), a controversial post, a deleted post with its discussion, a removed post, and a thread with deleted and removed comments.

### Manual testing tips

(Automated tests are in section 10.)

Voting needs two accounts (nobody can vote on their own content) that are at least a day old. On a development database, age every account with `UPDATE users SET created_at = now() - interval '2 days';` (never without a `WHERE` anywhere else). Admin rights are granted only in psql: `UPDATE users SET is_admin = true WHERE username = '...';`.

To test pagination, generate posts in psql (public IDs may only use `a-z` and `2-7`, so `translate` swaps out the other hex digits):

```sql
INSERT INTO posts (public_id, author_id, title, content_hash, created_at, last_activity_at)
SELECT translate(substr(md5(random()::text), 1, 8), '0189', 'wxyz'),
       (SELECT id FROM users WHERE deleted_at IS NULL ORDER BY id LIMIT 1),
       'Test post number ' || g, md5(g::text),
       now() - g * interval '1 minute', now() - g * interval '1 minute'
FROM generate_series(1, 75) AS g;
-- clean up: DELETE FROM posts WHERE title LIKE 'Test post number %';
```

All rate limits live in memory, so restarting the server resets them. When test content all comes from one account, the per-author vote limit (10 a day) is the first one you'll hit.

---

## 4. Repository layout

```
snowball/
  cmd/snowball/main.go      Wiring only: config, DB, server, timeouts, shutdown, session cleanup loop.
  cmd/seed/                 Development tool: fills snowball_dev with a made-up site (section 3, "Seeded data").
  cmd/bench/                Development tool: times every kind of page against a running server, plus a load test (section 10, "Performance").
    main.go                 Flags, the *_dev safety check, then build, write, VACUUM ANALYZE, verify.
    generate.go             The made-up world: accounts, posts, threads, votes, fixtures.
    text.go                 Titles, bodies, comments from templates and word lists; layout-testing oddities.
    write.go                COPY into Postgres, sequence fixes, the consistency checks.
  internal/
    auth/                   Pure security logic. No HTTP, no database. Easy to test.
      passwords.go          Password rules, NFKC normalization, argon2id hashing, hashing semaphore, dummy verify.
      recovery.go           Recovery code generation, normalization, hashing.
      tokens.go             Session token generation and hashing.
      usernames.go          Word lists, username generation (RandomName, GenerateUsername), NameSigner (signs offered names).
      formtokens.go         FormTokens: signed timestamps for anti-bot form checks.
      voters.go             VoterKeys: account ID -> vote fingerprint (HMAC with the vote secret).
    markdown/               Markdown -> sanitized HTML. The only code that returns template.HTML.
    tags/                   The tag system's rules, no HTTP or database: Normalize, the Tree (ancestors,
                            paths, lookups), Choose (a post's tags from the form), Chips (how they show).
    dbsetup/                Development helper: finds the project root (Root), creates a database if
                            missing and applies every migration (Ensure). Used by testdb and cmd/seed.
    testdb/                 Test-only helper: finds the project root, creates and migrates a named test
                            database (Prepare), lists the tables tests empty. Also holds the migration
                            round-trip test.
    store/                  The only package that talks to Postgres. All SQL lives here.
      store.go              Open (pool + ping + version check), Close.
      users.go              Users: availability, creation, credentials, password change, soft delete.
      sessions.go           Session create/lookup/delete, delete-all-for-user, expired cleanup.
      recovery.go           Account recovery transaction, remaining code count.
      posts.go              Creating posts (random public IDs), loading one post.
      tags.go               Tag rows (tags, parents, aliases) for building the tree; tag post counts.
      tagadmin.go           Changing the tag tree (create, rename, parents, aliases, merge, delete), checked and closure-rebuilt in one transaction.
      tagposts.go           Keeping tag_posts (every post under every tag it shows up under) in step.
      lists.go              Post lists with keyset pagination (new, active, top, hot), tag filters, post count.
      comments.go           Loading a post's comments, creating comments.
      moderation.go         Reports (with auto-hiding), removing with a reason, dismissing, the queue, the moderation log.
      suspensions.go        Suspensions, appeals, the admin's view of an account.
      cleanup.go            Cleaning up an account (removals and vote withdrawal), shadowbans.
      schema.go             SchemaVersion and CheckSchema: the server refuses a database missing migrations.
      spam.go               The domain blocklist; finding duplicate posts and comments by content hash.
      retention.go          Purge: erasing removed text, deleting handled reports, emptying old suspensions after 30 days.
      notes.go              Notes from the admin.
      replies.go            Reply notifications: the counter bumped with each reply, and the inbox query.
      polls.go              Polls: creating, voting, totals, per-poll fingerprints, closing.
      megapolls.go          The mega poll queue, publishing, the /polls archive, running the next one now.
      locks.go              Locking a post's comments; closing a poll early.
      votes.go              Voting (toggle logic, counts), the viewer's votes, vote state, author lookup.
      edits.go              Editing posts and comments (window and grace enforced in SQL).
      deletes.go            Erasing posts and comments, erasing an account's content, history queries.
    web/                    Everything HTTP: server, routes, middleware, handlers, rendering.
      server.go             Server, Config, New, routes and middleware stack, rate limit settings, template funcs.
      render.go             Template loading, basePage, render/renderFragment, serverError, hashError, taglines.
      session.go            Session cookie, withUser middleware, startSession, userFrom.
      headers.go            Security headers middleware, robots.txt, noStore, revalidate.
      ratelimit.go          Token-bucket limiter, clientIP, limit wrapper, 429 responses, friendlyDuration.
      loginguard.go         Per-account failed-login slowdown.
      formguard.go          Honeypot + signed timing token checks for every form.
      stats.go              Cached post count for the footer, commas formatting.
      home.go               Name colors, sort bar data, front page, list pages.
      tags.go               The tag tree in memory, tag pages, /tags, the sidebar, the forms' tag fields, the type-ahead.
      tagadmin.go           The admin tag page (/admin/tags).
      moderation.go         Report pages, the admin's queue (/admin/reports), removal reasons, /modlog, /rules.
      suspensions.go        The read-only guard, the suspension banner and page with its appeal, /admin/users/{name}.
      spamchecks.go         Probation, duplicate and blocklist checks on new posts and comments; /admin/domains.
      notes.go              Notes from the admin: the front page box, /notes, /admin/notes.
      replies.go            The reply inbox, /account/replies.
      polls.go              Polls on posts: the submit fields, the poll box, voting, live results.
      megapolls.go          Mega polls (publishing, the front page box, /polls, /admin/polls) and the /admin hub.
      locks.go              The admin's thread controls: lock, close a poll now, take today's mega poll down, run the next.
      posts.go              Submit page, post validation, post view preparation, post page, admin removal.
      comments.go           Comment trees (build, prune, sort, depth rules), Wilson score, reply forms, thread pages.
      votes.go              Vote arrows (widgets), who may vote, vote limits, the vote handler.
      edit.go               Edit pages and handlers, the edit-window helper, the edit limit.
      history.go            The private history page, deleting your own posts and comments.
      signup.go, login.go, recover.go, account.go   The account flows.
    (each package also has *_test.go files: *_db_test.go need Postgres, fuzz_test.go hold
     fuzz targets, web/requests_test.go and web/smoke_test.go drive the whole site; section 10)
  migrations/               goose SQL migrations, numbered. Never edit one that has run; add a new one.
  templates/
    layout.html             Shared frame: head, masthead, account links, footer.
    pages/                  One file per page; each defines a "content" block.
    partials/               Reusable pieces, also returned alone as htmx fragments.
  static/
    css/style.css           The only stylesheet. All CSS goes here.
    js/htmx.min.js          Self-hosted htmx.
    js/snowball.js          Site behavior (hidden username toggle, topic type-ahead keys).
  scripts/check.ps1         Every check in one command; CI runs it too (section 10).
  .github/workflows/ci.yml  GitHub Actions: Postgres service, then scripts/check.ps1.
  docs/PROJECT_CONTEXT.md   This document.
  compose.yaml, .env.example, .gitignore, .gitattributes, README.md, go.mod, go.sum
```

Several files share names across folders (`recovery.go` in both `auth` and `store`; `signup.go` in `web` and `signup.html` in templates; `partials/post.html` is one row in a list, while `pages/postpage.html` is a whole post). Always check the folder.

---

## 5. Architecture

### Package boundaries

Dependencies flow one way: `cmd/snowball` uses `web` and `store`; `web` uses `store`, `auth`, `markdown`, and `tags`; `store`, `auth`, `markdown`, and `tags` depend on nothing internal (the web package turns the store's tag rows into a `tags.Tree`). `testdb` is imported only by test files (in `store` and `web`), and `dbsetup` only by `testdb` and `cmd/seed`; the site itself uses neither. Go forbids import cycles, so this direction must be kept.

`auth` never sees a request or a database. `store` never sees a raw secret: it receives only hashes and fingerprints (password hashes, token hashes, recovery code hashes, vote fingerprints). `web` translates between them and owns everything user-facing.

### Request lifecycle

A request passes through the middleware stack, outermost first, then reaches its handler:

1. `securityHeaders` (web/headers.go) stamps security headers on every response, including rejected ones.
2. `http.NewCrossOriginProtection` rejects cross-site POSTs (CSRF), using `Sec-Fetch-Site`/`Origin` headers.
3. `withUser` (web/session.go) reads the session cookie, looks up the session, and attaches the user (`store.User`: ID, username, admin flag, account creation time) to the request context. Skipped for `/static/`. Logged-in responses get `Cache-Control: no-store`.
4. The mux routes to a handler, possibly through per-route wrappers: `limit` (rate limiting) and `noStore`.
5. The handler gathers data (from `store`), builds a page struct, and calls `render`.

Handlers find the current user with `userFrom(r.Context())`.

### Rendering

At startup, `loadPages` builds one template set per page: `layout.html` + all `partials/*.html` + that one page. Sets are separate because every page defines a block called `content`. Templates are parsed once, so **template changes need a server restart**.

`render(w, "page.html", data)` executes the `layout` template. `renderFragment(w, "page.html", "partialName", data)` executes a single partial, which is how htmx endpoints respond (the reroll returns the `username` partial; reply links return `replyform`; votes return `votes`). Both render into a buffer first, so a template error produces a clean 500 instead of half a page. Handlers tell htmx requests apart by the `HX-Request: true` header.

Every page struct embeds `basePage`: `Title`, `Tagline` (random from `taglines` in render.go), `User` (empty when logged out), `HideAccount` (hides the header account links on focused pages), and `Generated` (time spent building the data, shown in the footer).

Every page with a logical parent declares it with `newBasePage(r, "...").withUp(url, label)`, and the layout draws a `← label` "up" link in one consistent spot, just under the yellow bar. It always goes to the page's place in the site's structure (not "back" in browser history), so it works however someone arrived and needs no JavaScript.

Template functions, registered before parsing (`Server.templateFuncs`): `postCount` (the cached number of visible posts) and `commas` (12408 -> "12,408"). The footer uses `{{postCount | commas}}`.

Templates follow a rule: handlers do the mapping (database rows into structs, decisions, calculations), templates only display. Templates can call themselves: `partials/comment.html` renders a comment, then `{{template "comment" .}}` for each reply.

### Background work

Two goroutines run for the life of the server: expired-session cleanup (at startup, then hourly, in main.go) and the post count refresh (every minute, in stats.go). Each rate limiter also runs a cleanup loop (section 7.8).

### Static files

Served from `static/` under `/static/` by Go's file server, with `Cache-Control: no-cache` (the browser keeps a copy but revalidates each time, getting a cheap 304 when unchanged). CSS/JS edits appear on the next reload with no restart.

### Server settings (cmd/snowball/main.go)

`http.Server` with timeouts (ReadHeader 5s, Read 15s, Write 30s, Idle 60s) and 64 KB max headers, protecting against slow-client attacks. Graceful shutdown on Ctrl+C or SIGTERM gives in-flight requests up to 10 seconds.

### Secrets

| Secret | Lifetime | If it changes |
|---|---|---|
| `NameSigner` key (signs offered usernames) | Random at every start | Offered names stop verifying; visitors get a fresh name. Harmless. |
| `FormTokens` key (signs form timestamps) | Random at every start | Open forms show "This form expired." Harmless. |
| Vote secret (`SNOWBALL_VOTE_SECRET`) | Must persist forever | Past votes stop being recognized as their voters'; everyone could vote again. **Not harmless.** Never store it in the database or its backups. |

Because the first two are per-process, several app instances could not share them without a shared key; one server is the design.

---

## 6. Database schema

The current schema, after migrations 00001 to 00009:

```sql
CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      text NOT NULL UNIQUE
                  CONSTRAINT username_format CHECK (username ~ '^[a-z]+-[a-z]+-[a-z]+$'),
    password_hash text,                       -- argon2id PHC string; NULL only when deleted
    created_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz,
    is_admin      boolean NOT NULL DEFAULT false,   -- set by hand in psql only
    CONSTRAINT password_only_while_active
        CHECK ((deleted_at IS NULL) = (password_hash IS NOT NULL))
);

CREATE TABLE recovery_codes (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  text NOT NULL                  -- SHA-256 hex of the normalized code
);                                            -- index on user_id

CREATE TABLE sessions (
    token_hash  text PRIMARY KEY,             -- SHA-256 hex of the cookie token
    user_id     bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);                                            -- index on user_id

CREATE TABLE tags (
    id           integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name         text NOT NULL UNIQUE,  -- the slug: lowercase words joined by hyphens, up to 40
    display_name text NOT NULL,         -- 1 to 60 characters: "Food & drink"
    description  text NOT NULL DEFAULT '',   -- up to 200
    facet        text NOT NULL          -- 'topic', 'format' or 'level'
);  -- loaded by 00010: 341 topics, 6 formats, 3 levels (section 11.1)

CREATE TABLE tag_parents (     -- a tag's direct parents; several allowed
    tag_id    integer NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    parent_id integer NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (tag_id, parent_id),
    CHECK (tag_id <> parent_id)
);  -- index on parent_id; loops are refused in Go (internal/tags)

CREATE TABLE tag_aliases (     -- other names; matched after normalizing in Go
    tag_id integer NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    alias  text NOT NULL,      -- 1 to 60 characters, as the admin wrote it
    PRIMARY KEY (tag_id, alias)
);

CREATE TABLE tag_closure (     -- every (ancestor, descendant) pair, self at depth 0
    ancestor_id   integer NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    descendant_id integer NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    depth         integer NOT NULL,
    PRIMARY KEY (ancestor_id, descendant_id)
);  -- index on descendant_id; rebuilt from tag_parents whenever the tree changes

CREATE TABLE posts (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id        text NOT NULL UNIQUE CHECK (public_id ~ '^[a-z2-7]{8}$'),  -- in URLs
    author_id        bigint NOT NULL REFERENCES users(id),   -- no cascade: users are soft-deleted
    title            text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    url              text CHECK (url IS NULL OR char_length(url) <= 2000),
    body             text NOT NULL DEFAULT '' CHECK (char_length(body) <= 40000),  -- Markdown
    created_at       timestamptz NOT NULL DEFAULT now(),
    edited_at        timestamptz,
    last_activity_at timestamptz NOT NULL DEFAULT now(),     -- bumped by new comments
    score            integer NOT NULL DEFAULT 0,
    upvotes          integer NOT NULL DEFAULT 0,
    downvotes        integer NOT NULL DEFAULT 0,
    comment_count    integer NOT NULL DEFAULT 0,             -- visible comments
    content_hash     text NOT NULL,                          -- for duplicate detection
    deleted_at       timestamptz,                            -- by its author
    removed_at       timestamptz,                            -- by the admin
    CONSTRAINT score_matches_votes CHECK (score = upvotes - downvotes),
    CONSTRAINT deleted_post_is_erased CHECK (deleted_at IS NULL OR (body = '' AND url IS NULL))
);  -- indexes: created_at DESC, last_activity_at DESC, author_id, content_hash

CREATE TABLE post_tags (
    post_id  bigint  NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    tag_id   integer NOT NULL REFERENCES tags(id),
    PRIMARY KEY (post_id, tag_id)
);  -- plus index (tag_id, post_id)

CREATE TABLE comments (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id    text NOT NULL UNIQUE CHECK (public_id ~ '^[a-z2-7]{8}$'),
    post_id      bigint NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    parent_id    bigint REFERENCES comments(id) ON DELETE CASCADE,  -- NULL = top-level
    author_id    bigint NOT NULL REFERENCES users(id),
    depth        integer NOT NULL DEFAULT 0 CHECK (depth >= 0),
    body         text NOT NULL,   -- 1 to 10,000 characters while live; '' once deleted
    created_at   timestamptz NOT NULL DEFAULT now(),
    edited_at    timestamptz,
    upvotes      integer NOT NULL DEFAULT 0,
    downvotes    integer NOT NULL DEFAULT 0,
    content_hash text NOT NULL,
    deleted_at   timestamptz,
    removed_at   timestamptz,
    CONSTRAINT comment_body_length CHECK (char_length(body) <= 10000
        AND (deleted_at IS NOT NULL OR char_length(body) >= 1)),
    CONSTRAINT deleted_comment_is_erased CHECK (deleted_at IS NULL OR body = '')
);  -- indexes: post_id, parent_id, author_id

CREATE TABLE post_votes (
    voter    text     NOT NULL,     -- HMAC(vote secret, account id), never the id
    post_id  bigint   NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    value    smallint NOT NULL CHECK (value IN (-1, 1)),
    PRIMARY KEY (voter, post_id)    -- one vote per account per post
);  -- deliberately no timestamps

CREATE TABLE comment_votes (
    voter       text     NOT NULL,
    comment_id  bigint   NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    value       smallint NOT NULL CHECK (value IN (-1, 1)),
    PRIMARY KEY (voter, comment_id)
);

-- Polls (00020). Any post can carry one; the title is the question.
CREATE TABLE polls (post_id bigint PRIMARY KEY REFERENCES posts ON DELETE CASCADE, closes_at timestamptz NOT NULL);
CREATE TABLE poll_options (
    post_id  bigint REFERENCES polls ON DELETE CASCADE,
    position smallint CHECK (position BETWEEN 0 AND 5),
    label    text CHECK (char_length(label) BETWEEN 1 AND 80),
    votes    integer NOT NULL DEFAULT 0 CHECK (votes >= 0),   -- the running total
    PRIMARY KEY (post_id, position)
);
CREATE TABLE poll_votes (
    voter    text NOT NULL,          -- per-poll fingerprint: sha256(poll key || ':' || post id)
    post_id  bigint NOT NULL,
    position smallint NOT NULL,      -- FK (post_id, position) -> poll_options
    PRIMARY KEY (post_id, voter)     -- one vote per account per poll; deleted when the poll closes
);
-- comments.poll_pick smallint: the option the comment's author voted for (NULL if none).

-- Mega polls (00021): posts.by_site (shown as from "Snowball"), polls.mega (results hidden until close), and the queue:
CREATE TABLE mega_polls (
    day date PRIMARY KEY,                     -- a Pacific-time calendar day
    question text, body text, options text[], -- 1-200 chars, <= 2000, 2-6 options
    topics text, format text, level text,     -- as typed; matched to tags when published
    created_by bigint REFERENCES users,       -- becomes the post's (hidden) author
    post_id bigint UNIQUE REFERENCES posts ON DELETE SET NULL,  -- set once published
    publish_problem text                      -- why it couldn't be published, if so
);
```

goose also maintains its own `goose_db_version` table.

### Invariants enforced by the database

| Rule | Enforced by |
|---|---|
| Usernames are unique forever, including deleted accounts. | `UNIQUE` on `users.username`; deleted rows are kept. |
| Usernames are lowercase `word-word-word`. | `username_format` check. |
| A live account always has a password; a deleted one never does. | `password_only_while_active` check. |
| Public IDs are 8 characters of `a-z2-7`. | Format checks on posts and comments. |
| Title, URL, body, and comment lengths are within limits. | Length checks. |
| A post's score always equals upvotes minus downvotes. | `score_matches_votes` check. |
| One vote per account per post or comment. | Primary keys on the votes tables. |
| One vote per account per poll, only for an option that exists; 2 to 6 options of 1 to 80 characters. | `poll_votes` primary key and foreign key; `poll_option_*` checks (the 2-option minimum is checked in Go). |
| Deleted content is erased, not hidden: a deleted post has no body or link (title kept), a deleted comment has no body. | `deleted_post_is_erased`, `deleted_comment_is_erased`. |
| Codes, sessions, tags links, comments, and votes never outlive their parent row. | `ON DELETE CASCADE` (only fires on hard deletes; soft deletes clean up explicitly). |

Rules enforced in Go rather than the database: 1 to 3 topics plus at most one format and one level per post (a CHECK can't count rows), no loops in the tag tree, comment depth = parent depth + 1, no voting on your own content. The edit window and authorship are enforced inside the edit `UPDATE` statements themselves.

### Deliberately not stored

Email, IP addresses, user agents, device names, locations, last-login time, login history, recovery timestamps (used codes are deleted), which account cast a vote (only a keyed fingerprint), when a vote was cast, poll vote fingerprints once a poll has closed (only the totals stay), and anything identifying in logs.

### Migration history

| File | Change |
|---|---|
| 00001_create_users.sql | `users` with username format check. |
| 00002_create_recovery_codes.sql | `recovery_codes` with user index (and a `used_at` later removed). |
| 00003_create_sessions.sql | `sessions` and user index. |
| 00004_drop_recovery_used_at.sql | Used codes are deleted instead of timestamped. |
| 00005_soft_delete_users.sql | `deleted_at`, nullable `password_hash`, `password_only_while_active`. Its Down fails if deleted accounts exist. |
| 00006_create_posts.sql | `users.is_admin`, `tags` (seeded), `posts`, `post_tags`. |
| 00007_create_comments.sql | `comments`. |
| 00008_create_votes.sql | `posts.upvotes/downvotes`, `score_matches_votes`, `post_votes`, `comment_votes`. |
| 00010_tags_v2.sql | Tags v2: `display_name`, `description`, `facet`; `tag_parents`, `tag_aliases`, `tag_closure`; the starter tree (generated from the reviewed tree file); old tags renamed in place so their posts follow (`food`, `tech`, `diy`, `ask`, `show`). Its Down removes the new tags and restores the old ten. |
| 00011_drop_dotnet_alias.sql | Removes C#'s alias `.net`: matching ignores punctuation, so it really meant "net" and caught people meaning networking. The admin page now refuses such aliases (`tags.AliasProblem`). |
| 00012_tag_posts.sql | `tag_posts (tag_id, post_id, created_at, direct)`: every post under every tag it shows up under, filled from `post_tags` and `tag_closure`; indexes in tag-page order (and a partial one for "this tag only"). Derived data, kept in step by store/tagposts.go. |
| 00013_moderation.sql | `removal_reason` on posts and comments (spam, harassment, personal, illegal, rules, or `held` by reports); `reports` (post, optional comment, reporter fingerprint, reason, note, handled time; one per person per item); `mod_log` (action, public IDs, reason, reports settled, time). |
| 00014_suspensions.sql | `suspensions` (account, reason, note, created, `ends_at` NULL for permanent, `lifted_at`, one `appeal` with its times); `mod_log` gains account actions (`suspend`, `lift`, `keep`), a `detail` column, and a nullable post ID. |
| 00015_cleanup_shadowbans.sql | `users.shadowbanned_at`; the removal reason `shadow`; log actions `cleanup`, `shadowban`, `unshadowban`. |
| 00016_spam_checks.sql | `blocked_domains (domain, note, created_at)`; an index on `comments (content_hash, created_at)` for duplicate checks. |
| 00017_retention.sql | `erased_at` on posts and comments (with constraints that erased means empty, and removed comments allowed an empty body once erased); partial indexes for the hourly sweep. Its Down fails once any comment has been erased. |
| 00018_admin_notes.sql | `admin_notes (body, created_at, edited_at, hidden_at)`: notes from the admin, in Markdown. |
| 00019_reply_notifications.sql | `users.unread_replies` (counter for the header) and `users.replies_seen_at` (when the inbox was last opened). Nothing else about replies is stored. |
| 00020_polls.sql | `polls (post_id, closes_at)`, `poll_options (post_id, position, label, votes)`, `poll_votes (voter, post_id, position)` with per-poll fingerprints, `comments.poll_pick`. |
| 00021_mega_polls.sql | `posts.by_site`, `polls.mega`, and the `mega_polls` queue (one entry per day; `post_id` once published; `publish_problem`). |
| 00022_locks.sql | `posts.locked_at`; log actions `lock`, `unlock`, `close`. |
| 00009_erase_deleted_content.sql | `deleted_post_is_erased`; comment body may be empty only when deleted; `deleted_comment_is_erased`. |

### Query conventions

All queries use `$1`-style placeholders, never string building with input (no SQL injection). Where SQL is assembled (list filters, vote statements), the pieces are fixed strings chosen by code, and every value travels as an argument. Multi-step writes use transactions with `defer tx.Rollback(ctx)` and an explicit `Commit`. A failed statement aborts a Postgres transaction, so retries (like a public ID collision) redo the whole transaction. Rows whose counters change concurrently are locked with `SELECT ... FOR UPDATE` first (posts when commenting, the item when voting). Unique violations are detected by error code `23505` (and constraint name where it matters). Queries that must exclude deleted accounts or hidden content say so explicitly (`deleted_at IS NULL`, `removed_at IS NULL`).

---

## 7. Features: what exists and where

### 7.1 Routes

| Method and path | Handler (web/) | Wrappers | Notes |
|---|---|---|---|
| GET `/` | `handleHome` (home.go) | | Popular today (hot) + active. |
| GET `/new`, `/active`, `/top` | `handleList` (home.go) | | Paginated with `?after=<cursor>`. |
| GET `/t/{tag}` | `handleTag` (tags.go) | | Newest posts under the tag, including everything below it; `?only=1` for exactly this tag. Aliases and old names 301 to the canonical slug (query kept); a name meaning several tags ("football") shows a choice page; unknown names 404. |
| GET `/tags` | `handleTags` (tags.go) | | The tree two levels deep, with counts, plus formats and levels. |
| GET `/tags/suggest?topics=` | `handleTopicSuggest` (tags.go) | noStore; limit `suggest` | htmx, while typing in the topics box: the `topichelp` fragment (suggestions for the part after the last comma, plus what the box already holds). |
| GET `/tags/pick?choose=<slug>&topics=` | `handleTopicPick` (tags.go) | noStore; limit `suggest` | htmx, a suggestion clicked: the whole `topicfield` fragment with the topic put in, focused. 400 for anything but a real topic. |
| GET `/robots.txt` | `robotsTxt` (headers.go) | | `Disallow: /`. |
| GET `/submit`, POST `/submit` | `handleSubmitPage`, `handleSubmit` (posts.go) | noStore; POST: limit `submit` | Login required. |
| GET `/p/{id}` | `handlePost` (posts.go) | | Post, comments, `?sort=best|new|old`. |
| POST `/p/{id}/remove` | `handleRemovePost` (posts.go) | noStore | Admin only; others get 404. |
| GET, POST `/p/{id}/report`, `/p/{id}/c/{cid}/report` | `handleReportPage`, `handleReport` (moderation.go) | noStore; POST: limit `report` | Report a post or comment. Login, past the first day, not your own, still up. |
| GET, POST `/admin/reports` | `handleQueue`, `handleModerate` (moderation.go) | noStore; admin only | The queue; POST dismisses or removes (with a reason) one item. |
| GET `/account/replies` | `handleReplies` (replies.go) | noStore | The reply inbox, newest first, `?after=` for older; opening the first page marks everything seen. Logged out: 303 to `/login`. |
| GET, POST `/account/suspension` | `handleSuspensionPage`, `handleAppeal` (suspensions.go) | noStore; POST: limit `report` | Why and until when; the one appeal. Not suspended: 303 to `/account`. |
| GET `/admin/users/{name}` (and `?name=` lookup) | `handleAdminUser` (suspensions.go) | noStore; admin only | An account: standing, suspensions, recent posts and comments, suspend and lift forms. |
| POST `/admin/users/{name}/suspend`, `/lift` | `handleAdminSuspend`, `handleAdminLift` | noStore; admin only | |
| POST `/admin/users/{name}/cleanup` | `handleAdminCleanup` (suspensions.go) | noStore; admin only | Remove everything the account posted (one reason) and withdraw its votes. |
| POST `/admin/users/{name}/shadowban` | `handleAdminShadowban` | noStore; admin only | `action=on` (with confirm) or `off`. |
| GET, POST `/admin/domains` | `handleDomains`, `handleDomainChange` (spamchecks.go) | noStore; admin only | The blocklist; POST `action=add` (domain, note) or `remove`. |
| POST `/admin/appeals` | `handleAppealDecision` | noStore; admin only | `action=lift` or `keep`. |
| GET `/modlog` | `handleModLog` (moderation.go) | | The public moderation log (`?before=<id>` for older); the admin also sees dismissals. |
| GET `/notes` | `handleNotes` (notes.go) | | Every visible note from the admin, newest first. |
| GET, POST `/admin/notes` | `handleAdminNotes`, `handleAdminNoteChange` (notes.go) | noStore; admin only | Write, edit, hide, show, delete (with a confirm box) notes. |
| GET `/about` | `handleAbout` (moderation.go) | | What Snowball is, what it knows about you, what it keeps, who runs it. A draft in the owner's voice; every claim must stay true of the code. Linked from the sidebar and the footer. |
| GET `/rules` | `handleRules` (moderation.go) | | The rules the removal reasons point at. |
| GET `/polls` | `handlePollArchive` (megapolls.go) | | Every published mega poll still up, newest day first, with its winner(s); `?before=YYYY-MM-DD` for older (30 a page). Linked from the footer and the front page box. |
| POST `/p/{id}/lock` | `handleLock` (locks.go) | noStore; admin only (404 for others) | `action=lock` or `unlock`; returns to `back` (or the post). |
| POST `/p/{id}/poll/close` | `handleClosePoll` (locks.go) | noStore; admin only | Ends the poll now; returns to `back`. |
| POST `/admin/polls/today` | `handleMegaToday` (locks.go) | noStore; admin only | `action=takedown` (with `reason`): removes today's mega poll post; `action=run-next`: once today's is down (or there's none), publishes the next queued one and moves the rest up a day. |
| GET `/admin` | `handleAdminHub` (megapolls.go) | noStore; admin only | The admin's starting point: links to every admin page, open reports and appeals, today's mega poll and how far the queue runs. The account page links here. |
| GET, POST `/admin/polls` | `handleAdminPolls`, `handleAdminPollChange` (megapolls.go) | noStore; admin only | The mega poll queue (from a week ago on) and the form to add one. Queuing one for today publishes it at once. |
| GET, POST `/admin/polls/{day}` | same | noStore; admin only | Change a queued (unpublished) one, or `action=delete` with `confirm=1`. A published one redirects to its post. |
| GET, POST `/admin/tags` | `handleAdminTags`, `handleAdminCreateTag` (tagadmin.go) | noStore; admin only (`adminOnly`, 404 for others) | Every tag (filter with `?q=`), health warnings, the new-tag form. |
| GET `/admin/tags/{tag}` | `handleAdminTag` | noStore; admin only | One tag's admin page. An old name or alias 303s to the current slug's page. |
| POST `/admin/tags/{tag}/details`, `/parents`, `/aliases`, `/merge`, `/delete` | `handleAdminTag…` | noStore; admin only | The changes (section 7.11). Success redirects with `?done=<code>` for the notice; a refusal shows the page again with the reason. |
| GET/POST `/p/{id}/edit` | `handleEditPostPage`, `handleEditPost` (edit.go) | noStore; POST: limit `submit` | Author only (others 404). Text and tags. |
| POST `/p/{id}/delete` | `handleDeletePost` (history.go) | limit `submit`, noStore | Author only. Erases; returns to `back`. |
| POST `/p/{id}/comment` | `handleComment` (comments.go) | limit `comment`, noStore | Top-level or reply (`parent` field). |
| GET `/p/{id}/c/{cid}/reply` | `handleReplyForm` (comments.go) | noStore | htmx: form fragment; otherwise a page. |
| GET `/p/{id}/c/{cid}` | `handleCommentThread` (comments.go) | | "Continue this thread". |
| POST `/p/{id}/c/{cid}/remove` | `handleRemoveComment` (comments.go) | noStore | Admin only; others get 404. |
| GET/POST `/p/{id}/c/{cid}/edit` | `handleEditCommentForm`, `handleEditComment` (edit.go) | noStore; POST: limit `comment` | Author only. htmx: inline form and swap; otherwise a page. |
| POST `/p/{id}/c/{cid}/delete` | `handleDeleteComment` (history.go) | limit `comment`, noStore | Author only. Erases; returns to `back`. |
| POST `/vote` | `handleVote` (votes.go) | limit `vote`, noStore | htmx: arrows fragment; otherwise redirect to `back`. |
| POST `/p/{id}/poll` | `handlePollVote` (polls.go) | limit `vote`, noStore, notSuspended | `option=N`. htmx: the poll box; otherwise redirect to `/p/{id}#poll`. |
| GET `/p/{id}/poll` | `handlePollBox` (polls.go) | limit `pollBox`, noStore | htmx only (others are redirected to the post): the poll box, for the live refresh every 15 s. |
| GET/POST `/signup`, GET `/signup/name` | signup.go | limits `names`/`signup`, noStore | |
| GET/POST `/login`, POST `/logout` | login.go | POST login: limit `login`, noStore | Logout deletes all sessions. |
| GET `/account`, POST `/account/password`, POST `/account/delete`, GET `/goodbye` | account.go | password/delete: limit `login`, noStore | |
| GET `/account/history` | `handleHistory` (history.go) | noStore | Private. `?show=posts|comments|upvoted|downvoted` (anything else means posts), `&after=<cursor>`. |
| GET/POST `/recover`, GET `/recovered` | recover.go | POST: limit `recover`, noStore | |
| GET `/static/...` | Go file server | revalidate | |

Planned: `/changelog`. There is no public profile page listing a user's history (section 8); the only history view is the owner's private one.

### 7.2 Sign-up flow

1. **Name offer.** A `color-verb-animal` name (auth/usernames.go: 8 colors, 80 verbs, 150 animals, 96,000 combinations), checked against the database so an offered name is never taken. Word lists are hand-curated to avoid offensive or slur-adjacent combinations; review any new word against every color.
2. **Signing.** Each offered name is HMAC-signed (`NameSigner`) so a user cannot edit the form to pick their own name.
3. **Remembered offer.** The `__Secure-snowball_offer` cookie (Path `/signup`, 1 hour, `name.sig`) keeps the offer across refreshes.
4. **Reroll allowance.** Rerolls spend from a per-IP bucket (10, one more every 6 minutes), shown beside the name. When empty, the current name returns with "More rerolls in X". A nudge, not a security control.
5. **Form.** Password twice, two required acknowledgment checkboxes (`ack_recovery`, `ack_username`), and the form guard.
6. **Validation order.** Form guard, name signature, password rules, confirmation, checkboxes, then the account-creation limit (3, then one per 20 minutes per IP), checked last so typos don't consume it.
7. **Creation.** argon2id hash, 8 recovery codes, user and code hashes inserted in one transaction. A unique violation produces a fresh name and a message.
8. **Welcome page.** Shows the codes once, `Cache-Control: no-store`. (Refreshing it resubmits the form; a known, harmless limitation.)

### 7.3 Login and logout

Login lowercases and trims the name, checks the form guard, the per-account login guard, then verifies the password. Nonexistent names run a full argon2 check against a dummy hash (`auth.VerifyDummy`) so timing doesn't reveal whether a name exists; both failures show the same message. Success creates a new session and redirects (Post/Redirect/Get). Logout is a POST and **deletes all of the account's sessions on every device** (a deliberate product decision).

### 7.4 Sessions and cookies

| Cookie | Contents | Path | Lifetime | Flags |
|---|---|---|---|---|
| `__Host-snowball_session` | 32 random bytes, base64url | `/` | 30 days, absolute | HttpOnly, Secure, SameSite=Lax |
| `__Secure-snowball_offer` | `name.signature` | `/signup` | 1 hour | HttpOnly, Secure, SameSite=Lax |

Only the SHA-256 hash of the session token is stored. Expired sessions are rejected at lookup and deleted hourly. `Secure` cookies work on `http://localhost` in Chrome, Edge, and Firefox (not Safari). Renaming a cookie logs everyone out once.

### 7.5 Account recovery

8 codes per account, `XXXX-XXXX-XXXX` (base32, no 0/1/8/9, 60 bits), stored as SHA-256 of the normalized code. The form takes username, code, and new password twice; password rules are checked first so a typo never burns a code. `store.RecoverAccount` spends the code with a single `DELETE ... USING users ... RETURNING` (atomic: never usable twice), sets the password, and deletes all sessions in one transaction. The user is logged in and sees how many codes remain. No code regeneration (deliberately deferred).

### 7.6 Account page (`/account`)

Username (masked, with "show name"), a link to the private history page, recovery codes remaining, change password, delete account. Both forms re-check the current password through `checkCurrentPassword`, which shares the login guard. Change password requires the new one to differ, saves it, and deletes every session including the current one. **Delete account (soft delete)** wipes the password hash, marks the row deleted, removes codes and sessions, and replaces the account's vote fingerprints with random values (its votes keep counting but can never be traced back), all in one transaction. The username is retired forever. An optional checkbox, "Also delete everything I've posted", erases all of the account's posts and comments in the same transaction (`eraseAllContent`: posts in one statement; comments plus every affected post's count in one `WITH ... UPDATE` statement).

### 7.7 Password policy (auth/passwords.go)

`PasswordProblems(password, username)` returns phrases that read after "Your password needs to ...": 6 to 128 characters (counted in characters), an uppercase letter, a digit, a symbol, not a common word dressed up with digits/symbols (trims non-letters from the ends, undoes leetspeak, checks a short list), and not containing the username. Passwords are NFKC-normalized before rules, hashing, and verification. Hashes are argon2id PHC strings (`m=19456,t=2,p=1`, 16-byte salt, 32-byte key) with parameters stored per hash. Verification reads a stored hash through `parseHash`, which refuses anything malformed or out of bounds (memory over 256 MiB, time over 10, threads over 16 or memory below 8 per thread, salt under 8 bytes, key under 16 or over 64 bytes), so a damaged or planted hash can't crash the server, exhaust its memory, or (with an empty key) match every password. Fuzzing found the need for this (section 10). At most 4 hashes run at once; a request waits up to 10 seconds for a slot, otherwise `auth.ErrBusy` becomes a 503 with `Retry-After: 5`.

### 7.8 Rate limiting (web/ratelimit.go, web/loginguard.go, web/votes.go)

Token buckets held only in memory: "burst" is how many at once, then one more per refill interval. Beyond 50,000 tracked keys, new ones share an overflow bucket. A bucket is forgotten only after it has sat unused long enough to refill completely (burst × interval) or 15 minutes, whichever is longer, so waiting can never reset a slow limit early. Everything resets on restart. Refusals by route wrappers return 429 with `Retry-After` and plain text (styled page on the backlog); limits checked inside handlers show a message on the form or under the vote arrows instead.

| Limiter | Burst, refill | Key | Applies to |
|---|---|---|---|
| `names` | 30, 1 per 0.5s | IP | GET `/signup`, `/signup/name` |
| `rerolls` | 10, 1 per 6 min | IP | Actual rerolls only; shown to the user |
| `signup` | 10, 1 per 6s | IP | POST `/signup` |
| `accounts` | 3, 1 per 20 min | IP | Inside `handleSignup`, just before creating an account |
| `login` | 10, 1 per 6s | IP | POST `/login`, `/account/password`, `/account/delete` |
| `recover` | 5, 1 per min | IP | POST `/recover` |
| `submit` | 10, 1 per 6s | IP | POST `/submit` |
| `posting` | 5, 1 per 5 min | account | Inside `handleSubmit`, when publishing |
| `editing` | 20, 1 per 10s | account | Saving edits to posts and comments |
| `suggest` | 40, 1 per 250 ms | IP | Topic suggestions and picks while typing |
| `posting-new`, `commenting-new` | 2, 1 per 30 min; 5, 1 per 2 min | account | Used instead of `posting` and `commenting` during probation (an account's first 3 days) |
| `report` | 10, 1 per 30 s | IP | Sending reports (each account also has a cap of 10 a day, counted in the database) |
| `comment` | 20, 1 per 3s | IP | POST `/p/{id}/comment` |
| `commenting` | 10, 1 per 30s | account | Inside `handleComment`, when publishing |
| `vote` | 30, 1 per 3s | IP | POST `/vote` |
| `voting` | 10, 1 per 3s | account | Every vote click (stops arrow-mashing) |
| `voting-daily` | 250, 1 per 5.76 min (rolling day) | account | New votes only |
| `voting-author` | 10, 1 per 2.4 h | account fingerprint + author ID | New votes only, on one author's content |

The three vote limits are checked together by `voteLimitNote`: it peeks at all of them first and spends only if all allow, so a refusal by one doesn't use up the others. "New vote" means voting on an item the voter hadn't voted on; switching direction or taking a vote back only counts against the fast limit. The per-author key pairs a voter's fingerprint with an author's ID, exactly the link the votes tables refuse to store, so it must never be logged or persisted.

Visitor identity comes from `clientIP`: the connection address, or with `SNOWBALL_TRUST_PROXY=1` the **last** `X-Forwarded-For` entry. IPv4-mapped IPv6 is unwrapped; IPv6 is grouped by /64.

**Per-account login guard:** 3 free wrong passwords, then that account's password checks are blocked for 30s, 1m, 2m, 4m, then 5m (cap, kept low so nobody can lock a user out for long). Only existing accounts are tracked; records are forgotten after an hour without failures and cleared on success.

### 7.9 Anti-bot form guard (web/formguard.go, auth/formtokens.go)

Every form includes `{{template "formguard" .Token}}`: a honeypot input named `homepage` (off-screen via `.hp`, `tabindex="-1"`, `aria-hidden`) and a hidden `form_token` (`<issued-at-ms>.<HMAC over form name and time>`, so tokens only work on their own form). Every POST handler calls `s.checkForm(w, r, rule)` after `ParseForm`. A filled honeypot or missing token is treated as a bot: quiet redirect, logged without details. Too young: "quicker than a person" message. Expired or unrecognized (including after a restart): "This form expired." Tokens last 2 hours and are not single-use.

| Form | Rule | Minimum age |
|---|---|---|
| Sign-up | `formSignup` | 3 s |
| New post | `formSubmit` | 5 s |
| Recovery | `formRecover` | 2 s |
| Change password, delete account | `formPassword`, `formDelete` | 2 s |
| Comment or reply | `formComment` | 2 s |
| Login | `formLogin` | none (password managers submit instantly) |
| Vote | `formVote` | none (one click), token still required |
| Edit post | `formEditPost` | 2 s |
| Edit comment | `formEditComment` | 1 s |
| Delete your own post or comment | `formDeleteItem` | 1 s |
| Admin: remove post, remove comment | `formRemovePost`, `formRemoveComment` | none |
| Admin: every tag change | `formTagAdmin` (one token shared by the page's forms) | none |
| Admin: queue actions | `formModerate` | none |
| Reporting a post or comment | `formReport` | 2 s |
| Appealing a suspension | `formAppeal` | 5 s |

To add a form: add a `formRule`, a token field set via `s.formToken(rule)` wherever the page is built, the `formguard` partial inside the `<form>`, and a `checkForm` call in its POST handler.

### 7.10 Posts (web/posts.go, store/posts.go, internal/markdown)

**Submitting** (`/submit`, login required): a title (1 to 200 characters), an optional link (complete `http(s)://` URLs only, up to 2000), optional Markdown text (up to 40,000), and tags: 1 to 3 topics typed into one box, separated by commas, plus an optional format and level from drop-downs (see Tags below). Everything typed is kept when the form is shown again with errors. Validation, then the per-account `posting` limit, then `store.CreatePost` inserts the post and its tags in one transaction with a random 8-character public ID (retried on the astronomically unlikely collision), and a content hash for future duplicate detection. Redirects to the new post.

**Public IDs** are random (`a-z2-7`, 40 bits) so nobody can walk `/p/1, /p/2...` to scrape everything or gauge site activity. URL patterns are validated before any database lookup.

**Markdown** (`markdown.Render`): goldmark with raw HTML off, then bluemonday with an allowlist (`p, br, strong, em, del, ul, ol, li, blockquote, code, pre, hr, a[href]`). No headings (stripped, text kept), no images (an external image would leak every reader's IP to its host), only `http`/`https` links, always `rel="nofollow"`. Rendered on every display, so tightening the rules fixes old posts too. This is the only place in Snowball that returns `template.HTML`, and its tests pin down that scripts, raw HTML, `javascript:` links, images, iframes, headings, and style attributes can't get through. A post's own link gets `rel="nofollow ugc noopener"`. In lists, a title always opens the post page; for link posts, the domain beside it is the link out. On the post page, the title links out. Spoiler tags need a small custom extension (backlog).

**The post page** (`/p/{id}`): title (linked for link posts, with the domain), vote arrows, time, author (or "[deleted]"), tags, "edited" marker, rendered body, then comments.

**Tags on posts** (internal/tags, web/tags.go, partials/tagpicker.html). The tag tree lives in memory (`Server.tagTree`, an atomic pointer to an immutable `tags.Tree`, loaded at startup by `loadTags`). `Tree.Choose` turns the form into tag IDs: typed topics are normalized (`tags.Normalize`: NFKC, lowercase, only letters, digits, `+` and `#`) and matched against slugs, display names and aliases; an unknown name, an ambiguous one ("football"), or a format typed as a topic each get a message saying what to do; a topic and its ancestor keep only the deeper one; then 1 to 3 topics. Format and level must be real tags of that facet (anything else means a tampered form). Posts store only the chosen tag IDs in `post_tags`. On display, `Tree.Chips` shows topics (by name), then the format (italic), then the level (dashed border). The edit page fills the box back in with `Tree.SelectionFor` (display names), which `Choose` accepts again.

**Topic type-ahead** (htmx; `topicfield` and `topichelp` in partials/tagpicker.html). The topics box stays a plain text field; htmx adds `hx-get="/tags/suggest"` with `hx-trigger="input changed delay:200ms"` and `hx-sync="this:replace"` (a newer keystroke cancels the request in flight). The server splits the box with `tags.Typing` (settled parts before the last comma, the part being typed after it) and ranks matches with `Tree.Suggest`: exact name or alias, then display name or slug prefix, alias prefix, a word in the display name, and from three letters on anything containing it; ties go to the busier tag (cached counts), then alphabetical; at most 8, each shown with its place in the tree ("Sourdough in Food & drink › Baking › Bread"). Settled topics and their ancestors aren't offered again; with three settled nothing is offered. Under the box, `Tree.Recognized` confirms what's already matched and what it brings along ("Topics: Sourdough. Also shows under Baking, Bread, Food & drink."), also on page load (handy on the edit page). Each suggestion is a `<button type="button">` asking `/tags/pick`, which returns the whole field with `tags.WithPick` applied (settled parts kept, the pick's display name added, then ", ") and `autofocus`; snowball.js puts the caret at the end and adds arrow keys (Down from the box into the list, Up/Down within it, Escape back). The matching lives only in Go, so suggestions can never disagree with what posting accepts, and no tag list is shipped to the page. Without JavaScript nothing changes: the box is matched when the form is sent.

**Admin removal**: `POST /p/{id}/remove` (with a reason; section 7.19) sets `removed_at` and `removal_reason`. Removed posts vanish from every list and count, show "[removed: spam]" (the reason) to everyone else, take no new comments or votes, and stay fully visible to the admin with a banner. The endpoint answers non-admins with a 404 so it doesn't admit it exists.

### 7.11 Lists, tags, and ranking (web/home.go, web/tags.go, store/lists.go)

`store.ListPosts` serves every list with options: sort, optional tag (`TagID`, with `TagOnly` for exactly that tag), cursor, limit. A tag filter joins `tag_closure`, so a tag page includes every post tagged with anything below it. Removed and deleted posts never appear. Each list fetches one more post than it shows to know whether another page exists.

| Sort | Order | Window | Used by |
|---|---|---|---|
| new | `created_at` | all | `/new`, tag pages |
| active | `last_activity_at` (bumped by comments) | all | front page (20), `/active` |
| top | `score` | last 7 days | `/top` |
| popular | "hot": `sign(score)·log10(max(|score|,1)) + age_seconds/45000` | last 24 hours | front page "Popular today" (3, not paginated) |

The hot formula is Reddit's: score counts logarithmically (1 to 10 points matters as much as 10 to 100), and 12.5 hours of age is worth a tenfold change in score, so a post taking off now outranks an older one that already peaked.

**Keyset pagination**: "older posts" links carry `?after=<value>.<id>` (the last post's sort key, as microseconds or score, plus its ID to break ties). The next page queries `(key, id) < (cursor)`: fast at any size, and new posts arriving never cause skips or repeats. A malformed cursor redirects to the first page. Later pages offer "back to the start".

Partials shared by all list pages: `sortnav` (the sort bar, current sort in bold, "new post" button for logged-in users) and `sidebar` (About, the 12 busiest top-level topics with counts, House rules).

**Tag pages** show the path above the tag (first parent at each step, alphabetically), any other parents ("Also under"), the description, the children with counts, and a switch between "including everything below" and "this tag only". Up goes to the parent tag, or to `/tags` for a top-level one. **Tag counts** include everything below a tag, count each post once (`store.TagPostCounts`, over `tag_closure`), and are cached with the footer's post count: refreshed every minute, after removals, and in the background after a new post. On the large seed this took list pages from about 75 ms to 4 to 12 ms. Rank numbers come from a CSS counter and appear only on the ranked "Popular today" list (`.posts.unranked` hides them elsewhere). The footer's post count is cached in memory (refreshed every minute, bumped immediately on new posts, refreshed on removals).

**The admin tag page** (web/tagadmin.go, store/tagadmin.go, tags/admin.go; templates admintags.html and admintag.html). Admins reach it from their account page, or "edit this tag" on any tag page; for everyone else every `/admin/tags` address is a 404. The index shows warnings from `Tree.Check` (if any), a filter over names, slugs and aliases, the new-tag form (slug optional: `tags.Slugify` makes one from the name, "Food & drink" → `food-and-drink`), and the whole topic tree as one collapsible box per top-level topic. Each tag's page has, from everyday to permanent: **names** (display name, slug, description; "keep the old slug and name as aliases" is ticked by default, so renames never break links), **parents** (topics only; add by typing a name, remove with a button), **aliases**, **merge** into another tag of the same kind, and **delete** (only when no post, not even a removed one, carries it and nothing is underneath; otherwise merge).

Every change goes through one path. The handler checks what it can for a clear message (slug, name and alias rules in `tags/admin.go`; a parent that's already below the tag, or already above it further up; a merge into its own descendant; the confirm box). Then the store runs it in a transaction (`changeTags`): take an advisory lock, make the change in SQL, read the whole tree back inside the transaction, and pass it to the web package's check (`tagCheck`), which builds a `tags.Tree` (refusing loops) and runs `Tree.Check`. Any NEW problem refuses the change and rolls it all back; problems that were already there don't block unrelated changes. Otherwise `tag_closure` is rebuilt from the parent links (a few milliseconds) and the transaction commits; the server reloads its in-memory tree and refreshes the counts, so the site shows the change at once. `Tree.Check` finds names that work but mislead: an alias starting with punctuation (`.net` would really mean "net"), an alias that is another tag's own slug or display name, two tags with the same name once normalized, a format or level with parents. Shared aliases between tags ("football") stay allowed: typing them asks which. A merge moves the posts (a post carrying both keeps one row), moves the children under the other tag, adds the old slug, name and aliases as the other tag's aliases (except ones that already lead there), then deletes the tag.

### 7.12 Comments (web/comments.go, store/comments.go)

**Loading**: every comment on a post in one query (oldest first, capped at 2,000), then `buildTree` assembles the tree in Go: each comment is attached to its parent, then `finishLevel` works bottom-up, tidying replies first, then dropping deleted or removed comments that have no replies left, applying depth rules, and sorting siblings.

**Sorting** (`?sort=`): **best** (default) uses the Wilson score lower bound on `(upvotes, downvotes)`, a cautious estimate of the share of voters who liked a comment, so 10 up / 1 down (0.62) beats 40 up / 20 down (0.54) and a lone upvote (0.21) doesn't leap to the top; ties go to the older comment. **new** and **old** sort by time.

**Depth**: replies indent with thin lines up to depth 5; deeper replies sit flush and show "replying to <name>"; past depth 8 a "continue this thread" link opens `/p/{id}/c/{cid}`, which builds the tree from that comment down. Phones get smaller indent steps.

**Collapsing**: each comment is a native `<details>` element whose `<summary>` is the author line, shown with an old-web `[–]`/`[+]` marker. No JavaScript.

**Replying**: a comment box under the post. Each comment's "reply" link has an `href` to a standalone reply page and an `hx-get` that slots a fresh reply form under the comment. The standalone page is also where errors are shown, with the text preserved. Comments are 1 to 10,000 characters of Markdown (same renderer as posts). After posting, the redirect lands on the new comment's anchor (`#c-<id>`), or on its parent's thread page if it's deeper than depth 8.

**Creating** (`store.CreateComment`): locks the post row (`FOR UPDATE`), refuses removed or deleted posts (`ErrPostLocked`) and gone or foreign parents (`ErrParentGone`), inserts with depth = parent + 1, and bumps the post's `comment_count` and `last_activity_at` in the same transaction.

**Deleted and removed comments**: with replies, they stay as "[deleted]" or "[removed]" placeholders with the author hidden; without replies, they vanish. The admin sees removed comments in full with a note, and has a "remove" button on each comment (decrements the post's count).

### 7.13 Votes (web/votes.go, store/votes.go, auth/voters.go)

**Privacy design.** The votes tables map accounts to opinions, so: votes are stored against `HMAC(vote secret, account id)` fingerprints, never account IDs, so a leaked database or backup can't attribute votes (someone holding both the database and the secret could, so the secret lives outside the database); votes have no timestamps; deleting an account replaces its fingerprints with random values; nothing ever displays or logs individual votes. The only vote anyone sees is their own, highlighted.

**Rules**: no voting on your own posts or comments (checked in the store by author ID, which isn't stored with the vote); accounts must be at least 24 hours old (`voteMinAccountAge`, the first piece of probation); no voting on removed or deleted content, or comments on removed or deleted posts; the three vote limits in 7.8.

**Mechanics**: clicking an arrow votes; clicking the same arrow takes the vote back; clicking the other switches (`voteChange`, a small tested pure function returning the new vote and the up/down deltas). `store.Vote` locks the item row, reads the old vote, inserts/updates/deletes it, and adjusts the counts in one transaction. Posts keep `score` in step (enforced by `score_matches_votes`); comments compute `upvotes - downvotes`.

**Display** (`partials/votes.html`, `voteWidget`): up arrow, score, down arrow. Stacked in lists; inline (horizontal) on the post page and in each comment's action row. Your own vote is highlighted (blue up, red down). Arrows link to login when logged out, and are faded with a hover reason when voting isn't possible ("This one's yours.", "New accounts can vote after their first day.", "Voting is closed on this."). With htmx, a click swaps just the widget; notes from refused votes appear under inline arrows, or in a small yellow box beside stacked ones. Without JavaScript, the form submits and returns to the `back` path, which `safeBack` restricts to paths on Snowball (no open redirects): it must start with one `/`, contain no backslash, and contain no control characters (browsers silently drop tabs and newlines, which turned `/\t/evil.example` into `//evil.example` until fuzzing caught it).

### 7.14 Editing (web/edit.go, store/edits.go)

Authors can edit for **10 minutes** (`store.EditWindow`). Edits within the first **2 minutes** (`store.EditGrace`) are silent; later ones set `edited_at`, shown as "edited". After 10 minutes the content is final. No revisions are kept.

**Posts:** text and tags only. The **title and link can never be edited** (Reddit's lesson: swapping a link after a post collects votes is a classic spam trick, and changing a title changes what people voted on). The edit page (`/p/{id}/edit`) shows them read-only with a note explaining why, and reuses the shared `tagpicker` partial. **Comments:** text only. With htmx, "edit" swaps the comment's text for a textarea holding the original Markdown; saving swaps the new text back and updates the "edited" marker in the comment's header line via an **out-of-band swap** (the response carries a `<span id="ce-<id>" hx-swap-oob="true">` that htmx puts in place of the matching element). Without JavaScript, editing happens on a small page of its own. "Cancel" just links back to the comment.

The **database clock decides**: `EditPost` and `EditComment` are single `UPDATE`s that only match when the editor is the author, the item isn't removed or deleted, and `created_at > now() - window`; a `CASE` sets `edited_at` only after the grace period. A form held open past the deadline gets "The 10-minute editing window has closed". The window and grace are Go constants passed in as seconds, so the "N minutes left" shown on pages (`editTimeLeft`, `canEdit`) can't drift from the rule. Edit links appear only for the author within the window. Edits re-run the same validation as posting, update the content hash, use the form guard and the `editing` limit, and **don't bump** `last_activity_at` (otherwise re-saving would keep a post on top).

### 7.15 Private history and self-deletion (web/history.go, store/deletes.go)

`/account/history` ("Your history") lists the viewer's own posts or comments, or what they upvoted or downvoted (a row of four choices), newest first, 25 per page with keyset pagination by creation time. Items the admin removed appear labeled "removed by the admin" (only to their author); items the author deleted don't appear. Comment entries show a plain-text snippet (`snippet`, first 160 characters).

**Upvoted and downvoted** (`MyVotedItems`) list posts and comments together, found by the viewer's vote fingerprint (the same lookup that highlights arrows, so nothing new is stored or exposed; the votes tables' primary key `(voter, ...)` makes it an indexed lookup). They're ordered by when each item was written, since votes deliberately have no timestamps; the page says so. Each entry keeps its vote arrows, so a vote can be changed or taken back there. Left out: anything the admin removed (and comments on removed posts), and deleted comments (nothing left to show); deleted posts stay, by title, with voting closed. Posts and comments are paged as one list with the cursor `(created_at, key)`, where `key` is `id*2+1` for posts and `id*2` for comments, unique across both tables. On the large seed, the busiest voter's page takes about 20 ms.

**Deleting** is offered on each entry as a `<details>` element: "delete" opens a confirmation explaining what happens, and only the red button inside deletes. No JavaScript, and one stray click can't delete anything.

**Deletion erases, it doesn't hide.** `DeletePost` overwrites the body and link (title kept, Reddit's choice, so the discussion under it still makes sense) and sets `deleted_at`. `DeleteComment` overwrites the body and decrements the post's visible comment count. The database enforces that deleted rows are empty (migration 00009), so no bug can leave deleted text behind, and there is no undelete. The rows themselves remain (so threads keep their shape) and still record their author: breaking that link was considered and set aside because it would hinder moderation, such as cleaning up after a spammer. Deleted content gets an empty `content_hash`.

**How deleted content shows:** a deleted post drops out of every list and the post count; its page still loads with its title, "[deleted]" as the author, a note that the author deleted it, and its comments intact. It takes no new comments or votes. A deleted comment with replies shows as "[deleted]" with the replies kept; without replies, it disappears.

**Deleting from the post page.** The same delete controls also sit where the things are: "delete this post" under your own post's byline, and "delete" in your own comments' action rows. Each is a `<details>` that opens a confirmation with the real button (no JavaScript, and one stray click can't delete anything). They post to the same endpoints as the history page (`formDeleteItem`), with `back` set to the post, or to the comment's place in the thread. Deleting works at any age (it isn't tied to the edit window) and while suspended.

### 7.16 Site-wide HTTP security (web/headers.go, main.go)

Headers on every response: `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, `Cross-Origin-Opener-Policy: same-origin`, `Permissions-Policy: camera=(), microphone=(), geolocation=(), payment=(), usb=()`, and `X-Robots-Tag: noindex, nofollow, noarchive`.

**No inline `<script>`, no inline `style=""`, no `<style>` tags, no third-party assets.** Dynamic styling uses classes (name colors). htmx is configured via a meta tag with `includeIndicatorStyles: false` and `allowEval: false`.

**Search engines**: the `X-Robots-Tag` header, a matching `<meta name="robots">`, and `/robots.txt` disallowing everything. A crawler obeying `robots.txt` never sees `noindex`, so a page linked from elsewhere may still appear as a bare URL. Impolite crawlers are for read limits and Anubis.

Also: CSRF protection; `no-store` on account routes and every logged-in response; form bodies capped with `http.MaxBytesReader` (64 KB, or 256 KB for posts); server timeouts; vague errors to visitors (`serverError`), details to the log.

### 7.17 Privacy and logging

No log line contains passwords, tokens, recovery codes, cookies, IP addresses, or votes (audited). The rate limiter logs only its name and a count; bot detections log only the form name. Database errors may contain usernames, which are public. Postgres logs failing statements with placeholders, not values. A Go handler panic logs the connecting address, which behind Caddy is Caddy's. The account page states that Snowball doesn't know which devices or locations people use.

### 7.18 Front end

**Design system (static/css/style.css).** One stylesheet. Tokens on `:root`: `--frost`, `--paper`, `--shade`, `--ink`, `--muted`, `--rule`, `--link`, `--visited`, `--sled` (the single loud accent: yellow rules under the masthead and above the footer), `--note`. Verdana body (14px, 13px on wide screens), Georgia for the wordmark and headings. Thin borders, no shadows or rounded cards. Mobile-first single column; at 50rem and up a fixed-width bordered column (max 60rem) with a sidebar. Touch screens get larger targets via `@media (pointer: coarse)`. `[hidden] { display: none !important; }` guarantees the `hidden` attribute always hides.

**Name colors.** A username's first word is a color; `colorClass` (web/home.go) maps it to `nc-blue` ... `nc-purple` (fallback `nc-x0`..`nc-x3` by hash), and CSS sets `--c` per class. The owner chose vibrant values.

**Navigation.** The logo goes home. Every page with a parent shows a quiet `← label` up link under the yellow bar (section 5, Rendering): posts, account, submit, tags, login, sign-up, goodbye, and recovered go to the front page; tag pages to their parent tag (or all tags); history to the account; reply, edit, and thread pages to their post; recovery to login. The front page and sort lists have none (the sort bar covers them), and the welcome page deliberately has none, so its only way on is "I've saved them".

**Hidden username.** The logged-in header shows eight dots plus a "show name" checkbox (shoulder-surfing protection), a boxed **account** button, and log out. snowball.js wires every `.reveal-group` (header and account page), resetting to hidden on `pageshow`.

**Header and front page.** Masthead with hand-drawn SVG snowball, random tagline, account area (wraps neatly on phones). Front page: sort bar, an admin note (a static, dated placeholder in `home.html`, written during development rather than by the owner; rewrite or remove it until real admin notes exist), "Popular today", "Active right now", sidebar, footer with the owner's manifesto, render time, and post count.

**Copy voice.** Direct, human, slightly raw. Copy changes are the owner's call.

---

### 7.19 Moderation (web/moderation.go, store/moderation.go)

Slice 1 of the moderation chapter (design in section 11.2).

**Reporting.** Under every post and comment, a "report" link for logged-in accounts past their first day, on things that aren't their own and are still up (`voteViewer.canReport`). It leads to a small page (works without JavaScript): the reasons as radio buttons with a line of explanation each (spam, harassment, personal information, illegal content, wrong tags, something else), and an optional note (200 characters; required for "something else"). The report is stored under `auth.VoterKeys.Reporter(userID)`, an HMAC of the account ID with the vote secret but a different label from the vote fingerprint, so reports can't be traced to accounts, nor matched with the same account's votes. The database allows one report per person per item (`one_report_per_person`) and the store allows 10 per person per 24 hours, counted from the table, so it survives restarts.

**Auto-hiding.** When a report is for illegal content or personal information and that makes `HoldAfter` (3) open serious reports on the item, the same transaction hides it: `removed_at` set, `removal_reason = 'held'`, a comment's post loses one from its visible count, and a `hold` goes in the log. Readers see "[hidden while reports are reviewed]"; the reporter who tipped it over is told. Other reasons never hide anything.

**The queue** (`/admin/reports`, admin only): one entry per reported item, serious reasons first, then the most reported, then the newest; up to 100. Each shows the item's kind, its post's title (linked to the item in context), any hidden state, the author and time, the reasons with counts (serious ones in red), the reporters' notes, and the raw text (never rendered, so hidden links and tricks show plainly). Two actions: **Dismiss** closes the reports (a held item comes back, logged as `restore`); **Remove** with a reason. The header shows the admin "N reported" (posts and comments with open reports, cached and refreshed after every report and action, and each minute).

**Removing.** `store.Remove` is the one path, from the queue or from the remove buttons on the post page and comments (which now ask for a reason: spam, harassment, personal information, illegal content, breaking the rules). In one transaction: lock the row, set `removed_at` and the reason (removing something held just changes its reason; counts change only once), close its open reports, write the log. Readers see "[removed: spam]"; posts removed before reasons existed say "[removed]".

**The log** (`mod_log`, `/modlog`): every hold, removal, restore and dismissal, with public IDs (not foreign keys, so the log outlives what it describes), the reason, and how many reports it settled. The public page shows the first three ("Post removed: spam, after 2 reports"), never content or names, linking to the item's place (which shows only its placeholder). The admin's view also shows dismissals.

**Rules** (`/rules`): one section per removal reason, with anchors matching the keys. The text is a draft for the owner to rewrite.

**Suspensions** (slice 2; web/suspensions.go, store/suspensions.go). From the admin's page for an account (`/admin/users/{name}`, reached from author names in the queue or the lookup on the account page), or straight from the queue ("Remove" with "and suspend the author for ..."): a reason from the removal list, a length (1, 7 or 30 days, or permanent) and an optional note to the person. A new suspension replaces one in force; the admin account can't be suspended. `UserBySession` loads the suspension in force in the same query (`LEFT JOIN LATERAL`), so every request knows at no extra cost. A suspended account is **read-only**: `notSuspended` wraps every route that writes (submit, comment, reply form, vote, report, edit) and sends it to `/account/suspension` (htmx gets a 403 line); the pages hide the controls (`voteViewer.suspended`: no comment form, no reply, report or edit links, vote arrows explain). Reading, logging in and out, changing the password, and deleting its own posts, comments or whole account all still work. A banner on every page gives the reason and end date and links to `/account/suspension`, which shows the reason (linked to its rule), the admin's note, the end, and the **appeal**: one per suspension, 10 to 500 characters. Appeals appear at the top of the queue (and in the header count, now "N to review") with "Lift the suspension" or "Keep it"; a kept suspension tells the person their appeal was read. Suspensions end on their own when `ends_at` passes; nothing has to run. The log records `suspend` ("Account suspended for 7 days: harassment", "Account banned: spam"), `lift` ("Suspension lifted", "... after an appeal") and `keep` (admin-only), never naming the account.

**Cleanup** (slice 3; store/cleanup.go). "Remove everything by this account": from the account's admin page (a reason and a confirm box), or from the queue (the "and everything else they posted" box next to Remove, using the removal's reason). One transaction removes every post and comment of theirs that's up (or only provisionally hidden: held or shadowed) with the reason, takes their comments off their posts' counts, closes the reports on all of it, and **withdraws their votes**: votes are stored under the vote fingerprint, so the web package computes it (`VoterKeys.Voter`) and the store subtracts those votes from every count and deletes them. Nothing new about who voted is stored. The log says "Account cleaned up (spam): 2 posts, 3 comments, 40 votes withdrawn". The admin account is exempt.

**Shadowbans, for obvious spam bots only** (`users.shadowbanned_at`, set from the account's admin page with a "this account is a spam bot" box). Turning one on withdraws the votes already cast. From then on the store saves the account's posts and comments already removed with the reason `shadow` (no comment count, no activity bump), and the web package shows them to their author as if they were up (`ownShadow`; the author can even comment under its own shadowed post and comments). Everyone else sees "[removed: spam]" or nothing; the admin sees "silenced (shadowbanned account)". Its votes return the arrows it expects but store nothing; its reports say "Thanks" and are dropped. Lists don't show its posts even to itself. The public log says "Spam account silenced" without naming it; lifting it ("Silencing lifted") leaves what was shadowed hidden.

**Schema check at startup.** `store.SchemaVersion` is the newest migration's number (a test in internal/testdb fails if they differ, so a new migration means bumping it). On startup the server reads goose's own table: a database that's behind stops the server with "it's at 00014, this code needs 00015 (run goose ... up)"; one that's ahead only logs a warning. That replaces a broken front page with a clear message.

**Content checks** (slice 4; web/spamchecks.go, store/spam.go), on every new post and comment (the link rules also on edits):

- *Probation:* for its first 3 days (`probation`) an account can't share links: not in a post's link field, and not in any text. Links are found by rendering the Markdown (through the cache) and reading the `href`s back, so exactly what would become a link on the page is caught, bare `www.` text included. It also posts and comments more slowly (`posting-new`, `commenting-new`). The submit page says when links unlock. The admin is exempt.
- *Duplicates:* by content hash. The same post (title, link and text) from the same account within 7 days, or the same comment within a day, is refused; so is text that 2 or more other accounts posted in that window (a spam wave). Comments under 20 characters are never duplicates ("Thanks!"), and the wave rule only applies from 40.
- *Domain blocklist* (`/admin/domains`, linked from the admin's account page): what the admin types is normalized (`https://www.Example.com/x` becomes `example.com`); a blocked domain covers its subdomains but not lookalikes (`notspam.example` isn't `spam.example`). Kept in memory, reloaded after each change. Existing posts are left alone.

**Retention** (slice 5; store/retention.go, run hourly by `housekeeping` in cmd/snowball/main.go alongside the expired-session sweep). `Purge` erases the words of posts and comments the admin removed more than 30 days ago (`Retention`): body, link and content hash emptied, a post's title becomes "[removed]", `erased_at` set. The rows stay, so replies keep their places and the public still sees "[removed: spam]". Items still held by reports are left until the admin decides. Handled reports older than 30 days are deleted (open ones never), and suspensions that ended or were lifted more than 30 days ago keep their dates and reason but lose their note and appeal text. The log keeps its entries (they hold no content). The server log records counts only. The admin sees "text erased after 30 days" on such items; `/rules` explains what's kept and for how long.

The moderation chapter is complete.

### 7.20 Notes from the admin (web/notes.go, store/notes.go)

The front page's yellow box shows the newest note that isn't hidden ("From the admin, Oct 15:"), rendered from Markdown through the same cache and sanitizer as posts, with a link to `/notes`, which lists every visible note newest first (a running record of how the site changed). With no notes, there's no box. The admin writes them at `/admin/notes` (also linked from the account page): post, edit (marked "edited"), hide (off the front page and `/notes`, still in the admin's list; the next newest takes the front page), show, and delete (with a confirm box). The current note is kept in memory (`latestNote`), loaded at startup and after every change, so the front page doesn't query for it. Like posts, notes only link with full `https://` addresses (the sanitizer drops relative links). The seed program adds one note so the box shows.

### 7.21 Reply notifications (web/replies.go, store/replies.go)

A reply to you is a comment directly under one of your comments, or a top-level comment on one of your posts, by someone else. Deeper replies, your own replies, and @mentions (which don't exist) don't count. **Nothing about replies is stored** beyond the comments themselves: each account has only `unread_replies`, bumped by one in the same transaction that saves a reply (`notifyReply`, the single place push notifications would hook into later), and `replies_seen_at`. Both ride along in `UserBySession`, so the header costs nothing: a quiet "replies" link, or a yellow "3 new replies" when there are some. `/account/replies` (private, like the history page) lists replies newest first with who, the start of the text, which post, a link to the reply in its thread (or its own thread page when it's deeper than the post page shows), and a "new" badge for anything after `replies_seen_at`. Opening its first page resets the counter and the time; the page is worked out first, so what was new still shows as new that once. Replies that were removed or deleted, replies under a comment or post you've since deleted, and replies on removed posts drop out of the list (the counter may briefly count one; harmless). A shadowbanned bot's replies never count. Suspended accounts still see their replies. The inbox query reads all of one account's replies to sort them: about 20 ms warm for the busiest seeded commenter (1,800 replies).

**Push notifications later.** The design is ready for them: a "push subscriptions" table (opt-in per device), a service worker and manifest so the site installs as an app, and a sender called from `notifyReply`. Pings would carry no content ("You have new replies on Snowball"), since the delivery services (Apple, Google, Mozilla) see that a ping happened, though not what it says. Decided: wait until there are real users who want it.

### 7.22 Polls (web/polls.go, store/polls.go)

Any post can carry a poll: the submit form's "Add a poll" section (a `<details>`, so no JavaScript) takes 2 to 6 options (up to 80 characters, plain text, no duplicates; empty slots are skipped) and how long it stays open (3 hours, 12 hours, 1 day, 3 days, a week). Like the title and link, the options and closing time can't be changed after posting, so nobody's vote ends up behind different words. Lists mark poll posts `[poll]`.

**Voting.** Same eligibility as the arrows: logged in, past the first day, not suspended. Votes are final. Before voting you see the options and the vote count; after, the results (bars are `<progress>` elements, since the CSP forbids inline styles), with your pick ticked. Open results refresh themselves every 15 seconds: htmx `hx-trigger="every 15s, poll-refresh"` on the box, replaced whole, so a closed poll stops refreshing by itself; snowball.js skips refreshes while the tab is hidden and fires `poll-refresh` when it comes back. Logged-out visitors see options and the count until the poll closes. Once closed, everyone sees the final results, the leader (or tied leaders) starred. The admin always sees results. A shadowbanned account's vote looks counted in its response (without the live refresh, which would give it away) and is never stored. A removed or deleted post's poll takes no votes.

**Pick tags.** A comment on a poll by someone who voted shows their pick by their name, in the option's colour (`pick-0`..`pick-5`, shared with the bars). It's stored on the comment (`comments.poll_pick`) when it's written, or set on their earlier comments there when they vote, so it survives the fingerprints being deleted. Voting alone shows nothing to anyone; commenting on a poll you voted in shows your side. This is the one public link between an account and a vote, and it was the owner's call (no opt-in checkbox).

**Privacy.** `poll_votes.voter` is `sha256(poll key + ":" + post id)`, where the poll key is `HMAC(vote secret, "poll:" + account id)` (`auth.VoterKeys.PollKey`, never stored). So an account has a different fingerprint in every poll, and a leaked database can't line one person's picks up across polls. The store can still find an account's votes in every poll from its key alone, in SQL (`pollFingerprintSQL`; a test checks it matches Go's `store.PollVoter`), which cleanups, shadowbans (`withdrawPollVotes`, which also corrects the totals) and account deletion (`cutPollVotesLoose`: votes keep counting, fingerprints become random) rely on. Every 5 minutes the housekeeping loop (cmd/snowball) runs `ClosePolls`, deleting the fingerprints of closed polls; only the totals remain. Deleting a post deletes its poll (the options are its words), as do account erasure and the 30-day erasure of removed posts.

**Seed.** About one post in 40 carries a poll (cmd/seed/polls.go): polls on posts from the last three days are open, with fingerprints made the way the site makes them, so a seeded account that voted sees its pick; older ones are closed, totals only. Comments carry their author's pick.

### 7.23 Mega polls and the admin hub (web/megapolls.go, store/megapolls.go)

**The daily question.** The admin queues mega polls at `/admin/polls`, one per day: a question, optional Markdown text, 2 to 6 options, and topics (the usual tag picker). Days are calendar days in Pacific time (`siteZone`, with `time/tzdata` built in so the server needs no time zone files). Once a minute (`statsLoop`) and at startup, `publishMegaPolls` publishes today's entry: an ordinary post with a poll, `by_site` (shown everywhere as from "Snowball", with a snowflake, never the admin's name), its poll `mega`, closing at the next Pacific midnight (`dayEnd`, which handles the 23- and 25-hour days). Topics are matched to tags at publish time, so tag renames and merges in between are fine; if they no longer match, it isn't published and `publish_problem` shows on `/admin` and the queue. Queued entries can be changed, moved to a free day, or deleted until published; after that it's a post with the usual rules (the admin is its real author, so the edit window and delete work). A day that passes without its entry going up shows as missed.

**On the site.** Today's mega poll is kept in memory (`megaToday`) and gets a box at the top of the front page: the question, then a plain vote form that sends people to the post (or "You picked X", or a log-in link). It also appears in the normal lists and tag pages, marked `[mega poll]`. Its results stay hidden until it closes, even after voting: voters see their pick and the running total (which refreshes live on the post page); the admin always sees results. Pick tags on comments still show. Comments on Snowball's own posts don't notify the admin or fill their inbox (`notifyReply` gets 0; `Replies` skips `by_site` posts); replies to the admin's own comments there still do. `/polls` is the public archive: each day's question, the winner (or a tie) with its share of the votes, and the comment count; open ones say "open now" with no result.

**The hub.** `/admin` gathers every admin page (reports with the open count and appeals, the moderation log, blocked sites, account lookup, notes, tags) and the mega poll status: today's question, a warning if it couldn't be published or there's none, and how far the queue runs. The account page's admin panel is now one link to it.

**Seed.** cmd/seed/megapolls.go adds 21 closed mega polls (totals only), today's (open, with fingerprints), and a week queued ahead, all from the admin account as "Snowball".

### 7.24 Thread controls (web/locks.go, store/locks.go)

For a thread that's getting out of hand, short of removing it. On any post's page the admin's bar now has **lock comments** / **unlock comments** and, for an open poll, **close the poll now**, next to "remove post". Locking (`posts.locked_at`) stops new comments, replies and comment edits (the reply form, `CreateComment` and `EditComment` all refuse; the post page says "The admin has locked this post"); everything stays readable, and votes, reports and deleting your own comment still work. Closing a poll sets its close time to now, deletes its fingerprints at once, and shows everyone the results (for a mega poll too). All three go in the public moderation log ("Post locked: no new comments for now", "Post unlocked", "Poll closed early").

For mega polls, `/admin/polls` has a **Today** panel: the poll's state, lock/unlock and close-voting buttons, and **take it down** (a removal with a reason; the front page box goes at once, as it does whenever today's is removed or closed, via `refreshMegaToday`). With nothing up today, it offers **run it now** for the next queued question (`RunNextNow`): today's taken-down entry leaves the queue (its post stays removed), every unpublished entry after today moves up a day (gaps kept), and the new today's is published straight away. It refuses while today's is still up, or when today's hasn't gone up yet (fix or delete that one instead), or when nothing's queued. Editing a published question was left out on purpose: votes must stay behind the words they were cast for.

## 8. Product decisions log

| Decision | Reasoning |
|---|---|
| Generated names, no choosing, no renaming | Anonymity; accountability for moderation; reputation can't be shed. |
| One name per account, shown everywhere | Accounts can have personality and history without identifying a person. |
| No email; recovery codes instead | No personal data. Losing password and codes means losing the account, and users must acknowledge this. |
| Tags instead of boards | One shared room is easier to moderate and livelier when small. Tags are the site's only community and search structure, so they get a full design (v2, section 11.1). |
| Tags v2: a tree of concepts with aliases | Lets very niche topics be found without splintering into duplicates. Owner's own design doc, adapted to one-person running. |
| No catch-all "general" tag | Every post needs a real topic; the tree must be broad enough instead. Posts that fit nowhere pick the closest tag and request a better one. |
| Politics and religion included | The site should hold any conversation; both are their own top-level tags. Moderation tools become more urgent because of them. |
| Adult content later, as a post flag | Explicit content waits for moderation and legal research (age-verification laws, host terms). When it comes, NSFW is a flag on the post (hidden by default, opt-in), never a tag; tags can force it. |
| Tag changes are checked against the whole tree, inside the transaction | One path for every change: make it, read the result back, check it with the same rules the site uses, roll back on any new problem. A change that would loop or mislead never reaches the database, and `tag_closure` always matches the parents it was built from. |
| Renames and merges keep the old names as aliases | Old links, bookmarks and habits keep working; nothing a member typed before stops meaning something. |
| Reporters are anonymous, even to the admin | A keyed fingerprint (like votes) is enough to stop duplicate and excessive reports; storing who reported whom would be a record of members' relationships. |
| Auto-hide only for illegal content and personal information (3 reports) | Harm that can't wait for the admin to wake up; anything else waits for review, so groups can't brigade ordinary posts away. |
| Suspended accounts are read-only, and can still delete their own content | Sanctions limit participation, not the right to erase what you wrote. |
| Shadowbans for spam bots only | Bots keep posting into nothing instead of making new accounts; on people it would be deception. |
| Public moderation log without content | Shows rules are enforced evenly, without repeating what was removed or naming anyone. |
| Reply notifications are worked out, not stored | The inbox is a query over existing comments; each account keeps only a counter and a time. Nothing to purge, nothing extra to leak. Votes never notify (they'd push people to chase points). |
| Removed text erased after 30 days | Long enough for appeals and mistakes; fits "store as little as possible". |
| Topic suggestions come from the server | The type-ahead asks the same Go code that checks the form, so a suggestion can never be refused on posting, and the page carries no tag list or JavaScript matcher to keep in step. Costs a small request per pause in typing (rate limited, around 1 ms each). |
| Follows and mutes stored on the account | Accepted cost: visible only on private account pages, and an account carries no real-world identity. |
| Front page is a hybrid | "Popular today" (hot ranking) plus bump-ordered "active" conversations. |
| Every logout logs out everywhere; password change too | Fits the "leave no trace" character. |
| Deleted names are never reused | Prevents a newcomer inheriting someone else's reputation. Soft delete. |
| Rerolls limited per IP (10, refilling) | Nudges people to consider each name. |
| Password minimum 6 with composition rules | Owner's choice (below the usual 8+), mitigated by rate limits, the login guard, and weak-password checks. |
| No third-party CAPTCHA or tracking | Would contradict the privacy promise. All defenses are self-hosted. |
| Discussions public for now; members-only later | A young forum needs visitors to see what they'd join. Built so it can be switched later. |
| Out of search engines from day one | `robots.txt`, `noindex` header and meta tag. |
| Your own votes are listed only on your private page, by item date | The site already knows your fingerprint (to highlight arrows), so listing them adds no exposure. No vote timestamps, so no "when you voted" order. |
| Complete post history only on a private page | Nobody can browse someone's history in one place; assembling a profile means hunting thread by thread. Users who dox themselves are responsible for that. |
| Keep every post (no expiry) | An archive problem is a good problem to have. |
| Random public IDs for posts and comments | Prevents enumeration scraping and hides site volume. |
| Media phased: text, links, Markdown first | Uploads carry the most legal, privacy, and security risk. Images next, then audio and video. |
| Exact timestamps shown | Kept for now; date-only display is the fallback if timestamps become a profiling issue. |
| Markdown: generous subset, no headings, no external images, no HTML | Expression without shouting, tracking pixels, or injection. |
| Comments: deep threads, Best sort by default (also New, Old) | Best (Wilson score) rewards quality, not being first. |
| Up and down votes, scores public, voters private | Votes stored as keyed fingerprints with no timestamps; unlinked on account deletion. |
| No voting on your own content; 1-day wait before voting | Self-votes mean nothing; the wait blunts fresh-account brigading. |
| Vote limits: 10/burst then 1 per 3s; 250 a day; 10 a day per author | Stops mashing, scripts, and targeting one person. Daily and per-author count only new votes. |
| 10-minute edit window, 2-minute grace, no revisions | Typos fixed silently; later edits marked; then final. One record per item. |
| Deleted content keeps threads intact; deleted accounts show "[deleted]" | Conversations survive; retired names stop being attached to anything. |
| "Top" is raw score; "Popular today" is hot | Top means top; popular should favor what's rising now. |
| Post titles and links can't be edited, only text and tags | Reddit's lesson: link swaps after votes are a spam trick; titles define what was voted on. |
| Edits don't bump activity | Re-saving shouldn't keep a post on top. |
| Deleted posts keep their title | Reddit's choice: the discussion underneath needs its context. |
| Deletion erases content from the database, irreversibly | Store as little as possible; hidden text can still leak through backups or bugs. Authorship links remain, for moderation. |
| Own removed content stays visible to its author, labeled | Being straightforward with people about their own content. |
| "Up" links to each page's logical parent, not browser-style "back" | Predictable however someone arrived, no JavaScript; the logo still goes home. |
| Any post can be a poll; mega polls (step 2) are polls the admin schedules | One voting system, one results display, one set of moderation rules. |
| Poll options and length fixed at posting | The same rule as titles and links: votes must stay behind the words they were cast for. Stricter than "lock at the first vote", and simpler. |
| Poll votes are final, results after voting, live while open | A pick should mean something; hiding results until you vote stops the early count from steering everyone. Live refresh makes the site feel alive. |
| Pick tags on poll comments, always on | The owner's call: it's what makes the arguing fun. Voting alone stays private; commenting shows your side. |
| Poll fingerprints differ per poll and are deleted at close | Store as little as possible: once nobody can vote again, nothing needs to know who voted. |
| Mega polls are from "Snowball" | The owner's call: it's the site asking, not one member. The admin is still the stored author, for moderation and editing. |
| Mega poll results hidden until midnight, even from voters | The suspense is the point: people come back the next day to see how it came out. |
| Mega polls also appear in the normal lists | Two ways in (the box and browsing); one comment thread. |
| Lock and close-early before removal | Gentler tools for a heated thread: stop the fire without erasing the poll or the decent comments. |
| Taking down today's mega poll can pull the next one forward, shifting the queue | The owner's call: a mistake shouldn't leave the front page empty, and nothing goes missing from the queue. |
| One `/admin` hub; no general "events" system yet | Build sections when there's a real second kind of event. |
| When in doubt, learn from Reddit's battles | Twenty years of fighting spam and manipulation behind its odd-looking rules. |

---

## 9. Conventions and gotchas

Restart the server after template changes; CSS/JS changes only need a reload. All CSS goes in `static/css/style.css`: other CSS files are never loaded (snippets must be pasted in, not saved alongside). Template comments `{{/* */}}` never reach the browser; HTML comments `<!-- -->` are sent to every visitor. Never edit a migration that has run anywhere important; add a new numbered one. Run `go mod tidy` after adding or removing imports (silence means success). In psql, statements end with `;` (a `-#` prompt means it's waiting; Ctrl+C clears). Folder = package in Go: exported names start with a capital letter. Lowercase all file and folder names (Windows is case-insensitive, Linux isn't). When CSS seems ignored, check the rule appears in the dev tools Styles panel; if absent, it isn't in the served stylesheet. Tests use table-driven style and inject time rather than sleeping (more in section 10). Go templates don't convert number types: a function taking `int64` can't receive an `int` (why `TagCount.Count` is `int64`). Functions that end with identical lines (like `ChangePassword` and `DeleteAccount`) are easy to edit by mistake; check which function an edit landed in. Deleted posts and comments have an empty `content_hash`, so duplicate detection must ignore empty hashes. To test the edit window without waiting, move an item back in time: `UPDATE posts SET created_at = now() - interval '3 minutes' WHERE public_id = '...';`.

---

## 10. Testing and checks

### One command

`.\scripts\check.ps1` runs everything, cheapest first, and stops at the first failure: start Postgres (`docker compose up -d --wait`, skipped in CI), gofmt, `go mod tidy -diff`, `go vet`, staticcheck, govulncheck, `go build`, then `go test -count=1 -race ./...`. CI runs the same script, so a local pass means a CI pass, and the list of checks lives in one place.

| Option | Effect |
|---|---|
| `-Quick` | Skips staticcheck, govulncheck, and the database tests (adds `-short`). For use while working. |
| `-Skip staticcheck` (or `govulncheck`, or both comma-separated) | Leaves out a tool, for example while it's broken upstream or when offline. |
| `-Fuzz` (with optional `-FuzzSeconds 120`) | After everything else, runs each fuzz target for 20 seconds (or as given). |

The race detector needs cgo and so a C compiler; the script asks `go env CGO_ENABLED` and, without one, tests without `-race` and says so (CI always has it). On Windows, PowerShell refuses downloaded scripts: `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned` once, and `Unblock-File` on a script (or on a zip before extracting it) that came from the internet. staticcheck and govulncheck run through `go run ...@latest`, so they need no install and stay current, but a new tool release (or a new Go release the tool can't read yet) can change results without any code change.

### Kinds of tests

| Kind | Where | What it gives |
|---|---|---|
| Unit tests | Most `*_test.go` files | Pure logic, no database: password rules, hashing, tokens, limiters, comment trees, Wilson scores, validation, durations. Fast. |
| Database tests | `store/*_db_test.go` | Every store function against a real Postgres, including the constraints the database enforces. |
| Concurrency tests | `store/concurrency_db_test.go` | Many goroutines released together by a starting gate (`together`) hit one post, code, or name, then the data must still add up. |
| Request-level tests | `web/requests_test.go` | The complete handler stack (headers, CSRF protection, sessions, rate limits, routes) driven with `httptest`: real flows, permission boundaries, cross-site refusal. |
| Smoke tests | `web/smoke_test.go` | Every page as every kind of visitor: expected status, security headers, a complete page (template errors only appear when rendered), `no-store` when logged in; htmx endpoints return fragments. |
| Fuzz tests | `fuzz_test.go` in `auth`, `web`, `store`, `markdown`, `tags` | Go's fuzzer generates inputs to break invariants. Seeds run as ordinary tests every time. |
| Migration round trip | `testdb/testdb_test.go` | Every migration up, all the way down, up again; the schema (columns, constraints, indexes) must come back identical and the down leave nothing behind. |

### Test databases

Database tests need Postgres running (`docker compose up -d`). Each package uses its own database so packages can run in parallel without emptying each other's tables: `snowball_test` (store), `snowball_web_test` (web), `snowball_migrations_test` (the round trip, which never holds data, since some Down sections refuse to run when certain data exists). `testdb.Prepare` refuses any name not ending in `_test`, as a safety catch, then has `dbsetup.Ensure` create the database if missing and migrate it up. The server and credentials come from `SNOWBALL_TEST_DATABASE_URL`, defaulting to the compose development values (`testdb.DefaultURL`); CI sets it for its Postgres service.

Each test starts from empty tables: `freshDB` (store) and `newSite` (web, which also builds a new server, so rate limits start untouched) run `TRUNCATE ... RESTART IDENTITY CASCADE` over `testdb.Tables` (every table except the tag tree's: `tags`, `tag_parents`, `tag_aliases` and `tag_closure` keep the starter tree from the migrations, so tests that change tags make their own, with slugs starting `zz-test-`, and delete them afterwards). **When a migration adds a table, add it to `testdb.Tables`; every new migration also bumps `store.SchemaVersion`.** Tests in one package therefore run one at a time (no `t.Parallel()`).

`go test -short ./...` skips everything that needs Postgres. Each package's `TestMain` prepares its database once, and fails with a hint ("is Postgres running?") when it can't.

### Helpers

**store** (`store/main_test.go`): `freshDB`, `exec`, `count` (one-number queries), `expectConstraint` (a statement must be refused by a named constraint), `mustUser` (recovery code hashes `code-a`, `code-b`), `mustTag`, `mustPost` (tagged `tech`), `mustLoadPost`, `mustComment`, `mustLoadComment`.

**web** (`web/main_test.go`): `newSite`; `s.user` (an account two days old, logged in by creating a session directly, password `Correct-Horse-7!` hashed once for all tests), `s.session` (another device), `young`, `admin`; `s.post`, `s.comment`; `s.token(form)` (a valid form token issued an hour ago, so the "too fast" check never interferes); `s.get`, `s.send`, `s.do` (requests through the whole stack, over an `https://` URL as in production; extra header pairs like `"HX-Request", "true"`); `expectStatus`, `expectRedirect`, `expectContains`, `expectMissing` (these search the body with HTML escapes undone), `sessionCookieFrom`; `sqlExec` to arrange scenes directly.

### Fuzzing

| Target | Package | Invariant |
|---|---|---|
| `FuzzParseHash` | auth | Any stored hash string parses within bounds or is refused; never panics. |
| `FuzzPasswordProblems` | auth | Never panics; an accepted password is within the length limits and doesn't contain the username. |
| `FuzzSafeBack` | web | The result is a path on Snowball as a browser would read it (after dropping tabs and newlines). |
| `FuzzParseCursor` | store | Any string is refused or becomes a cursor (ID at least 1) that survives a round trip through its URL form. |
| `FuzzTopicsBox` | tags | Any text in the topics box splits into clean parts; suggestions are topics and never excluded ones; picking any topic leaves the box ready for the next with the pick recognized. |
| `FuzzRender` | markdown | The output, read with a real HTML5 parser (`golang.org/x/net/html`), contains only allowlisted elements and attributes, and links only with allowed schemes. |

Run one with `go test -short -run '^$' -fuzz '^FuzzSafeBack$' -fuzztime 60s ./internal/web` (`-short` because no fuzz target needs the database, and `-run '^$'` so no ordinary tests run first), or all with `.\scripts\check.ps1 -Fuzz`. A failing input is saved under that package's `testdata/fuzz/`; commit it, and it runs as a regular test from then on. Fuzzing found two real bugs: the `safeBack` open redirect through tab and newline characters (section 7.13), and stored-hash parameters that could panic or exhaust memory (section 7.7).

### Concurrency tests and the race detector

The concurrency tests cover: one voter's rapid clicks on one arrow (must all succeed and toggle to nothing), many voters at once (counters match vote rows), many comments at once (comment count matches comments), one recovery code raced from many places (exactly one succeeds), and one name signed up for many times at once (exactly one account, and the losers leave no recovery codes behind). Goroutines can't call `t.Fatal`, so each reports its result on a channel and the test checks them afterwards.

**Break-it exercise:** remove `FOR UPDATE` from the post lock statement in `store/votes.go` and `TestRapidClicksByOneVoter` fails with duplicate-key errors on `post_votes_pkey`, because simultaneous clicks all read "no vote yet" and all insert. Simple `counter = counter + 1` updates are atomic without a lock; the lock matters where code reads, decides, then writes.

`-race` (always on in CI) watches for goroutines touching the same memory without synchronization: the rate limiters, the login guard, the cached post count.

### CI

`.github/workflows/ci.yml` runs on every push and pull request: a Postgres 18 service container with the development credentials, `actions/checkout@v5`, `actions/setup-go@v6` with `go-version: stable` (the newest patch release, so govulncheck doesn't flag holes already fixed), then `pwsh ./scripts/check.ps1`. Each check is a collapsible section of the log. A failed run caused by an outside tool can be re-run from the Actions tab once the tool is fixed, without a new commit. A full run takes about two minutes.

### What's covered

- `auth`: hashing and verification, damaged and unsafe hashes, normalization, password rules, the hashing semaphore, form tokens, voter fingerprints and the vote secret's minimum length; reporter fingerprints differ from voter fingerprints.
- `markdown`: allowed formatting, dangerous input blocked, links `nofollow`, headings flattened to text; the render cache (same output as rendering, bounded, safe to share between requests).
- `store`: usernames unique forever and well-formed, credentials, sessions, logout everywhere, password change, recovery codes spent exactly once, account deletion (with and without content, including vote unlinking), posts and their database-enforced limits, lists hiding removed and deleted posts, pagination that never skips or repeats, comments and their rules, vote toggling and rules, comment votes and "my votes", vote tables staying private, edit windows and grace, deletion erasing content but keeping threads, history, the voted history views (what's included, and paging posts and comments together without skips or repeats), the database refusing broken data, cursor parsing, and the concurrency cases above; changing tags: creating one builds its closure rows, a check that objects rolls everything back, parents and aliases added and removed, renaming with old names kept, merging (posts, children, names), deleting only unused tags; `tag_posts` staying in step through posting, editing, re-parenting and merging; moderation: one report per person, the daily cap, serious reports hiding at the third (ordinary ones never), dismissing restoring a held item and its count, removing with a reason (held to removed counts once), the queue's order and contents, and the public log leaving out dismissals; suspensions: carried by the session lookup, ending on time, one appeal, keeping and lifting, replacing, the admin exempt, the log's account entries; cleanup (everything removed including held items, counts and scores corrected, only that fingerprint's votes withdrawn, reports closed, the admin exempt); shadowbans (earlier votes withdrawn, new posts and comments arriving shadowed and uncounted, commenting under its own shadowed things, nobody else able to, lifting); the blocklist (format check, no doubles) and duplicate lookups (own and others', inside the window only); retention (old removals erased with rows kept and replies untouched, recent removals and held items kept, handled reports deleted after 30 days and open ones never, old suspensions emptied, a second sweep finding nothing).
- `tags`: normalizing names, ancestors and paths (including two parents), loops refused, lookups (aliases, ambiguity), choosing a post's tags (forgiving spelling, implied ancestors, limits, wrong facets, tampered values), chips and the edit round trip; the type-ahead pieces (splitting the box, ranking, picking; fuzzed by `FuzzTopicsBox`); slug, display name and alias rules, `Slugify`, and `Tree.Check` finding each kind of misleading name.
- `web`: client IPs, limiters (burst, forgetting, status), the login guard, durations, post validation, number formatting, Wilson scores, comment trees and depth rules, safe return paths, vote widget rules, the edit window, history snippets; and through whole requests: login (including bot checks), logout everywhere, sign-up (including a forged name), logged-out redirects, cross-site refusal, nobody editing or deleting others' work, admin endpoints hidden from everyone else, submitting, commenting and replying, voting, the upvoted and downvoted views (private to the voter, unvoting removes an item), tag pages including everything below and the "this tag only" switch, old names and aliases redirecting, ambiguous names, the tag index and sidebar counts, typed topics on submit (unknown, ambiguous, forgiving spelling) and the edit page's tag round trip, topic suggestions (ranking, shared aliases, settled topics and their ancestors left out, three-topic cap, no match, length cap) and picks (what the box ends up holding posts cleanly; formats and unknown slugs refused); the real starter tree's health (no misleading names, every alias leads home, `tag_closure` matching the parent links); the admin tag page (a 404 and no links for non-admins; creating, renaming with old links still arriving, re-parenting and aliases live at once, including in the type-ahead; each refusal with its message and nothing changed; merging and deleting); who sees report links, the report form's checks and refusals (own content, new accounts, logged out), reports stored under the reporter fingerprint, serious reports hiding a comment, the queue for admins only with its header count, removing and dismissing from it, removal reasons required and shown in place, the public log without content or dismissals, and the rules page; a suspended account's banner, hidden controls, refused writes (posting, commenting, reporting, editing, voting over htmx) and allowed reading and deleting; the suspension page and its one appeal; the admin's account page, suspending and lifting, the admin exempt; deciding appeals; removing and suspending in one step from the queue; a shadowbanned bot seeing its own posts while nobody else does, its votes and reports dropped quietly; cleanup from the account page and from the queue; probation (links refused in the link field, text, bare `www.` and Markdown links, comments; slower posting; old accounts unaffected), duplicates (own posts and comments, short comments exempt, spam waves), the blocklist (normalizing, subdomains caught, lookalikes not, unblocking), domain normalization; erased items as the admin and the public see them; plus the smoke tests of every page and fragment.
- `testdb`: the migration round trip.

Not covered yet: a red-team bot attacking the defenses from outside, load testing, and the browser side (snowball.js, htmx behavior in a real browser).

### Known tool issue (October 2026)

Go 1.27.2 writes export data that staticcheck 2026.2.1 (v0.8.1) can't read ("export data version 5 is greater than maximum supported version 4"; dominikh/go-tools issue #1832). A fix was announced for release on 9 October 2026. Until it ships, run `.\scripts\check.ps1 -Skip staticcheck` locally, and expect CI's staticcheck section to fail; once a fixed release is out, `@latest` picks it up automatically and the failed CI run can be re-run. Remove this note then.

---

### Performance

**How to measure.** Seed the large site (`go run ./cmd/seed -size large`: 20,000 accounts, 100,000 posts, 1.2 million comments, 5.8 million votes, about 2 GB), start Snowball against `snowball_dev`, and run `go run ./cmd/bench`. It picks real examples from the database (the busiest thread, a busy account, the widest and narrowest tags, page 30 of `/new`), logs in through the real form as a seeded account and as the admin, and requests each page 15 times, printing median, p95, slowest and size. `-load 30s -c 50` then has 50 logged-out visitors browse at once. Run it before and after a change; compare runs on the same machine, not with production.

**Finding a slow query.** Postgres can log every statement over a threshold: `ALTER SYSTEM SET log_min_duration_statement = 10; SELECT pg_reload_conf();` (as the database owner), then read the log (`docker compose logs db`). Paste the query into `EXPLAIN (ANALYZE, BUFFERS) ...` to see the plan and where time goes. If no query is slow but the page is, the time is in Go: profile with `net/http/pprof`.

**The pass (October 2026), large seed, median times on the development machine:**

| Page | Before | After | What changed |
|---|---|---|---|
| `/t/question` (a format on 25% of posts) | 92 ms | 3 ms | `tag_posts` (below) |
| A narrow tag (`/t/lgbtq`) | 10 ms | 5 ms | `tag_posts` |
| Tag counts (background, once a minute) | 245 ms | 96 ms | counted from `tag_posts` |
| Biggest thread (2,400 comments), logged out | 92 ms | 61 ms | rendered-comment cache |
| Biggest thread, logged in | 153 ms | 96 ms | rendered-comment cache |
| Everything else (lists, posts, tags, history, admin) | 0.5 to 10 ms | about the same | already fine |

Load test, 50 visitors at once for 20 seconds, with server, Postgres and load generator sharing 2 CPU cores: about 710 pages a second, median 73 ms (mostly queueing), p99 141 ms, no failures.

**What was slow, and why.**

- *Tag pages for big tags.* Listing "newest posts under X" started from `post_tags` and `tag_closure`, gathered every matching post (25,000 for Question), then sorted them all to show 30. Postgres can't know in advance how many posts a tag has (it plans for an average one), so it picked that plan for every tag. Fix: `tag_posts` (migration 00012) lists every post under every tag it shows up under, with the post's time, indexed in page order; a tag page reads 31 index entries and stops, however big the site gets. It's derived data, kept in step in the same transaction as whatever changes it: `CreatePost` and `EditPost` rebuild the post's rows; tree changes on the admin page rebuild the rows of every post under the tag that moved (worst case, moving all of Food & drink: about 0.7 s, admin-only). `TagPostsMismatches` (tests) and the seed program's checks compare it with `post_tags` and the tree.
- *Huge threads.* No query was slow; profiling showed Go's time split between rendering Markdown (40%) and executing templates for 2,400 comments (50%). Fix: `markdown.Cache`, a bounded two-generation memo of `Render`, keyed by the comment's text (an edit changes the text, so nothing needs invalidating; about 20,000 texts at most). The rest is template work proportional to the page: 1.9 MB of HTML logged out, 3.3 MB logged in (each comment's vote form). The real remedy is comment paging (backlog, 11.5); compression (below) makes the transfer about 12 times smaller meanwhile.

**Known and fine for now.** The voted-history views read all of an account's votes of that direction, then sort: 21 ms for a busy account, 47 ms for the busiest voter (3,600 votes); it grows with one account's votes, not the site. A query that has to read from disk is many times slower than these warm numbers (the busiest voter's history took 3 s cold): production needs enough memory for Postgres to keep the busy part of the database cached (section 11.4).

**Compression.** Snowball sends pages uncompressed; gzip makes the biggest thread 151 KB instead of 1.9 MB. Caddy compresses in production (`encode zstd gzip`, section 11.4), so Go doesn't. (The BREACH attack on compressed HTTPS needs a secret in the page next to attacker-chosen text; Snowball's form tokens only time forms, cross-site requests are refused by origin checks, and the session lives in a cookie, never in a page.)

## 11. Roadmap and backlog

Order from here: tags v2 Phase 1 (11.1), then the performance pass (11.5), then moderation (11.2), then images.

### 11.1 Tag system v2 (Phase 1 built)

The owner's design doc ("Forum Tag System: rules and examples") is the base; the starter tree lives in a separate working doc ("Snowball Starter Tag Tree"). What was decided:

**Concepts and structure.**
- A tag is a concept with one canonical slug (`sm64-blj`), a display name ("BLJ (Backwards Long Jump)"), a one-line description, and any number of aliases. Typed input is normalized (case, spaces, hyphens, punctuation) and matched against slugs and aliases. Plurals are handled by aliases, not by stripping a trailing "s" (which breaks "news", "glass").
- Tags form a tree that allows several parents (a directed graph without loops; a change that would create a loop is refused). Picking a tag implies all its ancestors. Posts store only the tags chosen; ancestors are computed when browsing (the `tag_closure` table holds every ancestor and descendant pair, rebuilt when the tree changes), so moving a tag fixes every post at once.
- Three facets: Topic (1 to 3 per post, required), Format (0 or 1: question, discussion, guide, showcase, news, review), Level (0 or 1: beginner, intermediate, advanced). At most 5 tags per post. Format and Level are fixed lists only the admin changes. `video`/`pov` formats wait for media posts.
- Current events are not a topic: a news post takes the `news` format plus its subject, and a news page lists every news post.
- Naming rules: lowercase slugs; the community's own name; scoped names for ambiguous terms (`sm64-wall-clip`); name the subject, never the post type (no `-tips`, `-help`); cross-cutting concepts exist once; combine two tags before creating a compound one.
- Tag pages include descendants, with a "this tag only" switch. Sidebar and tag counts include descendants and are cached, not counted on every page load.

**Status.** Phase 1 is built. Part A: schema and migration 00010, typed topics with format and level, tag pages with everything below them, `/tags`, cached counts, the seed program tagging from the tree. Part B: type-ahead suggestions in the topics box (section 7.10) and the admin tag page (section 7.11). From now on the tree is data, changed on the admin page, not by migrations. Phase 2 (members asking for and creating tags) waits until after moderation.

**Starter tree.** 341 tags: 35 top-level (Humanities was split into History and Philosophy; Religion, Politics, Law, Fashion & beauty, Internet culture, True crime, Cycling, LGBTQ+ and Paranormal are roots; Everyday life became Relationships and Family), about 250 first-layer and 50 deeper tags. Every tag went through two independent reviews; 12 with more than one plausible home are parked (digital photography, comics and manga, worldbuilding, blogging, computer science, economics, civil rights, criminal trials, mythology, disability, living abroad, podcasts). Places (countries, cities) wait. The working data is a plain-text tree file that will feed the migration.

**Two-parent rule.** Picking a tag files the post under all its ancestors, so a tag gets a second parent only if every post using it belongs under both (bike racing: cycling and sports; never horror under both books and film). Genres are scoped to their medium (`fantasy-books`). Ambiguous words get scoped slugs (`go-language`, `rock-music`, `ice-hockey`) and, where a word really means two tags, a shared alias that makes the picker ask (`football`, `go`). Today's ten tags map onto it (`food` to `food-and-drink`, `diy` to `crafts`, `tech` to `technology`, `ask` to the `question` format, `show` to `showcase`); old names become aliases so `/t/food` keeps working.

**Who changes tags, and how tags are added over time.** The starter tree goes in once by migration. After that, tags are data: the admin creates, renames, merges (the old name becomes an alias), and re-parents them from an admin page, with no migration or deploy. Members ask for tags under `meta` > `site-tags`. The admin is the only curator to start.

**Phases.**
1. Phase 1 (built): tag schema (display name, description, facet, aliases, parents, closure), the migration with the starter tree and the old-tag mapping, the type-ahead picker (htmx) with ancestors shown as "included", 1 to 3 topics plus optional format and level, tag pages with descendants, cached counts, and the admin tag page.
2. Phase 2: member-created provisional tags (account age and post minimums, duplicate checks by spelling with Postgres `pg_trgm`, a parent suggested from the post's other tags), the admin's approval queue, expiry by merge or removal. Provisional tags stay out of autocomplete until approved, and a word blocklist applies to tag names.
3. Phase 3: AND/OR/NOT filters; follow and mute (stored on the account; following or muting includes descendants; mute beats follow); saved feeds as shareable links with no owner.
4. Later, if ever: trusted members who retag others' posts (needs reputation and notifications).

**Adult content.** Waits. Sexual health and relationships as text discussion can be topics under normal rules. Explicit material needs moderation tools and legal research first (several US states and the UK require age verification for sites with substantial sexual content; it conflicts with anonymity; some hosts forbid it). When it comes, NSFW is a flag on the post: authors and the admin can set it, a tag can force it, comments inherit it, viewers opt in with an account setting (off by default, never for logged-out visitors), and a direct link shows a confirmation step. It can grow into a rating or content warnings (spoilers use the same mechanism).

### 11.2 Posting chapter: status and next slices

| Slice | Status |
|---|---|
| 1. Posts table, submit page, post page, admin removal | Done |
| 2. Real front page, lists, tags, keyset pagination | Done |
| 3. Comments | Done |
| 4. Votes, vote limits, hot ranking | Done |
| 5. Editing | Done |
| 6. Private history page and self-deletion | Done |
| 7. Images | Waiting for moderation (below) |

**Images, then audio and video.** Planned work: an upload field on the submit page with size and dimension limits; server-side re-encoding of every upload (decode and save fresh, which strips metadata such as GPS coordinates and neutralizes malicious files; Go's standard library covers JPEG, PNG, and GIF, with `golang.org/x/image` for WebP); no SVG; storage in a folder to start, object storage later; serving with strict content types, ideally from a separate subdomain (with matching CSP changes); thumbnails in lists; no uploads during probation; per-account upload limits; duplicate detection by image hash; admin removal that deletes the file. No third-party embeds; link cards or click-to-load instead.

**Why images wait.** A working report, review, and takedown process must exist before uploads open: anonymous upload sites attract illegal content, the site's operator is responsible for what it hosts, and US providers aware of apparent child sexual abuse material must report it (research the obligations before launch). The leaning is a short moderation chapter first, then images built on it. The alternative is building images now behind a setting that keeps uploads switched off.

**Moderation chapter (designed and built October 2026).** Built so far: the admin can remove posts and comments. The design, agreed with the owner:

- **Reports.** Logged-in accounts past the 1-day probation (the voting rule) can report posts and comments. Reasons from a fixed list: spam, harassment or abuse, personal information (doxxing), illegal content, wrong tags, other; plus an optional note (200 characters). The reporter is stored only as a keyed fingerprint (like votes), never an account ID, so not even the admin can see who reported; the fingerprint allows one report per person per item and a limit of 10 a day. The reporter sees "Thanks, the admin will look at it." Report rows are deleted 30 days after being handled.
- **Auto-hide, for serious reasons only.** Three reports from different accounts for illegal content or personal information hide the item until the admin reviews it; other reasons only wait in the queue (so a group can't brigade an ordinary post away).
- **The queue** (`/admin/reports`): one row per reported item with its reasons and count; illegal content and personal information first, then the most reported. It shows the text, a link to its context, and the author's name linked to an admin view of their history. Actions: dismiss, remove, remove and suspend the author, remove everything by the author. The admin's header shows the number of open reports.
- **Removal reasons, shown publicly.** Removing asks for a reason from the same list; readers see "[removed: spam]" rather than a bare "[removed]".
- **Suspensions and bans:** 1 day, 7 days, 30 days, or permanent, with a reason and an optional note. A suspended account is read-only: it can log in, read, and delete its own content, but not post, comment, vote or report. A banner gives the reason and end date. Its content stays unless the admin also cleans it up. One appeal per suspension (500 characters) from the banner, delivered to the queue. Ban evasion (signing up again) is accepted; the defenses are probation, proof-of-work and invites, not fingerprinting people.
- **Shadowbans, for obvious spam bots only.** The author still sees their posts and comments; nobody else does, and their votes aren't counted. Never used on people, since it deceives them.
- **Cleanup:** one action removes everything an account posted and withdraws its votes, correcting scores (the server can recompute an account's vote fingerprint with the secret; nothing new is stored).
- **Moderation log:** every action with its target, reason and time. A public `/modlog` lists actions without content or names ("post removed: spam, Oct 12"), so enforcement can be seen to be even.
- **Retention:** removed posts and comments keep their text for 30 days (for appeals and mistakes), then it's erased; handled reports are deleted after 30 days too.
- **Anti-spam layers:** probation for the first 3 days (no links, slower posting limits), duplicate detection with `content_hash`, and an admin-edited domain blocklist.
- **A `/rules` page** in the owner's voice, so removal reasons point at something.

Build order: (1) reports, the queue, removal reasons, the log and `/modlog` (built: section 7.19); (2) suspensions and appeals (built); (3) cleanup, vote withdrawal and spam shadowbans (built); (4) probation, duplicates and the blocklist (built); (5) the 30-day erasing of removed text and handled reports (built). Before images: research the US duties for reporting and preserving illegal images (NCMEC), ideally with a lawyer.

Anti-bot layers still to build into posting:

| Layer | Status |
|---|---|
| Per-account limits on posting, commenting, voting | Done |
| Voting probation (1 day) | Done |
| Full probation: no links, no media, slower limits for new accounts | Done (3 days; media when images exist) |
| Content checks: duplicate detection via `content_hash`, a domain blocklist | Done (link caps not needed yet) |
| Shadowbans, one-click cleanup | Done (moderation slice 3) |
| `rel="nofollow ugc"` on user links | Done (`nofollow` inside bodies, `nofollow ugc noopener` on post links) |
| Downvote-specific budget | Held in reserve |
| Vote ring detection | Not built; worth it only once vote manipulation is actually seen |

### 11.3 Security switches (build when needed or before launch)

**ALTCHA (proof-of-work on sign-up).** Self-hosted, privacy-first. Serve the widget's *external* build (component, worker, CSS as separate files from `static/`, so the strict CSP needs no `blob:` or inline styles), add `GET /altcha/challenge` (rate limited, no-store) using `github.com/altcha-org/altcha-lib-go`, place the widget in the sign-up form with auto-solve on page load, verify in `handleSignup` beside `checkForm`, and remember spent solutions in memory (replay protection). Match the widget version to the protocol the Go library supports (v1 hash-based vs v2 key-derivation). Needs HTTPS or localhost. Makes sign-up require JavaScript. About half a day.

**Anubis (proof-of-work gate for the whole site).** A separate reverse proxy: Caddy → Anubis → Snowball. Configuration only (compose service, Caddy upstream, policy file). Verify `clientIP` still sees the real visitor through the extra hop, and that Anubis's logs meet the no-IP standard. Blocks visitors without JavaScript. A few hours at deployment.

**Invite system.** The strongest defense. A config switch between open and invite-only; single-use invite codes expiring in 1 to 2 weeks; invites earned over time with a cap; an `invited_by` link kept only for a limited window (for example 90 days), since an invite tree is a social graph that could de-anonymize people; revoking invite rights from accounts that invite abusers.

**Members-only reading.** The switch for when the user base is strong: reading requires an account, per page type.

### 11.4 Production and deployment checklist

VPS with Docker Compose running Postgres, Snowball, and Caddy (automatic HTTPS). Set `DATABASE_URL` with real credentials, **`SNOWBALL_VOTE_SECRET` (32+ random characters, stable forever, backed up separately from the database)**, and `SNOWBALL_TRUST_PROXY=1`. HSTS via Caddy. Caddy access logs off or scrubbed of IPs. Nightly `pg_dump` backups shipped off the server (without the vote secret alongside). Embed templates and static files into the binary with Go's `embed`. Versioned static URLs with long caching, replacing `revalidate`. Styled 429 and 503 pages. Optionally Anubis. Optionally a trap link for crawlers that ignore `robots.txt`. Rewrite or remove the placeholder admin note.

**Performance settings (from section 10, "Performance").** Caddy: `encode zstd gzip` (pages compress about 12 times). Memory: enough for Postgres to cache the busy part of the database (`shared_buffers` about a quarter of RAM; the large seed is about 2 GB on disk); cold reads are many times slower than the benchmark numbers. Backups double as the undo for tag merges and deletes, which can't be reversed on the site. Re-run `cmd/bench` on the server itself once to see real numbers.

### 11.5 Smaller backlog items

| Item | Notes |
|---|---|
| `/changelog` | Partly covered by `/notes`. (`/about` and `/rules` exist as drafts for the owner to rewrite.) |
| Live password checklist | Rules tick off as the user types (snowball.js). |
| Spoiler tags | A small goldmark extension. |
| Welcome page refresh | Refreshing resubmits sign-up; consider a one-time server-side handoff. |
| Recovery code regeneration | Deliberately deferred; the owner leans against it. |
| Single-use form tokens | Currently reusable within 2 hours. |
| Comment paging | Threads load at most 2,000 comments today. The biggest seeded thread (2,400) takes 61 ms logged out and 96 ms logged in, nearly all template work for 2 to 3 MB of HTML; paging (Reddit shows about 200, then "more comments") would cut that and the page weight. |
| Hot ranking for more lists | Only "Popular today" uses it; a `/hot` list or tag-page option could follow. |
| Performance follow-ups | Done: the pass in section 10, "Performance". Left: comment paging for huge threads; `/top` (7 ms) and the history views would be next if they grow. |
| sqlc | Considered; on Windows its Postgres parser needs cgo, so run it via Docker if adopted. |
| More testing | A red-team bot to attack the defenses from outside; load testing before launch; browser tests if the JavaScript grows. |
| Pinned tool versions | staticcheck and govulncheck run `@latest`. If upstream surprises become a nuisance, pin them with Go's `tool` directive in go.mod (`go get -tool ...`) and run them with `go tool`. |

### 11.6 Polls: done, and ideas for later

Both steps are built (sections 7.22 and 7.23). Ideas, not planned: a weekly bracket (8 options narrowed over 7 days), polls tied to a tag, members suggesting mega poll questions into the queue, hiding pick tags on mega polls until they close (the owner chose to keep them visible).
