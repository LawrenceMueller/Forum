package web

// Polls on posts (store/polls.go has the storage, migration 00020 the
// privacy rules).
//
//   - Anyone who can post can add a poll: 2-6 options, open for 3 hours to
//     a week. The options are fixed once posted, like the title and link.
//   - Votes are final. Before voting you see only the options; after, the
//     results, which refresh themselves every 15 seconds while the poll is
//     open (htmx polling, paused while the tab is in the background; see
//     snowball.js). Once a poll closes, everyone sees the final results.
//   - Voting follows the arrow rules: logged in, past the first day, not
//     suspended. A shadowbanned account's vote is dropped without a word.
//   - A comment on a poll shows the option its author picked, as a tag by
//     their name: voting alone shows nothing to anyone.

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"snowball/internal/store"
)

// pollDuration is one "open for" choice on the submit form.
type pollDuration struct {
	Value string
	Label string
	For   time.Duration
}

var pollDurations = []pollDuration{
	{"3h", "3 hours", 3 * time.Hour},
	{"12h", "12 hours", 12 * time.Hour},
	{"1d", "1 day", 24 * time.Hour},
	{"3d", "3 days", 3 * 24 * time.Hour},
	{"7d", "a week", 7 * 24 * time.Hour},
}

const defaultPollDuration = "1d"

// pollRefresh is how often open results refresh themselves. It's written
// into the page as htmx's "every 15s".
const pollRefresh = "15s"

// pollForm is the poll part of the submit form, as typed.
type pollForm struct {
	Options   []string // always MaxPollOptions slots, for the inputs
	Closes    string
	Durations []pollDuration
}

func newPollForm(options []string, closes string) pollForm {
	f := pollForm{Options: make([]string, store.MaxPollOptions), Closes: closes, Durations: pollDurations}
	copy(f.Options, options)
	if f.Closes == "" {
		f.Closes = defaultPollDuration
	}
	return f
}

// pollFromForm reads the poll fields of the submit form. A post with no
// options filled in has no poll (nil, no problems).
func pollFromForm(r *http.Request, now time.Time) (*store.NewPoll, pollForm, []string) {
	options, errs := pollOptionsFrom(r)
	form := newPollForm(options, r.PostForm.Get("closes"))
	if len(options) == 0 {
		return nil, form, nil
	}
	var length time.Duration
	for _, d := range pollDurations {
		if d.Value == form.Closes {
			length = d.For
		}
	}
	if length == 0 {
		errs = append(errs, "Pick how long the poll stays open.")
		form.Closes = defaultPollDuration
	}
	if len(errs) > 0 {
		return nil, form, errs
	}
	return &store.NewPoll{Options: options, ClosesAt: now.Add(length)}, form, nil
}

// pollOptionsFrom reads and checks the "option" fields (the submit form's,
// and the mega poll form's), dropping empty ones. With none filled in, there
// are no problems: the caller decides whether that's allowed.
func pollOptionsFrom(r *http.Request) (options, errs []string) {
	raw := r.PostForm["option"]
	if len(raw) > store.MaxPollOptions {
		raw = raw[:store.MaxPollOptions]
	}
	for _, o := range raw {
		if o = strings.Join(strings.Fields(o), " "); o != "" { // tidy spacing, drop empty slots
			options = append(options, o)
		}
	}
	if len(options) == 0 {
		return nil, nil
	}
	if len(options) < store.MinPollOptions {
		errs = append(errs, "A poll needs at least two options.")
	}
	seen := map[string]bool{}
	for _, o := range options {
		switch {
		case utf8.RuneCountInString(o) > store.MaxPollOptionLen:
			errs = append(errs, "Poll options can be up to "+strconv.Itoa(store.MaxPollOptionLen)+" characters.")
		case strings.ContainsFunc(o, unicode.IsControl):
			errs = append(errs, "Poll options can't contain control characters.")
		case seen[strings.ToLower(o)]:
			errs = append(errs, "Each poll option needs to be different (“"+o+"” is in twice).")
		}
		seen[strings.ToLower(o)] = true
	}
	return options, errs
}

// ---- showing a poll ----

// pollView drives partials/poll.html.
type pollView struct {
	PostID      string
	Mega        bool // results hidden until it closes
	Open        bool
	TimeLeft    string    // "2 days 4 hours", while open
	ClosesAt    time.Time // when it closes or closed
	Total       int
	ShowResults bool
	Voted       bool
	CanVote     bool
	LoggedIn    bool
	Reason      string // why the viewer can't vote, if they can't
	Note        string // after a vote that didn't go through
	Token       string
	Live        bool // the results refresh themselves
	Refresh     string
	Options     []pollOptionView
}

type pollOptionView struct {
	Index   int
	Label   string
	Votes   int
	Percent int
	Mine    bool
	Winner  bool   // the most votes, once closed
	Class   string // "pick-0".."pick-5": each option's colour
}

// pickClass is the colour class for option i, shared with the comment tags.
func pickClass(i int) string { return "pick-" + strconv.Itoa(i) }

// pollTimeLeft is "3 days 4 hours", "5 hours", "12 minutes".
func pollTimeLeft(d time.Duration) string {
	if d >= 24*time.Hour {
		days, hours := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour)
		if hours == 0 {
			return plural(days, "day")
		}
		return plural(days, "day") + " " + plural(hours, "hour")
	}
	if d >= time.Hour {
		return plural(int(d/time.Hour), "hour")
	}
	return friendlyDuration(d)
}

// pollState is what a poll view needs to know about the viewer and post.
type pollState struct {
	viewer voteViewer
	gone   bool // the post was removed or deleted
	voted  bool
	pick   int
}

func (s *Server) newPollView(postID string, p *store.Poll, st pollState) pollView {
	now := time.Now()
	v := pollView{
		PostID: postID, Mega: p.Mega, Open: p.Open() && !st.gone, ClosesAt: p.ClosesAt, Total: p.Total,
		Voted: st.voted, LoggedIn: st.viewer.loggedIn, Refresh: pollRefresh,
	}
	if v.Open {
		v.TimeLeft = pollTimeLeft(p.ClosesAt.Sub(now))
	}
	switch {
	case !v.Open || st.voted:
	case !st.viewer.loggedIn:
	case st.viewer.suspended:
		v.Reason = "Your account is suspended, so it can't vote for now."
	case !st.viewer.eligible:
		v.Reason = "New accounts can vote after their first day."
	default:
		v.CanVote, v.Token = true, s.formToken(formPoll)
	}
	// A mega poll keeps its results hidden until it closes, even from
	// those who've voted: they see their pick and the running total.
	v.ShowResults = !v.Open || (st.voted && !p.Mega) || st.viewer.user.IsAdmin
	v.Live = v.Open && (v.ShowResults || st.voted)

	best := 0
	for _, o := range p.Options {
		best = max(best, o.Votes)
	}
	for i, o := range p.Options {
		ov := pollOptionView{Index: i, Label: o.Label, Votes: o.Votes, Class: pickClass(i),
			Mine: st.voted && st.pick == i, Winner: !v.Open && best > 0 && o.Votes == best}
		if p.Total > 0 {
			ov.Percent = int(math.Round(100 * float64(o.Votes) / float64(p.Total)))
		}
		v.Options = append(v.Options, ov)
	}
	return v
}

// pollFor loads a post's poll and the viewer's pick in it (nil if the post
// has no poll).
func (s *Server) pollFor(r *http.Request, post store.Post, viewer voteViewer) (*store.Poll, pollState, error) {
	st := pollState{viewer: viewer, gone: post.Removed || post.Deleted}
	if !post.IsPoll {
		return nil, st, nil
	}
	p, err := s.store.PollForPost(r.Context(), post.ID)
	if err != nil || p == nil {
		return nil, st, err
	}
	if viewer.loggedIn {
		st.pick, st.voted, err = s.store.MyPollPick(r.Context(), post.ID, s.pollVoter(viewer.user.ID, post.ID))
	}
	return p, st, err
}

// pollVoter is an account's fingerprint in one poll.
func (s *Server) pollVoter(userID, postID int64) string {
	return store.PollVoter(s.voters.PollKey(userID), postID)
}

// pollLabels lists a poll's options, for the pick tags on comments.
func pollLabels(p *store.Poll) []string {
	if p == nil {
		return nil
	}
	labels := make([]string, len(p.Options))
	for i, o := range p.Options {
		labels[i] = o.Label
	}
	return labels
}

// ---- voting ----

// GET /p/{id}/poll: the poll box on its own, for htmx's refreshes.
func (s *Server) handlePollBox(w http.ResponseWriter, r *http.Request) {
	post, ok := s.loadPost(w, r)
	if !ok {
		return
	}
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, "/p/"+post.PublicID+"#poll", http.StatusSeeOther)
		return
	}
	viewer := s.voteViewerFor(r)
	if post.Removed && !viewer.user.IsAdmin {
		http.NotFound(w, r)
		return
	}
	poll, st, err := s.pollFor(r, post, viewer)
	if err != nil {
		serverError(w, err)
		return
	}
	if poll == nil {
		http.NotFound(w, r)
		return
	}
	s.renderFragment(w, "postpage.html", "poll", s.newPollView(post.PublicID, poll, st))
}

// POST /p/{id}/poll (option=N)
func (s *Server) handlePollVote(w http.ResponseWriter, r *http.Request) {
	htmx := r.Header.Get("HX-Request") == "true"
	u, ok := userFrom(r.Context())
	if !ok {
		if htmx {
			w.Header().Set("HX-Redirect", "/login")
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	post, ok := s.loadPost(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	formProblem, handled := s.checkForm(w, r, formPoll)
	if handled {
		return
	}
	back := "/p/" + post.PublicID + "#poll"
	viewer := s.voteViewerFor(r)

	pick, perr := strconv.Atoi(r.PostForm.Get("option"))
	note := ""
	switch {
	case formProblem != "":
		note = "This page is out of date. Refresh it to vote."
	case perr != nil || r.PostForm.Get("option") == "":
		note = "Pick an option first."
	case !viewer.eligible:
		note = "New accounts can vote after their first day."
	default:
		if ok, wait := s.limits.voting.allow("user:" + strconv.FormatInt(u.ID, 10)); !ok {
			note = "Slow down a little. Try again in " + friendlyDuration(wait) + "."
		}
	}

	shadowPick := -1
	if note == "" && u.Shadowbanned {
		shadowPick = pick // dropped without a word: shown as counted, never stored
	} else if note == "" {
		err := s.store.PollVote(r.Context(), post.ID, s.pollVoter(u.ID, post.ID), u.ID, pick)
		switch {
		case errors.Is(err, store.ErrPollNotFound):
			http.NotFound(w, r)
			return
		case errors.Is(err, store.ErrPollClosed):
			note = "This poll is closed."
		case errors.Is(err, store.ErrAlreadyVoted):
			note = "You've already voted in this poll."
		case errors.Is(err, store.ErrNoSuchOption):
			note = "Pick an option first."
		case err != nil:
			serverError(w, err)
			return
		}
	}

	if !htmx {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	poll, st, err := s.pollFor(r, post, viewer)
	if err != nil {
		serverError(w, err)
		return
	}
	if poll == nil {
		http.NotFound(w, r)
		return
	}
	if shadowPick >= 0 && shadowPick < len(poll.Options) && !st.voted {
		poll.Options[shadowPick].Votes++
		poll.Total++
		st.voted, st.pick = true, shadowPick
	}
	v := s.newPollView(post.PublicID, poll, st)
	v.Note = note
	if shadowPick >= 0 {
		v.Live = false // a refresh would show the vote was never counted
	}
	s.renderFragment(w, "postpage.html", "poll", v)
}
