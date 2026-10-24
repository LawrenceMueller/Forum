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

	tree := s.tree()
	out := make([]postView, 0, len(posts))
	for _, p := range posts {
		v := toPostView(p, tree)
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
	Tags       []tagLink
	Note       *noteView // the newest note from the admin; nil for none
	Mega       *megaBox  // today's mega poll; nil for none
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

	mega, err := s.megaBoxFor(r)
	if err != nil {
		serverError(w, err)
		return
	}

	data := homePage{
		Mega:       mega,
		basePage:   newBasePage(r, "front page"),
		Nav:        navFor(r, "front"),
		Popular:    popularViews,
		Active:     activeViews,
		MoreActive: next != nil,
		Note:       s.latestNote.Load(),
		Tags:       s.sidebarTags(),
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
	NextURL     string     // "older posts", if there are more
	FirstURL    string     // "back to the start", when not on the first page
	Tag         *tagHeader // set on tag pages
	Tags        []tagLink  // the sidebar
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
		s.renderList(w, r, spec, path, store.ListOptions{}, nil)
	}
}

// renderList shows one page of a list. path is the list's own address
// (with any query it needs, like ?only=1); opt carries any tag filter.
func (s *Server) renderList(w http.ResponseWriter, r *http.Request, spec listSpec, path string, opt store.ListOptions, tag *tagHeader) {
	start := time.Now()
	ctx := r.Context()

	opt.Sort, opt.Limit = spec.sort, 30
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
		Tag:         tag,
		Tags:        s.sidebarTags(),
	}
	if tag != nil {
		// Up goes to the parent tag, or to the index for a top-level one.
		if n := len(tag.Path); n > 0 {
			data.basePage = data.basePage.withUp("/t/"+tag.Path[n-1].Slug, tag.Path[n-1].Display)
		} else {
			data.basePage = data.basePage.withUp("/tags", "all tags")
		}
	}
	if opt.After != nil {
		data.FirstURL = path
		if len(posts) == 0 {
			data.EmptyText = "No older posts."
		}
	}
	if next != nil {
		data.NextURL = withQuery(path, "after", next.String())
	}
	data.Generated = time.Since(start)
	s.render(w, "list.html", data)
}
