package web

import (
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"snowball/internal/markdown"
	"snowball/internal/store"
)

const (
	maxTitleLen = 200
	maxURLLen   = 2000
	maxBodyLen  = 40000
	maxTags     = 3
)

// ---- submitting a post ----

type tagChoice struct {
	ID      int
	Name    string
	Checked bool
}

type submitPage struct {
	basePage
	Title     string
	URL       string
	Body      string
	Tags      []tagChoice
	Errors    []string
	FormToken string
}

// renderSubmit shows the submit form, keeping whatever was typed so a
// mistake doesn't cost someone their post.
func (s *Server) renderSubmit(w http.ResponseWriter, r *http.Request, start time.Time, form submitPage, checked map[int]bool) {
	tags, err := s.store.Tags(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	for _, t := range tags {
		form.Tags = append(form.Tags, tagChoice{ID: t.ID, Name: t.Name, Checked: checked[t.ID]})
	}
	form.basePage = newBasePage(r, "new post")
	form.FormToken = s.formToken(formSubmit)
	form.Generated = time.Since(start)
	s.render(w, "submit.html", form)
}

// GET /submit
func (s *Server) handleSubmitPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := userFrom(r.Context()); !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.renderSubmit(w, r, time.Now(), submitPage{}, nil)
}

// POST /submit
func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	start := time.Now()
	ctx := r.Context()

	// Bodies can be up to 40,000 characters, and a character can take up
	// to 4 bytes, so this limit is larger than on the other forms.
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	form := submitPage{
		Title: strings.TrimSpace(r.PostForm.Get("title")),
		URL:   strings.TrimSpace(r.PostForm.Get("url")),
		// Browsers send line breaks as \r\n; store plain \n.
		Body: strings.ReplaceAll(r.PostForm.Get("body"), "\r\n", "\n"),
	}

	formProblem, handled := s.checkForm(w, r, formSubmit)
	if handled {
		return
	}

	tags, err := s.store.Tags(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	checked, tagIDs, tagProblem := pickTags(r.PostForm["tag"], tags)

	var errs []string
	if formProblem != "" {
		errs = append(errs, formProblem)
	}
	errs = append(errs, validatePost(form.Title, form.URL, form.Body)...)
	if tagProblem != "" {
		errs = append(errs, tagProblem)
	}

	// Per-account limit, checked last so mistakes above don't use it up.
	// (The per-IP limit on this route is applied in server.go.)
	if len(errs) == 0 {
		if ok, wait := s.limits.posting.allow("user:" + strconv.FormatInt(u.ID, 10)); !ok {
			errs = append(errs, "You've posted a lot recently. You can post again in "+friendlyDuration(wait)+".")
		}
	}

	if len(errs) > 0 {
		form.Errors = errs
		s.renderSubmit(w, r, start, form, checked)
		return
	}

	publicID, err := s.store.CreatePost(ctx, store.NewPost{
		AuthorID: u.ID,
		Title:    form.Title,
		URL:      normalizeURL(form.URL),
		Body:     form.Body,
		TagIDs:   tagIDs,
	})
	if err != nil {
		serverError(w, err)
		return
	}

	s.stats.posts.Add(1) // show the new count right away, not at the next refresh

	// Post/Redirect/Get: go and look at the new post.
	http.Redirect(w, r, "/p/"+publicID, http.StatusSeeOther)
}

// validatePost checks the parts of a post that don't need the database.
func validatePost(title, link, body string) []string {
	var errs []string

	switch n := utf8.RuneCountInString(title); {
	case n == 0:
		errs = append(errs, "Your post needs a title.")
	case n > maxTitleLen:
		errs = append(errs, "Titles can be up to "+strconv.Itoa(maxTitleLen)+" characters.")
	}

	if link != "" && !validLink(link) {
		errs = append(errs, "That link doesn't look right. It should start with http:// or https://.")
	}

	if utf8.RuneCountInString(body) > maxBodyLen {
		errs = append(errs, "Posts can be up to "+strconv.Itoa(maxBodyLen)+" characters.")
	}
	return errs
}

// validLink accepts only complete http(s) addresses: no javascript:,
// no data:, nothing relative.
func validLink(link string) bool {
	if len(link) > maxURLLen {
		return false
	}
	u, err := url.Parse(link)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// normalizeURL returns the link in its tidy parsed form ("" stays "").
func normalizeURL(link string) string {
	if link == "" {
		return ""
	}
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	return u.String()
}

// pickTags turns the ticked checkboxes into tag IDs, allowing only real
// tags, each once, 1 to 3 of them. checked remembers the ticks for re-display.
func pickTags(values []string, all []store.Tag) (checked map[int]bool, ids []int, problem string) {
	valid := make(map[int]bool, len(all))
	for _, t := range all {
		valid[t.ID] = true
	}

	checked = make(map[int]bool)
	for _, v := range values {
		id, err := strconv.Atoi(v)
		if err != nil || !valid[id] || checked[id] {
			continue // ignore anything that isn't a real, new tag
		}
		checked[id] = true
		ids = append(ids, id)
	}

	switch {
	case len(ids) == 0:
		problem = "Pick at least one tag."
	case len(ids) > maxTags:
		problem = "Pick at most " + strconv.Itoa(maxTags) + " tags."
	}
	return checked, ids, problem
}

// ---- viewing a post ----

// postView is a post prepared for display.
type postView struct {
	PublicID      string
	Title         string
	Href          string // where the title points: the link, or the post itself
	Link          string // the external URL, for link posts
	Domain        string
	BodyHTML      template.HTML
	Author        string
	AuthorDeleted bool
	ColorClass    string
	Posted        time.Time
	Edited        bool
	Score         int
	Tags          []string
	Deleted       bool
	Removed       bool
	Comments      int
	LastActivity  time.Time
	ShowLastReply bool // lists sorted by activity show when the last reply came
	Vote          voteWidget
}

func toPostView(p store.Post) postView {
	v := postView{
		PublicID:      p.PublicID,
		Title:         p.Title,
		Link:          p.URL,
		Author:        p.Author,
		AuthorDeleted: p.AuthorDeleted,
		Posted:        p.CreatedAt,
		Edited:        p.EditedAt != nil,
		Score:         p.Score,
		Tags:          p.Tags,
		Deleted:       p.Deleted,
		Removed:       p.Removed,
		Comments:      p.CommentCount,
		LastActivity:  p.LastActivityAt,
		Href:          "/p/" + p.PublicID,
	}
	if p.URL != "" {
		v.Href = p.URL
	}
	if p.Body != "" {
		v.BodyHTML = markdown.Render(p.Body)
	}
	if p.URL != "" {
		if u, err := url.Parse(p.URL); err == nil {
			v.Domain = strings.TrimPrefix(u.Hostname(), "www.")
		}
	}
	if p.AuthorDeleted {
		v.Author = "" // the retired name is never shown
	} else {
		v.ColorClass = colorClass(p.Author)
	}
	return v
}

type postPage struct {
	basePage
	Post        postView
	IsAdmin     bool
	RemoveToken string

	// comments
	Comments    []*commentNode
	CommentSort string
	LoggedIn    bool
	Locked      bool // removed or deleted posts take no new comments
	CommentForm replyForm
}

// Public IDs are exactly 8 characters of a-z and 2-7. Anything else can't
// be a post, so don't even ask the database.
var publicIDPattern = regexp.MustCompile(`^[a-z2-7]{8}$`)

// GET /p/{id}
func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	id := r.PathValue("id")
	if !publicIDPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}

	p, found, err := s.store.PostByPublicID(r.Context(), id)
	if err != nil {
		serverError(w, err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	comments, err := s.store.CommentsForPost(r.Context(), p.ID)
	if err != nil {
		serverError(w, err)
		return
	}

	u, loggedIn := userFrom(r.Context())
	sortMode := commentSort(r)
	opts, err := s.treeOptionsFor(r, p, sortMode, 0, comments)
	if err != nil {
		serverError(w, err)
		return
	}

	view := toPostView(p)
	myPostVote, err := s.myVotes(r, opts.viewer, store.VotePost, []int64{p.ID})
	if err != nil {
		serverError(w, err)
		return
	}
	view.Vote = opts.viewer.widget("post", p.PublicID, p.Score, myPostVote[p.ID],
		p.Author, p.AuthorDeleted, p.Removed || p.Deleted, true)

	data := postPage{
		basePage:    newBasePage(r, p.Title),
		Post:        view,
		IsAdmin:     u.IsAdmin,
		Comments:    buildTree(comments, opts),
		CommentSort: sortMode,
		LoggedIn:    loggedIn,
		Locked:      p.Removed || p.Deleted,
	}
	if loggedIn && !data.Locked {
		data.CommentForm = s.newReplyForm(p.PublicID, "", "")
	}
	if p.Removed && !u.IsAdmin {
		data.Title = "removed post" // don't leak a removed title into the tab
	}
	if u.IsAdmin {
		data.RemoveToken = s.formToken(formRemovePost)
	}
	data.Generated = time.Since(start)
	s.render(w, "postpage.html", data)
}

// POST /p/{id}/remove: admins only. Everyone else gets a plain 404, so the
// endpoint doesn't even admit to existing.
func (s *Server) handleRemovePost(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok || !u.IsAdmin {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	if !publicIDPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if problem, handled := s.checkForm(w, r, formRemovePost); handled {
		return
	} else if problem != "" {
		http.Error(w, problem, http.StatusBadRequest)
		return
	}

	if err := s.store.RemovePost(r.Context(), id); err != nil {
		serverError(w, err)
		return
	}
	s.refreshStats()
	http.Redirect(w, r, "/p/"+id, http.StatusSeeOther)
}
