package web

// The admin's controls for a thread getting out of hand (store/locks.go):
// locking a post's comments, and closing a poll early, from the post page
// (any post) or the mega poll page (today's). Plus, for mega polls: taking
// today's down, and running tomorrow's now with the queue moving up a day.

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"snowball/internal/store"
)

// refreshMegaToday reloads the front page's mega poll box, so a change to
// today's (taken down, closed) shows straight away rather than within a
// minute.
func (s *Server) refreshMegaToday(ctx context.Context) {
	if err := s.loadMegaToday(ctx, siteDay(time.Now())); err != nil {
		log.Printf("mega poll: %v", err)
	}
}

// adminPost parses an admin-only form (formModerate). Everyone else gets a
// 404, so the endpoints don't admit to existing. ok is false if the caller
// should stop.
func (s *Server) adminPost(w http.ResponseWriter, r *http.Request) bool {
	if u, ok := userFrom(r.Context()); !ok || !u.IsAdmin {
		http.NotFound(w, r)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return false
	}
	problem, handled := s.checkForm(w, r, formModerate)
	if handled {
		return false
	}
	if problem != "" {
		http.Error(w, problem, http.StatusBadRequest)
		return false
	}
	return true
}

// POST /p/{id}/lock (action=lock or unlock; back= where to return)
func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	if !s.adminPost(w, r) {
		return
	}
	id := r.PathValue("id")
	if !publicIDPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	on := r.PostForm.Get("action") == "lock"
	if !on && r.PostForm.Get("action") != "unlock" {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	err := s.store.SetLocked(r.Context(), id, on)
	if errors.Is(err, store.ErrItemNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, backOr(r, "/p/"+id), http.StatusSeeOther)
}

// POST /p/{id}/poll/close (back= where to return)
func (s *Server) handleClosePoll(w http.ResponseWriter, r *http.Request) {
	if !s.adminPost(w, r) {
		return
	}
	id := r.PathValue("id")
	if !publicIDPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	err := s.store.ClosePollNow(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrPollNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrPollNotOpen):
		// already closed: nothing to do
	case err != nil:
		serverError(w, err)
		return
	}
	s.refreshMegaToday(r.Context())
	http.Redirect(w, r, backOr(r, "/p/"+id), http.StatusSeeOther)
}

// backOr is the form's back= address, if it's on this site, or fallback.
func backOr(r *http.Request, fallback string) string {
	if b := r.PostForm.Get("back"); b != "" {
		return safeBack(b)
	}
	return fallback
}

// POST /admin/polls/today (action=takedown with reason=, or action=run-next)
func (s *Server) handleMegaToday(w http.ResponseWriter, r *http.Request) {
	if !s.adminPost(w, r) {
		return
	}
	ctx := r.Context()
	today := siteDay(time.Now())
	done := ""
	switch r.PostForm.Get("action") {
	case "takedown":
		reason, ok := removalReason(w, r)
		if !ok {
			return
		}
		post, found, err := s.store.MegaPollPost(ctx, today)
		if err != nil {
			serverError(w, err)
			return
		}
		if found {
			if err := s.store.Remove(ctx, store.Item{PostID: post.PublicID}, reason); err != nil {
				serverError(w, err)
				return
			}
			s.refreshStats()
			s.refreshReports()
		}
		done = "taken-down"
	case "run-next":
		switch err := s.store.RunNextNow(ctx, today); {
		case errors.Is(err, store.ErrMegaTodayLive):
			done = "still-up"
		case errors.Is(err, store.ErrMegaTodayWait):
			done = "waiting"
		case errors.Is(err, store.ErrMegaQueueDry):
			done = "queue-dry"
		case err != nil:
			serverError(w, err)
			return
		default:
			s.publishMegaPolls(ctx)
			done = "ran-next"
		}
	default:
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	s.refreshMegaToday(ctx)
	http.Redirect(w, r, "/admin/polls?done="+done, http.StatusSeeOther)
}
