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

	go cleanExpiredSessions(ctx, st)

	// Only set SNOWBALL_TRUST_PROXY=1 when running behind Caddy (or another
	// reverse proxy). See web.Config.
	cfg := web.Config{TrustProxy: os.Getenv("SNOWBALL_TRUST_PROXY") == "1"}

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

// cleanExpiredSessions deletes expired sessions once at startup and then
// every hour, until ctx is cancelled at shutdown.
func cleanExpiredSessions(ctx context.Context, st *store.Store) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		n, err := st.DeleteExpiredSessions(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Printf("session cleanup: %v", err)
		case n > 0:
			log.Printf("session cleanup: removed %d expired sessions", n)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
