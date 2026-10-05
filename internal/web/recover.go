package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"snowball/internal/auth"
	"snowball/internal/store"
)

type recoverPage struct {
	basePage
	Username  string
	Errors    []string
	FormToken string
}

func recoverBase(r *http.Request) basePage {
	b := newBasePage(r, "recover your account").withUp("/login", "log in")
	b.HideAccount = true
	return b
}

// GET /recover
func (s *Server) handleRecoverPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := userFrom(r.Context()); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	start := time.Now()
	data := recoverPage{basePage: recoverBase(r), FormToken: s.formToken(formRecover)}
	data.Generated = time.Since(start)
	s.render(w, "recover.html", data)
}

// POST /recover
func (s *Server) handleRecover(w http.ResponseWriter, r *http.Request) {
	if _, ok := userFrom(r.Context()); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	start := time.Now()
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	name := strings.ToLower(strings.TrimSpace(r.PostForm.Get("username")))
	code := r.PostForm.Get("code")
	password := r.PostForm.Get("password")
	confirm := r.PostForm.Get("confirm")

	showErrors := func(errs ...string) {
		data := recoverPage{basePage: recoverBase(r), Username: name, Errors: errs, FormToken: s.formToken(formRecover)}
		data.Generated = time.Since(start)
		s.render(w, "recover.html", data)
	}

	// Honeypot and timing token (see formguard.go).
	if problem, handled := s.checkForm(w, r, formRecover); handled {
		return
	} else if problem != "" {
		showErrors(problem)
		return
	}

	// Check the new password first. A typo here shouldn't spend a code.
	var errs []string
	if name == "" || strings.TrimSpace(code) == "" {
		errs = append(errs, "Enter your name and one of your recovery codes.")
	}
	if missing := auth.PasswordProblems(password, name); len(missing) > 0 {
		errs = append(errs, "Your new password needs to "+strings.Join(missing, ", ")+".")
	} else if password != confirm {
		errs = append(errs, "The two passwords don't match.")
	}
	if len(errs) > 0 {
		showErrors(errs...)
		return
	}

	newHash, err := auth.HashPassword(password)
	if err != nil {
		hashError(w, err)
		return
	}

	userID, err := s.store.RecoverAccount(ctx, name, auth.HashRecoveryCode(code), newHash)
	if errors.Is(err, store.ErrRecoveryFailed) {
		// Same message whether the name or the code was wrong.
		showErrors("That name and recovery code don't match.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}

	if err := s.startSession(ctx, w, userID); err != nil {
		serverError(w, err)
		return
	}

	// Post/Redirect/Get: refreshing the next page won't resubmit this form.
	http.Redirect(w, r, "/recovered", http.StatusSeeOther)
}

type recoveredPage struct {
	basePage
	Remaining int
}

// GET /recovered: the confirmation after a successful recovery.
func (s *Server) handleRecovered(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	start := time.Now()

	n, err := s.store.RemainingRecoveryCodes(r.Context(), u.ID)
	if err != nil {
		serverError(w, err)
		return
	}

	data := recoveredPage{basePage: newBasePage(r, "account recovered").withUp("/", "front page"), Remaining: n}
	data.Generated = time.Since(start)
	s.render(w, "recovered.html", data)
}
