package web

import (
	"errors"
	"log"
	"net/http"
	"time"

	"snowball/internal/auth"
)

// Every form on Snowball carries two anti-bot checks, added in the template
// with {{template "formguard" .FormToken}} and checked here:
//
//   - a honeypot field, invisible to people, that simple bots fill in;
//   - a signed timing token (see auth.FormTokens), which bots either lack
//     (they posted without loading the page) or return too quickly.

const (
	honeypotField = "homepage"
	tokenField    = "form_token"
	formMaxAge    = 2 * time.Hour
)

// formRule describes one form: its name (tokens only work for the form
// they were issued for), how long a person needs at the very least, and
// where to send a detected bot.
type formRule struct {
	name   string
	minAge time.Duration
	back   string
}

var (
	formSignup   = formRule{"signup", 3 * time.Second, "/signup"}
	formRecover  = formRule{"recover", 2 * time.Second, "/recover"}
	formPassword = formRule{"password", 2 * time.Second, "/account"}
	formDelete   = formRule{"delete", 2 * time.Second, "/account"}
	formSubmit   = formRule{"submit", 5 * time.Second, "/submit"} // nobody writes a post in under 5s

	// "Ha, same" is a real comment, so the minimum is short.
	formComment = formRule{"comment", 2 * time.Second, "/"}

	// Admin actions: no minimum (a quick click is fine), but still need a token.
	formRemovePost    = formRule{"remove-post", 0, "/"}
	formRemoveComment = formRule{"remove-comment", 0, "/"}

	// Login has no minimum: password managers can fill and submit it in
	// well under a second, and slowing them down would push people toward
	// weaker passwords. It still needs a valid token.
	formLogin = formRule{"login", 0, "/login"}
)

// formToken issues a fresh token for a form that's about to be rendered.
func (s *Server) formToken(f formRule) string {
	return s.forms.Issue(f.name, time.Now())
}

// checkForm runs both checks on a parsed form. Call it right after ParseForm.
//
// Clear bot signs (honeypot filled, no token at all) get a quiet redirect
// back to the form, with nothing to learn from: handled is true, and the
// caller should just return. Borderline cases that a real person could hit
// (too fast, expired) come back as a problem message to show on the form.
func (s *Server) checkForm(w http.ResponseWriter, r *http.Request, f formRule) (problem string, handled bool) {
	token := r.PostForm.Get(tokenField)

	if r.PostForm.Get(honeypotField) != "" || token == "" {
		log.Printf("%s: bot-like submission ignored", f.name) // nothing identifying
		http.Redirect(w, r, f.back, http.StatusSeeOther)
		return "", true
	}

	err := s.forms.Check(token, f.name, time.Now(), f.minAge, formMaxAge)
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, auth.ErrFormTooFast):
		return "That was quicker than a person usually manages. Please check the form and submit it again.", false
	default:
		// Expired, or signed with an old key because the server restarted
		// while the form was open. Both happen to real people.
		return "This form expired. Please submit it again.", false
	}
}
