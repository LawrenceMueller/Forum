package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"snowball/internal/store"
	"snowball/internal/tags"
)

// editTimeLeft says whether something posted at created can still be edited
// at now, and for how long. The database has the final say (store.EditPost
// and EditComment check its clock); this is for showing "edit, 7 minutes
// left" and for deciding whether to offer the link at all.
func editTimeLeft(created, now time.Time) (time.Duration, bool) {
	age := max(now.Sub(created), 0)
	if age >= store.EditWindow {
		return 0, false
	}
	return store.EditWindow - age, true
}

// canEdit is whether the viewer may edit an item, and the time left as words.
func canEdit(viewer voteViewer, author string, authorDeleted, gone bool, created time.Time) (bool, string) {
	if !viewer.loggedIn || viewer.suspended || authorDeleted || gone || author == "" || author != viewer.user.Username {
		return false, ""
	}
	left, ok := editTimeLeft(created, time.Now())
	if !ok {
		return false, ""
	}
	return true, friendlyDuration(left)
}

// editingAllowed applies the per-account edit limit (cheap insurance against
// a script editing in a loop). It returns a message, or "" to go ahead.
func (s *Server) editingAllowed(userID int64) string {
	if ok, wait := s.limits.editing.allow("user:" + strconv.FormatInt(userID, 10)); !ok {
		return "You've made a lot of edits in a short time. Try again in " + friendlyDuration(wait) + "."
	}
	return ""
}

const closedMessage = "The 10-minute editing window has closed, so this is final now."

// ---- editing a post ----

type editPostPage struct {
	basePage
	PostID    string
	PostTitle string
	Link      string
	Body      string
	Tags      tagFields
	TimeLeft  string
	Closed    bool
	Errors    []string
	FormToken string
}

func (s *Server) renderEditPost(w http.ResponseWriter, r *http.Request, start time.Time, post store.Post, body string, sel tags.Selection, errs []string) {
	data := editPostPage{
		basePage:  newBasePage(r, "edit post").withUp("/p/"+post.PublicID, "back to the post"),
		PostID:    post.PublicID,
		PostTitle: post.Title,
		Link:      post.URL,
		Body:      body,
		Tags:      s.tagFieldsFor(sel),
		Errors:    errs,
		FormToken: s.formToken(formEditPost),
	}
	if left, ok := editTimeLeft(post.CreatedAt, time.Now()); ok && !post.Removed && !post.Deleted {
		data.TimeLeft = friendlyDuration(left)
	} else {
		data.Closed = true
	}
	data.Generated = time.Since(start)
	s.render(w, "editpost.html", data)
}

// ownPost loads the {id} post and checks the viewer wrote it. Anyone else
// gets a 404: the edit pages don't admit to existing for other people's posts.
func (s *Server) ownPost(w http.ResponseWriter, r *http.Request) (store.User, store.Post, bool) {
	u, ok := userFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return store.User{}, store.Post{}, false
	}
	post, ok := s.loadPost(w, r)
	if !ok {
		return store.User{}, store.Post{}, false
	}
	if post.AuthorDeleted || post.Author != u.Username {
		http.NotFound(w, r)
		return store.User{}, store.Post{}, false
	}
	return u, post, true
}

// GET /p/{id}/edit
func (s *Server) handleEditPostPage(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	_, post, ok := s.ownPost(w, r)
	if !ok {
		return
	}
	// Fill in the post's current tags.
	s.renderEditPost(w, r, start, post, post.Body, s.tree().SelectionFor(post.Tags), nil)
}

// POST /p/{id}/edit
func (s *Server) handleEditPost(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	u, post, ok := s.ownPost(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	formProblem, handled := s.checkForm(w, r, formEditPost)
	if handled {
		return
	}

	body := strings.ReplaceAll(r.PostForm.Get("body"), "\r\n", "\n")
	sel := selectionFrom(r)
	tagIDs, tagProblems := s.tree().Choose(sel)

	var errs []string
	if formProblem != "" {
		errs = append(errs, formProblem)
	}
	// Title and link are the post's own, unchanged, so only the body can fail here.
	errs = append(errs, validatePost(post.Title, post.URL, body)...)
	errs = append(errs, tagProblems...)
	if len(errs) == 0 {
		if msg := s.editingAllowed(u.ID); msg != "" {
			errs = append(errs, msg)
		}
	}
	if len(errs) > 0 {
		s.renderEditPost(w, r, start, post, body, sel, errs)
		return
	}

	err := s.store.EditPost(r.Context(), post.PublicID, u.ID, post.Title, post.URL, body, tagIDs)
	if errors.Is(err, store.ErrEditClosed) {
		s.renderEditPost(w, r, start, post, body, sel, []string{closedMessage})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/p/"+post.PublicID, http.StatusSeeOther)
}

// ---- editing a comment ----

// commentEditForm drives partials/commentedit.html.
type commentEditForm struct {
	PostID    string
	CommentID string
	Body      string
	TimeLeft  string
	Error     string
	Closed    bool
	Inline    bool // slotted into the thread by htmx, rather than on its own page
	FormToken string
}

type editCommentPage struct {
	basePage
	PostID    string
	PostTitle string
	Form      commentEditForm
}

// ownComment loads the {cid} comment on the {id} post and checks the viewer wrote it.
func (s *Server) ownComment(w http.ResponseWriter, r *http.Request) (store.User, store.Post, store.Comment, bool) {
	u, ok := userFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return store.User{}, store.Post{}, store.Comment{}, false
	}
	post, ok := s.loadPost(w, r)
	if !ok {
		return store.User{}, store.Post{}, store.Comment{}, false
	}
	c, ok := s.loadComment(w, r, post)
	if !ok {
		return store.User{}, store.Post{}, store.Comment{}, false
	}
	if c.AuthorDeleted || c.Author != u.Username {
		http.NotFound(w, r)
		return store.User{}, store.Post{}, store.Comment{}, false
	}
	return u, post, c, true
}

func (s *Server) newCommentEditForm(r *http.Request, post store.Post, c store.Comment, body, problem string) commentEditForm {
	f := commentEditForm{
		PostID:    post.PublicID,
		CommentID: c.PublicID,
		Body:      body,
		Error:     problem,
		Inline:    r.Header.Get("HX-Request") == "true",
		FormToken: s.formToken(formEditComment),
	}
	if left, ok := editTimeLeft(c.CreatedAt, time.Now()); ok && !c.Removed && !c.Deleted {
		f.TimeLeft = friendlyDuration(left)
	} else {
		f.Closed, f.Error = true, closedMessage
	}
	return f
}

// showCommentEdit sends the edit form: just the form for htmx, a page otherwise.
func (s *Server) showCommentEdit(w http.ResponseWriter, r *http.Request, start time.Time, post store.Post, f commentEditForm) {
	if f.Inline {
		s.renderFragment(w, "postpage.html", "commentedit", f)
		return
	}
	data := editCommentPage{
		basePage:  newBasePage(r, "edit comment").withUp("/p/"+post.PublicID, "back to the post"),
		PostID:    post.PublicID,
		PostTitle: post.Title,
		Form:      f,
	}
	data.Generated = time.Since(start)
	s.render(w, "editcomment.html", data)
}

// GET /p/{id}/c/{cid}/edit
func (s *Server) handleEditCommentForm(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	_, post, c, ok := s.ownComment(w, r)
	if !ok {
		return
	}
	s.showCommentEdit(w, r, start, post, s.newCommentEditForm(r, post, c, c.Body, ""))
}

// POST /p/{id}/c/{cid}/edit
func (s *Server) handleEditComment(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	u, post, c, ok := s.ownComment(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	formProblem, handled := s.checkForm(w, r, formEditComment)
	if handled {
		return
	}

	body := strings.TrimSpace(strings.ReplaceAll(r.PostForm.Get("body"), "\r\n", "\n"))
	problem := formProblem
	if problem == "" {
		switch n := utf8.RuneCountInString(body); {
		case n == 0:
			problem = "A comment can't be empty. (To remove it entirely, deleting is coming soon.)"
		case n > maxCommentLen:
			problem = "Comments can be up to " + strconv.Itoa(maxCommentLen) + " characters."
		}
	}
	if problem == "" {
		problem = s.editingAllowed(u.ID)
	}
	if problem != "" {
		s.showCommentEdit(w, r, start, post, s.newCommentEditForm(r, post, c, body, problem))
		return
	}

	updated, err := s.store.EditComment(r.Context(), c.PublicID, u.ID, body)
	if errors.Is(err, store.ErrEditClosed) {
		s.showCommentEdit(w, r, start, post, s.newCommentEditForm(r, post, c, body, closedMessage))
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}

	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, "/p/"+post.PublicID+"#c-"+c.PublicID, http.StatusSeeOther)
		return
	}
	// htmx: the new text replaces the form, and the "edited" marker in the
	// comment's header line updates too (an out-of-band swap; see the partial).
	node := newCommentNode(updated, treeOptions{postID: post.PublicID, viewer: s.voteViewerFor(r)})
	s.renderFragment(w, "postpage.html", "commentedited", node)
}
