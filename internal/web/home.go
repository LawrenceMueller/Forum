package web

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"time"

	"snowball/internal/store"
)

// ---- name colors ----

// Name colors are CSS classes (nc-blue, nc-green, ... in style.css) rather
// than inline style attributes, so the Content Security Policy can forbid
// inline styles entirely.
var nameColors = map[string]bool{
	"blue": true, "green": true, "red": true, "pink": true,
	"orange": true, "yellow": true, "brown": true, "purple": true,
}

const fallbackColorCount = 4 // nc-x0 to nc-x3 in style.css

// colorClass gives a name's little square its color. Names start with a color
// word, so "blue-humming-otter" gets nc-blue. Anything else falls back to
// hashing the name, so it still always gets the same color.
func colorClass(name string) string {
	first, _, _ := strings.Cut(name, "-")
	if nameColors[first] {
		return "nc-" + first
	}
	h := fnv.New32a()
	h.Write([]byte(name))
	return fmt.Sprintf("nc-x%d", h.Sum32()%fallbackColorCount)
}

// ---- the sort bar and sidebar shared by every list page ----

// navData drives partials/sortnav.html: which sort is current, and whether
// to show the "new post" button.
type navData struct {
	Current  string // "front", "active", "new", "top", or "" (e.g. on a tag page)
	LoggedIn bool
}

func navFor(r *http.Request, current string) navData {
	_, ok := userFrom(r.Context())
	return navData{Current: current, LoggedIn: ok}
}

// postViews turns store posts into display-ready ones, with vote arrows
// showing which ones the viewer has already voted on.
func (s *Server) postViews(r *http.Request, posts []store.Post, showLastActivity bool) ([]postView, error) {
	viewer := s.voteViewerFor(r)
	ids := make([]int64, len(posts))
	for i, p := range posts {
		ids[i] = p.ID
	}
	mine, err := s.myVotes(r, viewer, store.VotePost, ids)
	if err != nil {
		return nil, err
	}

	out := make([]postView, 0, len(posts))
	for _, p := range posts {
		v := toPostView(p)
		// "last reply" only means something once a post has comments.
		v.ShowLastReply = showLastActivity && p.CommentCount > 0
		v.Vote = viewer.widget("post", p.PublicID, p.Score, mine[p.ID],
			p.Author, p.AuthorDeleted, p.Removed || p.Deleted, false)
		out = append(out, v)
	}
	return out, nil
}

// ---- the front page ----

type homePage struct {
	basePage
	Nav        navData
	Popular    []postView
	Active     []postView
	MoreActive bool // there are more active posts than fit on the front page
	Tags       []store.TagCount
}

// GET /
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	popular, _, err := s.store.ListPosts(ctx, store.ListOptions{Sort: store.SortPopular, Limit: 3})
	if err != nil {
		serverError(w, err)
		return
	}
	active, next, err := s.store.ListPosts(ctx, store.ListOptions{Sort: store.SortActive, Limit: 20})
	if err != nil {
		serverError(w, err)
		return
	}
	tags, err := s.store.TagCounts(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	popularViews, err := s.postViews(r, popular, false)
	if err != nil {
		serverError(w, err)
		return
	}
	activeViews, err := s.postViews(r, active, true)
	if err != nil {
		serverError(w, err)
		return
	}

	data := homePage{
		basePage:   newBasePage(r, "front page"),
		Nav:        navFor(r, "front"),
		Popular:    popularViews,
		Active:     activeViews,
		MoreActive: next != nil,
		Tags:       tags,
	}
	data.Generated = time.Since(start)
	s.render(w, "home.html", data)
}

// ---- full lists: newest, active, top, and tag pages ----

type listPage struct {
	basePage
	Nav         navData
	Heading     string
	Description string
	Posts       []postView
	EmptyText   string
	NextURL     string // "older posts", if there are more
	FirstURL    string // "back to the start", when not on the first page
	Tags        []store.TagCount
}

// listSpec describes one kind of list page.
type listSpec struct {
	sort        store.Sort
	nav         string
	heading     string
	description string
	emptyText   string
}

var (
	listNew = listSpec{store.SortNew, "new", "Newest", "newest first",
		"Nothing's been posted yet."}
	listActive = listSpec{store.SortActive, "active", "Active conversations", "sorted by latest activity",
		"Nothing's been posted yet."}
	listTop = listSpec{store.SortTop, "top", "Top this week", "most points in the last 7 days",
		"Nothing's been posted this week."}
)

// handleList returns a handler for a sorted list of all posts.
func (s *Server) handleList(spec listSpec, path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.renderList(w, r, spec, path, "")
	}
}

// GET /t/{tag}: newest posts with one tag.
func (s *Server) handleTag(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("tag")
	ok, err := s.store.TagExists(r.Context(), name)
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	spec := listSpec{
		sort:        store.SortNew,
		heading:     "Tagged " + name,
		description: "newest first",
		emptyText:   "No posts with this tag yet.",
	}
	s.renderList(w, r, spec, "/t/"+name, name)
}

func (s *Server) renderList(w http.ResponseWriter, r *http.Request, spec listSpec, path, tag string) {
	start := time.Now()
	ctx := r.Context()

	opt := store.ListOptions{Sort: spec.sort, Tag: tag, Limit: 30}
	if raw := r.URL.Query().Get("after"); raw != "" {
		c, err := store.ParseCursor(raw)
		if err != nil {
			// A mangled link: just show the first page.
			http.Redirect(w, r, path, http.StatusSeeOther)
			return
		}
		opt.After = &c
	}

	posts, next, err := s.store.ListPosts(ctx, opt)
	if err != nil {
		serverError(w, err)
		return
	}
	tags, err := s.store.TagCounts(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	postViews, err := s.postViews(r, posts, spec.sort == store.SortActive)
	if err != nil {
		serverError(w, err)
		return
	}

	data := listPage{
		basePage:    newBasePage(r, strings.ToLower(spec.heading)),
		Nav:         navFor(r, spec.nav),
		Heading:     spec.heading,
		Description: spec.description,
		Posts:       postViews,
		EmptyText:   spec.emptyText,
		Tags:        tags,
	}
	if opt.After != nil {
		data.FirstURL = path
		if len(posts) == 0 {
			data.EmptyText = "No older posts."
		}
	}
	if next != nil {
		data.NextURL = path + "?after=" + next.String()
	}
	data.Generated = time.Since(start)
	s.render(w, "list.html", data)
}

// ---- all tags ----

type tagsPage struct {
	basePage
	Tags []store.TagCount
}

// GET /tags
func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	tags, err := s.store.TagCounts(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	data := tagsPage{basePage: newBasePage(r, "tags"), Tags: tags}
	data.Generated = time.Since(start)
	s.render(w, "tags.html", data)
}
