# Snowball: Project Context

This document is the single place to understand Snowball: what it is, how it is built, what exists today and where it lives in the code, and what is planned. Someone who has never seen the project should be able to read it top to bottom and start working on tickets.

Last updated: end of the authentication chapter (sign-up, login, recovery, account management, rate limiting, anti-bot form guards), plus the posting design decisions (section 10.1). Posting has not been built yet; the front page shows fake data.

---

## 1. What Snowball is

Snowball is a small, anonymous, Reddit-style forum, run by one person. Four values drive nearly every decision, and a ticket that conflicts with one of them should be questioned before it is built.

**Anonymity.** Accounts are not tied to real identities. There is no email, no real name, and users cannot choose their username: the site generates one (like `blue-humming-otter`). Account history exists, but it never traces back to a person.

**Store as little as possible.** No email addresses, no IP addresses, no user agents, no device lists, no last-login times, no tracking, no analytics, no ads. Anything not stored can never leak. Rate limiting uses IPs only in memory, briefly, and never writes them anywhere. When a feature seems to need personal data, the default answer is to find a design that doesn't.

**Manageable by one person.** Moderation surface and operational load are kept small. That is why there are tags instead of per-community boards, and why defenses are automated.

**Hand-built, old-web character.** The site should feel like something a person made: dense, text-first, honest browser defaults (underlined links, visited colors), one deliberate accent color, informative footer, copy written in the owner's voice. It is not a retro costume; it is modern, responsive, and fast.

The project is also a learning vehicle for in-demand skills (Go, PostgreSQL, Docker, security engineering), so clarity and good practice in the code matter as much as features.

---

## 2. Tech stack

| Layer | Choice | Notes |
|---|---|---|
| Language | Go 1.25 or newer | 1.25 is required for `http.NewCrossOriginProtection` (CSRF defense). |
| Web layer | Go standard library `net/http` | No framework. Method-and-path routing (`"GET /login"`). |
| Templates | Go `html/template` | Auto-escapes all output. Server-rendered HTML. |
| Interactivity | htmx 2.x (self-hosted) | Used for in-place updates (the username reroll). |
| Site JavaScript | `static/js/snowball.js` | Small, plain JS. No inline scripts anywhere (CSP forbids them). |
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

Go 1.25+, Docker Desktop (on Windows, with WSL2), and the goose CLI:

```
go install github.com/pressly/goose/v3/cmd/goose@latest
```

### Everyday commands (run from the project root)

| Task | Command |
|---|---|
| Start Postgres | `docker compose up -d` |
| Check Postgres is healthy | `docker compose ps` |
| Apply migrations | `goose up` |
| See migration status | `goose status` |
| Undo the latest migration | `goose down` |
| Sync dependencies | `go mod tidy` (prints nothing on success) |
| Check code | `go vet ./...` |
| Run tests | `go test ./...` (add `-v` for names, `-cover` for coverage) |
| Run the site | `go run ./cmd/snowball` then open http://localhost:8080 |
| Database shell | `docker compose exec db psql -U snowball` |
| Database shell (from any folder) | `docker exec -it snowball-db-1 psql -U snowball` |
| Stop Postgres | `docker compose down` |
| Wipe the database completely | `docker compose down -v` (destroys all data) |

The server must be started from the project root, because templates and static files are found relative to the current folder.

### Configuration

`.env` (never committed; listed in `.gitignore`) holds local development settings. Docker Compose and goose both read it automatically.

| Variable | Used by | Purpose |
|---|---|---|
| `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` | Compose | Creates the database user and database on first start. Dev values: `snowball` / `snowball-dev-only` / `snowball`. |
| `GOOSE_DRIVER`, `GOOSE_DBSTRING`, `GOOSE_MIGRATION_DIR` | goose | Where migrations run. |

Environment variables read by the Go app (not from `.env`; set in the shell or deployment):

| Variable | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | Local dev URL (see `devDatabaseURL` in `cmd/snowball/main.go`) | Postgres connection string. The dev fallback is logged when used. |
| `SNOWBALL_TRUST_PROXY` | unset | Set to `1` only when running behind Caddy (or another reverse proxy). Makes the app read the visitor IP from the last `X-Forwarded-For` entry. Never set without a proxy. |

### Docker notes

`compose.yaml` runs `postgres:18`, binds port `5432` to `127.0.0.1` only (not the whole network), stores data in the named volume `pgdata` mounted at `/var/lib/postgresql` (the location Postgres 18 images expect), and has a `pg_isready` healthcheck. Compose names projects after the folder they run in; adding `name: snowball` as the first line of `compose.yaml` keeps the project, container (`snowball-db-1`), and volume names stable regardless of folder name.

---

## 4. Repository layout

```
snowball/
  cmd/snowball/main.go      Wiring only: config, DB, server, timeouts, shutdown, session cleanup loop.
  internal/
    auth/                   Pure security logic. No HTTP, no database. Easy to test.
      doc.go                Package overview.
      passwords.go          Password rules, NFKC normalization, argon2id hashing, hashing semaphore, dummy verify.
      recovery.go           Recovery code generation, normalization, hashing.
      tokens.go             Session token generation and hashing.
      usernames.go          Word lists, username generation, NameSigner (HMAC signing of offered names).
      formtokens.go         FormTokens: signed timestamps for anti-bot form checks.
      *_test.go             Unit tests.
    store/                  The only package that talks to Postgres. All SQL lives here.
      store.go              Open (pool + ping + version check), Close.
      users.go              Users: availability, creation, credentials, password change, soft delete.
      sessions.go           Session create/lookup/delete, delete-all-for-user, expired cleanup.
      recovery.go           Account recovery transaction, remaining code count.
    web/                    Everything HTTP: server, routes, middleware, handlers, rendering.
      server.go             Server struct, Config, New, routes and middleware stack, rate limit settings.
      render.go             Template loading, basePage, render/renderFragment, serverError, hashError, taglines.
      session.go            Session cookie, withUser middleware, startSession, userFrom.
      headers.go            Security headers middleware, noStore, revalidate.
      ratelimit.go          Token-bucket limiter, clientIP, limit wrapper, 429 responses.
      loginguard.go         Per-account failed-login slowdown.
      formguard.go          Honeypot + signed timing token checks for every form.
      home.go               Front page handler, Post type, name color classes, FAKE sample data.
      signup.go             Sign-up page, reroll, offer cookie, account creation.
      login.go              Login, logout (all sessions).
      recover.go            Account recovery with a code.
      account.go            Account page: change password, delete account, goodbye page.
      *_test.go             Unit tests.
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
  compose.yaml, .env, .gitignore, go.mod, go.sum
```

Several files share names across packages (for example `recovery.go` exists in both `auth` and `store`; `signup.go` in `web` and `signup.html` in templates). Always check the folder.

---

## 5. Architecture

### Package boundaries

Dependencies flow one way: `cmd/snowball` uses `web` and `store`; `web` uses `store` and `auth`; `store` and `auth` depend on nothing internal. Go forbids import cycles, so this direction must be kept.

`auth` never sees a request or a database. `store` never sees a raw secret: it receives only hashes (password hashes, token hashes, recovery code hashes). `web` translates between the two and owns everything user-facing.

### Request lifecycle

A request passes through the middleware stack, outermost first, then reaches its handler:

1. `securityHeaders` (web/headers.go) stamps security headers on every response, including rejected ones.
2. `http.NewCrossOriginProtection` rejects cross-site POSTs (CSRF), using `Sec-Fetch-Site`/`Origin` headers.
3. `withUser` (web/session.go) reads the session cookie, looks up the session, and attaches the user to the request context. Skipped for `/static/`. Logged-in responses get `Cache-Control: no-store`.
4. The mux routes to a handler, possibly through per-route wrappers: `limit` (rate limiting) and `noStore`.
5. The handler gathers data (from `store`), builds a page struct, and calls `render`.

Handlers find the current user with `userFrom(r.Context())`, which returns a `store.User{ID, Username}` and a boolean.

### Rendering

At startup, `loadPages` builds one template set per page: `layout.html` + all `partials/*.html` + that one page. Sets are separate because every page defines a block called `content`. Templates are parsed once, so **template changes need a server restart**.

`render(w, "page.html", data)` executes the `layout` template. `renderFragment(w, "page.html", "partialName", data)` executes a single partial, which is how htmx endpoints respond (for example the reroll returns only the `username` partial). Both render into a buffer first, so a template error produces a clean 500 instead of half a page.

Every page struct embeds `basePage`, which holds what the layout needs: `Title`, `Tagline` (random from `taglines` in render.go), `User` (empty when logged out), `HideAccount` (hides the header account links on focused pages), `PostCount` (currently fake), and `Generated` (time spent building the data, shown in the footer).

Templates follow a rule: handlers do the mapping (database rows into structs, decisions, calculations), templates only display. Small derived values live as methods on structs (for example `Post.Link()`, `Post.AuthorColorClass()`).

### Static files

Served from `static/` under `/static/` by Go's file server, with `Cache-Control: no-cache` (the browser keeps a copy but revalidates each time, getting a cheap 304 when unchanged). This means CSS/JS edits appear on the next reload with no restart.

### Server settings (cmd/snowball/main.go)

`http.Server` with timeouts (ReadHeader 5s, Read 15s, Write 30s, Idle 60s) and 64 KB max headers, protecting against slow-client attacks. Graceful shutdown on Ctrl+C or SIGTERM gives in-flight requests up to 10 seconds. A background goroutine deletes expired sessions at startup and hourly.

### Secrets generated at startup

Two random HMAC keys are created each time the server starts: the `NameSigner` key (signs offered usernames) and the `FormTokens` key (signs form timestamps). A restart therefore invalidates offered names and open forms. This is harmless by design (visitors get a fresh name or a "form expired" message), but it means multiple app instances could not share these without a shared key.

---

## 6. Database schema

The current schema, after all migrations (00001 to 00005):

```sql
CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      text NOT NULL UNIQUE
                  CONSTRAINT username_format CHECK (username ~ '^[a-z]+-[a-z]+-[a-z]+$'),
    password_hash text,                       -- argon2id PHC string; NULL only when deleted
    created_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz,                -- set when the account is soft-deleted
    CONSTRAINT password_only_while_active
        CHECK ((deleted_at IS NULL) = (password_hash IS NOT NULL))
);

CREATE TABLE recovery_codes (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  text NOT NULL                  -- SHA-256 hex of the normalized code
);
CREATE INDEX recovery_codes_user_id_idx ON recovery_codes (user_id);

CREATE TABLE sessions (
    token_hash  text PRIMARY KEY,             -- SHA-256 hex of the cookie token
    user_id     bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
```

goose also maintains its own `goose_db_version` table.

### Invariants enforced by the database

| Rule | Enforced by |
|---|---|
| Usernames are unique, forever (including deleted accounts). | `UNIQUE` on `users.username`; deleted rows are kept. |
| Usernames are lowercase `word-word-word`. | `username_format` check. |
| A live account always has a password; a deleted one never does. | `password_only_while_active` check. |
| Codes and sessions never outlive their user row. | `ON DELETE CASCADE` (only fires on a hard delete; soft delete removes them explicitly). |

### Deliberately not stored

Email, IP addresses, user agents, device names, locations, last-login time, login history, recovery timestamps (used codes are deleted, not timestamped), and anything identifying in logs.

### Migration history

| File | Change |
|---|---|
| 00001_create_users.sql | `users` table with username format check. |
| 00002_create_recovery_codes.sql | `recovery_codes` with `used_at` and user index. |
| 00003_create_sessions.sql | `sessions` table and user index. |
| 00004_drop_recovery_used_at.sql | Used codes are deleted instead of timestamped. |
| 00005_soft_delete_users.sql | `deleted_at`, nullable `password_hash`, `password_only_while_active`. Its Down fails if deleted accounts exist. |

### Query conventions

All queries use `$1`-style placeholders, never string building (no SQL injection). Multi-step writes use transactions with `defer tx.Rollback(ctx)` and an explicit `Commit`. Unique violations are detected by Postgres error code `23505`. Queries that must exclude deleted accounts include `deleted_at IS NULL` (credentials, session lookup, recovery, password change).

---

## 7. Features: what exists and where

### 7.1 Routes

| Method and path | Handler (web/) | Wrappers | Notes |
|---|---|---|---|
| GET `/` | `handleHome` (home.go) | | Front page. FAKE data. |
| GET `/robots.txt` | `robotsTxt` (headers.go) | | Asks all crawlers to stay out (`Disallow: /`). |
| GET `/signup` | `handleSignupPage` (signup.go) | limit `names`, noStore | Redirects home if logged in. `?reroll=1` = no-JS reroll. |
| GET `/signup/name` | `handleRerollName` (signup.go) | limit `names`, noStore | htmx: returns the `username` partial only. |
| POST `/signup` | `handleSignup` (signup.go) | limit `signup`, noStore | Creates the account; renders `welcome.html` with codes. |
| GET `/login` | `handleLoginPage` (login.go) | noStore | `?notice=password-changed` shows a fixed notice. |
| POST `/login` | `handleLogin` (login.go) | limit `login`, noStore | Redirects to `/` on success. |
| POST `/logout` | `handleLogout` (login.go) | noStore | Deletes **all** of the user's sessions. |
| GET `/account` | `handleAccount` (account.go) | noStore | Requires login. |
| POST `/account/password` | `handleChangePassword` (account.go) | limit `login`, noStore | Logs out everywhere; redirects to `/login?notice=password-changed`. |
| POST `/account/delete` | `handleDeleteAccount` (account.go) | limit `login`, noStore | Soft delete; redirects to `/goodbye`. |
| GET `/goodbye` | `handleGoodbye` (account.go) | noStore | |
| GET `/recover` | `handleRecoverPage` (recover.go) | noStore | |
| POST `/recover` | `handleRecover` (recover.go) | limit `recover`, noStore | Logs in and redirects to `/recovered`. |
| GET `/recovered` | `handleRecovered` (recover.go) | noStore | Shows remaining code count. |
| GET `/static/...` | Go file server | revalidate | |

Links exist in templates for pages not built yet (they 404 or show the same fake front page): `/active`, `/new`, `/top`, `/t/{tag}`, `/tags`, `/p/{id}`, `/about`, and pagination via `/?page=2`. Also planned but not yet linked: `/submit`, a private post history page under `/account`, `/rules`, `/changelog`. There will be no public profile page listing a user's history (see section 10.1).

### 7.2 Sign-up flow

1. **Name offer.** The server generates a `color-verb-animal` name (auth/usernames.go: 8 colors, 80 verbs, 150 animals, 96,000 combinations), checking the database so an offered name is never taken. Word lists are hand-curated to avoid offensive or slur-adjacent combinations; review any new word against every color. Names are lowercase and hyphenated.
2. **Signing.** Each offered name is HMAC-signed (`NameSigner`) and the signature travels in a hidden `sig` field, so a user cannot edit the form to pick their own name.
3. **Remembered offer.** The current offer is kept in the `__Secure-snowball_offer` cookie (Path `/signup`, 1 hour, value `name.sig`), so refreshing does not reroll. Re-validated on use.
4. **Reroll allowance.** Rerolls spend from a per-IP bucket (`rerolls` limiter: 10, one more every 6 minutes). The count shows beside the name ("N left"). When empty, the current name is returned with "More rerolls in X". Signing up is never blocked by this. Clearing cookies gets a new starting name; this is a nudge, not a security control.
5. **Form fields.** Password twice, two required acknowledgment checkboxes (`ack_recovery`, `ack_username`), plus the form guard (honeypot + timing token).
6. **Validation order.** Form guard, name signature, password rules, confirmation match, checkboxes (all enforced server-side as well as in the browser), then the **account-creation limit** (`accounts`: 3, then one per 20 minutes per IP, checked only when everything else passed so typos don't consume it).
7. **Creation.** Password hashed with argon2id; 8 recovery codes generated; user row and code hashes inserted in one transaction (`store.CreateUser`). A unique violation (name taken in a race) produces a fresh name and a message.
8. **Welcome page.** Shows the codes once, with `Cache-Control: no-store`. The user then logs in. (Refreshing it resubmits the form; a known, harmless limitation.)

### 7.3 Login and logout

Login lowercases and trims the name, checks the form guard, then the per-account login guard, then verifies the password. Nonexistent names still run a full argon2 check against a dummy hash (`auth.VerifyDummy`) so timing does not reveal whether a name exists, and both failure cases show the same message. Success creates a new session (new token every time) and redirects (Post/Redirect/Get).

Logout is a POST form (a link could be triggered cross-site). **Every logout deletes all of the account's sessions on every device.** This is a deliberate product decision matching the "leave no trace" feel.

### 7.4 Sessions and cookies

| Cookie | Contents | Path | Lifetime | Flags |
|---|---|---|---|---|
| `__Host-snowball_session` | 32 random bytes, base64url | `/` | 30 days, absolute | HttpOnly, Secure, SameSite=Lax |
| `__Secure-snowball_offer` | `name.signature` | `/signup` | 1 hour | HttpOnly, Secure, SameSite=Lax |

Only the SHA-256 hash of the session token is stored (`sessions.token_hash`), so a database leak cannot be used to log in. Expired sessions are rejected at lookup and deleted by the hourly cleanup. Cookie prefixes are browser-enforced; `Secure` cookies work on `http://localhost` in Chrome, Edge, and Firefox (Safari does not allow this on localhost, so test elsewhere). Renaming a cookie logs everyone out once.

### 7.5 Account recovery

Recovery codes: 8 per account, format `XXXX-XXXX-XXXX` (base32, no 0/1/8/9, 60 bits each), stored as SHA-256 of the normalized code (uppercase, dashes and spaces removed). A fast hash is fine because codes are random. The recovery form takes username, code, and new password twice. Password rules are checked first so a typo never burns a code. `store.RecoverAccount` spends the code with a single `DELETE ... USING users ... RETURNING` (atomic: a code can never be used twice, even concurrently), sets the new password, and deletes all sessions, in one transaction. The user is then logged in and sees how many codes remain, with a warning at 2 or fewer and a stronger one at 0. There is currently no way to generate new codes (deliberately deferred).

### 7.6 Account page (`/account`)

Requires login. Sections: username (masked, with a "show name" checkbox), recovery codes remaining, change password, delete account (red, at the bottom). Both forms re-check the current password through `checkCurrentPassword`, which shares the login guard (a stolen session cannot be used to brute-force the password). Cheap validation runs before any hashing.

**Change password:** new password must pass the rules, match its confirmation, and differ from the current one. Saves the hash and deletes every session including the current one, then redirects to login with a notice.

**Delete account (soft delete):** requires password and a confirmation checkbox. `store.DeleteAccount` sets `password_hash = NULL`, `deleted_at = now()`, and deletes codes and sessions, in one transaction. The row and username remain, so the name is retired forever. Login, recovery, and session lookup all exclude deleted accounts. What happens to a deleted user's posts is undecided.

### 7.7 Password policy (auth/passwords.go)

Rules (checked by `PasswordProblems(password, username)`, which returns phrases that read after "Your password needs to ..."): 6 to 128 characters (counted in characters, not bytes), at least one uppercase letter, one digit, one symbol (Unicode punctuation or symbol), not a common word dressed up with digits/symbols (trims non-letters from both ends, undoes leetspeak like `@`→a and `0`→o, compares against a short built-in list such as `password`, `summer`, `snowball`), and not containing the username.

Passwords are normalized to Unicode NFKC before rules, hashing, and verification, so the same visible password matches across devices. Hashes are argon2id PHC strings (`$argon2id$v=19$m=19456,t=2,p=1$salt$hash`, 16-byte salt, 32-byte key) with parameters stored per hash so they can be raised later. Verification uses constant-time comparison.

At most 4 argon2 hashes run at once (a channel semaphore). A request waits up to 10 seconds for a slot and otherwise gets `auth.ErrBusy`, which `hashError` turns into a 503 with `Retry-After: 5`.

### 7.8 Rate limiting (web/ratelimit.go, web/loginguard.go)

Token buckets per visitor, held only in memory. Idle buckets are forgotten after 15 minutes. Beyond 50,000 tracked visitors, new ones share one overflow bucket so memory cannot grow without limit. Everything resets when the server restarts. Refusals return 429 with `Retry-After` and a plain-text message (a styled page is on the backlog).

| Limiter | Rate | Applies to |
|---|---|---|
| `names` | burst 30, 1 per 0.5s | GET `/signup`, GET `/signup/name` (abuse guard) |
| `rerolls` | burst 10, 1 per 6 min | Spent only by actual rerolls; shown to the user |
| `signup` | burst 10, 1 per 6s | POST `/signup` |
| `accounts` | burst 3, 1 per 20 min | Checked inside `handleSignup` just before creating an account |
| `login` | burst 10, 1 per 6s | POST `/login`, `/account/password`, `/account/delete` |
| `recover` | burst 5, 1 per 1 min | POST `/recover` |

Visitor identity comes from `clientIP`: the connection address, or with `SNOWBALL_TRUST_PROXY=1` the **last** `X-Forwarded-For` entry (the one the proxy appended; earlier entries can be forged). IPv4-mapped IPv6 is unwrapped; IPv6 is grouped by /64.

**Per-account login guard:** first 3 wrong passwords are free, then each further failure blocks that account's password checks for 30s, 1m, 2m, 4m, then 5m (cap). The cap is low on purpose, because long lockouts let anyone lock a user out. Only existing accounts are tracked. Records are forgotten after an hour of no failures, and cleared on success. Blocked attempts are refused before hashing.

### 7.9 Anti-bot form guard (web/formguard.go, auth/formtokens.go)

Every form includes `{{template "formguard" .FormToken}}` (templates/partials/formguard.html), which renders a honeypot input named `homepage` (moved off-screen by the `.hp` CSS class, `tabindex="-1"`, `aria-hidden`) and a hidden `form_token`: `<issued-at-milliseconds>.<HMAC>` where the HMAC covers the form's name, so tokens only work on the form they came from.

Every POST handler calls `s.checkForm(w, r, rule)` right after `ParseForm`. A filled honeypot or missing token is treated as a bot: quiet redirect back to the form, logged without identifying details. A token that is too young gets a polite "quicker than a person" message; an expired or unrecognized token (including after a server restart) gets "This form expired. Please submit it again." Tokens last 2 hours and are not single-use (the rate limits bound reuse).

| Form | Rule | Minimum age |
|---|---|---|
| Sign-up | `formSignup` | 3 s |
| Recovery | `formRecover` | 2 s |
| Change password | `formPassword` | 2 s |
| Delete account | `formDelete` | 2 s |
| Login | `formLogin` | none (password managers submit instantly) |

To add a form: add a `formRule`, a token field on its page struct set via `s.formToken(rule)` wherever the page is built, the `formguard` partial inside the `<form>`, and a `checkForm` call in its POST handler.

### 7.10 Site-wide HTTP security (web/headers.go, main.go)

Headers on every response: `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, `Cross-Origin-Opener-Policy: same-origin`, `Permissions-Policy: camera=(), microphone=(), geolocation=(), payment=(), usb=()`, and `X-Robots-Tag: noindex, nofollow, noarchive`.

**Search engines.** Snowball stays out of search engines and archives: the `X-Robots-Tag` header on every response, a matching `<meta name="robots">` tag in the layout, and `/robots.txt` disallowing everything. Caveat: a crawler that obeys `robots.txt` never fetches pages, so never sees `noindex`; a page linked from elsewhere may still appear in search results as a bare URL with no content. Impolite crawlers are the job of read limits and Anubis (section 10.2).

Consequences for development: **no inline `<script>`, no inline `style=""` attributes, no `<style>` tags, no third-party assets.** Dynamic styling is done with classes (see name colors below). htmx is configured via a meta tag with `includeIndicatorStyles: false` (it would inject a `<style>` tag) and `allowEval: false`.

Also: CSRF protection via `http.NewCrossOriginProtection`; `Cache-Control: no-store` on account routes and on every response to a logged-in user; form bodies capped at 64 KB with `http.MaxBytesReader`; server timeouts; errors shown to visitors are vague (`serverError`) while details go to the log.

### 7.11 Privacy and logging

No log line contains passwords, tokens, recovery codes, cookies, or IP addresses (audited). The rate limiter logs only its name and a count; bot detections log only the form name. Database error messages may contain usernames, which are public. Postgres logs failing statements with placeholders, not values. A Go handler panic would log the connecting address, which behind Caddy is always Caddy's. The account page tells users Snowball does not know which devices or locations they use.

### 7.12 Front end

**Design system (static/css/style.css).** One stylesheet for the whole site. Tokens on `:root`: `--frost` (page background), `--paper` (column), `--shade` (bars, headers, zebra rows), `--ink`, `--muted`, `--rule` (borders), `--link` and `--visited` (classic link colors), `--sled` (the single loud accent: yellow rules under the masthead and above the footer), `--note` (yellow notice boxes). Verdana body text (14px, 13px on wide screens), Georgia for the wordmark and headings. Thin borders instead of shadows, no rounded cards. Mobile-first single column; at 50rem and up a bordered fixed-width column (max 60rem) with a sidebar. Touch screens get larger targets via `@media (pointer: coarse)`. A global `[hidden] { display: none !important; }` guarantees the `hidden` attribute always hides.

**Name colors.** A username's first word is a color; `colorClass` (web/home.go) maps it to a class (`nc-blue` ... `nc-purple`, fallback `nc-x0` to `nc-x3` by hash), and CSS sets `--c` per class. Swatches and the post author square use `var(--c)`.

**Hidden username.** The logged-in header shows eight dots instead of the name (fixed length so length isn't revealed) plus a "show name" checkbox, to protect against shoulder-surfing. The name links to `/account`, not `/u/<name>`, so hovering doesn't reveal it. snowball.js wires every `.reveal-group` element: it shows the checkbox (hidden without JS, since it couldn't work), toggles `hidden` between `.name-masked` and `.name-real`, and resets to hidden on `pageshow` (including back-button restores). The account page uses the same pattern.

**Header account area.** Logged out: boxed "log in" and "sign up" buttons. Logged in: the hidden name with its "show name" checkbox, a boxed **account** button (to `/account`), and a "log out" link-styled button. The `.account` group uses `flex-wrap` with right alignment, so on narrow phones it wraps onto two right-aligned lines instead of squeezing. Focused pages (sign-up, login, recovery, goodbye) hide it via `basePage.HideAccount`.

**Front page.** Masthead with hand-drawn SVG snowball, random tagline, and account links; sort-only nav (front page, active conversations, newest, top this week); an admin note; "Popular today" (top 3 by points) and "Active right now" (sorted by latest reply); pagination; sidebar with About, Tags (with counts), House rules; footer with the owner's manifesto, render time, and post count. Posts are rendered through `partials/post.html` with rank numbers from a CSS counter. **All of this data is fake** (`samplePopular`, `sampleActive`, `sampleTags`, and `PostCount` in home.go/render.go).

**Copy voice.** Direct, human, slightly raw. The tagline contains one swear; elsewhere the owner chose to keep profanity out (the house rules keep one on purpose). Copy changes are the owner's call.

---

## 8. Product decisions log

| Decision | Reasoning |
|---|---|
| Generated names, no choosing, no renaming | Anonymity; accountability for moderation; reputation can't be shed. |
| One name per account, shown everywhere | Accounts can have personality and history without identifying a person. |
| No email; recovery codes instead | No personal data. Losing password and codes means losing the account, and users must acknowledge this. |
| Tags instead of boards | One shared room is easier for one person to moderate and livelier when small. Up to 3 tags per post from a curated list; new tags suggested under `meta`. |
| Front page is a hybrid | "Popular today" plus bump-ordered "active" conversations. |
| Every logout logs out everywhere; password change too | Fits the "leave no trace" character. |
| Deleted names are never reused | Prevents a newcomer inheriting someone else's reputation. Implemented as soft delete. |
| Rerolls limited per IP (10, refilling) | Nudges people to consider each name. |
| Password minimum 6 with composition rules | Owner's choice (lower than the usual recommendation of 8+), mitigated by rate limiting, the login guard, and weak-password checks. |
| No third-party CAPTCHA or tracking | Would contradict the privacy promise. All defenses are self-hosted. |
| Discussions are public for now; members-only later | A young forum needs visitors to see what they'd join. Reading will become login-only once there's a strong user base, as another layer bots must get through. Build with this as a switch. |
| Out of search engines from day one | `robots.txt`, `noindex` header and meta tag. |
| Complete post history only on a private page | A user can see everything they've posted, but nobody else can browse it in one place, so assembling a profile means hunting thread by thread. Users who dox themselves are responsible for that. |
| Keep every post (no expiry) | An archive problem is a good problem to have. Design tables so expiry could be added later. |
| Posts can be text, links, and eventually media | Phased for safety: text, links, and Markdown first; images next; audio and video last. See 10.1. |
| 10-minute edit window, 2-minute grace | Edits in the first 2 minutes are silent; after that an "edited" marker shows; after 10 minutes the content is final. No revisions are stored: one record per post or comment. |
| Up and down votes, scores public, voters private | The votes table exists only to enforce one vote per account and must never be exposed anywhere. |
| Deleted content keeps threads intact | Deleting a post or comment removes its content and author but keeps its place, so the discussion survives. |
| Deleted accounts show as "[deleted]" | The retired name stops being attached to anything. Deleting an account can optionally delete everything it posted. |

---

## 9. Conventions and gotchas

Restart the server after template changes; CSS/JS changes only need a reload. All CSS goes in `static/css/style.css`: other CSS files are never loaded. Template comments `{{/* */}}` never reach the browser; HTML comments `<!-- -->` are sent to every visitor, so keep anything sensitive (like honeypot notes) in template comments. Never edit a migration that has run anywhere important; add a new numbered one. Run `go mod tidy` after adding or removing imports (silence means success). In psql, statements end with `;` (a `-#` prompt means it is waiting for more; Ctrl+C clears). Folder = package in Go: exported names start with a capital letter. Lowercase all file and folder names (Windows is case-insensitive, Linux servers are not). When a browser seems to ignore CSS, check the rule actually appears in the Styles panel of dev tools; if it's absent, it isn't in the served stylesheet. Tests use table-driven style and inject time (`Check(token, form, now, ...)`) rather than sleeping.

Current test coverage: `internal/auth` (hashing round trips, damaged hashes, Unicode normalization, password rules, busy semaphore, form tokens) and `internal/web` (clientIP cases, limiter burst/block/status, login guard, block durations). Handlers and the store have no tests yet.

---

## 10. Roadmap and backlog

### 10.1 Next chapter: posting (design decided, not yet built)

Posting replaces the fake front page data and builds the pages already linked from templates (`/p/{id}`, `/t/{tag}`, `/tags`, `/new`, `/top`, `/active`, `/submit`). The decisions below are settled; table and column details are still to be designed.

**Post types and phasing.** Phase 1: text posts and link posts, with Markdown. Phase 2: image uploads. Phase 3: audio and video. Media waits because hosting uploads on an anonymous forum carries the most legal, privacy, and security risk of anything on the site. Requirements when media arrives: every upload re-encoded server-side (strips metadata such as GPS coordinates from phone photos, and neutralizes malicious files); no SVG uploads; strict content types, ideally served from a separate domain; size limits; accounts in probation cannot upload. A working admin removal and takedown process must exist before uploads open, since anonymous upload sites attract illegal content, and US providers that become aware of apparent child sexual abuse material are legally required to report it (research the obligations properly before launch). Third-party embeds (for example YouTube players) are not allowed, since they load tracking scripts that the CSP forbids; use link cards or click-to-load instead.

**Markdown.** A generous subset: paragraphs, bold, italics, strikethrough, links, bulleted and numbered lists, block quotes, inline code, code blocks, and spoiler tags. Headings are not supported (or render as bold text). Raw HTML is never allowed. Image syntax pointing at other websites is disabled: an external image would send every reader's IP address to whoever controls that server (and the CSP blocks it anyway). Planned libraries: `goldmark` to render with HTML disabled, and `bluemonday` to sanitize the output as a second layer. All user links get `rel="nofollow ugc noopener"`.

**Comments.** Threaded, Reddit-style depth. Mobile approach (to be workshopped once real comments exist): thin vertical lines instead of wide margins; indentation stops growing after about 5 levels, with a small "replying to <name>" note on deeper replies; after about 8 levels a "continue this thread" link opens that branch on its own page; every comment collapsible with one tap; smaller indent steps on phones than on desktop.

**Votes.** Up and down, one per account per post or comment, enforced by a unique constraint. Scores are public. Who voted how is never shown anywhere, never exported, never logged: the votes table maps each account's opinions and is among the most sensitive data in the database. Votes from accounts in probation don't count (or count less).

**Editing.** Authors can edit for 10 minutes after posting. Edits in the first 2 minutes are silent (for immediate typo fixes); later edits show an "edited" marker. After 10 minutes the content is final. No revisions are kept: one record per post or comment.

**Deleting your own content.** From the private history page, an author can delete any of their posts or comments at any time. The content and author are removed and replaced with a "[deleted]" placeholder, but the item keeps its place: a deleted post's page stays reachable at its URL and its comments remain; a deleted comment's replies remain. (Open detail: whether a deleted post keeps its title so the thread stays findable, or shows a placeholder title.)

**Deleted accounts.** Their posts and comments stay, with the author shown only as "[deleted]" (never the old name). The delete-account form gets an optional checkbox, "also delete everything I've posted," which applies the per-item deletion above to all of the account's content.

**Private history page.** A page under the account area, visible only to its owner, listing everything they've posted and commented, with the delete controls. There is no public profile listing a user's history.

**Visibility.** Public reading for now, built so "members only" can be switched on per page type later without schema changes. Search engines already excluded (7.10).

**Admin abilities (built with the first posting slice).** The admin can remove any post or comment and ban an account. A full moderation system (reports, queues, shadowbans, mass cleanup) comes later. Ban semantics are still to be decided.

**Tags.** A curated list maintained by the admin; 1 to 3 tags per post; new tags suggested under the `meta` tag.

**Ranking.** "Popular today" by points in the last 24 hours; "active" by latest reply (bump order); "newest"; "top this week". A Reddit-style "hot" ranking is a candidate for other views.

**Suggested build order:** (1) posts table, submit page, post page, admin removal; (2) real front page with tags; (3) comments; (4) votes; (5) sorting and pagination; (6) private history page and self-deletion; (7) images. Every new form uses the form guard (7.9).

Anti-bot layers to build into posting from the start:

| Layer | Description |
|---|---|
| New-account probation | First few days: no links, no media, slower limits, votes that don't count yet. Ends on age plus some real activity. |
| Per-account limits | Separate buckets per account for posting, commenting, voting, alongside per-IP limits. |
| Content checks | Cap links per post; duplicate detection via a hash of post text; a domain blocklist the admin can extend. |
| Shadowbans | A flagged account's content is visible only to itself (moderation phase). |
| One-click cleanup | Delete an account and everything it posted in one action (moderation phase). |
| nofollow | `rel="nofollow ugc"` on all user-submitted links. |

### 10.2 Security switches (build when needed or before launch)

**ALTCHA (proof-of-work on sign-up).** Self-hosted, privacy-first. Plan: serve the widget's *external* build (component, worker, CSS as separate files from `static/`, so the strict CSP needs no `blob:` or inline styles), add `GET /altcha/challenge` (rate limited, no-store) using the official Go library (`github.com/altcha-org/altcha-lib-go`), place the widget in the sign-up form with auto-solve on page load, verify in `handleSignup` next to `checkForm`, and remember spent solutions in memory until expiry (replay protection). Check that the widget version matches the protocol version the Go library supports (ALTCHA has v1 hash-based and v2 key-derivation protocols). Needs a secure context (HTTPS or localhost). Makes sign-up require JavaScript. Estimated half a day.

**Anubis (proof-of-work gate for the whole site).** A separate reverse proxy placed between Caddy and Snowball (Caddy → Anubis → Snowball). Visitors solve a puzzle once and get a cookie. Configuration only: compose service, Caddy upstream, policy file (which clients to challenge or allow, including search engines if desired). Must verify: `clientIP` still sees the real visitor through the extra hop (review `X-Forwarded-For` handling and `SNOWBALL_TRUST_PROXY`), and Anubis's logs meet the no-IP standard. Blocks visitors without JavaScript. Estimated a few hours at deployment.

**Invite system.** The strongest defense. Planned design: a config switch between open sign-up and invite-only; single-use invite codes that expire in 1 to 2 weeks; invites earned over time (new accounts get none) with a cap; an `invited_by` link kept only for a limited window (for example 90 days) and then deleted, because an invite tree is a social graph that could de-anonymize people; revoking invite rights from accounts that invite abusers. Tables: an `invites` table (code hash, creator, used by, expiry) and invite fields on users.

**Proof-of-work (custom).** An alternative to ALTCHA if building it in-house is preferred: signed stateless challenges, a Web Worker searching for a SHA-256 hash with N leading zero bits, single-use solutions in memory.

### 10.3 Production and deployment checklist

VPS with Docker Compose running Postgres, Snowball, and Caddy (automatic HTTPS). Set `DATABASE_URL` with real secrets, `SNOWBALL_TRUST_PROXY=1`, and `name: snowball` in compose. HSTS via Caddy. Caddy access logs off or scrubbed of IPs. Nightly `pg_dump` backups shipped off the server. Embed templates and static files into the binary with Go's `embed` (single-file deploys). Versioned static URLs (for example `style.css?v=<hash>`) with long cache lifetimes, replacing `revalidate`. Styled pages for 429 ("slow down") and 503 ("busy") instead of plain text. Optionally Anubis in front. Optionally a trap link (hidden from people, disallowed in `robots.txt`) to catch crawlers that ignore the rules. When the user base is strong, switch discussions to members-only.

### 10.4 Smaller backlog items

| Item | Notes |
|---|---|
| `/about`, `/rules`, `/changelog` | Written in the owner's voice; changelog shows the site growing. |
| Live password checklist | Rules tick off as the user types (snowball.js), so errors don't wipe the form. |
| Welcome page refresh | Refreshing resubmits sign-up; consider a one-time server-side handoff. |
| Recovery code regeneration | Deliberately deferred; the owner leans against it. |
| Rate limiter persistence | Counters reset on restart and are per-instance; fine for one server. |
| Single-use form tokens | Currently reusable within 2 hours; would need an in-memory spent set. |
| sqlc | Considered at the start; on Windows its Postgres parser needs cgo, so run it via Docker if adopted. |
| Tests | Handler and store tests (Postgres test container), plus a red-team bot to attack the defenses. |
| Number formatting | `PostCount` shows without thousands separators; needs a template function. |
