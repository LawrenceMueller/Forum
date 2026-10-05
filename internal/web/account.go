package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"snowball/internal/auth"
	"snowball/internal/store"
)

// accountPage is the private settings page for whoever is logged in.
// Nothing on it is visible to anyone else.
type accountPage struct {
	basePage
	ColorClass     string
	RecoveryCodes  int      // how many unused codes are left
	PasswordErrors []string // problems with the change-password form
	DeleteErrors   []string // problems with the delete-account form
	PasswordToken  string   // anti-bot tokens, one per form (see formguard.go)
	DeleteToken    string
}

// renderAccount shows the account page, with errors for whichever form
// was just submitted, if any.
func (s *Server) renderAccount(w http.ResponseWriter, r *http.Request, u store.User, start time.Time, pwErrs, delErrs []string) {
	codes, err := s.store.RemainingRecoveryCodes(r.Context(), u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	data := accountPage{
		basePage:       newBasePage(r, "your account").withUp("/", "front page"),
		ColorClass:     colorClass(u.Username),
		RecoveryCodes:  codes,
		PasswordErrors: pwErrs,
		DeleteErrors:   delErrs,
		PasswordToken:  s.formToken(formPassword),
		DeleteToken:    s.formToken(formDelete),
	}
	data.Generated = time.Since(start)
	s.render(w, "account.html", data)
}

// GET /account
func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.renderAccount(w, r, u, time.Now(), nil, nil)
}

// checkCurrentPassword re-checks the password of the logged-in user before
// a sensitive change. A stolen session alone shouldn't be enough to change
// the password or delete the account, so this is guarded like login:
// failures count toward the same per-account slowdown.
//
// It returns a message for the user when the check fails, or an error when
// something broke (already handled: the caller should just return).
func (s *Server) checkCurrentPassword(ctx context.Context, w http.ResponseWriter, u store.User, password string) (problem string, failed bool) {
	if wait := s.logins.wait(u.Username); wait > 0 {
		return "Too many wrong passwords. Try again in " + friendlyDuration(wait) + ".", false
	}

	hash, err := s.store.PasswordHash(ctx, u.ID)
	if err != nil {
		serverError(w, err)
		return "", true
	}
	ok, err := auth.VerifyPassword(password, hash)
	if err != nil {
		hashError(w, err)
		return "", true
	}
	if !ok {
		s.logins.fail(u.Username)
		return "That's not your current password.", false
	}
	s.logins.succeed(u.Username)
	return "", false
}

// POST /account/password
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	start := time.Now()
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if problem, handled := s.checkForm(w, r, formPassword); handled {
		return
	} else if problem != "" {
		s.renderAccount(w, r, u, start, []string{problem}, nil)
		return
	}

	current := r.PostForm.Get("current")
	password := r.PostForm.Get("password")
	confirm := r.PostForm.Get("confirm")

	// Cheap checks first: no point spending a password hash on a form
	// that's going to be rejected anyway.
	var errs []string
	if missing := auth.PasswordProblems(password, u.Username); len(missing) > 0 {
		errs = append(errs, "Your new password needs to "+strings.Join(missing, ", ")+".")
	} else if password != confirm {
		errs = append(errs, "The two new passwords don't match.")
	} else if password == current {
		errs = append(errs, "Your new password is the same as your current one.")
	}
	if len(errs) > 0 {
		s.renderAccount(w, r, u, start, errs, nil)
		return
	}

	problem, failed := s.checkCurrentPassword(ctx, w, u, current)
	if failed {
		return
	}
	if problem != "" {
		s.renderAccount(w, r, u, start, []string{problem}, nil)
		return
	}

	newHash, err := auth.HashPassword(password)
	if err != nil {
		hashError(w, err)
		return
	}
	// Saves the new password and deletes every session, this one included.
	if err := s.store.ChangePassword(ctx, u.ID, newHash); err != nil {
		serverError(w, err)
		return
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/login?notice=password-changed", http.StatusSeeOther)
}

// POST /account/delete
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	start := time.Now()
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	if problem, handled := s.checkForm(w, r, formDelete); handled {
		return
	} else if problem != "" {
		s.renderAccount(w, r, u, start, nil, []string{problem})
		return
	}

	// The browser enforces the checkbox too, but browsers can be bypassed.
	if r.PostForm.Get("confirm") != "yes" {
		s.renderAccount(w, r, u, start, nil,
			[]string{"Tick the box to confirm you want your account deleted."})
		return
	}

	problem, failed := s.checkCurrentPassword(ctx, w, u, r.PostForm.Get("password"))
	if failed {
		return
	}
	if problem != "" {
		s.renderAccount(w, r, u, start, nil, []string{problem})
		return
	}

	eraseContent := r.PostForm.Get("erase_content") == "yes"
	if err := s.store.DeleteAccount(ctx, u.ID, s.voters.Voter(u.ID), eraseContent); err != nil {
		serverError(w, err)
		return
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/goodbye", http.StatusSeeOther)
}

// GET /goodbye: shown once an account has been deleted.
func (s *Server) handleGoodbye(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	b := newBasePage(r, "account deleted").withUp("/", "front page")
	b.HideAccount = true
	data := struct{ basePage }{b}
	data.Generated = time.Since(start)
	s.render(w, "goodbye.html", data)
}
