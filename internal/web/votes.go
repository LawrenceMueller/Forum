package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"snowball/internal/store"
)

// New accounts wait this long before voting: the first piece of "probation",
// so a freshly made account can't immediately pile votes onto something.
const voteMinAccountAge = 24 * time.Hour

// voteWidget drives partials/votes.html: the two arrows and the score
// between them, for one post or comment.
type voteWidget struct {
	Kind     string // "post" or "comment"
	ID       string // public ID
	Score    int
	Mine     int // the viewer's vote: 1, -1, or 0
	CanVote  bool
	LoggedIn bool
	Reason   string // why the arrows are switched off, shown on hover
	Note     string // a message after a vote that couldn't be recorded
	Token    string // form guard token
	Back     string // where to return without JavaScript
	Inline   bool   // horizontal (post page, comments) rather than stacked (lists)
}

// voteViewer is what every widget on a page needs to know about the viewer.
// It's built once per page.
type voteViewer struct {
	user      store.User
	loggedIn  bool
	voter     string // the viewer's vote fingerprint
	eligible  bool   // old enough to vote
	suspended bool   // read-only for now (moderation)
	token     string
	back      string
}

func (s *Server) voteViewerFor(r *http.Request) voteViewer {
	u, ok := userFrom(r.Context())
	v := voteViewer{user: u, loggedIn: ok, back: r.URL.RequestURI()}
	if ok {
		v.voter = s.voters.Voter(u.ID)
		v.eligible = time.Since(u.CreatedAt) >= voteMinAccountAge
		v.suspended = u.Suspension != nil
		v.token = s.formToken(formVote)
	}
	return v
}

// widget builds the arrows for one item. closed means the item (or the
// post it's on) was removed or deleted.
func (v voteViewer) widget(kind, id string, score, mine int, author string, authorDeleted, closed, inline bool) voteWidget {
	w := voteWidget{
		Kind: kind, ID: id, Score: score, Mine: mine,
		LoggedIn: v.loggedIn, Token: v.token, Back: v.back, Inline: inline,
	}
	switch {
	case !v.loggedIn:
		// the template shows the arrows as links to the login page
	case closed:
		w.Reason = "Voting is closed on this."
	case !authorDeleted && author != "" && author == v.user.Username:
		w.Reason = "This one's yours."
	case v.suspended:
		w.Reason = "Your account is suspended, so it can't vote for now."
	case !v.eligible:
		w.Reason = "New accounts can vote after their first day."
	default:
		w.CanVote = true
	}
	return w
}

// canReport says whether the viewer may report an item: logged in, past
// the first day (the voting rule), not their own, and still up.
func (v voteViewer) canReport(author string, authorDeleted, gone bool) bool {
	return v.loggedIn && v.eligible && !v.suspended && !gone && (authorDeleted || author != v.user.Username)
}

// myVotes looks up which of these items the viewer has voted on.
// Logged-out viewers have no votes, so no query is made.
func (s *Server) myVotes(r *http.Request, v voteViewer, t store.VoteTarget, ids []int64) (map[int64]int, error) {
	if !v.loggedIn {
		return map[int64]int{}, nil
	}
	return s.store.MyVotes(r.Context(), t, v.voter, ids)
}

// safeBack accepts only a path on this site as a return address. Anything
// else ("//evil.example", "https://...") becomes the front page, so the
// vote form can't be used to bounce people to another site.
//
// Control characters are refused too: browsers silently delete tabs and
// newlines from a URL before following it, so "/<tab>/evil.example" would
// become "//evil.example", another site. (Found by FuzzSafeBack.)
func safeBack(back string) string {
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") || strings.Contains(back, `\`) {
		return "/"
	}
	if strings.ContainsFunc(back, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return "/"
	}
	return back
}

// voteLimitNote checks the vote limits and, only if all of them allow it,
// spends a token from each. Peeking first means a vote refused by one limit
// doesn't quietly use up the others. It returns "" when the vote may go
// ahead, or a message saying which limit was reached.
//
// The fast limit counts every click, which is what stops arrow-mashing.
// The daily budget and per-author limit count only newVote clicks (voting
// on something not already voted on): switching direction or taking a vote
// back doesn't use them up.
//
// The keys exist only in this process's memory and are forgotten once their
// buckets refill. The per-author key pairs the voter's fingerprint with the
// author's ID, which is exactly the link the votes tables refuse to store,
// so it must never be logged or written anywhere.
func (s *Server) voteLimitNote(userID int64, voter string, authorID int64, newVote bool) string {
	account := "user:" + strconv.FormatInt(userID, 10)
	type check struct {
		lim     *limiter
		key     string
		message string
	}
	checks := []check{
		{s.limits.voting, account, "Slow down a little. Try again in %s."},
	}
	if newVote {
		checks = append(checks,
			check{s.limits.votingDaily, account, "You've used today's votes. The next one arrives in %s."},
			check{s.limits.votingAuthor, voter + ":" + strconv.FormatInt(authorID, 10),
				"That's a lot of votes on this person's posts today. Try again in %s."},
		)
	}

	for _, c := range checks {
		if left, wait := c.lim.status(c.key); left < 1 {
			return strings.Replace(c.message, "%s", friendlyDuration(wait), 1)
		}
	}
	for _, c := range checks {
		c.lim.allow(c.key)
	}
	return ""
}

// POST /vote
func (s *Server) handleVote(w http.ResponseWriter, r *http.Request) {
	htmx := r.Header.Get("HX-Request") == "true"

	u, ok := userFrom(r.Context())
	if !ok {
		if htmx {
			w.Header().Set("HX-Redirect", "/login") // tells htmx to load a whole new page
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	formProblem, handled := s.checkForm(w, r, formVote)
	if handled {
		return
	}

	kind := r.PostForm.Get("kind")
	id := r.PostForm.Get("id")
	back := safeBack(r.PostForm.Get("back"))

	var target store.VoteTarget
	switch kind {
	case "post":
		target = store.VotePost
	case "comment":
		target = store.VoteComment
	default:
		http.Error(w, "bad vote", http.StatusBadRequest)
		return
	}
	if !publicIDPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	dir := 0
	switch r.PostForm.Get("dir") {
	case "up":
		dir = 1
	case "down":
		dir = -1
	default:
		http.Error(w, "bad vote", http.StatusBadRequest)
		return
	}

	viewer := s.voteViewerFor(r)
	viewer.back = back

	// Work out whether the vote can go through. Each problem becomes a short
	// note under the arrows (with htmx) or is quietly skipped (without).
	note := ""
	temporary := false // a problem that goes away on its own: keep the arrows clickable
	switch {
	case formProblem != "":
		note, temporary = "This page is out of date. Refresh it to vote.", true
	case !viewer.eligible:
		note = "New accounts can vote after their first day."
	default:
		author, current, err := s.store.VoteAuthor(ctx, target, id, viewer.voter)
		if errors.Is(err, store.ErrVoteNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		// Switching or taking back a vote isn't a NEW vote on this
		// person's content, so only the fast limit applies to it.
		if note = s.voteLimitNote(u.ID, viewer.voter, author, current == 0); note != "" {
			temporary = true
		}
	}

	var score, mine int
	var err error
	if note == "" && u.Shadowbanned {
		// A spam bot's vote is dropped without a word: it gets the arrows
		// it expects, and nothing is stored or counted.
		if score, mine, err = s.store.VoteState(ctx, target, id, viewer.voter); err != nil {
			serverError(w, err)
			return
		}
		mine = dir
		if !htmx {
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		s.renderFragment(w, "postpage.html", "votes", voteWidget{
			Kind: kind, ID: id, Score: score + dir, Mine: mine, CanVote: true, LoggedIn: true,
			Token: viewer.token, Back: back, Inline: r.PostForm.Get("inline") == "1",
		})
		return
	}
	if note == "" {
		score, mine, err = s.store.Vote(ctx, target, id, viewer.voter, u.ID, dir)
		switch {
		case errors.Is(err, store.ErrVoteNotFound):
			http.NotFound(w, r)
			return
		case errors.Is(err, store.ErrOwnContent):
			note = "You can't vote on your own."
		case errors.Is(err, store.ErrVoteLocked):
			note = "Voting is closed on this."
		case err != nil:
			serverError(w, err)
			return
		}
	}

	if !htmx {
		// Without JavaScript: back to the page, where the arrows show the result.
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}

	// With htmx: send back just the arrows, which replace the old ones.
	if note != "" {
		if score, mine, err = s.store.VoteState(ctx, target, id, viewer.voter); err != nil {
			serverError(w, err)
			return
		}
	}
	widget := voteWidget{
		Kind: kind, ID: id, Score: score, Mine: mine,
		CanVote: note == "" || temporary, LoggedIn: true,
		Note: note, Token: viewer.token, Back: back,
		Inline: r.PostForm.Get("inline") == "1",
	}
	s.renderFragment(w, "postpage.html", "votes", widget)
}
