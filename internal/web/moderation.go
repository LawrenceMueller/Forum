package web

// Moderation, slice 1: members report posts and comments; the admin works
// through a queue, removing (with a reason, shown in the item's place) or
// dismissing; a public log shows what was done, without content or names.
// The store does the work in transactions (store/moderation.go); this file
// is the pages around it. Design: section 11.2 of the context doc.
//
//	GET, POST /p/{id}/report            report a post
//	GET, POST /p/{id}/c/{cid}/report    report a comment
//	GET  /admin/reports                 the queue (admin only)
//	POST /admin/reports                 dismiss or remove one item (admin only)
//	GET  /modlog                        the public moderation log
//	GET  /rules                         the rules the reasons point at

import (
	"context"
	"errors"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"snowball/internal/store"
)

// ---- reasons, as people see them ----

type reasonInfo struct {
	Label string // in sentences and placeholders: "personal information"
	Hint  string // on the report form
}

var reasonText = map[string]reasonInfo{
	"spam":       {"spam", "Ads, scams, or the same thing posted over and over."},
	"harassment": {"harassment", "Attacking, threatening, or hounding someone."},
	"personal":   {"personal information", "Anything that could identify someone: names, addresses, workplaces, photos."},
	"illegal":    {"illegal content", "Something against the law to post or host."},
	"tags":       {"wrong tags", "Fine to post, but filed under the wrong topics."},
	"other":      {"something else", "Explain in the note."},
	"rules":      {"breaking the rules", ""},
}

// reasonLabel is a reason in words; unknown keys fall back to the key.
func reasonLabel(key string) string {
	if r, ok := reasonText[key]; ok {
		return r.Label
	}
	return key
}

// removedText is what shows in place of something removed.
func removedText(reason string) string {
	switch reason {
	case "":
		return "[removed]" // removed before reasons were recorded
	case "held":
		return "[hidden while reports are reviewed]"
	case "shadow":
		return "[removed: spam]" // a shadowbanned account's; see cleanup.go in the store
	}
	return "[removed: " + reasonLabel(reason) + "]"
}

// adminRemovedText is how the admin sees why something is gone.
func adminRemovedText(reason string) string {
	if reason == "shadow" {
		return "silenced (shadowbanned account)"
	}
	return strings.Trim(removedText(reason), "[]")
}

// ownShadow reports whether the viewer is looking at their own shadowed
// post or comment, which they see as if it were up.
func ownShadow(viewer store.User, author string, authorDeleted, removed bool, reason string) bool {
	return removed && reason == "shadow" && !authorDeleted && viewer.Username != "" && author == viewer.Username
}

type reasonOption struct {
	Key, Label, Hint string
}

func reasonOptions(keys []string) []reasonOption {
	out := make([]reasonOption, len(keys))
	for i, k := range keys {
		out[i] = reasonOption{Key: k, Label: reasonText[k].Label, Hint: reasonText[k].Hint}
	}
	return out
}

// ---- the open-reports count in the admin's header ----

// refreshReports recounts what's waiting for the admin: reported items and
// appeals (cheap: partial indexes over the open ones).
func (s *Server) refreshReports() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := s.store.OpenReportItems(ctx)
	if err != nil {
		log.Printf("stats: open reports: %v", err)
		return
	}
	appeals, err := s.store.OpenAppealCount(ctx)
	if err != nil {
		log.Printf("stats: open appeals: %v", err)
		return
	}
	s.stats.openReports.Store(n + appeals)
}

// ---- reporting ----

const maxReportNote = 200

type reportPage struct {
	basePage
	FormToken string
	Action    string // where the form posts
	Back      string // the item, to go back to
	What      string // "post" or "comment"
	Quote     string // the title, or the start of the comment
	Reasons   []reasonOption
	Reason    string
	Note      string
	Errors    []string
	Done      bool
	Held      bool
}

// reportTarget works out what's being reported and whether the viewer may
// report it. ok is false when the response is written already.
func (s *Server) reportTarget(w http.ResponseWriter, r *http.Request) (page reportPage, it store.Item, ok bool) {
	u, loggedIn := userFrom(r.Context())
	if !loggedIn {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return page, it, false
	}
	post, ok := s.loadPost(w, r)
	if !ok {
		return page, it, false
	}
	it = store.Item{PostID: post.PublicID}
	page.What, page.Quote = "post", post.Title
	page.Action = "/p/" + post.PublicID + "/report"
	page.Back = "/p/" + post.PublicID
	author, authorDeleted, gone := post.Author, post.AuthorDeleted, post.Removed || post.Deleted

	if r.PathValue("cid") != "" {
		c, ok := s.loadComment(w, r, post)
		if !ok {
			return page, it, false
		}
		it.CommentID = c.PublicID
		page.What, page.Quote = "comment", snippet(c.Body, 140)
		page.Action = "/p/" + post.PublicID + "/c/" + c.PublicID + "/report"
		page.Back = "/p/" + post.PublicID + "#c-" + c.PublicID
		author, authorDeleted, gone = c.Author, c.AuthorDeleted, gone || c.Removed || c.Deleted
	}

	switch {
	case gone:
		http.Error(w, "This has already been removed or deleted.", http.StatusGone)
		return page, it, false
	case !authorDeleted && author == u.Username:
		http.Error(w, "That's your own; you can delete it from your history instead.", http.StatusBadRequest)
		return page, it, false
	case time.Since(u.CreatedAt) < voteMinAccountAge:
		http.Error(w, "New accounts can report after their first day.", http.StatusForbidden)
		return page, it, false
	}
	page.basePage = newBasePage(r, "report a "+page.What).withUp(page.Back, "back to the "+page.What)
	page.Reasons = reasonOptions(store.ReportReasons)
	page.FormToken = s.formToken(formReport)
	return page, it, true
}

// GET /p/{id}/report and /p/{id}/c/{cid}/report
func (s *Server) handleReportPage(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	page, _, ok := s.reportTarget(w, r)
	if !ok {
		return
	}
	page.Generated = time.Since(start)
	s.render(w, "report.html", page)
}

// POST /p/{id}/report and /p/{id}/c/{cid}/report
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	page, it, ok := s.reportTarget(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	problem, handled := s.checkForm(w, r, formReport)
	if handled {
		return
	}
	page.Reason = r.PostForm.Get("reason")
	page.Note = strings.TrimSpace(r.PostForm.Get("note"))
	if problem != "" {
		page.Errors = append(page.Errors, problem)
	}
	if !slices.Contains(store.ReportReasons, page.Reason) {
		page.Errors = append(page.Errors, "Pick a reason.")
	}
	if utf8.RuneCountInString(page.Note) > maxReportNote {
		page.Errors = append(page.Errors, "Keep the note to 200 characters.")
	}
	if page.Reason == "other" && page.Note == "" {
		page.Errors = append(page.Errors, "Say what's wrong in the note.")
	}

	if u, _ := userFrom(r.Context()); len(page.Errors) == 0 && u.Shadowbanned {
		page.Done = true // a spam bot's report goes nowhere
	} else if len(page.Errors) == 0 {
		held, err := s.store.Report(r.Context(), store.NewReport{
			Item: it, Reporter: s.voters.Reporter(u.ID), Reason: page.Reason, Note: page.Note,
		})
		switch {
		case errors.Is(err, store.ErrAlreadyReported):
			page.Errors = append(page.Errors, "You've already reported this. The admin will see it.")
		case errors.Is(err, store.ErrReportLimit):
			page.Errors = append(page.Errors, "That's the most reports one account can send in a day. Try again tomorrow.")
		case err != nil:
			serverError(w, err)
			return
		default:
			page.Done, page.Held = true, held
			s.refreshReports()
			if held {
				s.refreshStats()
			}
		}
	}
	page.FormToken = s.formToken(formReport)
	page.Generated = time.Since(start)
	s.render(w, "report.html", page)
}

// ---- the admin's queue ----

type queueRow struct {
	store.QueueItem
	Link        string // the item in context
	Kind        string // "post" or "comment"
	Text        string // its text, plain
	Status      string // "" when it's up; otherwise how it's hidden
	ReasonCount []reasonCount
}

type reasonCount struct {
	Label   string
	N       int
	Serious bool
}

type queuePage struct {
	basePage
	FormToken string
	Notice    string
	Errors    []string
	Appeals   []appealRow
	Rows      []queueRow
	Removal   []reasonOption
	Lengths   []lengthOption
}

type appealRow struct {
	store.OpenAppeal
	Reason, Length string
}

var queueNotices = map[string]string{
	"removed":   "Removed. Its reports are closed.",
	"suspended": "Removed, and its author suspended. The reports are closed.",
	"cleaned":   "Removed, along with everything else its author posted; their votes are withdrawn.",
	"dismissed": "Dismissed. Its reports are closed.",
	"lifted":    "Suspension lifted.",
	"kept":      "The suspension stands; they'll see their appeal was read.",
}

const queueLimit = 100

// GET /admin/reports
func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	s.renderQueue(w, r, time.Now(), nil)
}

func (s *Server) renderQueue(w http.ResponseWriter, r *http.Request, start time.Time, errs []string) {
	items, err := s.store.ReportQueue(r.Context(), queueLimit)
	if err != nil {
		serverError(w, err)
		return
	}
	appeals, err := s.store.OpenAppeals(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	data := queuePage{
		basePage:  newBasePage(r, "reports (admin)").withUp("/account", "your account"),
		FormToken: s.formToken(formModerate),
		Notice:    queueNotices[r.URL.Query().Get("done")],
		Errors:    errs,
		Removal:   reasonOptions(store.RemovalReasons),
		Lengths:   lengthOptions(),
	}
	for _, a := range appeals {
		row := appealRow{OpenAppeal: a, Reason: reasonLabel(a.Reason), Length: "permanent"}
		if a.EndsAt != nil {
			row.Length = "until " + a.EndsAt.Format("2006-01-02 15:04")
		}
		data.Appeals = append(data.Appeals, row)
	}
	for _, q := range items {
		row := queueRow{QueueItem: q, Kind: "post", Link: "/p/" + q.PostID, Text: q.Body}
		if q.CommentID != "" {
			row.Kind, row.Link = "comment", row.Link+"#c-"+q.CommentID
		} else if q.URL != "" {
			row.Text = strings.TrimSpace(q.URL + "\n\n" + q.Body)
		}
		switch {
		case q.Deleted:
			row.Status = "deleted by its author"
		case q.Removed:
			row.Status = adminRemovedText(q.RemovalReason)
		}
		for _, key := range store.ReportReasons {
			if n := q.Reasons[key]; n > 0 {
				row.ReasonCount = append(row.ReasonCount, reasonCount{
					Label: reasonLabel(key), N: n, Serious: slices.Contains(store.SeriousReasons, key),
				})
			}
		}
		data.Rows = append(data.Rows, row)
	}
	data.Generated = time.Since(start)
	s.render(w, "adminreports.html", data)
}

// POST /admin/reports (post=, comment=, action=dismiss|remove, reason=)
func (s *Server) handleModerate(w http.ResponseWriter, r *http.Request) {
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
	if problem != "" {
		s.renderQueue(w, r, start, []string{problem})
		return
	}
	it := store.Item{PostID: r.PostForm.Get("post"), CommentID: r.PostForm.Get("comment")}
	if !publicIDPattern.MatchString(it.PostID) || (it.CommentID != "" && !publicIDPattern.MatchString(it.CommentID)) {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	var err error
	var done string
	switch r.PostForm.Get("action") {
	case "dismiss":
		err, done = s.store.Dismiss(r.Context(), it), "dismissed"
	case "remove":
		reason := r.PostForm.Get("reason")
		if !slices.Contains(store.RemovalReasons, reason) {
			s.renderQueue(w, r, start, []string{"Pick a reason for removing it."})
			return
		}
		err, done = s.store.Remove(r.Context(), it, reason), "removed"
		// "and suspend the author for ...", when chosen.
		if days := r.PostForm.Get("days"); err == nil && days != "" {
			acct, aerr := s.store.AccountByName(r.Context(), r.PostForm.Get("author"))
			if aerr != nil {
				s.renderQueue(w, r, start, []string{"Removed, but couldn't find the author to suspend."})
				return
			}
			problem, serr := s.suspendFromForm(r, acct.ID)
			if serr != nil {
				serverError(w, serr)
				return
			}
			if problem != "" {
				s.renderQueue(w, r, start, []string{"Removed, but not suspended: " + problem})
				return
			}
			done = "suspended"
		}
		// "and everything else they posted", when ticked.
		if err == nil && r.PostForm.Get("cleanup") == "1" {
			acct, aerr := s.store.AccountByName(r.Context(), r.PostForm.Get("author"))
			if aerr != nil {
				s.renderQueue(w, r, start, []string{"Removed, but couldn't find the author to clean up after."})
				return
			}
			if _, cerr := s.store.Cleanup(r.Context(), acct.ID, s.voters.Voter(acct.ID), reason); cerr != nil {
				if errors.Is(cerr, store.ErrCantSuspendAdmin) {
					s.renderQueue(w, r, start, []string{"Removed. (The admin's own things aren't cleaned up.)"})
					return
				}
				serverError(w, cerr)
				return
			}
			done = "cleaned"
		}
	default:
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if errors.Is(err, store.ErrItemNotFound) {
		s.renderQueue(w, r, start, []string{"That post or comment doesn't exist any more."})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	s.refreshStats()
	s.refreshReports()
	http.Redirect(w, r, "/admin/reports?done="+done, http.StatusSeeOther)
}

// removalReason reads and checks the reason on an admin's removal form.
// ok is false when the response is written already.
func removalReason(w http.ResponseWriter, r *http.Request) (string, bool) {
	reason := r.PostForm.Get("reason")
	if !slices.Contains(store.RemovalReasons, reason) {
		http.Error(w, "Pick a reason for removing it (go back and choose one).", http.StatusBadRequest)
		return "", false
	}
	return reason, true
}

// ---- the public log ----

type modlogRow struct {
	When   time.Time
	What   string // "Post removed: spam"
	Link   string
	Detail string // "after 3 reports"
}

type modlogPage struct {
	basePage
	Rows    []modlogRow
	NextURL string
}

const modlogPageSize = 50

// GET /modlog (?before=<id> for older entries)
func (s *Server) handleModLog(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	u, _ := userFrom(r.Context())
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	entries, err := s.store.ModLog(r.Context(), before, modlogPageSize+1, u.IsAdmin)
	if err != nil {
		serverError(w, err)
		return
	}
	data := modlogPage{basePage: newBasePage(r, "moderation log").withUp("/", "front page")}
	if len(entries) > modlogPageSize {
		entries = entries[:modlogPageSize]
		data.NextURL = "/modlog?before=" + strconv.FormatInt(entries[len(entries)-1].ID, 10)
	}
	for _, e := range entries {
		kind, link := "Post", "/p/"+e.PostID
		if e.CommentID != "" {
			kind, link = "Comment", link+"#c-"+e.CommentID
		}
		if e.PostID == "" {
			link = "" // an action on an account, which the log never names
		}
		row := modlogRow{When: e.CreatedAt, Link: link}
		switch e.Action {
		case "suspend":
			if e.Detail == "permanent" {
				row.What = "Account banned: " + reasonLabel(e.Reason)
			} else {
				row.What = "Account suspended for " + e.Detail + ": " + reasonLabel(e.Reason)
			}
		case "lift":
			row.What = "Suspension lifted"
			if e.Detail != "" {
				row.What += " " + e.Detail
			}
		case "keep":
			row.What = "Appeal turned down: suspension stands"
		case "cleanup":
			row.What = "Account cleaned up (" + reasonLabel(e.Reason) + "): " + e.Detail
		case "shadowban":
			row.What = "Spam account silenced"
		case "unshadowban":
			row.What = "Silencing lifted"
		case "remove":
			row.What = kind + " removed: " + reasonLabel(e.Reason)
		case "hold":
			row.What = kind + " hidden until reviewed"
		case "restore":
			row.What = kind + " put back after review"
		case "dismiss":
			row.What = "Reports on a " + strings.ToLower(kind) + " dismissed"
		}
		if e.Reports > 0 {
			row.Detail = "after " + strconv.Itoa(e.Reports) + " report"
			if e.Reports > 1 {
				row.Detail += "s"
			}
		}
		data.Rows = append(data.Rows, row)
	}
	data.Generated = time.Since(start)
	s.render(w, "modlog.html", data)
}

// GET /about
func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	data := newBasePage(r, "about").withUp("/", "front page")
	data.Generated = time.Since(start)
	s.render(w, "about.html", data)
}

// GET /rules
func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	data := newBasePage(r, "rules").withUp("/", "front page")
	data.Generated = time.Since(start)
	s.render(w, "rules.html", data)
}
