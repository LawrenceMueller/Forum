package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"snowball/internal/auth"
	"snowball/internal/store"
)

const (
	// The __Host- prefix makes browsers enforce the safest cookie rules:
	// it must be Secure, sent over HTTPS, valid for the whole site (Path=/),
	// and can't be set or overwritten by any subdomain. A browser simply
	// rejects a cookie with this name that breaks any of those rules.
	sessionCookie   = "__Host-snowball_session"
	sessionLifetime = 30 * 24 * time.Hour
)

// ctxKey is a private type, so nothing outside this package can
// accidentally read or overwrite the value we store in the context.
type ctxKey struct{}

// userFrom reports who's making the request, if anyone.
func userFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(ctxKey{}).(store.User)
	return u, ok
}

// startSession logs a user in: a brand-new random token for the cookie,
// and its hash in the database.
func (s *Server) startSession(ctx context.Context, w http.ResponseWriter, userID int64) error {
	token, err := auth.NewSessionToken()
	if err != nil {
		return err
	}
	expires := time.Now().Add(sessionLifetime)
	if err := s.store.CreateSession(ctx, userID, auth.HashToken(token), expires); err != nil {
		return err
	}
	setSessionCookie(w, token, expires)
	return nil
}

// The cookie settings do a lot of security work:
//
//	HttpOnly  JavaScript can't read it, so injected scripts can't steal it.
//	Secure    only sent over HTTPS (browsers make an exception for localhost).
//	SameSite  not sent along with form posts from other websites.
func setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1, // tells the browser to delete it now
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// withUser is middleware: it runs before every handler, looks up the session
// cookie, and if it's valid, attaches the user to the request's context.
// Handlers then call userFrom(r.Context()) to find out who's asking.
func (s *Server) withUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Static files don't care who you are; skip the database trip.
		if strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}

		c, err := r.Cookie(sessionCookie)
		if err != nil {
			next.ServeHTTP(w, r) // no cookie: logged out
			return
		}

		u, ok, err := s.store.UserBySession(r.Context(), auth.HashToken(c.Value))
		if err != nil {
			serverError(w, err)
			return
		}
		if !ok {
			clearSessionCookie(w) // expired or bogus: throw it away
			next.ServeHTTP(w, r)
			return
		}

		// Pages for a logged-in user carry their name (even if hidden),
		// so never let a browser or shared computer cache them.
		w.Header().Set("Cache-Control", "no-store")

		ctx := context.WithValue(r.Context(), ctxKey{}, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
