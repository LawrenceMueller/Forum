package web

// Content checks (moderation slice 4), run on every new post and comment
// (and on edits, for the link rules):
//
//   - Probation: for its first 3 days an account can't share links (the
//     post's link field, or links in any text), and posts and comments more
//     slowly. Spam accounts are usually used the moment they're made.
//   - Duplicates: the same text from the same account (within a week for
//     posts, a day for comments), or from several accounts at once (a spam
//     wave), is refused. Short comments ("Same here.") are left alone.
//   - The domain blocklist: links to listed sites, or their subdomains, are
//     refused. The admin edits it at /admin/domains.
//
// Links are found by rendering the Markdown and reading the hrefs back out,
// so whatever would become a link on the page (including bare "www." text,
// which the renderer links up) is caught, and nothing that wouldn't is.

import (
	"context"
	"errors"
	"html"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"snowball/internal/store"
)

const (
	// probation is how long a new account goes without links and at the
	// slower posting pace.
	probation = 3 * 24 * time.Hour

	// How far back duplicates are looked for, the author's own and other
	// accounts' alike.
	postDuplicateWindow    = 7 * 24 * time.Hour
	commentDuplicateWindow = 24 * time.Hour
	// waveOthers: this many OTHER accounts posting the same text within the
	// window makes it a spam wave.
	waveOthers = 2
	// Comments shorter than these are never treated as duplicates: "Thanks!"
	// is a fine thing to say twice.
	minDuplicateComment = 20
	minWaveComment      = 40
)

// onProbation reports whether an account is still new, and when it won't be.
func onProbation(u store.User) (bool, time.Time) {
	until := u.CreatedAt.Add(probation)
	return time.Now().Before(until), until
}

var hrefPattern = regexp.MustCompile(`href="([^"]*)"`)

// linkHosts returns the hosts of every link the text would show, rendered
// as the site renders it, lowercased and without "www.".
func (s *Server) linkHosts(body string) []string {
	var hosts []string
	for _, m := range hrefPattern.FindAllStringSubmatch(string(s.bodies.Render(body)), -1) {
		if h := hostOf(html.UnescapeString(m[1])); h != "" && !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// hostOf is a link's host, lowercased and without "www." ("" if none).
func hostOf(link string) string {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// blockedHost returns the blocklist entry a host falls under, or "".
func (s *Server) blockedHost(host string) string {
	list := s.blocked.Load()
	if list == nil {
		return ""
	}
	for _, d := range *list {
		if host == d || strings.HasSuffix(host, "."+d) {
			return d
		}
	}
	return ""
}

// linkProblems checks a post's link (or "") and its text, or a comment's
// text, against probation and the blocklist.
func (s *Server) linkProblems(u store.User, link, body string) []string {
	hosts := s.linkHosts(body)
	if h := hostOf(link); link != "" && h != "" && !slices.Contains(hosts, h) {
		hosts = append(hosts, h)
	}
	if len(hosts) == 0 && link == "" {
		return nil
	}
	if fresh, until := onProbation(u); fresh && !u.IsAdmin {
		return []string{"New accounts can share links after their first 3 days (yours: from " +
			until.Format("2006-01-02 15:04") + "). For now, leave links out."}
	}
	var errs []string
	for _, h := range hosts {
		if d := s.blockedHost(h); d != "" {
			errs = append(errs, "Links to "+d+" aren't allowed here.")
		}
	}
	return errs
}

// duplicateProblem checks new text against recent posts or comments, and
// returns a message, or "".
func (s *Server) duplicateProblem(ctx context.Context, comment bool, u store.User, hash string, length int) (string, error) {
	window, minMine, minWave := postDuplicateWindow, 0, 0
	if comment {
		window, minMine, minWave = commentDuplicateWindow, minDuplicateComment, minWaveComment
	}
	if length < minMine {
		return "", nil
	}
	mine, others, err := s.store.Duplicates(ctx, comment, u.ID, hash, window)
	if err != nil {
		return "", err
	}
	switch {
	case mine && comment:
		return "You've already posted that comment today.", nil
	case mine:
		return "You've already posted this in the last week (same title, link and text).", nil
	case others >= waveOthers && length >= minWave:
		return "That exact text was just posted by several other accounts, so it looks like spam.", nil
	}
	return "", nil
}

// postingLimiter is the per-account posting limit: slower during probation.
func (s *Server) postingLimiter(u store.User) *limiter {
	if fresh, _ := onProbation(u); fresh && !u.IsAdmin {
		return s.limits.postingNew
	}
	return s.limits.posting
}

func (s *Server) commentingLimiter(u store.User) *limiter {
	if fresh, _ := onProbation(u); fresh && !u.IsAdmin {
		return s.limits.commentingNew
	}
	return s.limits.commenting
}

// ---- the blocklist ----

// loadBlocklist reads the blocklist into memory: it's checked on every new
// post and comment, and changes only from the admin page.
func (s *Server) loadBlocklist(ctx context.Context) error {
	list, err := s.store.BlockedDomains(ctx)
	if err != nil {
		return err
	}
	domains := make([]string, len(list))
	for i, d := range list {
		domains[i] = d.Domain
	}
	s.blocked.Store(&domains)
	return nil
}

var domainPattern = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+$`)

// normalizeDomain turns what the admin typed ("https://www.Example.com/x")
// into a blocklist entry ("example.com"), or "" if it isn't a domain.
func normalizeDomain(typed string) string {
	typed = strings.ToLower(strings.TrimSpace(typed))
	if !strings.Contains(typed, "://") {
		typed = "http://" + typed
	}
	d := hostOf(typed)
	if !domainPattern.MatchString(d) || len(d) > 253 {
		return ""
	}
	return d
}

type domainsPage struct {
	basePage
	FormToken string
	Notice    string
	Errors    []string
	Domains   []store.BlockedDomain
	Typed     string
	Note      string
}

var domainNotices = map[string]string{
	"added":   "Blocked. New links to it are refused from now on (existing posts are left alone).",
	"removed": "Unblocked.",
}

// GET /admin/domains
func (s *Server) handleDomains(w http.ResponseWriter, r *http.Request) {
	s.renderDomains(w, r, time.Now(), "", "", nil)
}

func (s *Server) renderDomains(w http.ResponseWriter, r *http.Request, start time.Time, typed, note string, errs []string) {
	list, err := s.store.BlockedDomains(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	data := domainsPage{
		basePage:  newBasePage(r, "blocked sites (admin)").withUp("/account", "your account"),
		FormToken: s.formToken(formModerate),
		Notice:    domainNotices[r.URL.Query().Get("done")],
		Errors:    errs,
		Domains:   list,
		Typed:     typed,
		Note:      note,
	}
	data.Generated = time.Since(start)
	s.render(w, "admindomains.html", data)
}

// POST /admin/domains (action=add with domain= and note=, or action=remove with domain=)
func (s *Server) handleDomainChange(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	problem, handled := s.checkForm(w, r, formModerate)
	if handled {
		return
	}
	typed := strings.TrimSpace(r.PostForm.Get("domain"))
	note := strings.TrimSpace(r.PostForm.Get("note"))
	if problem != "" {
		s.renderDomains(w, r, start, typed, note, []string{problem})
		return
	}
	var err error
	var done string
	switch r.PostForm.Get("action") {
	case "add":
		d := normalizeDomain(typed)
		switch {
		case d == "":
			s.renderDomains(w, r, start, typed, note, []string{"That doesn't look like a site's address, like example.com."})
			return
		case utf8.RuneCountInString(note) > 200:
			s.renderDomains(w, r, start, typed, note, []string{"Keep the note to 200 characters."})
			return
		}
		err, done = s.store.BlockDomain(r.Context(), d, note), "added"
		if errors.Is(err, store.ErrDomainBlocked) {
			s.renderDomains(w, r, start, typed, note, []string{d + " is already blocked."})
			return
		}
	case "remove":
		err, done = s.store.UnblockDomain(r.Context(), typed), "removed"
	default:
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if err := s.loadBlocklist(r.Context()); err != nil {
		log.Printf("reloading the blocklist: %v", err)
	}
	http.Redirect(w, r, "/admin/domains?done="+done, http.StatusSeeOther)
}
