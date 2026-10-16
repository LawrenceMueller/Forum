package web

// Notes from the admin. The newest visible note sits in a box at the top of
// the front page; /notes lists them all, newest first, so they double as a
// record of how the site has changed. The admin writes, edits, hides and
// deletes them at /admin/notes.
//
// The front page's note is kept in memory (latestNote), loaded at startup
// and after every change, so the busiest page doesn't query for it.

import (
	"context"
	"errors"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"snowball/internal/store"
)

const maxNoteLen = 2000

// noteView is a note ready for a template.
type noteView struct {
	ID       int64
	Date     string // "Oct 15"
	FullDate string // "2026-10-15"
	HTML     template.HTML
	Body     string // the Markdown, for the admin's edit box
	Edited   bool
	Hidden   bool
}

func (s *Server) toNoteView(n store.Note) noteView {
	return noteView{
		ID: n.ID, Date: n.CreatedAt.Format("Jan 2"), FullDate: n.CreatedAt.Format("2006-01-02"),
		HTML: s.bodies.Render(n.Body), Body: n.Body, Edited: n.EditedAt != nil, Hidden: n.Hidden,
	}
}

// loadLatestNote refreshes the front page's note.
func (s *Server) loadLatestNote(ctx context.Context) error {
	n, err := s.store.LatestNote(ctx)
	if err != nil {
		return err
	}
	if n == nil {
		s.latestNote.Store(nil)
		return nil
	}
	v := s.toNoteView(*n)
	s.latestNote.Store(&v)
	return nil
}

// ---- /notes ----

type notesPage struct {
	basePage
	Notes []noteView
}

const notesShown = 200

// GET /notes
func (s *Server) handleNotes(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	notes, err := s.store.Notes(r.Context(), false, notesShown)
	if err != nil {
		serverError(w, err)
		return
	}
	data := notesPage{basePage: newBasePage(r, "notes from the admin").withUp("/", "front page")}
	for _, n := range notes {
		data.Notes = append(data.Notes, s.toNoteView(n))
	}
	data.Generated = time.Since(start)
	s.render(w, "notes.html", data)
}

// ---- /admin/notes ----

type adminNotesPage struct {
	basePage
	FormToken string
	Notice    string
	Errors    []string
	Draft     string
	Notes     []noteView
}

var noteNotices = map[string]string{
	"added":   "Posted. It's on the front page now.",
	"edited":  "Saved.",
	"hidden":  "Hidden: it's off the front page and /notes.",
	"shown":   "Shown again.",
	"deleted": "Deleted.",
}

// GET /admin/notes
func (s *Server) handleAdminNotes(w http.ResponseWriter, r *http.Request) {
	s.renderAdminNotes(w, r, time.Now(), "", nil)
}

func (s *Server) renderAdminNotes(w http.ResponseWriter, r *http.Request, start time.Time, draft string, errs []string) {
	notes, err := s.store.Notes(r.Context(), true, notesShown)
	if err != nil {
		serverError(w, err)
		return
	}
	data := adminNotesPage{
		basePage:  newBasePage(r, "notes (admin)").withUp("/account", "your account"),
		FormToken: s.formToken(formModerate),
		Notice:    noteNotices[r.URL.Query().Get("done")],
		Errors:    errs,
		Draft:     draft,
	}
	for _, n := range notes {
		data.Notes = append(data.Notes, s.toNoteView(n))
	}
	data.Generated = time.Since(start)
	s.render(w, "adminnotes.html", data)
}

// POST /admin/notes (action=add|edit|hide|show|delete, id=, body=)
func (s *Server) handleAdminNoteChange(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	problem, handled := s.checkForm(w, r, formModerate)
	if handled {
		return
	}
	body := strings.TrimSpace(strings.ReplaceAll(r.PostForm.Get("body"), "\r\n", "\n"))
	action := r.PostForm.Get("action")
	if problem != "" {
		s.renderAdminNotes(w, r, start, body, []string{problem})
		return
	}
	if action == "add" || action == "edit" {
		switch n := utf8.RuneCountInString(body); {
		case n == 0:
			s.renderAdminNotes(w, r, start, body, []string{"Write something first."})
			return
		case n > maxNoteLen:
			s.renderAdminNotes(w, r, start, body, []string{"Notes can be up to 2,000 characters."})
			return
		}
	}
	id, _ := strconv.ParseInt(r.PostForm.Get("id"), 10, 64)

	var err error
	done := map[string]string{"add": "added", "edit": "edited", "hide": "hidden", "show": "shown", "delete": "deleted"}[action]
	switch action {
	case "add":
		_, err = s.store.AddNote(r.Context(), body)
	case "edit":
		err = s.store.EditNote(r.Context(), id, body)
	case "hide", "show":
		err = s.store.SetNoteHidden(r.Context(), id, action == "hide")
	case "delete":
		if r.PostForm.Get("confirm") != "1" {
			s.renderAdminNotes(w, r, start, "", []string{"Tick the box to confirm deleting the note."})
			return
		}
		err = s.store.DeleteNote(r.Context(), id)
	default:
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if errors.Is(err, store.ErrNoteNotFound) {
		s.renderAdminNotes(w, r, start, "", []string{"That note doesn't exist any more."})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if err := s.loadLatestNote(r.Context()); err != nil {
		log.Printf("reloading the front page note: %v", err)
	}
	http.Redirect(w, r, "/admin/notes?done="+done, http.StatusSeeOther)
}
