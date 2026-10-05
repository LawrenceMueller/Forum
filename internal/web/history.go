package web

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"snowball/internal/store"
)

const historyPageSize = 25

// historyPage is the private list of everything the viewer has written.
// Nobody else can see it: there's no public profile, so piecing together
// someone's history means hunting thread by thread.
type historyPage struct {
	basePage
	Show        string // "posts" or "comments"
	Posts       []store.HistoryPost
	Comments    []historyComment
	NextURL     string
	FirstURL    string
	DeleteToken string
	Back        string
}

type historyComment struct {
	store.HistoryComment
	Snippet string
}

// snippet is the start of a comment's Markdown, as plain one-line text.
// The template escapes it, so any markup shows as harmless characters.
func snippet(body string, limit int) string {
	s := strings.Join(strings.Fields(body), " ")
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:limit])) + "…"
}

// GET /account/history
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	start := time.Now()
	ctx := r.Context()

	show := "posts"
	if r.URL.Query().Get("show") == "comments" {
		show = "comments"
	}
	path := "/account/history?show=" + show

	var after *store.Cursor
	if raw := r.URL.Query().Get("after"); raw != "" {
		c, err := store.ParseCursor(raw)
		if err != nil {
			http.Redirect(w, r, path, http.StatusSeeOther)
			return
		}
		after = &c
	}

	data := historyPage{
		basePage:    newBasePage(r, "your history").withUp("/account", "your account"),
		Show:        show,
		DeleteToken: s.formToken(formDeleteItem),
		Back:        r.URL.RequestURI(),
	}

	var next *store.Cursor
	var err error
	if show == "posts" {
		data.Posts, next, err = s.store.MyPosts(ctx, u.ID, after, historyPageSize)
	} else {
		var comments []store.HistoryComment
		comments, next, err = s.store.MyComments(ctx, u.ID, after, historyPageSize)
		for _, c := range comments {
			data.Comments = append(data.Comments, historyComment{c, snippet(c.Body, 160)})
		}
	}
	if err != nil {
		serverError(w, err)
		return
	}

	if after != nil {
		data.FirstURL = path
	}
	if next != nil {
		data.NextURL = path + "&after=" + next.String()
	}
	data.Generated = time.Since(start)
	s.render(w, "history.html", data)
}

// POST /p/{id}/delete: erase one of your own posts.
func (s *Server) handleDeletePost(w http.ResponseWriter, r *http.Request) {
	u, post, ok := s.ownPost(w, r)
	if !ok {
		return
	}
	back, ok := s.parseDeleteForm(w, r)
	if !ok {
		return
	}
	if err := s.store.DeletePost(r.Context(), post.PublicID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	s.refreshStats() // one fewer post in the footer count
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// POST /p/{id}/c/{cid}/delete: erase one of your own comments.
func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	u, _, c, ok := s.ownComment(w, r)
	if !ok {
		return
	}
	back, ok := s.parseDeleteForm(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteComment(r.Context(), c.PublicID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// parseDeleteForm checks a delete form and returns where to go afterwards.
func (s *Server) parseDeleteForm(w http.ResponseWriter, r *http.Request) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return "", false
	}
	if problem, handled := s.checkForm(w, r, formDeleteItem); handled {
		return "", false
	} else if problem != "" {
		// The page was open long enough for its token to expire (or the
		// server restarted). Send them back to try again from a fresh page.
		http.Redirect(w, r, "/account/history", http.StatusSeeOther)
		return "", false
	}
	return safeBack(r.PostForm.Get("back")), true
}
