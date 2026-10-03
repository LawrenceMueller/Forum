// Package web is everything about requests and pages: the server, its
// routes and middleware, rendering, and the handlers.
package web

import (
	"html/template"
	"net/http"
	"time"

	"snowball/internal/auth"
	"snowball/internal/store"
)

// Config holds settings that differ between your machine and production.
type Config struct {
	// TrustProxy: set when running behind Caddy, so the visitor's real IP is
	// read from the X-Forwarded-For header. Never set it without a proxy in
	// front, or anyone could claim any IP just by sending that header.
	TrustProxy bool

	// VoteSecret keys the fingerprints stored with votes instead of account
	// IDs (see auth.VoterKeys). At least 32 characters. It must stay the same
	// across restarts, and must never be stored in the database.
	VoteSecret string
}

// Server holds everything handlers share.
type Server struct {
	store      *store.Store
	names      *auth.NameSigner // signs generated usernames on the sign-up page
	forms      *auth.FormTokens // signs form timestamps (see formguard.go)
	voters     *auth.VoterKeys  // turns account IDs into vote fingerprints
	pages      map[string]*template.Template
	trustProxy bool
	limits     rateLimits
	logins     *loginGuard
	stats      siteStats
}

// rateLimits are per visitor (per IP). The numbers are "one token refills
// every X, bucket holds Y": Y requests at once, then one per X.
type rateLimits struct {
	names    *limiter // loading the sign-up page and rerolling (abuse guard)
	rerolls  *limiter // the reroll allowance shown on the sign-up page
	signup   *limiter // submitting the sign-up form (typos and all)
	accounts *limiter // actually creating an account
	login    *limiter // submitting the login form
	recover  *limiter // submitting the recovery form
	submit   *limiter // submitting the new-post form, per IP
	posting  *limiter // actually publishing a post, per ACCOUNT (key "user:<id>")
	comment    *limiter // submitting the comment form, per IP
	commenting *limiter // actually publishing a comment, per ACCOUNT
	vote         *limiter // vote clicks, per IP
	voting       *limiter // vote clicks, per ACCOUNT: the fast limit
	votingDaily  *limiter // per ACCOUNT: the daily budget
	votingAuthor *limiter // per ACCOUNT per AUTHOR: votes on one person's content
	editing      *limiter // saving edits to posts and comments, per ACCOUNT
}

func newRateLimits() rateLimits {
	return rateLimits{
		names:    newLimiter("names", 500*time.Millisecond, 30),
		rerolls:  newLimiter("rerolls", 6*time.Minute, 10), // 10 rerolls, one more every 6 minutes
		signup:   newLimiter("signup", 6*time.Second, 10),
		accounts: newLimiter("accounts", 20*time.Minute, 3),
		login:    newLimiter("login", 6*time.Second, 10),
		recover:  newLimiter("recover", time.Minute, 5),
		submit:   newLimiter("submit", 6*time.Second, 10),
		posting:  newLimiter("posting", 5*time.Minute, 5), // 5 posts at once, then one per 5 minutes
		comment:    newLimiter("comment", 3*time.Second, 20),
		commenting: newLimiter("commenting", 30*time.Second, 10), // 10 at once, then one per 30s
		vote:         newLimiter("vote", 3*time.Second, 30),   // 30 at once, then one per 3s
		voting:       newLimiter("voting", 3*time.Second, 10), // 10 at once, then one per 3s
		votingDaily:  newLimiter("voting-daily", 24*time.Hour/250, 250), // 250 a day, rolling
		votingAuthor: newLimiter("voting-author", 24*time.Hour/10, 10),  // 10 a day per author
		editing:      newLimiter("editing", 10*time.Second, 20),         // 20 at once, then one per 10s
	}
}

func New(st *store.Store, cfg Config) (*Server, error) {
	names, err := auth.NewNameSigner()
	if err != nil {
		return nil, err
	}
	forms, err := auth.NewFormTokens()
	if err != nil {
		return nil, err
	}
	voters, err := auth.NewVoterKeys(cfg.VoteSecret)
	if err != nil {
		return nil, err
	}
	s := &Server{
		store:      st,
		names:      names,
		forms:      forms,
		voters:     voters,
		trustProxy: cfg.TrustProxy,
		limits:     newRateLimits(),
		logins:     newLoginGuard(),
	}

	// Templates come after the Server exists, because some template
	// functions read from it (the post count).
	if s.pages, err = loadPages(s.templateFuncs()); err != nil {
		return nil, err
	}

	s.refreshStats()
	go s.statsLoop()
	return s, nil
}

// templateFuncs are the functions every template can call.
func (s *Server) templateFuncs() template.FuncMap {
	return template.FuncMap{
		"postCount": func() int64 { return s.stats.posts.Load() },
		"commas":    commas,
	}
}

// Handler returns the whole site as one http.Handler: the routes,
// wrapped in middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Reading.
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("GET /new", s.handleList(listNew, "/new"))
	mux.HandleFunc("GET /active", s.handleList(listActive, "/active"))
	mux.HandleFunc("GET /top", s.handleList(listTop, "/top"))
	mux.HandleFunc("GET /t/{tag}", s.handleTag)
	mux.HandleFunc("GET /tags", s.handleTags)
	mux.HandleFunc("GET /robots.txt", robotsTxt)

	// Account pages are never cached (see noStore in headers.go), and the
	// ones that cost real work are rate limited (see ratelimit.go).
	lim := s.limits
	mux.Handle("GET /signup", s.limit(lim.names, noStore(s.handleSignupPage)))
	mux.Handle("GET /signup/name", s.limit(lim.names, noStore(s.handleRerollName)))
	mux.Handle("POST /signup", s.limit(lim.signup, noStore(s.handleSignup)))

	mux.Handle("GET /login", noStore(s.handleLoginPage))
	mux.Handle("POST /login", s.limit(lim.login, noStore(s.handleLogin)))
	mux.Handle("POST /logout", noStore(s.handleLogout))

	// Posts.
	mux.Handle("GET /submit", noStore(s.handleSubmitPage))
	mux.Handle("POST /submit", s.limit(lim.submit, noStore(s.handleSubmit)))
	mux.HandleFunc("GET /p/{id}", s.handlePost)
	mux.Handle("POST /p/{id}/remove", noStore(s.handleRemovePost))
	mux.Handle("GET /p/{id}/edit", noStore(s.handleEditPostPage))
	mux.Handle("POST /p/{id}/edit", s.limit(lim.submit, noStore(s.handleEditPost)))

	// Votes.
	mux.Handle("POST /vote", s.limit(lim.vote, noStore(s.handleVote)))

	// Comments.
	mux.Handle("POST /p/{id}/comment", s.limit(lim.comment, noStore(s.handleComment)))
	mux.Handle("GET /p/{id}/c/{cid}/reply", noStore(s.handleReplyForm))
	mux.HandleFunc("GET /p/{id}/c/{cid}", s.handleCommentThread)
	mux.Handle("POST /p/{id}/c/{cid}/remove", noStore(s.handleRemoveComment))
	mux.Handle("GET /p/{id}/c/{cid}/edit", noStore(s.handleEditCommentForm))
	mux.Handle("POST /p/{id}/c/{cid}/edit", s.limit(lim.comment, noStore(s.handleEditComment)))

	mux.Handle("GET /account", noStore(s.handleAccount))
	mux.Handle("POST /account/password", s.limit(lim.login, noStore(s.handleChangePassword)))
	mux.Handle("POST /account/delete", s.limit(lim.login, noStore(s.handleDeleteAccount)))
	mux.Handle("GET /goodbye", noStore(s.handleGoodbye))

	mux.Handle("GET /recover", noStore(s.handleRecoverPage))
	mux.Handle("POST /recover", s.limit(lim.recover, noStore(s.handleRecover)))
	mux.Handle("GET /recovered", noStore(s.handleRecovered))

	mux.Handle("GET /static/", revalidate(http.StripPrefix("/static/", http.FileServer(http.Dir("static")))))

	// Middleware wraps the router. A request passes through, outermost first:
	//   1. securityHeaders stamps every response, even rejected ones.
	//   2. CrossOriginProtection rejects POSTs sent from other websites (CSRF).
	//   3. withUser works out who's logged in from the session cookie.
	//   4. The mux picks the handler.
	var h http.Handler = mux
	h = s.withUser(h)
	h = http.NewCrossOriginProtection().Handler(h)
	h = securityHeaders(h)
	return h
}
