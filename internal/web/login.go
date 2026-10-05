package web

import (
	"log"
	"net/http"
	"strings"
	"time"

	"snowball/internal/auth"
)

type loginPage struct {
	basePage
	Username  string
	Error     string
	Notice    string
	FormToken string
}

// Fixed messages the login page can show, picked by ?notice=... in the URL.
// Only these exact texts can appear: nothing from the URL is ever shown
// directly, so a crafted link can't put words on the page.
var loginNotices = map[string]string{
	"password-changed": "Your password has been changed and you've been logged out everywhere. Log in with your new password.",
}

func loginBase(r *http.Request) basePage {
	b := newBasePage(r, "log in").withUp("/", "front page")
	b.HideAccount = true
	return b
}

// GET /login
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := userFrom(r.Context()); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	start := time.Now()
	data := loginPage{
		basePage:  loginBase(r),
		Notice:    loginNotices[r.URL.Query().Get("notice")],
		FormToken: s.formToken(formLogin),
	}
	data.Generated = time.Since(start)
	s.render(w, "login.html", data)
}

// POST /login
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	// Be forgiving about how the name is typed: " Blue-Humming-Otter " works.
	name := strings.ToLower(strings.TrimSpace(r.PostForm.Get("username")))
	password := r.PostForm.Get("password")

	showError := func(msg string) {
		data := loginPage{basePage: loginBase(r), Username: name, Error: msg, FormToken: s.formToken(formLogin)}
		data.Generated = time.Since(start)
		s.render(w, "login.html", data)
	}

	// Honeypot and timing token (see formguard.go).
	if problem, handled := s.checkForm(w, r, formLogin); handled {
		return
	} else if problem != "" {
		showError(problem)
		return
	}

	// Too many recent failures for this account? Refuse before even checking
	// the password: the guesser learns nothing, and the server skips the
	// expensive hash.
	if wait := s.logins.wait(name); wait > 0 {
		showError("Too many failed attempts for this name. Try again in " + friendlyDuration(wait) + ".")
		return
	}

	id, hash, found, err := s.store.Credentials(ctx, name)
	if err != nil {
		serverError(w, err)
		return
	}

	ok := false
	if found {
		if ok, err = auth.VerifyPassword(password, hash); err != nil {
			hashError(w, err)
			return
		}
	} else {
		// No such user, but do the same work anyway so the response
		// takes as long as a real check.
		auth.VerifyDummy(password)
	}

	if !ok {
		// Only real accounts are tracked, so junk names can't fill memory.
		if found {
			s.logins.fail(name)
		}
		// Same message either way, so it doesn't confirm which part was wrong.
		showError("That name and password don't match.")
		return
	}
	s.logins.succeed(name)

	if err := s.startSession(ctx, w, id); err != nil {
		serverError(w, err)
		return
	}

	// Redirect after a successful POST (Post/Redirect/Get), so refreshing
	// the next page doesn't resubmit the login form.
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// POST /logout: a form, not a link. A link could be triggered by any
// website embedding it, logging people out without asking.
//
// On Snowball, every log out is a log out of everything: all of the
// account's sessions are deleted, on every device.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if u, ok := userFrom(r.Context()); ok {
		if err := s.store.DeleteUserSessions(r.Context(), u.ID); err != nil {
			log.Printf("logout: %v", err)
		}
	} else if c, err := r.Cookie(sessionCookie); err == nil {
		// Not recognized as logged in, but holding a cookie: remove that one.
		if err := s.store.DeleteSession(r.Context(), auth.HashToken(c.Value)); err != nil {
			log.Printf("logout: %v", err)
		}
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
