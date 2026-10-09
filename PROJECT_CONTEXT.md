# Snowball: Project Context

This document is the single place to understand Snowball: what it is, how it is built, what exists today and where it lives in the code, and what is planned. Someone who has never seen the project should be able to read it top to bottom and start working on tickets.

Last updated: after posting slices five and six (editing; the private history page and self-deletion) and the "up" navigation links. The posting chapter's last slice, images, waits for moderation (section 10.1).

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
| Language | Go 1.25 or newer | 1.25 is required for `http.NewCrossOriginProtection` (CSRF defense). |
| Web layer | Go standard library `net/http` | No framework. Method-and-path routing (`"GET /p/{id}"`). |
| Templates | Go `html/template` | Auto-escapes all output. Server-rendered HTML. |
| Interactivity | htmx 2.x (self-hosted) | In-place updates: username reroll, reply forms, vote arrows. |
| Site JavaScript | `static/js/snowball.js` | Small, plain JS. No inline scripts anywhere (CSP forbids them). |
| Markdown | `github.com/yuin/goldmark` + `github.com/microcosm-cc/bluemonday` | Render with HTML off, then sanitize against an allowlist. |
| Database | PostgreSQL 18 (Docker) | Accessed through `pgx` v5 with a connection pool. |
| Migrations | goose (CLI) | SQL files in `migrations/`, applied in order. |
| Password hashing | argon2id (`golang.org/x/crypto/argon2`) | Wrapped in `internal/auth`. |
| Unicode normalization | `golang.org/x/text/unicode/norm` | NFKC for passwords. |
| Rate limiting | `golang.org/x/time/rate` | Token buckets, in memory. |
| Local infrastructure | Docker Compose | Postgres only, for now. |
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
| Run tests | `go test ./...` (add `-v` for names, `-cover` for coverage) |
| Run the site | `go run ./cmd/snowball` then open http://localhost:8080 |
| Database shell | `docker compose exec db psql -U snowball` |
| Database shell (from any folder) | `docker exec -it snowball-db-1 psql -U snowball` |
| Stop Postgres | `docker compose down` |
| Wipe the database completely | `docker compose down -v` (destroys all data) |

Before every commit: `go fmt ./...`, `go vet ./...`, `go test ./...`, then `git status` to check `.env` is not staged and new migrations are.

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

### Testing tips

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
  internal/
    auth/                   Pure security logic. No HTTP, no database. Easy to test.
      passwords.go          Password rules, NFKC normalization, argon2id hashing, hashing semaphore, dummy verify.
      recovery.go           Recovery code generation, normalization, hashing.
      tokens.go             Session token generation and hashing.
      usernames.go          Word lists, username generation, NameSigner (signs offered names).
      formtokens.go         FormTokens: signed timestamps for anti-bot form checks.
      voters.go             VoterKeys: account ID -> vote fingerprint (HMAC with the vote secret).
    markdown/               Markdown -> sanitized HTML. The only code that returns template.HTML.
    store/                  The only package that talks to Postgres. All SQL lives here.
      store.go              Open (pool + ping + version check), Close.
      users.go              Users: availability, creation, credentials, password change, soft delete.
      sessions.go           Session create/lookup/delete, delete-all-for-user, expired cleanup.
      recovery.go           Account recovery transaction, remaining code count.
      posts.go              Tags list, creating posts (random public IDs), loading one post, admin removal.
      lists.go              Post lists with keyset pagination (new, active, top, hot), tag counts, post count.
      comments.go           Loading a post's comments, creating comments, admin removal.
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
      home.go               Name colors, sort bar data, front page, list pages, tag pages.
      posts.go              Submit page, post validation, post view preparation, post page, admin removal.
      comments.go           Comment trees (build, prune, sort, depth rules), Wilson score, reply forms, thread pages.
      votes.go              Vote arrows (widgets), who may vote, vote limits, the vote handler.
      edit.go               Edit pages and handlers, the edit-window helper, the edit limit.
      history.go            The private history page, deleting your own posts and comments.
      signup.go, login.go, recover.go, account.go   The account flows.
    (each package also has *_test.go files)
  migrations/               goose SQL migrations, numbered. Never edit one that has run; add a new one.
  templates/
    layout.html             Shared frame: head, masthead, account links, footer.
    pages/                  One file per page; each defines a "content" block.
    partials/               Reusable pieces, also returned alone as htmx fragments.
  static/
    css/style.css           The only stylesheet. All CSS goes here.
    js/htmx.min.js          Self-hosted htmx.
    js/snowball.js          Site behavior (hidden username toggle).
  docs/PROJECT_CONTEXT.md   This document.
  compose.yaml, .env.example, .gitignore, .gitattributes, README.md, go.mod, go.sum
```

Several files share names across folders (`recovery.go` in both `auth` and `store`; `signup.go` in `web` and `signup.html` in templates; `partials/post.html` is one row in a list, while `pages/postpage.html` is a whole post). Always check the folder.

---

## 5. Architecture

### Package boundaries

Dependencies flow one way: `cmd/snowball` uses `web` and `store`; `web` uses `store`, `auth`, and `markdown`; `store`, `auth`, and `markdown` depend on nothing internal. Go forbids import cycles, so this direction must be kept.

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
    id    integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name  text NOT NULL UNIQUE CHECK (name ~ '^[a-z0-9-]{1,24}$')
);  -- seeded: art, ask, books, diy, food, meta, music, outdoors, show, tech

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
| Deleted content is erased, not hidden: a deleted post has no body or link (title kept), a deleted comment has no body. | `deleted_post_is_erased`, `deleted_comment_is_erased`. |
| Codes, sessions, tags links, comments, and votes never outlive their parent row. | `ON DELETE CASCADE` (only fires on hard deletes; soft deletes clean up explicitly). |

Rules enforced in Go rather than the database: 1 to 3 tags per post (a CHECK can't count rows), comment depth = parent depth + 1, no voting on your own content. The edit window and authorship are enforced inside the edit `UPDATE` statements themselves.

### Deliberately not stored

Email, IP addresses, user agents, device names, locations, last-login time, login history, recovery timestamps (used codes are deleted), which account cast a vote (only a keyed fingerprint), when a vote was cast, and anything identifying in logs.

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
| GET `/t/{tag}` | `handleTag` (home.go) | | Unknown tags 404. |
| GET `/tags` | `handleTags` (home.go) | | All tags with counts. |
| GET `/robots.txt` | `robotsTxt` (headers.go) | | `Disallow: /`. |
| GET `/submit`, POST `/submit` | `handleSubmitPage`, `handleSubmit` (posts.go) | noStore; POST: limit `submit` | Login required. |
| GET `/p/{id}` | `handlePost` (posts.go) | | Post, comments, `?sort=best|new|old`. |
| POST `/p/{id}/remove` | `handleRemovePost` (posts.go) | noStore | Admin only; others get 404. |
| GET/POST `/p/{id}/edit` | `handleEditPostPage`, `handleEditPost` (edit.go) | noStore; POST: limit `submit` | Author only (others 404). Text and tags. |
| POST `/p/{id}/delete` | `handleDeletePost` (history.go) | limit `submit`, noStore | Author only. Erases; returns to `back`. |
| POST `/p/{id}/comment` | `handleComment` (comments.go) | limit `comment`, noStore | Top-level or reply (`parent` field). |
| GET `/p/{id}/c/{cid}/reply` | `handleReplyForm` (comments.go) | noStore | htmx: form fragment; otherwise a page. |
| GET `/p/{id}/c/{cid}` | `handleCommentThread` (comments.go) | | "Continue this thread". |
| POST `/p/{id}/c/{cid}/remove` | `handleRemoveComment` (comments.go) | noStore | Admin only; others get 404. |
| GET/POST `/p/{id}/c/{cid}/edit` | `handleEditCommentForm`, `handleEditComment` (edit.go) | noStore; POST: limit `comment` | Author only. htmx: inline form and swap; otherwise a page. |
| POST `/p/{id}/c/{cid}/delete` | `handleDeleteComment` (history.go) | limit `comment`, noStore | Author only. Erases; returns to `back`. |
| POST `/vote` | `handleVote` (votes.go) | limit `vote`, noStore | htmx: arrows fragment; otherwise redirect to `back`. |
| GET/POST `/signup`, GET `/signup/name` | signup.go | limits `names`/`signup`, noStore | |
| GET/POST `/login`, POST `/logout` | login.go | POST login: limit `login`, noStore | Logout deletes all sessions. |
| GET `/account`, POST `/account/password`, POST `/account/delete`, GET `/goodbye` | account.go | password/delete: limit `login`, noStore | |
| GET `/account/history` | `handleHistory` (history.go) | noStore | Private. `?show=posts|comments`, `&after=<cursor>`. |
| GET/POST `/recover`, GET `/recovered` | recover.go | POST: limit `recover`, noStore | |
| GET `/static/...` | Go file server | revalidate | |

Linked but not built yet (404): `/about`. Planned: `/rules`, `/changelog`. There is no public profile page listing a user's history (section 8); the only history view is the owner's private one.

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

`PasswordProblems(password, username)` returns phrases that read after "Your password needs to ...": 6 to 128 characters (counted in characters), an uppercase letter, a digit, a symbol, not a common word dressed up with digits/symbols (trims non-letters from the ends, undoes leetspeak, checks a short list), and not containing the username. Passwords are NFKC-normalized before rules, hashing, and verification. Hashes are argon2id PHC strings (`m=19456,t=2,p=1`, 16-byte salt, 32-byte key) with parameters stored per hash. At most 4 hashes run at once; a request waits up to 10 seconds for a slot, otherwise `auth.ErrBusy` becomes a 503 with `Retry-After: 5`.

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

To add a form: add a `formRule`, a token field set via `s.formToken(rule)` wherever the page is built, the `formguard` partial inside the `<form>`, and a `checkForm` call in its POST handler.

### 7.10 Posts (web/posts.go, store/posts.go, internal/markdown)

**Submitting** (`/submit`, login required): a title (1 to 200 characters), an optional link (complete `http(s)://` URLs only, up to 2000), optional Markdown text (up to 40,000), and 1 to 3 tags from the curated list. Everything typed is kept when the form is shown again with errors. Validation, then the per-account `posting` limit, then `store.CreatePost` inserts the post and its tags in one transaction with a random 8-character public ID (retried on the astronomically unlikely collision), and a content hash for future duplicate detection. Redirects to the new post.

**Public IDs** are random (`a-z2-7`, 40 bits) so nobody can walk `/p/1, /p/2...` to scrape everything or gauge site activity. URL patterns are validated before any database lookup.

**Markdown** (`markdown.Render`): goldmark with raw HTML off, then bluemonday with an allowlist (`p, br, strong, em, del, ul, ol, li, blockquote, code, pre, hr, a[href]`). No headings (stripped, text kept), no images (an external image would leak every reader's IP to its host), only `http`/`https` links, always `rel="nofollow"`. Rendered on every display, so tightening the rules fixes old posts too. This is the only place in Snowball that returns `template.HTML`, and its tests pin down that scripts, raw HTML, `javascript:` links, images, iframes, headings, and style attributes can't get through. A post's own link gets `rel="nofollow ugc noopener"`. Spoiler tags need a small custom extension (backlog).

**The post page** (`/p/{id}`): title (linked for link posts, with the domain), vote arrows, time, author (or "[deleted]"), tags, "edited" marker, rendered body, then comments.

**Admin removal**: `POST /p/{id}/remove` sets `removed_at`. Removed posts vanish from every list and count, show "[removed]" to everyone else, take no new comments or votes, and stay fully visible to the admin with a banner. The endpoint answers non-admins with a 404 so it doesn't admit it exists.

### 7.11 Lists, tags, and ranking (web/home.go, store/lists.go)

`store.ListPosts` serves every list with options: sort, optional tag, cursor, limit. Removed and deleted posts never appear. Each list fetches one more post than it shows to know whether another page exists.

| Sort | Order | Window | Used by |
|---|---|---|---|
| new | `created_at` | all | `/new`, tag pages |
| active | `last_activity_at` (bumped by comments) | all | front page (20), `/active` |
| top | `score` | last 7 days | `/top` |
| popular | "hot": `sign(score)·log10(max(|score|,1)) + age_seconds/45000` | last 24 hours | front page "Popular today" (3, not paginated) |

The hot formula is Reddit's: score counts logarithmically (1 to 10 points matters as much as 10 to 100), and 12.5 hours of age is worth a tenfold change in score, so a post taking off now outranks an older one that already peaked.

**Keyset pagination**: "older posts" links carry `?after=<value>.<id>` (the last post's sort key, as microseconds or score, plus its ID to break ties). The next page queries `(key, id) < (cursor)`: fast at any size, and new posts arriving never cause skips or repeats. A malformed cursor redirects to the first page. Later pages offer "back to the start".

Partials shared by all list pages: `sortnav` (the sort bar, current sort in bold, "new post" button for logged-in users) and `sidebar` (About, Tags with counts, House rules). Rank numbers come from a CSS counter and appear only on the ranked "Popular today" list (`.posts.unranked` hides them elsewhere). The footer's post count is cached in memory (refreshed every minute, bumped immediately on new posts, refreshed on removals).

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

**Display** (`partials/votes.html`, `voteWidget`): up arrow, score, down arrow. Stacked in lists; inline (horizontal) on the post page and in each comment's action row. Your own vote is highlighted (blue up, red down). Arrows link to login when logged out, and are faded with a hover reason when voting isn't possible ("This one's yours.", "New accounts can vote after their first day.", "Voting is closed on this."). With htmx, a click swaps just the widget; notes from refused votes appear under inline arrows, or in a small yellow box beside stacked ones. Without JavaScript, the form submits and returns to the `back` path, which `safeBack` restricts to paths on Snowball (no open redirects).

### 7.14 Editing (web/edit.go, store/edits.go)

Authors can edit for **10 minutes** (`store.EditWindow`). Edits within the first **2 minutes** (`store.EditGrace`) are silent; later ones set `edited_at`, shown as "edited". After 10 minutes the content is final. No revisions are kept.

**Posts:** text and tags only. The **title and link can never be edited** (Reddit's lesson: swapping a link after a post collects votes is a classic spam trick, and changing a title changes what people voted on). The edit page (`/p/{id}/edit`) shows them read-only with a note explaining why, and reuses the shared `tagpicker` partial. **Comments:** text only. With htmx, "edit" swaps the comment's text for a textarea holding the original Markdown; saving swaps the new text back and updates the "edited" marker in the comment's header line via an **out-of-band swap** (the response carries a `<span id="ce-<id>" hx-swap-oob="true">` that htmx puts in place of the matching element). Without JavaScript, editing happens on a small page of its own. "Cancel" just links back to the comment.

The **database clock decides**: `EditPost` and `EditComment` are single `UPDATE`s that only match when the editor is the author, the item isn't removed or deleted, and `created_at > now() - window`; a `CASE` sets `edited_at` only after the grace period. A form held open past the deadline gets "The 10-minute editing window has closed". The window and grace are Go constants passed in as seconds, so the "N minutes left" shown on pages (`editTimeLeft`, `canEdit`) can't drift from the rule. Edit links appear only for the author within the window. Edits re-run the same validation as posting, update the content hash, use the form guard and the `editing` limit, and **don't bump** `last_activity_at` (otherwise re-saving would keep a post on top).

### 7.15 Private history and self-deletion (web/history.go, store/deletes.go)

`/account/history` lists the viewer's own posts or comments (tabs), newest first, 25 per page with keyset pagination by creation time. Items the admin removed appear labeled "removed by the admin" (only to their author); items the author deleted don't appear. Comment entries show a plain-text snippet (`snippet`, first 160 characters).

**Deleting** is offered on each entry as a `<details>` element: "delete" opens a confirmation explaining what happens, and only the red button inside deletes. No JavaScript, and one stray click can't delete anything.

**Deletion erases, it doesn't hide.** `DeletePost` overwrites the body and link (title kept, Reddit's choice, so the discussion under it still makes sense) and sets `deleted_at`. `DeleteComment` overwrites the body and decrements the post's visible comment count. The database enforces that deleted rows are empty (migration 00009), so no bug can leave deleted text behind, and there is no undelete. The rows themselves remain (so threads keep their shape) and still record their author: breaking that link was considered and set aside because it would hinder moderation, such as cleaning up after a spammer. Deleted content gets an empty `content_hash`.

**How deleted content shows:** a deleted post drops out of every list and the post count; its page still loads with its title, "[deleted]" as the author, a note that the author deleted it, and its comments intact. It takes no new comments or votes. A deleted comment with replies shows as "[deleted]" with the replies kept; without replies, it disappears.

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

**Navigation.** The logo goes home. Every page with a parent shows a quiet `← label` up link under the yellow bar (section 5, Rendering): posts, account, submit, tags, login, sign-up, goodbye, and recovered go to the front page; tag pages to all tags; history to the account; reply, edit, and thread pages to their post; recovery to login. The front page and sort lists have none (the sort bar covers them), and the welcome page deliberately has none, so its only way on is "I've saved them".

**Hidden username.** The logged-in header shows eight dots plus a "show name" checkbox (shoulder-surfing protection), a boxed **account** button, and log out. snowball.js wires every `.reveal-group` (header and account page), resetting to hidden on `pageshow`.

**Header and front page.** Masthead with hand-drawn SVG snowball, random tagline, account area (wraps neatly on phones). Front page: sort bar, an admin note (a static, dated placeholder in `home.html`, written during development rather than by the owner; rewrite or remove it until real admin notes exist), "Popular today", "Active right now", sidebar, footer with the owner's manifesto, render time, and post count.

**Copy voice.** Direct, human, slightly raw. Copy changes are the owner's call.

---

## 8. Product decisions log

| Decision | Reasoning |
|---|---|
| Generated names, no choosing, no renaming | Anonymity; accountability for moderation; reputation can't be shed. |
| One name per account, shown everywhere | Accounts can have personality and history without identifying a person. |
| No email; recovery codes instead | No personal data. Losing password and codes means losing the account, and users must acknowledge this. |
| Tags instead of boards | One shared room is easier to moderate and livelier when small. 1 to 3 tags per post from a curated list; new tags suggested under `meta`. |
| Front page is a hybrid | "Popular today" (hot ranking) plus bump-ordered "active" conversations. |
| Every logout logs out everywhere; password change too | Fits the "leave no trace" character. |
| Deleted names are never reused | Prevents a newcomer inheriting someone else's reputation. Soft delete. |
| Rerolls limited per IP (10, refilling) | Nudges people to consider each name. |
| Password minimum 6 with composition rules | Owner's choice (below the usual 8+), mitigated by rate limits, the login guard, and weak-password checks. |
| No third-party CAPTCHA or tracking | Would contradict the privacy promise. All defenses are self-hosted. |
| Discussions public for now; members-only later | A young forum needs visitors to see what they'd join. Built so it can be switched later. |
| Out of search engines from day one | `robots.txt`, `noindex` header and meta tag. |
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
| When in doubt, learn from Reddit's battles | Twenty years of fighting spam and manipulation behind its odd-looking rules. |

---

## 9. Conventions and gotchas

Restart the server after template changes; CSS/JS changes only need a reload. All CSS goes in `static/css/style.css`: other CSS files are never loaded (snippets must be pasted in, not saved alongside). Template comments `{{/* */}}` never reach the browser; HTML comments `<!-- -->` are sent to every visitor. Never edit a migration that has run anywhere important; add a new numbered one. Run `go mod tidy` after adding or removing imports (silence means success). In psql, statements end with `;` (a `-#` prompt means it's waiting; Ctrl+C clears). Folder = package in Go: exported names start with a capital letter. Lowercase all file and folder names (Windows is case-insensitive, Linux isn't). When CSS seems ignored, check the rule appears in the dev tools Styles panel; if absent, it isn't in the served stylesheet. Tests use table-driven style and inject time rather than sleeping. Go templates don't convert number types: a function taking `int64` can't receive an `int` (why `TagCount.Count` is `int64`). Functions that end with identical lines (like `ChangePassword` and `DeleteAccount`) are easy to edit by mistake; check which function an edit landed in. Deleted posts and comments have an empty `content_hash`, so duplicate detection must ignore empty hashes. To test the edit window without waiting, move an item back in time: `UPDATE posts SET created_at = now() - interval '3 minutes' WHERE public_id = '...';`.

**Test coverage today**: `auth` (hashing, damaged hashes, normalization, password rules, busy semaphore, form tokens, voter fingerprints), `markdown` (allowed formatting, blocked dangerous input), `store` (cursor parsing, vote toggling; pure functions only), `web` (clientIP, limiters including forgetting and status, login guard, durations, post validation, tag picking, number formatting, Wilson scores, comment trees and depth rules, safe return paths, who may vote, the edit window and who may edit, history snippets). Handlers and database queries have no tests yet.

---

## 10. Roadmap and backlog

### 10.1 Posting chapter: status and next slices

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

**Moderation chapter (proposed next).** Built so far: the admin can remove posts and comments. To decide and build: reports (users flag a post or comment with a reason), an admin queue to review them, bans (and what happens to a banned account's content), shadowbans, and one-click cleanup of an account and everything it posted.

Anti-bot layers still to build into posting:

| Layer | Status |
|---|---|
| Per-account limits on posting, commenting, voting | Done |
| Voting probation (1 day) | Done |
| Full probation: no links, no media, slower limits for new accounts | Planned |
| Content checks: link caps, duplicate detection via `content_hash`, a domain blocklist | Planned |
| Shadowbans, one-click cleanup | With moderation |
| `rel="nofollow ugc"` on user links | Done (`nofollow` inside bodies, `nofollow ugc noopener` on post links) |
| Downvote-specific budget | Held in reserve |
| Vote ring detection | With moderation |

### 10.2 Security switches (build when needed or before launch)

**ALTCHA (proof-of-work on sign-up).** Self-hosted, privacy-first. Serve the widget's *external* build (component, worker, CSS as separate files from `static/`, so the strict CSP needs no `blob:` or inline styles), add `GET /altcha/challenge` (rate limited, no-store) using `github.com/altcha-org/altcha-lib-go`, place the widget in the sign-up form with auto-solve on page load, verify in `handleSignup` beside `checkForm`, and remember spent solutions in memory (replay protection). Match the widget version to the protocol the Go library supports (v1 hash-based vs v2 key-derivation). Needs HTTPS or localhost. Makes sign-up require JavaScript. About half a day.

**Anubis (proof-of-work gate for the whole site).** A separate reverse proxy: Caddy → Anubis → Snowball. Configuration only (compose service, Caddy upstream, policy file). Verify `clientIP` still sees the real visitor through the extra hop, and that Anubis's logs meet the no-IP standard. Blocks visitors without JavaScript. A few hours at deployment.

**Invite system.** The strongest defense. A config switch between open and invite-only; single-use invite codes expiring in 1 to 2 weeks; invites earned over time with a cap; an `invited_by` link kept only for a limited window (for example 90 days), since an invite tree is a social graph that could de-anonymize people; revoking invite rights from accounts that invite abusers.

**Members-only reading.** The switch for when the user base is strong: reading requires an account, per page type.

### 10.3 Production and deployment checklist

VPS with Docker Compose running Postgres, Snowball, and Caddy (automatic HTTPS). Set `DATABASE_URL` with real credentials, **`SNOWBALL_VOTE_SECRET` (32+ random characters, stable forever, backed up separately from the database)**, and `SNOWBALL_TRUST_PROXY=1`. HSTS via Caddy. Caddy access logs off or scrubbed of IPs. Nightly `pg_dump` backups shipped off the server (without the vote secret alongside). Embed templates and static files into the binary with Go's `embed`. Versioned static URLs with long caching, replacing `revalidate`. Styled 429 and 503 pages. Optionally Anubis. Optionally a trap link for crawlers that ignore `robots.txt`. Rewrite or remove the placeholder admin note.

### 10.4 Smaller backlog items

| Item | Notes |
|---|---|
| `/about`, `/rules`, `/changelog` | In the owner's voice; changelog shows the site growing. |
| Admin notes | Real notes to replace the static placeholder on the front page. |
| Reply notifications | "Someone replied to you", on-site only, no email. |
| Live password checklist | Rules tick off as the user types (snowball.js). |
| Spoiler tags | A small goldmark extension. |
| Welcome page refresh | Refreshing resubmits sign-up; consider a one-time server-side handoff. |
| Recovery code regeneration | Deliberately deferred; the owner leans against it. |
| Single-use form tokens | Currently reusable within 2 hours. |
| Comment paging | Threads over 2,000 comments are truncated today. |
| Delete from the post page | Deleting is offered only on the history page today; a delete control beside "edit" on your own posts and comments would be a small addition. |
| Hot ranking for more lists | Only "Popular today" uses it; a `/hot` list or tag-page option could follow. |
| sqlc | Considered; on Windows its Postgres parser needs cgo, so run it via Docker if adopted. |
| Tests | Store tests against a test Postgres (including account deletion unlinking votes), handler tests, and a red-team bot to attack the defenses. |
