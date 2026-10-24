package web

import (
	"errors"
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
	"snowball/internal/tags"
)

const (
	maxTitleLen = 200
	maxURLLen   = 2000
	maxBodyLen  = 40000
)

// ---- submitting a post ----

type submitPage struct {
	basePage
	Title     string
	URL       string
	Body      string
	Tags      tagFields
	Errors    []string
	FormToken string
	LinksFrom string // during probation: when links unlock ("" otherwise)
	Poll      pollForm
}

// renderSubmit shows the submit form, keeping whatever was typed so a
// mistake doesn't cost someone their post.
func (s *Server) renderSubmit(w http.ResponseWriter, r *http.Request, start time.Time, form submitPage, sel tags.Selection) {
	form.Tags = s.tagFieldsFor(sel)
	form.basePage = newBasePage(r, "new post").withUp("/", "front page")
	form.FormToken = s.formToken(formSubmit)
	if form.Poll.Options == nil {
		form.Poll = newPollForm(nil, "")
	}
	if u, ok := userFrom(r.Context()); ok && !u.IsAdmin {
		if fresh, until := onProbation(u); fresh {
			form.LinksFrom = until.Format("2006-01-02 15:04")
		}
	}
	form.Generated = time.Since(start)
	s.render(w, "submit.html", form)
}

// GET /submit
func (s *Server) handleSubmitPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := userFrom(r.Context()); !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.renderSubmit(w, r, time.Now(), submitPage{}, tags.Selection{})
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

	sel := selectionFrom(r)
	tagIDs, tagProblems := s.tree().Choose(sel)

	var errs []string
	if formProblem != "" {
		errs = append(errs, formProblem)
	}
	errs = append(errs, validatePost(form.Title, form.URL, form.Body)...)
	errs = append(errs, tagProblems...)
	poll, pollForm, pollProblems := pollFromForm(r, time.Now())
	form.Poll = pollForm
	errs = append(errs, pollProblems...)
	if len(errs) == 0 {
		errs = append(errs, s.linkProblems(u, form.URL, form.Body)...) // probation, blocklist
	}
	if len(errs) == 0 {
		hash := store.ContentHash(form.Title, normalizeURL(form.URL), form.Body)
		problem, err := s.duplicateProblem(ctx, false, u, hash, utf8.RuneCountInString(form.Body))
		if err != nil {
			serverError(w, err)
			return
		}
		if problem != "" {
			errs = append(errs, problem)
		}
	}

	// Per-account limit, checked last so mistakes above don't use it up.
	// (The per-IP limit on this route is applied in server.go.)
	if len(errs) == 0 {
		if ok, wait := s.postingLimiter(u).allow("user:" + strconv.FormatInt(u.ID, 10)); !ok {
			errs = append(errs, "You've posted a lot recently. You can post again in "+friendlyDuration(wait)+".")
		}
	}

	if len(errs) > 0 {
		form.Errors = errs
		s.renderSubmit(w, r, start, form, sel)
		return
	}

	publicID, err := s.store.CreatePost(ctx, store.NewPost{
		AuthorID: u.ID,
		Title:    form.Title,
		URL:      normalizeURL(form.URL),
		Body:     form.Body,
		TagIDs:   tagIDs,
		Poll:     poll,
	})
	if err != nil {
		serverError(w, err)
		return
	}

	s.stats.posts.Add(1) // show the new count right away, not at the next refresh
	go s.refreshStats()  // and the tag counts soon, without making the poster wait

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

// ---- viewing a post ----

// postView is a post prepared for display.
type postView struct {
	PublicID      string
	Title         string
	Href          string // where the title points in lists: always the post itself
	Link          string // the external URL, for link posts
	Domain        string
	BodyHTML      template.HTML
	Author        string
	AuthorDeleted bool
	ColorClass    string
	Posted        time.Time
	Edited        bool
	Score         int
	Tags          []tags.Chip
	Deleted       bool
	Removed       bool
	Comments      int
	LastActivity  time.Time
	ShowLastReply bool // lists sorted by activity show when the last reply came
	Vote          voteWidget
	CanEdit       bool   // the viewer wrote it and the edit window is open
	EditLeft      string // "7 minutes", for the edit link
	RemovedText   string // in its place when removed: "[removed: spam]"
	Held          bool   // hidden by reports until the admin reviews it
	Erased        bool   // removed over 30 days ago: its words are gone
	CanReport     bool
	CanDelete     bool // the viewer wrote it and it isn't deleted yet
	IsPoll        bool
	IsMega        bool
	Locked        bool      // no new comments for now
	BySite        bool      // shown as from "Snowball" (mega polls)
	Poll          *pollView // on the post page, when it has a poll
}

func toPostView(p store.Post, tree *tags.Tree) postView {
	v := postView{
		PublicID:      p.PublicID,
		Title:         p.Title,
		Link:          p.URL,
		Author:        p.Author,
		AuthorDeleted: p.AuthorDeleted,
		Posted:        p.CreatedAt,
		Edited:        p.EditedAt != nil,
		Score:         p.Score,
		Tags:          tree.Chips(p.Tags),
		Deleted:       p.Deleted,
		Removed:       p.Removed,
		Comments:      p.CommentCount,
		LastActivity:  p.LastActivityAt,
		Href:          "/p/" + p.PublicID,
		RemovedText:   removedText(p.RemovalReason),
		Held:          p.RemovalReason == "held",
		Erased:        p.Erased,
		IsPoll:        p.IsPoll,
		IsMega:        p.IsMega,
		Locked:        p.Locked,
	}
	if p.Body != "" {
		v.BodyHTML = markdown.Render(p.Body)
	}
	if p.URL != "" {
		if u, err := url.Parse(p.URL); err == nil {
			v.Domain = strings.TrimPrefix(u.Hostname(), "www.")
		}
	}
	if p.Deleted {
		// Deleted by its author: the title stays (so the discussion makes
		// sense), but the text and link are already erased, and the author
		// is shown as [deleted].
		v.AuthorDeleted = true
	}
	switch {
	case v.AuthorDeleted:
		v.Author = "" // the retired name is never shown
	case p.BySite:
		v.Author, v.BySite = "Snowball", true // the admin's own name stays private
	default:
		v.ColorClass = colorClass(p.Author)
	}
	return v
}

type postPage struct {
	basePage
	Post        postView
	IsAdmin     bool
	RemoveToken string
	DeleteToken string // for "delete" on the viewer's own post and comments

	// comments
	Comments        []*commentNode
	CommentSort     string
	LoggedIn        bool
	Locked          bool   // removed or deleted posts take no new comments
	CommentsLocked  bool   // the admin locked it
	AdminToken      string // for the admin's lock and close-poll buttons
	ViewerSuspended bool   // the viewer is suspended: no comment form
	CommentForm     replyForm
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
	// A shadowbanned author sees their own post as if it were up.
	if ownShadow(u, p.Author, p.AuthorDeleted, p.Removed, p.RemovalReason) && !u.IsAdmin {
		p.Removed, p.RemovalReason = false, ""
	}
	sortMode := commentSort(r)
	opts, err := s.treeOptionsFor(r, p, sortMode, 0, comments)
	if err != nil {
		serverError(w, err)
		return
	}
	poll, pstate, err := s.pollFor(r, p, opts.viewer)
	if err != nil {
		serverError(w, err)
		return
	}
	opts.pollLabels = pollLabels(poll)

	view := toPostView(p, s.tree())
	if poll != nil {
		pv := s.newPollView(p.PublicID, poll, pstate)
		view.Poll = &pv
	}
	myPostVote, err := s.myVotes(r, opts.viewer, store.VotePost, []int64{p.ID})
	if err != nil {
		serverError(w, err)
		return
	}
	view.Vote = opts.viewer.widget("post", p.PublicID, p.Score, myPostVote[p.ID],
		p.Author, p.AuthorDeleted, p.Removed || p.Deleted, true)
	view.CanEdit, view.EditLeft = canEdit(opts.viewer, p.Author, p.AuthorDeleted,
		p.Removed || p.Deleted, p.CreatedAt)
	view.CanReport = opts.viewer.canReport(p.Author, p.AuthorDeleted, p.Removed || p.Deleted)
	view.CanDelete = loggedIn && !p.Deleted && !p.AuthorDeleted && p.Author == u.Username

	data := postPage{
		basePage:       newBasePage(r, p.Title).withUp("/", "front page"),
		Post:           view,
		IsAdmin:        u.IsAdmin,
		Comments:       buildTree(comments, opts),
		CommentSort:    sortMode,
		LoggedIn:       loggedIn,
		Locked:         p.Removed || p.Deleted,
		CommentsLocked: p.Locked,
	}
	data.ViewerSuspended = u.Suspension != nil
	if loggedIn && !data.Locked && !data.CommentsLocked && !data.ViewerSuspended {
		data.CommentForm = s.newReplyForm(p.PublicID, "", "")
	}
	if p.Removed && !u.IsAdmin {
		data.Title = "removed post" // don't leak a removed title into the tab
	}
	if u.IsAdmin {
		data.RemoveToken = s.formToken(formRemovePost)
		data.AdminToken = s.formToken(formModerate)
	}
	data.DeleteToken = opts.deleteToken
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

	reason, ok := removalReason(w, r)
	if !ok {
		return
	}
	err := s.store.Remove(r.Context(), store.Item{PostID: id}, reason)
	if errors.Is(err, store.ErrItemNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	s.refreshStats()
	s.refreshReports()
	s.refreshMegaToday(r.Context()) // if it was today's mega poll, its box goes now
	http.Redirect(w, r, "/p/"+id, http.StatusSeeOther)
}
