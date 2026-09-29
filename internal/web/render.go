package web

import (
	"bytes"
	"errors"
	"html/template"
	"log"
	"math/rand/v2"
	"net/http"
	"path/filepath"
	"time"

	"snowball/internal/auth"
)

// One is picked at random on every page load. Add more whenever you think of them.
var taglines = []string{
	"An anonymous forum, keep your shitty personal data",
}

// basePage is what layout.html needs on every page.
// Page-specific structs embed it, so its fields are available on every page.
type basePage struct {
	Title       string
	Tagline     string
	User        string // empty means logged out
	HideAccount bool   // true on sign up / log in, where the links would be redundant
	PostCount   int
	Generated   time.Duration
}

// newBasePage fills in the layout's data, including who's logged in
// (which the withUser middleware attached to the request).
func newBasePage(r *http.Request, title string) basePage {
	b := basePage{
		Title:     title,
		Tagline:   taglines[rand.IntN(len(taglines))],
		PostCount: 12408, // fake until posts are in the database
	}
	if u, ok := userFrom(r.Context()); ok {
		b.User = u.Username
	}
	return b
}

// loadPages builds a separate template set for each page: layout + partials + that one page.
// They have to be separate because every page defines a block called "content",
// and within one set, the last definition would win.
// Paths are relative to where the server is started: run it from the project root.
func loadPages() (map[string]*template.Template, error) {
	files, err := filepath.Glob("templates/pages/*.html")
	if err != nil {
		return nil, err
	}

	pages := make(map[string]*template.Template)
	for _, page := range files {
		t, err := template.ParseFiles("templates/layout.html", page)
		if err != nil {
			return nil, err
		}
		if _, err := t.ParseGlob("templates/partials/*.html"); err != nil {
			return nil, err
		}
		pages[filepath.Base(page)] = t
	}
	return pages, nil
}

// serverError logs the real problem for you and shows visitors something vague.
// Error details can reveal how the site works, so they stay in the log.
func serverError(w http.ResponseWriter, err error) {
	log.Printf("server error: %v", err)
	http.Error(w, "something broke on our end", http.StatusInternalServerError)
}

// hashError handles a failed password hash or check. When the server is
// flooded (auth.ErrBusy), say so and suggest a retry; anything else is a
// real server error.
func hashError(w http.ResponseWriter, err error) {
	if errors.Is(err, auth.ErrBusy) {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "Snowball is very busy right now. Please try again in a few seconds.",
			http.StatusServiceUnavailable)
		return
	}
	serverError(w, err)
}

// render sends a full page: the layout with the page's content inside.
func (s *Server) render(w http.ResponseWriter, page string, data any) {
	s.renderFragment(w, page, "layout", data)
}

// renderFragment executes one named template from a page's set, like a
// partial for htmx to swap in. It writes into a buffer first, so if the
// template fails halfway the visitor gets a clean error instead of half a page.
func (s *Server) renderFragment(w http.ResponseWriter, page, name string, data any) {
	t, ok := s.pages[page]
	if !ok {
		log.Printf("render: no template set for %q", page)
		http.Error(w, "something broke on our end", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("render %s/%s: %v", page, name, err)
		http.Error(w, "something broke on our end", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}
