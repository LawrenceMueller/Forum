package web

// Suspensions and appeals (moderation slice 2). A suspended account is
// read-only: it can log in, read, change its password, and delete its own
// posts, comments or whole account, but not post, comment, vote, report or
// edit. The routes that write go through notSuspended; the pages hide the
// controls (voteViewer.suspended). A banner on every page says why and until
// when, and links to /account/suspension, which holds the one appeal.
//
//	GET, POST /account/suspension         the details, and the appeal form
//	GET  /admin/users/{name}              an account, for the admin
//	POST /admin/users/{name}/suspend      suspend it
//	POST /admin/users/{name}/lift         lift its suspension
//	POST /admin/appeals                   decide an appeal (lift or keep)

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"snowball/internal/store"
)

const (
	maxAppealLen       = 500
	minAppealLen       = 10
	maxSuspensionNote  = 500
	adminHistoryLength = 10
)

// suspensionBanner is the strip at the top of every page while suspended.
type suspensionBanner struct {
	Reason    string // in words
	Until     string // "2026-10-21 18:04"; "" when permanent
	Permanent bool
}

func bannerFor(s *store.Suspension) *suspensionBanner {
	b := &suspensionBanner{Reason: reasonLabel(s.Reason), Permanent: s.Permanent()}
	if s.EndsAt != nil {
		b.Until = s.EndsAt.Format("2006-01-02 15:04")
	}
	return b
}

// notSuspended wraps a handler that writes something (posting, commenting,
// voting, reporting, editing). A suspended account is sent to the page
// explaining its suspension; htmx requests get a short refusal instead.
func (s *Server) notSuspended(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u, ok := userFrom(r.Context()); ok && u.Suspension != nil {
			if r.Header.Get("HX-Request") == "true" {
				http.Error(w, "Your account is suspended, so it can't do that for now.", http.StatusForbidden)
				return
			}
			http.Redirect(w, r, "/account/suspension", http.StatusSeeOther)
			return
		}
		h(w, r)
	}
}

// lengthLabel is a suspension's length in words, for forms and the log.
func lengthLabel(days int) string {
	switch days {
	case 0:
		return "permanently"
	case 1:
		return "1 day"
	}
	return strconv.Itoa(days) + " days"
}

type lengthOption struct {
	Days  int
	Label string
}

func lengthOptions() []lengthOption {
	out := make([]lengthOption, len(store.SuspensionLengths))
	for i, d := range store.SuspensionLengths {
		out[i] = lengthOption{Days: d, Label: lengthLabel(d)}
	}
	return out
}

// ---- the suspended person's page ----

type suspensionPage struct {
	basePage
	Sus       store.Suspension
	Reason    string
	Until     string
	FormToken string
	Appeal    string
	Errors    []string
	Sent      bool
}

// GET /account/suspension
func (s *Server) handleSuspensionPage(w http.ResponseWriter, r *http.Request) {
	s.renderSuspension(w, r, time.Now(), "", nil, false)
}

func (s *Server) renderSuspension(w http.ResponseWriter, r *http.Request, start time.Time, appeal string, errs []string, sent bool) {
	u, ok := userFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if u.Suspension == nil {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	sus := *u.Suspension
	if sent { // the context's copy predates the appeal just filed
		now := time.Now()
		sus.Appeal, sus.AppealedAt = appeal, &now
	}
	data := suspensionPage{
		basePage:  newBasePage(r, "your suspension").withUp("/account", "your account"),
		Sus:       sus,
		Reason:    reasonLabel(sus.Reason),
		FormToken: s.formToken(formAppeal),
		Appeal:    appeal,
		Errors:    errs,
		Sent:      sent,
	}
	if sus.EndsAt != nil {
		data.Until = sus.EndsAt.Format("2006-01-02 15:04")
	}
	data.Generated = time.Since(start)
	s.render(w, "suspension.html", data)
}

// POST /account/suspension (the appeal)
func (s *Server) handleAppeal(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	u, ok := userFrom(r.Context())
	if !ok || u.Suspension == nil {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	problem, handled := s.checkForm(w, r, formAppeal)
	if handled {
		return
	}
	text := strings.TrimSpace(r.PostForm.Get("appeal"))
	var errs []string
	if problem != "" {
		errs = append(errs, problem)
	}
	switch n := utf8.RuneCountInString(text); {
	case n < minAppealLen:
		errs = append(errs, "Say a little about why the suspension should be lifted.")
	case n > maxAppealLen:
		errs = append(errs, "Keep the appeal to 500 characters.")
	}
	if len(errs) > 0 {
		s.renderSuspension(w, r, start, text, errs, false)
		return
	}
	err := s.store.Appeal(r.Context(), u.ID, u.Suspension.ID, text)
	switch {
	case errors.Is(err, store.ErrAlreadyAppealed):
		s.renderSuspension(w, r, start, "", []string{"You've already appealed this suspension."}, false)
		return
	case errors.Is(err, store.ErrNotSuspended):
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	s.refreshReports()
	s.renderSuspension(w, r, start, text, nil, true)
}

// ---- the admin's view of an account ----

type adminUserPage struct {
	basePage
	FormToken  string
	Notice     string
	Errors     []string
	Account    store.AccountInfo
	Active     *store.Suspension
	History    []suspensionRow
	Posts      []store.HistoryPost
	Comments   []store.HistoryComment
	Reasons    []reasonOption
	Lengths    []lengthOption
	CanSuspend bool
}

type suspensionRow struct {
	store.Suspension
	Reason, Length, State string
}

var adminUserNotices = map[string]string{
	"suspended":      "Suspended.",
	"lifted":         "Suspension lifted.",
	"cleaned":        "Cleaned up: everything they posted is removed, and their votes are withdrawn.",
	"shadowbanned":   "Shadowbanned: from now on only they see what they post, and their votes and reports go nowhere.",
	"unshadowbanned": "Shadowban lifted. What they posted while it was on stays hidden.",
}

// GET /admin/users/{name} (also /admin/users?name= from the lookup form)
func (s *Server) handleAdminUser(w http.ResponseWriter, r *http.Request) {
	if name := r.PathValue("name"); name == "" {
		http.Redirect(w, r, "/admin/users/"+strings.TrimSpace(r.URL.Query().Get("name")), http.StatusSeeOther)
		return
	}
	s.renderAdminUser(w, r, time.Now(), nil)
}

func (s *Server) renderAdminUser(w http.ResponseWriter, r *http.Request, start time.Time, errs []string) {
	ctx := r.Context()
	acct, err := s.store.AccountByName(ctx, r.PathValue("name"))
	if errors.Is(err, store.ErrUserNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	history, err := s.store.Suspensions(ctx, acct.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	posts, _, err := s.store.MyPosts(ctx, acct.ID, nil, adminHistoryLength)
	if err != nil {
		serverError(w, err)
		return
	}
	comments, _, err := s.store.MyComments(ctx, acct.ID, nil, adminHistoryLength)
	if err != nil {
		serverError(w, err)
		return
	}
	data := adminUserPage{
		basePage:   newBasePage(r, acct.Username+" (admin)").withUp("/admin/reports", "reports"),
		FormToken:  s.formToken(formModerate),
		Notice:     adminUserNotices[r.URL.Query().Get("done")],
		Errors:     errs,
		Account:    acct,
		Posts:      posts,
		Comments:   comments,
		Reasons:    reasonOptions(store.RemovalReasons),
		Lengths:    lengthOptions(),
		CanSuspend: !acct.IsAdmin && acct.DeletedAt == nil,
	}
	now := time.Now()
	for _, sus := range history {
		row := suspensionRow{Suspension: sus, Reason: reasonLabel(sus.Reason)}
		switch {
		case sus.EndsAt == nil:
			row.Length = "permanent"
		default:
			row.Length = "until " + sus.EndsAt.Format("2006-01-02 15:04")
		}
		switch {
		case sus.Active(now):
			row.State = "in force"
			active := sus
			data.Active = &active
		case sus.LiftedAt != nil:
			row.State = "lifted " + sus.LiftedAt.Format("2006-01-02")
		default:
			row.State = "ended"
		}
		data.History = append(data.History, row)
	}
	for i := range data.Comments {
		data.Comments[i].Body = snippet(data.Comments[i].Body, 160)
	}
	data.Generated = time.Since(start)
	s.render(w, "adminuser.html", data)
}

// adminUserForm parses one of the account page's forms. ok is false when
// the response has been written already.
func (s *Server) adminUserForm(w http.ResponseWriter, r *http.Request, start time.Time) (store.AccountInfo, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return store.AccountInfo{}, false
	}
	problem, handled := s.checkForm(w, r, formModerate)
	if handled {
		return store.AccountInfo{}, false
	}
	if problem != "" {
		s.renderAdminUser(w, r, start, []string{problem})
		return store.AccountInfo{}, false
	}
	acct, err := s.store.AccountByName(r.Context(), r.PathValue("name"))
	if errors.Is(err, store.ErrUserNotFound) {
		http.NotFound(w, r)
		return acct, false
	}
	if err != nil {
		serverError(w, err)
		return acct, false
	}
	return acct, true
}

// suspendFromForm reads reason, length and note, and suspends. It returns
// a problem to show, or "".
func (s *Server) suspendFromForm(r *http.Request, userID int64) (string, error) {
	reason := r.PostForm.Get("reason")
	days, err := strconv.Atoi(r.PostForm.Get("days"))
	note := strings.TrimSpace(r.PostForm.Get("note"))
	switch {
	case !slices.Contains(store.RemovalReasons, reason):
		return "Pick a reason for the suspension.", nil
	case err != nil || !slices.Contains(store.SuspensionLengths, days):
		return "Pick how long the suspension lasts.", nil
	case utf8.RuneCountInString(note) > maxSuspensionNote:
		return "Keep the note to 500 characters.", nil
	}
	err = s.store.Suspend(r.Context(), userID, reason, note, days)
	switch {
	case errors.Is(err, store.ErrCantSuspendAdmin):
		return "The admin account can't be suspended.", nil
	case errors.Is(err, store.ErrUserNotFound):
		return "That account doesn't exist any more.", nil
	}
	return "", err
}

// POST /admin/users/{name}/suspend
func (s *Server) handleAdminSuspend(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	acct, ok := s.adminUserForm(w, r, start)
	if !ok {
		return
	}
	problem, err := s.suspendFromForm(r, acct.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if problem != "" {
		s.renderAdminUser(w, r, start, []string{problem})
		return
	}
	http.Redirect(w, r, "/admin/users/"+acct.Username+"?done=suspended", http.StatusSeeOther)
}

// POST /admin/users/{name}/lift (suspension=<id>)
func (s *Server) handleAdminLift(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	acct, ok := s.adminUserForm(w, r, start)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PostForm.Get("suspension"), 10, 64)
	err := s.store.LiftSuspension(r.Context(), id)
	if errors.Is(err, store.ErrNotSuspended) {
		s.renderAdminUser(w, r, start, []string{"That suspension has already ended."})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	s.refreshReports()
	http.Redirect(w, r, "/admin/users/"+acct.Username+"?done=lifted", http.StatusSeeOther)
}

// POST /admin/appeals (suspension=<id>, action=lift|keep)
func (s *Server) handleAppealDecision(w http.ResponseWriter, r *http.Request) {
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
	id, _ := strconv.ParseInt(r.PostForm.Get("suspension"), 10, 64)
	var err error
	var done string
	switch r.PostForm.Get("action") {
	case "lift":
		err, done = s.store.LiftSuspension(r.Context(), id), "lifted"
	case "keep":
		err, done = s.store.KeepSuspension(r.Context(), id), "kept"
	default:
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if errors.Is(err, store.ErrNotSuspended) || errors.Is(err, store.ErrAppealNotPossible) {
		s.renderQueue(w, r, start, []string{"That suspension or appeal has already been dealt with."})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	s.refreshReports()
	http.Redirect(w, r, "/admin/reports?done="+done, http.StatusSeeOther)
}

// ---- cleanup and shadowbans (moderation slice 3) ----

// POST /admin/users/{name}/cleanup (reason=, confirm=1)
func (s *Server) handleAdminCleanup(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	acct, ok := s.adminUserForm(w, r, start)
	if !ok {
		return
	}
	reason := r.PostForm.Get("reason")
	switch {
	case !slices.Contains(store.RemovalReasons, reason):
		s.renderAdminUser(w, r, start, []string{"Pick a reason for the cleanup."})
		return
	case r.PostForm.Get("confirm") != "1":
		s.renderAdminUser(w, r, start, []string{"Tick the box to confirm the cleanup."})
		return
	}
	_, err := s.store.Cleanup(r.Context(), acct.ID, s.voters.Voter(acct.ID), s.voters.PollKey(acct.ID), reason)
	if errors.Is(err, store.ErrCantSuspendAdmin) {
		s.renderAdminUser(w, r, start, []string{"The admin's own things can't be cleaned up this way."})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	s.refreshStats()
	s.refreshReports()
	http.Redirect(w, r, "/admin/users/"+acct.Username+"?done=cleaned", http.StatusSeeOther)
}

// POST /admin/users/{name}/shadowban (action=on with confirm=1, or action=off)
func (s *Server) handleAdminShadowban(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	acct, ok := s.adminUserForm(w, r, start)
	if !ok {
		return
	}
	on := r.PostForm.Get("action") == "on"
	if on && r.PostForm.Get("confirm") != "1" {
		s.renderAdminUser(w, r, start, []string{"Tick the box to confirm this account is a spam bot."})
		return
	}
	err := s.store.SetShadowban(r.Context(), acct.ID, s.voters.Voter(acct.ID), s.voters.PollKey(acct.ID), on)
	if errors.Is(err, store.ErrCantSuspendAdmin) {
		s.renderAdminUser(w, r, start, []string{"The admin account can't be shadowbanned."})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	done := "unshadowbanned"
	if on {
		done = "shadowbanned"
	}
	http.Redirect(w, r, "/admin/users/"+acct.Username+"?done="+done, http.StatusSeeOther)
}
