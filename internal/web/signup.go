package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"snowball/internal/auth"
	"snowball/internal/store"
)

// nameOffer is everything the username partial needs.
type nameOffer struct {
	Name        string
	Sig         string
	ColorClass  string
	RerollsLeft int
	RerollWait  string // when RerollsLeft is 0: how long until the next one
}

// withRerolls fills in the visitor's reroll allowance, without spending any.
func (s *Server) withRerolls(r *http.Request, o nameOffer) nameOffer {
	left, wait := s.limits.rerolls.status(clientIP(r, s.trustProxy))
	o.RerollsLeft = left
	if left == 0 {
		o.RerollWait = friendlyDuration(wait)
	}
	return o
}

func (s *Server) offerFor(name string) nameOffer {
	return nameOffer{Name: name, Sig: s.names.Sign(name), ColorClass: colorClass(name)}
}

// freshOffer generates a name nobody has, checking the database, and signs it.
func (s *Server) freshOffer(ctx context.Context) (nameOffer, error) {
	name, err := auth.GenerateUsername(func(n string) (bool, error) {
		return s.store.UsernameTaken(ctx, n)
	})
	if err != nil {
		return nameOffer{}, err
	}
	return s.offerFor(name), nil
}

// The current name offer is remembered in a cookie, so refreshing the
// sign-up page shows the same name instead of rolling a new one.
// The __Secure- prefix makes browsers refuse this cookie unless it's set
// with Secure over HTTPS (localhost counts), so it can't be planted over
// plain HTTP. (The stricter __Host- prefix needs Path=/, and this cookie
// is deliberately limited to /signup.)
const offerCookie = "__Secure-snowball_offer"

func setOfferCookie(w http.ResponseWriter, o nameOffer) {
	http.SetCookie(w, &http.Cookie{
		Name:     offerCookie,
		Value:    o.Name + "." + o.Sig, // names never contain dots, so this splits cleanly
		Path:     "/signup",            // only sent to the sign-up pages
		MaxAge:   60 * 60,              // an hour is plenty to finish signing up
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearOfferCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: offerCookie, Value: "", Path: "/signup", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

// savedOffer returns the name from the cookie, if there is one, it carries a
// valid signature (so it can't be edited to pick a name), and nobody has
// taken it in the meantime.
func (s *Server) savedOffer(ctx context.Context, r *http.Request) (nameOffer, bool) {
	c, err := r.Cookie(offerCookie)
	if err != nil {
		return nameOffer{}, false
	}
	name, sig, ok := strings.Cut(c.Value, ".")
	if !ok || !s.names.Valid(name, sig) {
		return nameOffer{}, false
	}
	taken, err := s.store.UsernameTaken(ctx, name)
	if err != nil || taken {
		return nameOffer{}, false
	}
	return s.offerFor(name), true
}

// offerFailed handles a freshOffer error: full name lists get a friendly
// pause message, anything else (like the database being down) is a server error.
func offerFailed(w http.ResponseWriter, err error) {
	if errors.Is(err, auth.ErrNamesExhausted) {
		log.Printf("signup: %v", err)
		http.Error(w, "Sign-ups are paused for a moment while we make more names. Try again soon.",
			http.StatusServiceUnavailable)
		return
	}
	serverError(w, err)
}

// signupBase is the layout data for every version of the sign-up page.
func signupBase(r *http.Request) basePage {
	b := newBasePage(r, "sign up").withUp("/", "front page")
	b.HideAccount = true
	return b
}

type signupPage struct {
	basePage
	Offer     nameOffer
	Errors    []string
	FormToken string
}

type welcomePage struct {
	basePage
	Username string
	ColorClass string
	Codes    []string
}

// GET /signup: the full page with a freshly generated name.
func (s *Server) handleSignupPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := userFrom(r.Context()); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther) // already have an account
		return
	}
	start := time.Now()

	// Reuse the remembered name, unless this is a no-JavaScript reroll
	// (the reroll link points at /signup?reroll=1) and one is left to spend.
	reroll := r.URL.Query().Get("reroll") != ""
	if reroll {
		reroll, _ = s.limits.rerolls.allow(clientIP(r, s.trustProxy))
	}
	offer, ok := nameOffer{}, false
	if !reroll {
		offer, ok = s.savedOffer(r.Context(), r)
	}
	if !ok {
		var err error
		if offer, err = s.freshOffer(r.Context()); err != nil {
			offerFailed(w, err)
			return
		}
	}
	setOfferCookie(w, offer)

	data := signupPage{basePage: signupBase(r), Offer: s.withRerolls(r, offer), FormToken: s.formToken(formSignup)}
	data.Generated = time.Since(start)
	s.render(w, "signup.html", data)
}

// GET /signup/name: htmx asks for this when "reroll" is clicked.
// It returns only the username partial, which htmx swaps in place.
// Each one spends from the visitor's reroll allowance; when it's used up,
// the current name comes back unchanged with a note about when more arrive.
func (s *Server) handleRerollName(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if ok, _ := s.limits.rerolls.allow(clientIP(r, s.trustProxy)); !ok {
		offer, found := s.savedOffer(ctx, r)
		if !found {
			// No remembered name to fall back on (cookie gone or expired).
			var err error
			if offer, err = s.freshOffer(ctx); err != nil {
				offerFailed(w, err)
				return
			}
			setOfferCookie(w, offer)
		}
		s.renderFragment(w, "signup.html", "username", s.withRerolls(r, offer))
		return
	}

	offer, err := s.freshOffer(ctx)
	if err != nil {
		offerFailed(w, err)
		return
	}
	setOfferCookie(w, offer)
	s.renderFragment(w, "signup.html", "username", s.withRerolls(r, offer))
}

// POST /signup: validate, create the account, show the recovery codes.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if _, ok := userFrom(r.Context()); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	start := time.Now()
	ctx := r.Context()

	// Refuse oversized bodies before parsing anything.
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	// Honeypot and timing token (see formguard.go).
	formProblem, handled := s.checkForm(w, r, formSignup)
	if handled {
		return
	}

	name := r.PostForm.Get("username")
	sig := r.PostForm.Get("sig")
	password := r.PostForm.Get("password")
	confirm := r.PostForm.Get("confirm")
	ackRecovery := r.PostForm.Get("ack_recovery") == "yes"
	ackUsername := r.PostForm.Get("ack_username") == "yes"

	var errs []string
	if formProblem != "" {
		errs = append(errs, formProblem)
	}

	nameOK := s.names.Valid(name, sig)
	if !nameOK {
		errs = append(errs, "That name didn't come from us, so here's a fresh one.")
	}

	// One message listing everything missing, rather than one problem per attempt.
	if missing := auth.PasswordProblems(password, name); len(missing) > 0 {
		errs = append(errs, "Your password needs to "+strings.Join(missing, ", ")+".")
	} else if password != confirm {
		errs = append(errs, "The two passwords don't match.")
	}

	// The browser enforces the checkboxes too, but browsers can be bypassed.
	if !ackRecovery || !ackUsername {
		errs = append(errs, "Please tick both boxes under \"Before you continue.\"")
	}

	// Everything checks out. Last gate: how many accounts has this network
	// created lately? Checked only now, so typos above don't use up the limit.
	if len(errs) == 0 {
		if ok, wait := s.limits.accounts.allow(clientIP(r, s.trustProxy)); !ok {
			errs = append(errs, "Several new accounts have come from your network recently. "+
				"Try again in "+friendlyDuration(wait)+".")
		}
	}

	// Create the account.
	if len(errs) == 0 {
		hash, err := auth.HashPassword(password)
		if err != nil {
			hashError(w, err)
			return
		}
		codes, codeHashes, err := auth.GenerateRecoveryCodes()
		if err != nil {
			serverError(w, err)
			return
		}

		_, err = s.store.CreateUser(ctx, name, hash, codeHashes)
		switch {
		case errors.Is(err, store.ErrUsernameTaken):
			errs = append(errs, "Someone grabbed that name a moment ago, so here's a fresh one.")
			nameOK = false
		case err != nil:
			serverError(w, err)
			return
		default:
			// Success. The codes exist in plain form only in this response,
			// so tell the browser not to keep a copy in its cache.
			w.Header().Set("Cache-Control", "no-store")
			clearOfferCookie(w) // the name is taken now, by them
			base := signupBase(r)
			base.Title = "welcome"
			// No "up" link here: the only way on should be "I've saved them".
			base.UpURL, base.UpLabel = "", ""
			data := welcomePage{basePage: base, Username: name, ColorClass: colorClass(name), Codes: codes}
			data.Generated = time.Since(start)
			s.render(w, "welcome.html", data)
			return
		}
	}

	// Something failed: show the form again with the errors.
	offer := s.offerFor(name)
	if !nameOK {
		var err error
		if offer, err = s.freshOffer(ctx); err != nil {
			offerFailed(w, err)
			return
		}
		setOfferCookie(w, offer)
	}

	data := signupPage{basePage: signupBase(r), Offer: s.withRerolls(r, offer), Errors: errs,
		FormToken: s.formToken(formSignup)}
	data.Generated = time.Since(start)
	s.render(w, "signup.html", data)
}
