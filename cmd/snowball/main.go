// Command snowball runs the forum. This file only wires the pieces together:
// the real work lives in internal/auth, internal/store, and internal/web.
//
// Run it from the project root, since templates and static files are found
// relative to the current folder:
//
//	go run ./cmd/snowball
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"snowball/internal/store"
	"snowball/internal/web"
)

// Matches compose.yaml and .env. Only used when DATABASE_URL isn't set,
// which should only ever happen on your own machine.
const devDatabaseURL = "postgres://snowball:snowball-dev-only@localhost:5432/snowball?sslmode=disable"

// Only used when SNOWBALL_VOTE_SECRET isn't set. Never use this in production:
// anyone who has read this code knows it.
const devVoteSecret = "snowball-dev-only-vote-secret-do-not-use-in-production"

func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Println("DATABASE_URL not set, using the local development database")
		url = devDatabaseURL
	}

	// Cancelled on Ctrl+C (or "docker stop", which sends SIGTERM), which is
	// the signal for everything to wind down.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// The pool gets a plain background context: it should outlive the
	// shutdown signal long enough for in-flight requests to finish.
	st, err := store.Open(context.Background(), url)
	if err != nil {
		log.Fatalf("database: %v\n(is Docker running? try: docker compose up -d)", err)
	}
	defer st.Close()

	// Refuse to run against a database that's missing migrations: every page
	// would fail on the first missing table or column, with a vaguer error.
	if _, err := st.CheckSchema(context.Background()); errors.Is(err, store.ErrSchemaBehind) {
		log.Fatalf("database: %v", err)
	} else if err != nil {
		log.Printf("warning: %v", err)
	}

	go housekeeping(ctx, st)

	// Only set SNOWBALL_TRUST_PROXY=1 when running behind Caddy (or another
	// reverse proxy). See web.Config.
	voteSecret := os.Getenv("SNOWBALL_VOTE_SECRET")
	if voteSecret == "" {
		log.Println("SNOWBALL_VOTE_SECRET not set, using the development vote secret")
		voteSecret = devVoteSecret
	}

	cfg := web.Config{
		TrustProxy: os.Getenv("SNOWBALL_TRUST_PROXY") == "1",
		VoteSecret: voteSecret,
	}

	site, err := web.New(st, cfg)
	if err != nil {
		log.Fatalf("starting up: %v", err)
	}

	// http.ListenAndServe on its own has no timeouts at all: a client could
	// open a connection and send one byte a minute, forever, and enough of
	// those would tie the server up. Each timeout caps one phase:
	srv := &http.Server{
		Addr:    ":8080",
		Handler: site.Handler(),

		ReadHeaderTimeout: 5 * time.Second,  // to send the request headers
		ReadTimeout:       15 * time.Second, // to send the whole request, body included
		WriteTimeout:      30 * time.Second, // for us to send the response
		IdleTimeout:       60 * time.Second, // for a kept-alive connection to sit unused
		MaxHeaderBytes:    64 << 10,         // 64 KB of headers is plenty
	}

	// Run the server in the background so main can wait for a shutdown signal.
	go func() {
		log.Printf("listening on http://localhost%s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	// Wait for the shutdown signal.
	<-ctx.Done()

	// Graceful shutdown: stop accepting new connections and give requests
	// already in progress up to 10 seconds to finish, instead of cutting
	// someone off halfway through submitting a form.
	log.Println("shutting down...")
	shutdownCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	log.Println("stopped")
}

// housekeeping runs at startup and then every 5 minutes, until ctx is
// cancelled at shutdown. Each time, it lets go of the vote fingerprints of
// polls that have closed (store.ClosePolls), so they outlive their poll by
// minutes at most. Once an hour it also deletes expired sessions, and lets
// go of what moderation has kept past its 30 days (store.Purge).
func housekeeping(ctx context.Context, st *store.Store) {
	const tick = 5 * time.Minute
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for round := 0; ; round++ {
		n, err := st.ClosePolls(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Printf("closing polls: %v", err)
		case n > 0:
			log.Printf("closing polls: let go of %d vote fingerprints", n)
		}

		if round%int(time.Hour/tick) == 0 {
			n, err := st.DeleteExpiredSessions(ctx)
			switch {
			case err != nil && ctx.Err() == nil:
				log.Printf("session cleanup: %v", err)
			case n > 0:
				log.Printf("session cleanup: removed %d expired sessions", n)
			}

			res, err := st.Purge(ctx)
			switch {
			case err != nil && ctx.Err() == nil:
				log.Printf("retention: %v", err)
			case res.Any():
				log.Printf("retention: %s", res) // counts only, never content
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
