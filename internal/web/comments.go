package web

import (
	"errors"
	"html/template"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"snowball/internal/markdown"
	"snowball/internal/store"
)

const (
	maxCommentLen = 10000

	// Replies indent up to this depth; deeper ones sit flush with their
	// parent and say who they're replying to, so threads stay readable on phones.
	maxIndentDepth = 5

	// Past this depth a thread continues on its own page.
	maxThreadDepth = 8
)

// ---- sorting ----

// Comment sorts readers can pick. "best" is the default.
func commentSort(r *http.Request) string {
	switch s := r.URL.Query().Get("sort"); s {
	case "new", "old":
		return s
	default:
		return "best"
	}
}

// wilson is the lower bound of the Wilson score interval: a cautious guess at
// the share of voters who like a comment. With many votes it's close to the
// real ratio; with few, it's pulled down until the evidence builds up. So
// 10 up / 1 down (about 0.62) beats 40 up / 20 down (about 0.54), and a lone
// upvote (about 0.21) doesn't leap to the top. This is Reddit's "best" sort.
func wilson(up, down int) float64 {
	n := float64(up + down)
	if n == 0 {
		return 0
	}
	const z = 1.96 // 95% confidence
	p := float64(up) / n
	return (p + z*z/(2*n) - z*math.Sqrt((p*(1-p)+z*z/(4*n))/n)) / (1 + z*z/n)
}

// ---- the tree ----

// commentNode is one comment prepared for display, with its replies.
// Each node carries everything its template needs, because the comment
// template calls itself for the replies and only ever sees one node.
type commentNode struct {
	PublicID      string
	PostID        string // the post's public ID, for building links
	Author        string
	AuthorDeleted bool
	ColorClass    string
	BodyHTML      template.HTML
	Placeholder   string // "[deleted]" or "[removed]" in place of the body
	AdminSeesGone bool   // a removed comment, shown in full to the admin
	Posted        time.Time
	Edited        bool
	Score         int
	ReplyingTo    string // set on deep replies, which sit flush with their parent
	Indent        bool   // whether this comment's replies are indented
	Children      []*commentNode
	ContinueURL   string // replies continue on another page
	CanReply      bool
	RemoveToken   string // set when the viewer is the admin
	Vote          voteWidget
	CanEdit       bool   // the viewer wrote it and the edit window is open
	EditLeft      string // "7 minutes", for the edit link
	CanReport     bool
	RemovedAs     string // for the admin: why it was removed ("spam", "hidden by reports")

	// used while building and sorting, not by templates
	id, parentID int64
	up, down     int
	created      time.Time
	hidden       bool // shows a placeholder instead of its content
}

// treeOptions is what building a tree needs to know about the page.
type treeOptions struct {
	sort        string
	postID      string
	canComment  bool // logged in, and the post isn't locked
	isAdmin     bool
	removeToken string
	rootID      int64 // build from this comment down; 0 for the whole post
	postClosed  bool  // the post was removed or deleted: no voting on its comments
	viewer      voteViewer
	myVotes     map[int64]int              // the viewer's votes, by comment ID
	render      func(string) template.HTML // Markdown to HTML: the server's cache (nil: render directly)
}

func newCommentNode(c store.Comment, o treeOptions) *commentNode {
	n := &commentNode{
		PublicID:      c.PublicID,
		PostID:        o.postID,
		Author:        c.Author,
		AuthorDeleted: c.AuthorDeleted,
		Posted:        c.CreatedAt,
		Edited:        c.EditedAt != nil,
		Score:         c.Upvotes - c.Downvotes,
		id:            c.ID,
		parentID:      c.ParentID,
		up:            c.Upvotes,
		down:          c.Downvotes,
		created:       c.CreatedAt,
	}

	switch {
	case c.Deleted:
		n.hidden, n.Placeholder = true, "[deleted]"
	case c.Removed && !o.isAdmin:
		n.hidden, n.Placeholder = true, removedText(c.RemovalReason)
	case c.Removed && o.isAdmin:
		n.AdminSeesGone = true
		n.RemovedAs = strings.Trim(removedText(c.RemovalReason), "[]")
	}

	if n.hidden {
		n.Author = "" // a hidden comment doesn't say who wrote it
	} else {
		render := o.render
		if render == nil { // a single comment, outside a page's tree
			render = markdown.Render
		}
		n.BodyHTML = render(c.Body)
		if !c.AuthorDeleted {
			n.ColorClass = colorClass(c.Author)
		}
	}

	n.CanReply = o.canComment && !c.Deleted && !c.Removed
	if !n.hidden {
		n.CanEdit, n.EditLeft = canEdit(o.viewer, c.Author, c.AuthorDeleted, c.Removed || c.Deleted, c.CreatedAt)
		n.CanReport = o.viewer.canReport(c.Author, c.AuthorDeleted, c.Removed || c.Deleted || o.postClosed)
		n.Vote = o.viewer.widget("comment", c.PublicID, c.Upvotes-c.Downvotes, o.myVotes[c.ID],
			c.Author, c.AuthorDeleted, c.Removed || c.Deleted || o.postClosed, true)
	}
	if o.isAdmin && !c.Removed {
		n.RemoveToken = o.removeToken
	}
	return n
}

// displayName is how a comment's author is referred to in "replying to".
func (n *commentNode) displayName() string {
	switch {
	case n.hidden:
		return n.Placeholder
	case n.AuthorDeleted:
		return "[deleted]"
	default:
		return n.Author
	}
}

// buildTree turns a flat list of comments (oldest first) into a sorted tree.
func buildTree(comments []store.Comment, o treeOptions) []*commentNode {
	nodes := make(map[int64]*commentNode, len(comments))
	for _, c := range comments {
		nodes[c.ID] = newCommentNode(c, o)
	}

	var roots []*commentNode
	for _, c := range comments {
		n := nodes[c.ID]
		if o.rootID != 0 && c.ID == o.rootID {
			roots = append(roots, n)
			continue
		}
		if parent, ok := nodes[c.ParentID]; ok {
			parent.Children = append(parent.Children, n)
		} else if c.ParentID == 0 && o.rootID == 0 {
			roots = append(roots, n)
		}
	}
	return finishLevel(roots, 0, "", o)
}

// finishLevel tidies one level of the tree (and everything under it):
// drops hidden comments that have no replies left, applies the depth rules,
// and sorts siblings.
func finishLevel(list []*commentNode, depth int, parentName string, o treeOptions) []*commentNode {
	var out []*commentNode
	for _, n := range list {
		n.Children = finishLevel(n.Children, depth+1, n.displayName(), o)

		// A deleted or removed comment with no replies has nothing to hold
		// in place, so it simply disappears.
		if n.hidden && len(n.Children) == 0 {
			continue
		}

		n.Indent = depth+1 < maxIndentDepth
		if depth >= maxIndentDepth {
			n.ReplyingTo = parentName
		}
		if depth == maxThreadDepth-1 && len(n.Children) > 0 {
			n.ContinueURL = "/p/" + o.postID + "/c/" + n.PublicID
			n.Children = nil
		}
		out = append(out, n)
	}
	sortComments(out, o.sort)
	return out
}

func sortComments(list []*commentNode, mode string) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch mode {
		case "new":
			return a.created.After(b.created)
		case "old":
			return a.created.Before(b.created)
		default: // best, with older comments winning ties
			if wa, wb := wilson(a.up, a.down), wilson(b.up, b.down); wa != wb {
				return wa > wb
			}
			return a.created.Before(b.created)
		}
	})
}

// ---- reply forms ----

// replyForm drives partials/replyform.html.
type replyForm struct {
	PostID    string
	ParentID  string // "" for a top-level comment
	Body      string
	FormToken string
}

func (s *Server) newReplyForm(postID, parentID, body string) replyForm {
	return replyForm{PostID: postID, ParentID: parentID, Body: body, FormToken: s.formToken(formComment)}
}

// replyPage is the full-page version of the reply form: used without
// JavaScript, and to show errors after a failed submission.
type replyPage struct {
	basePage
	PostID    string
	PostTitle string
	Parent    *commentNode // nil when commenting on the post itself
	Form      replyForm
	Errors    []string
}

func (s *Server) renderReplyPage(w http.ResponseWriter, r *http.Request, start time.Time,
	post store.Post, parent *store.Comment, body string, errs []string) {

	u, _ := userFrom(r.Context())
	data := replyPage{
		basePage:  newBasePage(r, "reply").withUp("/p/"+post.PublicID, "back to the post"),
		PostID:    post.PublicID,
		PostTitle: post.Title,
		Errors:    errs,
	}
	parentID := ""
	if parent != nil {
		parentID = parent.PublicID
		// Arrows on the replied-to comment work, but don't show the viewer's
		// earlier vote (one comment isn't worth an extra lookup here).
		node := newCommentNode(*parent, treeOptions{
			postID: post.PublicID, isAdmin: u.IsAdmin,
			postClosed: post.Removed || post.Deleted, viewer: s.voteViewerFor(r),
		})
		data.Parent = node
	}
	data.Form = s.newReplyForm(post.PublicID, parentID, body)
	data.Generated = time.Since(start)
	s.render(w, "reply.html", data)
}

// loadPost fetches a post by the {id} in the URL, answering 404 itself
// when there's no such post. ok is false if the caller should stop.
func (s *Server) loadPost(w http.ResponseWriter, r *http.Request) (store.Post, bool) {
	id := r.PathValue("id")
	if !publicIDPattern.MatchString(id) {
		http.NotFound(w, r)
		return store.Post{}, false
	}
	p, found, err := s.store.PostByPublicID(r.Context(), id)
	if err != nil {
		serverError(w, err)
		return store.Post{}, false
	}
	if !found {
		http.NotFound(w, r)
		return store.Post{}, false
	}
	return p, true
}

// loadComment fetches the {cid} comment and checks it belongs to post.
func (s *Server) loadComment(w http.ResponseWriter, r *http.Request, post store.Post) (store.Comment, bool) {
	cid := r.PathValue("cid")
	if !publicIDPattern.MatchString(cid) {
		http.NotFound(w, r)
		return store.Comment{}, false
	}
	c, found, err := s.store.CommentByPublicID(r.Context(), cid)
	if err != nil {
		serverError(w, err)
		return store.Comment{}, false
	}
	if !found || c.PostID != post.ID {
		http.NotFound(w, r)
		return store.Comment{}, false
	}
	return c, true
}

// GET /p/{id}/c/{cid}/reply: a reply form for one comment. htmx asks for
// just the form (to slot in under the comment); a plain link gets a page.
func (s *Server) handleReplyForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := userFrom(r.Context()); !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	start := time.Now()
	post, ok := s.loadPost(w, r)
	if !ok {
		return
	}
	parent, ok := s.loadComment(w, r, post)
	if !ok {
		return
	}
	if post.Removed || post.Deleted || parent.Removed || parent.Deleted {
		http.Error(w, "This can't be replied to anymore.", http.StatusGone)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		s.renderFragment(w, "postpage.html", "replyform", s.newReplyForm(post.PublicID, parent.PublicID, ""))
		return
	}
	s.renderReplyPage(w, r, start, post, &parent, "", nil)
}

// POST /p/{id}/comment: a new comment, on the post or (with "parent") on a comment.
func (s *Server) handleComment(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	start := time.Now()
	ctx := r.Context()

	post, ok := s.loadPost(w, r)
	if !ok {
		return
	}

	// 10,000 characters at up to 4 bytes each, plus room for the rest.
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	formProblem, handled := s.checkForm(w, r, formComment)
	if handled {
		return
	}

	body := strings.TrimSpace(strings.ReplaceAll(r.PostForm.Get("body"), "\r\n", "\n"))
	parentID := r.PostForm.Get("parent")
	if parentID != "" && !publicIDPattern.MatchString(parentID) {
		http.NotFound(w, r)
		return
	}

	// For re-showing the form with its context if something's wrong.
	var parent *store.Comment
	if parentID != "" {
		c, found, err := s.store.CommentByPublicID(ctx, parentID)
		if err != nil {
			serverError(w, err)
			return
		}
		if found && c.PostID == post.ID {
			parent = &c
		}
	}

	var errs []string
	if formProblem != "" {
		errs = append(errs, formProblem)
	}
	switch n := utf8.RuneCountInString(body); {
	case n == 0:
		errs = append(errs, "Write something first.")
	case n > maxCommentLen:
		errs = append(errs, "Comments can be up to "+strconv.Itoa(maxCommentLen)+" characters.")
	}
	if len(errs) == 0 {
		if ok, wait := s.limits.commenting.allow("user:" + strconv.FormatInt(u.ID, 10)); !ok {
			errs = append(errs, "You've commented a lot in a short time. You can comment again in "+friendlyDuration(wait)+".")
		}
	}
	if len(errs) > 0 {
		s.renderReplyPage(w, r, start, post, parent, body, errs)
		return
	}

	publicID, depth, err := s.store.CreateComment(ctx, store.NewComment{
		PostID: post.ID, ParentPublicID: parentID, AuthorID: u.ID, Body: body,
	})
	switch {
	case errors.Is(err, store.ErrPostLocked):
		s.renderReplyPage(w, r, start, post, parent, body,
			[]string{"This post was removed or deleted, so it's closed to new comments."})
		return
	case errors.Is(err, store.ErrParentGone):
		s.renderReplyPage(w, r, start, post, parent, body,
			[]string{"The comment you're replying to was removed or deleted."})
		return
	case err != nil:
		serverError(w, err)
		return
	}

	// Land on the new comment. Very deep replies live on their parent's
	// "continue this thread" page rather than the main one.
	target := "/p/" + post.PublicID
	if depth >= maxThreadDepth {
		target += "/c/" + parentID
	}
	http.Redirect(w, r, target+"#c-"+publicID, http.StatusSeeOther)
}

// ---- "continue this thread" ----

type threadPage struct {
	basePage
	PostID    string
	PostTitle string
	Comments  []*commentNode
	Sort      string
}

// GET /p/{id}/c/{cid}: one comment and everything under it.
func (s *Server) handleCommentThread(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	post, ok := s.loadPost(w, r)
	if !ok {
		return
	}
	root, ok := s.loadComment(w, r, post)
	if !ok {
		return
	}
	if post.Removed {
		if u, _ := userFrom(r.Context()); !u.IsAdmin {
			http.NotFound(w, r)
			return
		}
	}

	comments, err := s.store.CommentsForPost(r.Context(), post.ID)
	if err != nil {
		serverError(w, err)
		return
	}

	sortMode := commentSort(r)
	opts, err := s.treeOptionsFor(r, post, sortMode, root.ID, comments)
	if err != nil {
		serverError(w, err)
		return
	}
	data := threadPage{
		basePage:  newBasePage(r, "thread").withUp("/p/"+post.PublicID, "back to the full thread"),
		PostID:    post.PublicID,
		PostTitle: post.Title,
		Comments:  buildTree(comments, opts),
		Sort:      sortMode,
	}
	data.Generated = time.Since(start)
	s.render(w, "commentthread.html", data)
}

// treeOptionsFor gathers what tree building needs to know about the viewer,
// including which of these comments they've voted on.
func (s *Server) treeOptionsFor(r *http.Request, post store.Post, sortMode string, rootID int64, comments []store.Comment) (treeOptions, error) {
	u, loggedIn := userFrom(r.Context())
	o := treeOptions{
		sort:       sortMode,
		postID:     post.PublicID,
		canComment: loggedIn && u.Suspension == nil && !post.Removed && !post.Deleted,
		isAdmin:    u.IsAdmin,
		rootID:     rootID,
		postClosed: post.Removed || post.Deleted,
		viewer:     s.voteViewerFor(r),
		render:     s.bodies.Render,
	}
	if u.IsAdmin {
		o.removeToken = s.formToken(formRemoveComment)
	}

	ids := make([]int64, len(comments))
	for i, c := range comments {
		ids[i] = c.ID
	}
	mine, err := s.myVotes(r, o.viewer, store.VoteComment, ids)
	if err != nil {
		return treeOptions{}, err
	}
	o.myVotes = mine
	return o, nil
}

// ---- admin removal ----

// POST /p/{id}/c/{cid}/remove: admins only; everyone else gets a 404.
func (s *Server) handleRemoveComment(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok || !u.IsAdmin {
		http.NotFound(w, r)
		return
	}
	post, ok := s.loadPost(w, r)
	if !ok {
		return
	}
	c, ok := s.loadComment(w, r, post)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if problem, handled := s.checkForm(w, r, formRemoveComment); handled {
		return
	} else if problem != "" {
		http.Error(w, problem, http.StatusBadRequest)
		return
	}

	reason, ok := removalReason(w, r)
	if !ok {
		return
	}
	if err := s.store.Remove(r.Context(), store.Item{PostID: post.PublicID, CommentID: c.PublicID}, reason); err != nil {
		serverError(w, err)
		return
	}
	s.refreshReports()
	http.Redirect(w, r, "/p/"+post.PublicID+"#c-"+c.PublicID, http.StatusSeeOther)
}
