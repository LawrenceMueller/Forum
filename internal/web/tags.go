package web

// Tags on the web side: the tag tree kept in memory, tag pages, the /tags
// index, the sidebar, and the tag fields on the post forms. The rules
// themselves (matching names, implied parents, how many of each) live in
// internal/tags; this file only connects them to requests and pages.

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"snowball/internal/store"
	"snowball/internal/tags"
)

// loadTags reads the tag system from the database and swaps it in. It runs
// at startup, and should run again whenever tags change (the admin tag page).
func (s *Server) loadTags(ctx context.Context) error {
	data, err := s.store.LoadTags(ctx)
	if err != nil {
		return err
	}
	list := make([]tags.Tag, len(data.Tags))
	for i, t := range data.Tags {
		list[i] = tags.Tag{ID: t.ID, Slug: t.Name, Display: t.Display, Description: t.Description, Facet: tags.Facet(t.Facet)}
	}
	edges := make([]tags.Edge, len(data.Edges))
	for i, e := range data.Edges {
		edges[i] = tags.Edge{Tag: e.Tag, Parent: e.Parent}
	}
	aliases := make([]tags.Alias, len(data.Aliases))
	for i, a := range data.Aliases {
		aliases[i] = tags.Alias{Tag: a.Tag, Text: a.Alias}
	}
	tree, err := tags.New(list, edges, aliases)
	if err != nil {
		return err
	}
	s.tagTree.Store(tree)
	return nil
}

// tree is the current tag tree. Requests take it once and use that copy
// throughout, so a reload halfway through a request can't mix two trees.
func (s *Server) tree() *tags.Tree { return s.tagTree.Load() }

// tagCount is how many visible posts sit under a tag (cached; stats.go).
func (s *Server) tagCount(id int) int64 {
	if counts := s.stats.tagCounts.Load(); counts != nil {
		return (*counts)[id]
	}
	return 0
}

// tagLink is a tag as a link with its post count, for lists of tags.
type tagLink struct {
	Slug, Display, Description string
	Count                      int64
	Children                   []tagLink // only filled where a page shows them
}

func (s *Server) linkFor(t *tags.Tag) tagLink {
	return tagLink{Slug: t.Slug, Display: t.Display, Description: t.Description, Count: s.tagCount(t.ID)}
}

// sidebarTagCount is how many top-level topics the sidebar lists.
const sidebarTagCount = 12

// sidebarTags are the busiest top-level topics, for the sidebar.
func (s *Server) sidebarTags() []tagLink {
	var out []tagLink
	for _, t := range s.tree().Roots(tags.Topic) {
		if t.Slug != "meta" { // meta is about the site, not a topic to browse
			out = append(out, s.linkFor(t))
		}
	}
	slices.SortStableFunc(out, func(a, b tagLink) int {
		switch {
		case a.Count > b.Count:
			return -1
		case a.Count < b.Count:
			return 1
		}
		return 0
	})
	return out[:min(sidebarTagCount, len(out))]
}

// ---- the post forms ----

// tagFields drives partials/tagpicker.html on the submit and edit pages.
type tagFields struct {
	tags.Selection
	Topic   topicField
	Formats []*tags.Tag
	Levels  []*tags.Tag
}

func (s *Server) tagFieldsFor(sel tags.Selection) tagFields {
	tree := s.tree()
	return tagFields{
		Selection: sel,
		Topic:     topicField{Topics: sel.Topics, Help: s.topicHelpFor(sel.Topics, false)},
		Formats:   tree.All(tags.Format),
		Levels:    tree.All(tags.Level),
	}
}

// ---- type-ahead for the topics box ----
//
// Two small htmx endpoints. While someone types, GET /tags/suggest returns
// the topics the last part of the box could be; clicking one asks GET
// /tags/pick, which returns the whole topics field with that topic put in.
// The server does all the matching (the same tags.Tree that checks the form
// when it's sent), so there's no tag list in the page and no JavaScript
// matcher to keep in step with the Go one.

// suggestLimit is how many suggestions show at once.
const suggestLimit = 8

// maxTopicsText caps what the endpoints read. The box itself allows 300.
const maxTopicsText = 1000

// topicField drives the "topicfield" partial.
type topicField struct {
	Topics string
	Help   topicHelp
	Focus  bool // put the cursor back in the box (after a pick)
}

// topicHelp drives the "topichelp" partial, under the box.
type topicHelp struct {
	Suggestions []topicSuggestion
	Typed       string   // the part being typed, for "no topic matches"
	NoMatch     bool     // something's typed and nothing matches it
	Full        bool     // the most topics are already settled: nothing more to suggest
	Chosen      []string // topics the box already names exactly
	Covers      []string // what those bring along (their ancestors)
}

type topicSuggestion struct {
	Slug, Display string
	Where         string // its place in the tree: "Food & drink › Baking"
}

// topicHelpFor works out what to show under a box holding text. Suggestions
// only come while typing; a page load just confirms what's already there.
func (s *Server) topicHelpFor(text string, suggest bool) topicHelp {
	tree := s.tree()
	var help topicHelp

	chosen := tree.Recognized(text)
	implied := map[int]bool{}
	for _, t := range chosen {
		help.Chosen = append(help.Chosen, t.Display)
		for _, a := range tree.Ancestors(t.ID) {
			implied[a] = true
		}
	}
	for _, t := range chosen {
		delete(implied, t.ID) // named outright, so not "also"
	}
	for id := range implied {
		if t, ok := tree.ByID(id); ok {
			help.Covers = append(help.Covers, t.Display)
		}
	}
	slices.Sort(help.Covers)

	settled, current := tags.Typing(text)
	if !suggest || current == "" {
		return help
	}
	// Leave out what's already settled, and what that already brings along
	// (adding Baking next to Sourdough would change nothing).
	done := tree.Recognized(strings.Join(settled, ","))
	if len(done) >= tags.MaxTopics {
		help.Full = true
		return help
	}
	var exclude []int
	for _, t := range done {
		exclude = append(exclude, t.ID)
		exclude = append(exclude, tree.Ancestors(t.ID)...)
	}
	for _, t := range tree.Suggest(current, suggestLimit, s.tagCount, exclude) {
		sug := topicSuggestion{Slug: t.Slug, Display: t.Display}
		if path := tree.Path(t.ID); len(path) > 1 {
			names := make([]string, len(path)-1)
			for i, p := range path[:len(path)-1] {
				names[i] = p.Display
			}
			sug.Where = strings.Join(names, " › ")
		}
		help.Suggestions = append(help.Suggestions, sug)
	}
	help.Typed = current
	help.NoMatch = len(help.Suggestions) == 0
	return help
}

// topicsParam reads the box's text from the query, refusing silly lengths.
func topicsParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	text := r.URL.Query().Get("topics")
	if len(text) > maxTopicsText {
		http.Error(w, "too long", http.StatusRequestEntityTooLarge)
		return "", false
	}
	return text, true
}

// GET /tags/suggest?topics=... (htmx, while typing)
func (s *Server) handleTopicSuggest(w http.ResponseWriter, r *http.Request) {
	text, ok := topicsParam(w, r)
	if !ok {
		return
	}
	s.renderFragment(w, "submit.html", "topichelp", s.topicHelpFor(text, true))
}

// GET /tags/pick?choose=<slug>&topics=... (htmx, a suggestion clicked)
func (s *Server) handleTopicPick(w http.ResponseWriter, r *http.Request) {
	text, ok := topicsParam(w, r)
	if !ok {
		return
	}
	tag, found := s.tree().BySlug(r.URL.Query().Get("choose"))
	if !found || tag.Facet != tags.Topic {
		http.Error(w, "no such topic", http.StatusBadRequest)
		return
	}
	text = tags.WithPick(text, tag)
	s.renderFragment(w, "submit.html", "topicfield", topicField{
		Topics: text,
		Help:   s.topicHelpFor(text, false),
		Focus:  true,
	})
}

// selectionFrom reads the tag fields of a submitted form.
func selectionFrom(r *http.Request) tags.Selection {
	return tags.Selection{
		Topics: strings.TrimSpace(r.PostForm.Get("topics")),
		Format: r.PostForm.Get("format"),
		Level:  r.PostForm.Get("level"),
	}
}

// ---- tag pages ----

// tagHeader is the top of a tag page: where the tag sits and what's below it.
type tagHeader struct {
	Display     string
	Description string
	Facet       string
	Path        []tagLink // the tags above it, top-level first
	AlsoUnder   []tagLink // other direct parents, when it has more than one
	Children    []tagLink
	Only        bool   // showing only posts with exactly this tag
	AllURL      string // including everything below
	OnlyURL     string // just this tag
}

// GET /t/{tag}
func (s *Server) handleTag(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("tag")
	tree := s.tree()

	tag, ok := tree.BySlug(name)
	if !ok {
		// Not a slug: maybe an old name or an alias. One match goes to its
		// canonical address (so /t/food keeps working); several get asked.
		switch matches := tree.Lookup(name); len(matches) {
		case 0:
			http.NotFound(w, r)
		case 1:
			target := "/t/" + matches[0].Slug
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		default:
			s.renderTagChoice(w, r, name, matches)
		}
		return
	}

	base := "/t/" + tag.Slug
	only := r.URL.Query().Get("only") == "1"
	header := &tagHeader{
		Display:     tag.Display,
		Description: tag.Description,
		Facet:       string(tag.Facet),
		Only:        only,
		AllURL:      base,
		OnlyURL:     base + "?only=1",
	}
	path := tree.Path(tag.ID)
	for _, p := range path[:len(path)-1] {
		header.Path = append(header.Path, s.linkFor(p))
	}
	if parents := tree.Parents(tag.ID); len(parents) > 1 {
		for _, p := range parents {
			if len(path) < 2 || p.ID != path[len(path)-2].ID {
				header.AlsoUnder = append(header.AlsoUnder, s.linkFor(p))
			}
		}
	}
	for _, c := range tree.Children(tag.ID) {
		header.Children = append(header.Children, s.linkFor(c))
	}

	spec := listSpec{
		sort:        store.SortNew,
		heading:     tag.Display,
		description: "newest first",
		emptyText:   "No posts here yet.",
	}
	if len(tag.Children) > 0 && !only {
		spec.description = "newest first, including everything below it"
	}
	listPath := base
	if only {
		listPath = header.OnlyURL
	}
	s.renderList(w, r, spec, listPath, store.ListOptions{TagID: tag.ID, TagOnly: only}, header)
}

// tagChoicePage asks which tag an ambiguous name meant.
type tagChoicePage struct {
	basePage
	Name    string
	Choices []tagLink
}

func (s *Server) renderTagChoice(w http.ResponseWriter, r *http.Request, name string, matches []*tags.Tag) {
	start := time.Now()
	data := tagChoicePage{basePage: newBasePage(r, "which "+name+"?").withUp("/tags", "all tags"), Name: name}
	for _, m := range matches {
		link := s.linkFor(m)
		if path := s.tree().Path(m.ID); len(path) > 1 {
			link.Description = path[0].Display + " › " + link.Description
		}
		data.Choices = append(data.Choices, link)
	}
	data.Generated = time.Since(start)
	s.render(w, "tagchoice.html", data)
}

// ---- the tag index ----

type tagsPage struct {
	basePage
	Topics  []tagLink // top-level topics, each with its children
	Formats []tagLink
	Levels  []tagLink
}

// GET /tags
func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	tree := s.tree()
	data := tagsPage{basePage: newBasePage(r, "tags").withUp("/", "front page")}
	for _, root := range tree.Roots(tags.Topic) {
		link := s.linkFor(root)
		for _, c := range tree.Children(root.ID) {
			link.Children = append(link.Children, s.linkFor(c))
		}
		data.Topics = append(data.Topics, link)
	}
	for _, f := range tree.All(tags.Format) {
		data.Formats = append(data.Formats, s.linkFor(f))
	}
	for _, l := range tree.All(tags.Level) {
		data.Levels = append(data.Levels, s.linkFor(l))
	}
	data.Generated = time.Since(start)
	s.render(w, "tags.html", data)
}

// withQuery adds key=value to a path that may already have a query.
func withQuery(path, key, value string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + key + "=" + url.QueryEscape(value)
}
