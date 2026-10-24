package web

// Mega polls: the daily question (store/megapolls.go, migration 00021).
//
//   - The admin queues them at /admin/polls, one per day: a question, an
//     optional bit of text, 2-6 options and topics.
//   - Days run midnight to midnight Pacific time (siteZone). Once a minute
//     (statsLoop) and at startup, publishMegaPolls publishes today's entry
//     as a post from "Snowball" with a mega poll that closes at midnight.
//   - Today's mega poll gets a box at the top of the front page, where
//     people can vote straight away (the form sends them to the post). Its
//     results stay hidden until it closes, even after voting.
//   - /polls lists every published mega poll with how it came out.
//
// /admin is the admin's hub: the admin pages in one place, with what needs
// attention (reports, appeals, the mega poll queue).

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	_ "time/tzdata" // the server may not have time zone files installed
	"unicode/utf8"

	"snowball/internal/store"
	"snowball/internal/tags"
)

// siteZone is the time zone the site's days follow: mega polls change at
// midnight here.
var siteZone = func() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		panic(err) // can't happen: time/tzdata is built in
	}
	return loc
}()

// siteDay is the date (at midnight UTC, as the store keeps dates) it is at
// t on the site's clock.
func siteDay(t time.Time) time.Time {
	y, m, d := t.In(siteZone).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// dayEnd is the moment day ends on the site's clock: the next midnight.
func dayEnd(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, siteZone)
}

const dayFormat = "2006-01-02"

// megaToday is today's published mega poll, kept in memory for the front
// page (refreshed each minute, and when one is published).
type megaToday struct {
	PostID   int64
	PublicID string
	Title    string
}

// publishMegaPolls publishes today's queued mega poll, if it hasn't been,
// and refreshes the front page's copy. Safe to call any number of times.
func (s *Server) publishMegaPolls(ctx context.Context) {
	day := siteDay(time.Now())
	m, found, err := s.store.MegaPollFor(ctx, day)
	if err != nil {
		log.Printf("mega poll: %v", err)
		return
	}
	if found && !m.Published() {
		ids, problems := s.tree().Choose(tags.Selection{Topics: m.Topics, Format: m.Format, Level: m.Level})
		if len(problems) > 0 {
			if m.Problem == "" {
				log.Printf("mega poll for %s not published: its topics no longer match", day.Format(dayFormat))
			}
			if err := s.store.SetMegaProblem(ctx, day, strings.Join(problems, " ")); err != nil {
				log.Printf("mega poll: %v", err)
			}
		} else if id, err := s.store.PublishMegaPoll(ctx, day, ids, dayEnd(day)); err != nil {
			log.Printf("mega poll: publishing: %v", err)
		} else if id != "" {
			log.Printf("mega poll for %s published", day.Format(dayFormat))
			s.refreshStats()
		}
	}
	if err := s.loadMegaToday(ctx, day); err != nil {
		log.Printf("mega poll: %v", err)
	}
}

func (s *Server) loadMegaToday(ctx context.Context, day time.Time) error {
	p, found, err := s.store.MegaPollPost(ctx, day)
	if err != nil {
		return err
	}
	if !found {
		s.megaToday.Store(nil)
		return nil
	}
	s.megaToday.Store(&megaToday{PostID: p.ID, PublicID: p.PublicID, Title: p.Title})
	return nil
}

// ---- the front page's box ----

// megaBox drives the box at the top of the front page.
type megaBox struct {
	PublicID string
	Title    string
	Poll     pollView
}

// megaBoxFor builds today's box for this viewer (nil when there's none).
func (s *Server) megaBoxFor(r *http.Request) (*megaBox, error) {
	mt := s.megaToday.Load()
	if mt == nil {
		return nil, nil
	}
	post := store.Post{ID: mt.PostID, PublicID: mt.PublicID, IsPoll: true}
	poll, st, err := s.pollFor(r, post, s.voteViewerFor(r))
	if err != nil || poll == nil {
		return nil, err
	}
	v := s.newPollView(mt.PublicID, poll, st)
	v.Live = false // the box is a plain form that leads to the post; no refreshing here
	return &megaBox{PublicID: mt.PublicID, Title: mt.Title, Poll: v}, nil
}

// ---- /polls ----

type archivedView struct {
	store.ArchivedMegaPoll
	Date        string // "Fri Oct 23"
	Open        bool
	WinnerShare int // percent
	Tie         bool
}

type pollsPage struct {
	basePage
	Polls []archivedView
	Older string // the next page's ?before=
}

const archivePage = 30

// GET /polls (?before=2026-10-01 for older)
func (s *Server) handlePollArchive(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var before *time.Time
	if b := r.URL.Query().Get("before"); b != "" {
		d, err := time.Parse(dayFormat, b)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		before = &d
	}
	list, next, err := s.store.MegaPollArchive(r.Context(), before, archivePage)
	if err != nil {
		serverError(w, err)
		return
	}
	data := pollsPage{basePage: newBasePage(r, "mega polls").withUp("/", "front page")}
	now := time.Now()
	for _, a := range list {
		v := archivedView{ArchivedMegaPoll: a, Date: a.Day.Format("Mon Jan 2, 2006"), Open: now.Before(a.ClosesAt)}
		if v.Open {
			v.Winners = nil // no results until it closes
		} else if a.Total > 0 {
			v.WinnerShare = int(float64(a.WinnerVotes)*100/float64(a.Total) + 0.5)
			v.Tie = len(a.Winners) > 1
		}
		data.Polls = append(data.Polls, v)
	}
	if next != nil {
		data.Older = next.Format(dayFormat)
	}
	data.Generated = time.Since(start)
	s.render(w, "polls.html", data)
}

// ---- /admin/polls: the queue ----

type megaEntryView struct {
	store.MegaPoll
	DayText string // "2026-10-24"
	Label   string // "today", "tomorrow", "Sat Oct 31"
	Past    bool
	Today   bool
	Missed  bool // its day passed without it being published
}

type adminPollsPage struct {
	basePage
	FormToken string
	Notice    string
	Errors    []string
	Entries   []megaEntryView
	Editing   string // the day being edited ("" for a new one)
	Form      megaForm
	QueuedTo  string // the last queued day
	Today     megaTodayPanel
	Back      string // for the control forms: come back here
}

// megaForm is the add/edit form, as typed.
type megaForm struct {
	Day      string
	Question string
	Body     string
	Options  []string
	Tags     tagFields
}

var megaNotices = map[string]string{
	"added":     "Queued.",
	"published": "Queued for today, so it's on the front page now.",
	"saved":     "Saved.",
	"deleted":   "Deleted from the queue.",
	"taken-down": "Today's mega poll is down, and so is the front page box. " +
		"You can run tomorrow's now, below.",
	"ran-next":  "Tomorrow's is up now, and everything after it moved up a day.",
	"still-up":  "Today's mega poll is still up: take it down first.",
	"waiting":   "Today's hasn't gone up yet. Fix it (or delete it) rather than skip it.",
	"queue-dry": "There's nothing queued after today to run.",
}

// megaTodayPanel is the "today" section of /admin/polls: today's mega poll
// and the controls for it.
type megaTodayPanel struct {
	Up       bool // published and still up
	PublicID string
	Title    string
	Locked   bool
	Open     bool   // voting still open
	Next     string // the next queued question, for "run it now"
}

func (s *Server) newMegaForm(day, question, body string, options []string, sel tags.Selection) megaForm {
	f := megaForm{Day: day, Question: question, Body: body, Options: make([]string, store.MaxPollOptions),
		Tags: s.tagFieldsFor(sel)}
	copy(f.Options, options)
	return f
}

// GET /admin/polls, and GET /admin/polls/{day} to edit one.
func (s *Server) handleAdminPolls(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	editing := r.PathValue("day")
	if editing == "" {
		s.renderAdminPolls(w, r, start, "", s.newMegaForm(s.nextFreeDay(r.Context()), "", "", nil, tags.Selection{}), nil)
		return
	}
	day, err := time.Parse(dayFormat, editing)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	m, found, err := s.store.MegaPollFor(r.Context(), day)
	if err != nil {
		serverError(w, err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	if m.Published() {
		http.Redirect(w, r, "/p/"+m.PostPublicID, http.StatusSeeOther)
		return
	}
	f := s.newMegaForm(editing, m.Question, m.Body, m.Options, tags.Selection{Topics: m.Topics, Format: m.Format, Level: m.Level})
	s.renderAdminPolls(w, r, start, editing, f, nil)
}

// nextFreeDay suggests a day for a new entry: the first one from today
// without an entry.
func (s *Server) nextFreeDay(ctx context.Context) string {
	day := siteDay(time.Now())
	entries, err := s.store.MegaPollsFrom(ctx, day, 400)
	if err != nil {
		return day.Format(dayFormat)
	}
	for _, e := range entries {
		if !e.Day.Equal(day) {
			break
		}
		day = day.AddDate(0, 0, 1)
	}
	return day.Format(dayFormat)
}

func (s *Server) renderAdminPolls(w http.ResponseWriter, r *http.Request, start time.Time, editing string, f megaForm, errs []string) {
	today := siteDay(time.Now())
	entries, err := s.store.MegaPollsFrom(r.Context(), today.AddDate(0, 0, -7), 400)
	if err != nil {
		serverError(w, err)
		return
	}
	data := adminPollsPage{
		basePage:  newBasePage(r, "mega polls (admin)").withUp("/admin", "admin"),
		FormToken: s.formToken(formModerate),
		Notice:    megaNotices[r.URL.Query().Get("done")],
		Errors:    errs,
		Editing:   editing,
		Form:      f,
	}
	if editing != "" {
		data.basePage = data.basePage.withUp("/admin/polls", "mega polls")
	}
	for _, e := range entries {
		v := megaEntryView{MegaPoll: e, DayText: e.Day.Format(dayFormat), Past: e.Day.Before(today), Today: e.Day.Equal(today)}
		switch {
		case v.Today:
			v.Label = "today"
		case e.Day.Equal(today.AddDate(0, 0, 1)):
			v.Label = "tomorrow"
		default:
			v.Label = e.Day.Format("Mon Jan 2")
		}
		v.Missed = v.Past && !e.Published()
		if !v.Past && !e.Published() {
			data.QueuedTo = v.DayText
		}
		data.Entries = append(data.Entries, v)
	}
	data.Back = "/admin/polls"
	if data.Today, err = s.megaTodayPanel(r.Context(), today, entries); err != nil {
		serverError(w, err)
		return
	}
	data.Generated = time.Since(start)
	s.render(w, "adminpolls.html", data)
}

// POST /admin/polls (a new entry), and POST /admin/polls/{day}
// (action=save, or action=delete with confirm=1).
func (s *Server) handleAdminPollChange(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	u, _ := userFrom(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	problem, handled := s.checkForm(w, r, formModerate)
	if handled {
		return
	}

	editing := r.PathValue("day")
	var oldDay *time.Time
	if editing != "" {
		d, err := time.Parse(dayFormat, editing)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		oldDay = &d
	}

	question := strings.TrimSpace(r.PostForm.Get("question"))
	body := strings.TrimSpace(strings.ReplaceAll(r.PostForm.Get("body"), "\r\n", "\n"))
	options, errs := pollOptionsFrom(r)
	sel := selectionFrom(r)
	typedDay := strings.TrimSpace(r.PostForm.Get("day"))
	form := s.newMegaForm(typedDay, question, body, options, sel)
	if problem != "" {
		errs = append([]string{problem}, errs...)
	}

	if r.PostForm.Get("action") == "delete" && oldDay != nil {
		if r.PostForm.Get("confirm") != "1" {
			s.renderAdminPolls(w, r, start, editing, form, []string{"Tick the box to confirm deleting it."})
			return
		}
		if err := s.store.DeleteMegaPoll(r.Context(), *oldDay); err != nil {
			s.megaStoreError(w, r, start, editing, form, err)
			return
		}
		http.Redirect(w, r, "/admin/polls?done=deleted", http.StatusSeeOther)
		return
	}

	today := siteDay(time.Now())
	day, err := time.Parse(dayFormat, typedDay)
	switch {
	case err != nil:
		errs = append(errs, "Pick a day.")
	case day.Before(today):
		errs = append(errs, "That day has already been; pick today or later.")
	}
	switch n := utf8.RuneCountInString(question); {
	case n == 0:
		errs = append(errs, "Write the question.")
	case n > maxTitleLen:
		errs = append(errs, "Questions can be up to 200 characters.")
	}
	if utf8.RuneCountInString(body) > 2000 {
		errs = append(errs, "The text can be up to 2,000 characters.")
	}
	if len(options) == 0 {
		errs = append(errs, "A poll needs at least two options.")
	}
	_, tagProblems := s.tree().Choose(sel)
	errs = append(errs, tagProblems...)
	if len(errs) > 0 {
		s.renderAdminPolls(w, r, start, editing, form, errs)
		return
	}

	m := store.MegaPoll{Day: day, Question: question, Body: body, Options: options,
		Topics: sel.Topics, Format: sel.Format, Level: sel.Level, CreatedBy: u.ID}
	if err := s.store.SaveMegaPoll(r.Context(), oldDay, m); err != nil {
		s.megaStoreError(w, r, start, editing, form, err)
		return
	}
	done := "added"
	if oldDay != nil {
		done = "saved"
	}
	if day.Equal(today) {
		s.publishMegaPolls(r.Context())
		done = "published"
	}
	http.Redirect(w, r, "/admin/polls?done="+done, http.StatusSeeOther)
}

func (s *Server) megaStoreError(w http.ResponseWriter, r *http.Request, start time.Time, editing string, form megaForm, err error) {
	switch {
	case errors.Is(err, store.ErrMegaDayTaken):
		s.renderAdminPolls(w, r, start, editing, form, []string{"There's already a mega poll queued for " + form.Day + "."})
	case errors.Is(err, store.ErrMegaPublished):
		s.renderAdminPolls(w, r, start, "", form, []string{"That one has been published, so it's a post now: change it there."})
	case errors.Is(err, store.ErrMegaNotFound):
		s.renderAdminPolls(w, r, start, "", form, []string{"That one isn't in the queue any more."})
	default:
		serverError(w, err)
	}
}

// ---- /admin: the hub ----

type adminHubPage struct {
	basePage
	OpenReports int64
	OpenAppeals int64
	Today       *megaEntryView // today's queue entry, if any
	Queued      int            // entries from tomorrow on
	QueuedTo    string         // the last of them
}

// GET /admin
func (s *Server) handleAdminHub(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()
	appeals, err := s.store.OpenAppealCount(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	today := siteDay(time.Now())
	entries, err := s.store.MegaPollsFrom(ctx, today, 400)
	if err != nil {
		serverError(w, err)
		return
	}
	data := adminHubPage{
		basePage:    newBasePage(r, "admin").withUp("/account", "your account"),
		OpenReports: s.stats.openReports.Load(),
		OpenAppeals: appeals,
	}
	for _, e := range entries {
		if e.Day.Equal(today) {
			data.Today = &megaEntryView{MegaPoll: e, DayText: e.Day.Format(dayFormat), Today: true}
			continue
		}
		data.Queued++
		data.QueuedTo = e.Day.Format("Mon Jan 2")
	}
	data.Generated = time.Since(start)
	s.render(w, "adminhub.html", data)
}

// megaTodayPanel works out today's controls from the queue.
func (s *Server) megaTodayPanel(ctx context.Context, today time.Time, entries []store.MegaPoll) (megaTodayPanel, error) {
	var p megaTodayPanel
	for _, e := range entries {
		if e.Day.After(today) && !e.Published() && p.Next == "" {
			p.Next = e.Question
		}
	}
	post, found, err := s.store.MegaPollPost(ctx, today)
	if err != nil || !found {
		return p, err
	}
	p.Up, p.PublicID, p.Title, p.Locked = true, post.PublicID, post.Title, post.Locked
	poll, err := s.store.PollForPost(ctx, post.ID)
	if err != nil {
		return p, err
	}
	p.Open = poll != nil && poll.Open()
	return p, nil
}
