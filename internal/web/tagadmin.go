package web

// The admin tag page: changing the tag tree from the site instead of by
// migration. Admins only; to everyone else every address here is a 404,
// like the other admin endpoints.
//
//	GET  /admin/tags                  every tag, a filter, health warnings, the "new tag" form
//	POST /admin/tags                  create a tag
//	GET  /admin/tags/{tag}            one tag: details, parents, aliases, merge, delete
//	POST /admin/tags/{tag}/details    change its slug, display name, description
//	POST /admin/tags/{tag}/parents    add or remove a parent
//	POST /admin/tags/{tag}/aliases    add or remove an alias
//	POST /admin/tags/{tag}/merge      fold it into another tag
//	POST /admin/tags/{tag}/delete     delete it (only when nothing uses it)
//
// Each change checks what it can up front, for clear messages, then goes
// to the store with tagCheck, which rebuilds the whole tree as it would be
// and refuses loops and NEW misleading names (tags.Tree.Check). Afterwards
// the in-memory tree is reloaded and the counts refreshed, so the site
// shows the change at once.

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"snowball/internal/store"
	"snowball/internal/tags"
)

// ---- shared pieces ----

// adminOnly wraps a handler so only admins reach it.
func (s *Server) adminOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u, ok := userFrom(r.Context()); !ok || !u.IsAdmin {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}

// tagNotices are the "done" messages, by the code in ?done=.
var tagNotices = map[string]string{
	"created": "Tag created.",
	"saved":   "Saved.",
	"parents": "Parents updated.",
	"aliases": "Aliases updated.",
	"merged":  "Merged: the other tag's posts, children and names now belong to this one.",
	"deleted": "Tag deleted.",
}

// tagCheck is the store's last look at a change: the tree as it would be
// must build (no loops), and must not gain any misleading names. Problems
// that were already there don't block unrelated changes (fixing them is
// itself a change).
func (s *Server) tagCheck() store.TagCheck {
	before := map[string]bool{}
	for _, p := range s.tree().Check() {
		before[p] = true
	}
	return func(d store.TagData) error {
		tree, err := tagTreeFrom(d)
		if errors.Is(err, tags.ErrLoop) {
			return store.TagRejected{Reason: "That would put a tag underneath itself."}
		}
		if err != nil {
			return err
		}
		var fresh []string
		for _, p := range tree.Check() {
			if !before[p] {
				fresh = append(fresh, p)
			}
		}
		if len(fresh) > 0 {
			return store.TagRejected{Reason: strings.Join(fresh, " ")}
		}
		return nil
	}
}

// afterTagChange makes the site show a change straight away.
func (s *Server) afterTagChange(r *http.Request) error {
	if err := s.loadTags(r.Context()); err != nil {
		return err
	}
	s.refreshStats() // counts depend on the tree
	return nil
}

// resolveTag finds the one tag of a facet that a typed name means.
func resolveTag(tree *tags.Tree, name string, facet tags.Facet) (*tags.Tag, string) {
	name = strings.TrimSpace(name)
	var found []*tags.Tag
	for _, t := range tree.Lookup(name) {
		if t.Facet == facet {
			found = append(found, t)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Sprintf("There's no %s called “%s”.", facet, name)
	case 1:
		return found[0], ""
	}
	slugs := make([]string, len(found))
	for i, t := range found {
		slugs[i] = t.Slug
	}
	return nil, fmt.Sprintf("“%s” could mean %s. Type the slug of the one you mean.", name, strings.Join(slugs, " or "))
}

// rejected says whether err is a refusal to show the admin (and returns its
// words), as opposed to something actually broken.
func rejected(err error) (string, bool) {
	var r store.TagRejected
	if errors.As(err, &r) {
		return r.Reason, true
	}
	if errors.Is(err, store.ErrTagNotFound) {
		return "That tag doesn't exist any more.", true
	}
	return "", false
}

// newNames are the old names worth keeping as aliases after a rename or
// merge: those that don't already lead to the tag, and that would make
// acceptable aliases at all.
func newNames(names []string, keep *tags.Tag) []string {
	have := map[string]bool{tags.Normalize(keep.Slug): true, tags.Normalize(keep.Display): true}
	for _, a := range keep.Aliases {
		have[tags.Normalize(a)] = true
	}
	var out []string
	for _, n := range names {
		k := tags.Normalize(n)
		if !have[k] && tags.AliasProblem(n) == "" {
			out = append(out, n)
			have[k] = true
		}
	}
	return out
}

// ---- the index ----

type adminTagNode struct {
	Slug, Display string
	Facet         string
	Count         int64
	Children      []adminTagNode
}

type adminTagsPage struct {
	basePage
	FormToken string
	Notice    string
	Errors    []string
	Problems  []string // tags.Tree.Check on the live tree

	Query   string
	Matches []adminTagMatch
	Topics  []adminTagNode
	Formats []adminTagNode
	Levels  []adminTagNode

	New newTagForm
}

type adminTagMatch struct {
	Slug, Display, Facet, Where string
}

type newTagForm struct {
	Display, Slug, Description, Facet, Parents string
}

func (s *Server) adminNode(tree *tags.Tree, t *tags.Tag, depth int) adminTagNode {
	n := adminTagNode{Slug: t.Slug, Display: t.Display, Facet: string(t.Facet), Count: s.tagCount(t.ID)}
	if depth < 16 { // the tree has no loops (New refuses them); this is just a guard
		for _, c := range tree.Children(t.ID) {
			n.Children = append(n.Children, s.adminNode(tree, c, depth+1))
		}
	}
	return n
}

// GET /admin/tags
func (s *Server) handleAdminTags(w http.ResponseWriter, r *http.Request) {
	s.renderAdminTags(w, r, time.Now(), newTagForm{Facet: "topic"}, nil)
}

func (s *Server) renderAdminTags(w http.ResponseWriter, r *http.Request, start time.Time, form newTagForm, errs []string) {
	tree := s.tree()
	data := adminTagsPage{
		basePage:  newBasePage(r, "tags (admin)").withUp("/tags", "tags"),
		FormToken: s.formToken(formTagAdmin),
		Notice:    tagNotices[r.URL.Query().Get("done")],
		Errors:    errs,
		Problems:  tree.Check(),
		Query:     strings.TrimSpace(r.URL.Query().Get("q")),
		New:       form,
	}
	if q := tags.Normalize(data.Query); q != "" {
		for _, f := range []tags.Facet{tags.Topic, tags.Format, tags.Level} {
			for _, t := range tree.All(f) {
				names := append([]string{t.Slug, t.Display}, t.Aliases...)
				if slices.ContainsFunc(names, func(n string) bool { return strings.Contains(tags.Normalize(n), q) }) {
					data.Matches = append(data.Matches, adminTagMatch{
						Slug: t.Slug, Display: t.Display, Facet: string(t.Facet), Where: pathText(tree, t),
					})
				}
			}
		}
	}
	for _, root := range tree.Roots(tags.Topic) {
		data.Topics = append(data.Topics, s.adminNode(tree, root, 0))
	}
	for _, t := range tree.All(tags.Format) {
		data.Formats = append(data.Formats, s.adminNode(tree, t, 0))
	}
	for _, t := range tree.All(tags.Level) {
		data.Levels = append(data.Levels, s.adminNode(tree, t, 0))
	}
	data.Generated = time.Since(start)
	s.render(w, "admintags.html", data)
}

// pathText is where a tag sits: "Food & drink › Baking" (empty at the top).
func pathText(tree *tags.Tree, t *tags.Tag) string {
	path := tree.Path(t.ID)
	names := make([]string, 0, len(path))
	for _, p := range path[:len(path)-1] {
		names = append(names, p.Display)
	}
	return strings.Join(names, " › ")
}

// adminForm parses an admin form and runs the form guard. ok is false when
// the response has been written already.
func (s *Server) adminForm(w http.ResponseWriter, r *http.Request) (problem string, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return "", false
	}
	problem, handled := s.checkForm(w, r, formTagAdmin)
	return problem, !handled
}

// POST /admin/tags
func (s *Server) handleAdminCreateTag(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	problem, ok := s.adminForm(w, r)
	if !ok {
		return
	}
	form := newTagForm{
		Display:     strings.TrimSpace(r.PostForm.Get("display")),
		Slug:        strings.TrimSpace(r.PostForm.Get("slug")),
		Description: strings.TrimSpace(r.PostForm.Get("description")),
		Facet:       r.PostForm.Get("facet"),
		Parents:     strings.TrimSpace(r.PostForm.Get("parents")),
	}
	var errs []string
	if problem != "" {
		errs = append(errs, problem)
	}
	slug := form.Slug
	if slug == "" {
		slug = tags.Slugify(form.Display)
	}
	facet := tags.Facet(form.Facet)
	if facet != tags.Topic && facet != tags.Format && facet != tags.Level {
		errs = append(errs, "Pick a kind: topic, format or level.")
	}
	for _, p := range []string{tags.DisplayProblem(form.Display), tags.SlugProblem(slug), tags.DescriptionProblem(form.Description)} {
		if p != "" {
			errs = append(errs, p)
		}
	}

	tree := s.tree()
	var parents []int
	for _, name := range strings.Split(form.Parents, ",") {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if facet != tags.Topic {
			errs = append(errs, "Only topics have parents.")
			break
		}
		p, why := resolveTag(tree, name, tags.Topic)
		if why != "" {
			errs = append(errs, why)
		} else if !slices.Contains(parents, p.ID) {
			parents = append(parents, p.ID)
		}
	}
	if len(errs) > 0 {
		s.renderAdminTags(w, r, start, form, errs)
		return
	}

	_, err := s.store.CreateTag(r.Context(), store.NewTag{
		Slug: slug, Display: form.Display, Description: form.Description, Facet: form.Facet, Parents: parents,
	}, s.tagCheck())
	if why, ok := rejected(err); ok {
		s.renderAdminTags(w, r, start, form, []string{why})
		return
	}
	if err == nil {
		err = s.afterTagChange(r)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/tags/"+slug+"?done=created", http.StatusSeeOther)
}

// ---- one tag ----

type adminTagPage struct {
	basePage
	FormToken string
	Notice    string
	Errors    []string

	Slug, Display, Description, Facet string
	IsTopic                           bool
	Path                              []tagLink
	Parents                           []tagLink
	Children                          []tagLink
	Aliases                           []string
	Count                             int64 // visible posts under it, everything below included
	PostRows                          int64 // posts carrying exactly this tag, removed ones included
	CanDelete                         bool

	Details newTagForm // the details form, refilled after a refusal
}

// adminTag finds the tag named in the address. An old name or alias goes
// to the tag's admin page (after a rename, say); ok is false when the
// response has been written already.
func (s *Server) adminTag(w http.ResponseWriter, r *http.Request) (*tags.Tag, bool) {
	name := r.PathValue("tag")
	tree := s.tree()
	if t, ok := tree.BySlug(name); ok {
		return t, true
	}
	if found := tree.Lookup(name); len(found) == 1 && r.Method == http.MethodGet {
		http.Redirect(w, r, "/admin/tags/"+found[0].Slug, http.StatusSeeOther)
		return nil, false
	}
	http.NotFound(w, r)
	return nil, false
}

// GET /admin/tags/{tag}
func (s *Server) handleAdminTag(w http.ResponseWriter, r *http.Request) {
	t, ok := s.adminTag(w, r)
	if !ok {
		return
	}
	s.renderAdminTag(w, r, time.Now(), t, newTagForm{Display: t.Display, Slug: t.Slug, Description: t.Description}, nil)
}

func (s *Server) renderAdminTag(w http.ResponseWriter, r *http.Request, start time.Time, t *tags.Tag, details newTagForm, errs []string) {
	tree := s.tree()
	rows, err := s.store.TagPostRows(r.Context(), t.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	data := adminTagPage{
		basePage:    newBasePage(r, t.Display+" (admin)").withUp("/admin/tags", "all tags (admin)"),
		FormToken:   s.formToken(formTagAdmin),
		Notice:      tagNotices[r.URL.Query().Get("done")],
		Errors:      errs,
		Slug:        t.Slug,
		Display:     t.Display,
		Description: t.Description,
		Facet:       string(t.Facet),
		IsTopic:     t.Facet == tags.Topic,
		Aliases:     slices.Sorted(slices.Values(t.Aliases)),
		Count:       s.tagCount(t.ID),
		PostRows:    rows,
		CanDelete:   rows == 0 && len(t.Children) == 0,
		Details:     details,
	}
	path := tree.Path(t.ID)
	for _, p := range path[:len(path)-1] {
		data.Path = append(data.Path, s.linkFor(p))
	}
	for _, p := range tree.Parents(t.ID) {
		data.Parents = append(data.Parents, s.linkFor(p))
	}
	for _, c := range tree.Children(t.ID) {
		data.Children = append(data.Children, s.linkFor(c))
	}
	data.Generated = time.Since(start)
	s.render(w, "admintag.html", data)
}

// tagChange runs the shared end of every POST on one tag's page: on a
// refusal, show the page again with the reason; on success, reload and go
// to the tag's page (at its new slug, if that changed).
func (s *Server) tagChange(w http.ResponseWriter, r *http.Request, start time.Time, t *tags.Tag,
	details newTagForm, err error, nextSlug, done string) {
	if why, ok := rejected(err); ok {
		s.renderAdminTag(w, r, start, t, details, []string{why})
		return
	}
	if err == nil {
		err = s.afterTagChange(r)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/tags/"+nextSlug+"?done="+done, http.StatusSeeOther)
}

// refuse shows the tag's page again with the given problems.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, start time.Time, t *tags.Tag, details newTagForm, errs ...string) {
	s.renderAdminTag(w, r, start, t, details, errs)
}

func unchanged(t *tags.Tag) newTagForm {
	return newTagForm{Display: t.Display, Slug: t.Slug, Description: t.Description}
}

// POST /admin/tags/{tag}/details
func (s *Server) handleAdminTagDetails(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	t, ok := s.adminTag(w, r)
	if !ok {
		return
	}
	problem, ok := s.adminForm(w, r)
	if !ok {
		return
	}
	form := newTagForm{
		Display:     strings.TrimSpace(r.PostForm.Get("display")),
		Slug:        strings.TrimSpace(r.PostForm.Get("slug")),
		Description: strings.TrimSpace(r.PostForm.Get("description")),
	}
	var errs []string
	for _, p := range []string{problem, tags.DisplayProblem(form.Display), tags.SlugProblem(form.Slug), tags.DescriptionProblem(form.Description)} {
		if p != "" {
			errs = append(errs, p)
		}
	}
	if len(errs) > 0 {
		s.refuse(w, r, start, t, form, errs...)
		return
	}

	// Keeping the old names: they become aliases, unless they'd still lead
	// here anyway (a change of capitals, say).
	var keep []string
	if r.PostForm.Get("keep") == "1" {
		renamed := *t
		renamed.Slug, renamed.Display = form.Slug, form.Display
		keep = newNames([]string{t.Slug, t.Display}, &renamed)
	}
	err := s.store.UpdateTag(r.Context(), t.ID, store.TagDetails{
		Slug: form.Slug, Display: form.Display, Description: form.Description, AddAliases: keep,
	}, s.tagCheck())
	s.tagChange(w, r, start, t, form, err, form.Slug, "saved")
}

// POST /admin/tags/{tag}/parents (action=add with name=, or action=remove with parent=<slug>)
func (s *Server) handleAdminTagParents(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	t, ok := s.adminTag(w, r)
	if !ok {
		return
	}
	problem, ok := s.adminForm(w, r)
	if !ok {
		return
	}
	if problem != "" {
		s.refuse(w, r, start, t, unchanged(t), problem)
		return
	}
	if t.Facet != tags.Topic {
		s.refuse(w, r, start, t, unchanged(t), "Only topics have parents.")
		return
	}
	tree := s.tree()

	var err error
	switch r.PostForm.Get("action") {
	case "add":
		parent, why := resolveTag(tree, r.PostForm.Get("name"), tags.Topic)
		switch {
		case why != "":
			s.refuse(w, r, start, t, unchanged(t), why)
			return
		case parent.ID == t.ID:
			s.refuse(w, r, start, t, unchanged(t), "A tag can't be its own parent.")
			return
		case slices.Contains(t.Parents, parent.ID):
			s.refuse(w, r, start, t, unchanged(t), fmt.Sprintf("%s is already a parent.", parent.Display))
			return
		case tree.IsAncestor(t.ID, parent.ID):
			s.refuse(w, r, start, t, unchanged(t), fmt.Sprintf("%s is underneath %s, so it can't also be above it.", parent.Display, t.Display))
			return
		case tree.IsAncestor(parent.ID, t.ID):
			s.refuse(w, r, start, t, unchanged(t), fmt.Sprintf("%s is already under %s (further up), so that would change nothing.", t.Display, parent.Display))
			return
		}
		err = s.store.AddTagParent(r.Context(), t.ID, parent.ID, s.tagCheck())
	case "remove":
		parent, found := tree.BySlug(r.PostForm.Get("parent"))
		if !found || !slices.Contains(t.Parents, parent.ID) {
			s.refuse(w, r, start, t, unchanged(t), "That isn't one of its parents.")
			return
		}
		err = s.store.RemoveTagParent(r.Context(), t.ID, parent.ID, s.tagCheck())
	default:
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	s.tagChange(w, r, start, t, unchanged(t), err, t.Slug, "parents")
}

// POST /admin/tags/{tag}/aliases (action=add or remove, alias=)
func (s *Server) handleAdminTagAliases(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	t, ok := s.adminTag(w, r)
	if !ok {
		return
	}
	problem, ok := s.adminForm(w, r)
	if !ok {
		return
	}
	if problem != "" {
		s.refuse(w, r, start, t, unchanged(t), problem)
		return
	}
	alias := strings.TrimSpace(r.PostForm.Get("alias"))

	var err error
	switch r.PostForm.Get("action") {
	case "add":
		if why := tags.AliasProblem(alias); why != "" {
			s.refuse(w, r, start, t, unchanged(t), why)
			return
		}
		if len(newNames([]string{alias}, t)) == 0 {
			s.refuse(w, r, start, t, unchanged(t), fmt.Sprintf("“%s” already leads here.", alias))
			return
		}
		err = s.store.AddTagAlias(r.Context(), t.ID, alias, s.tagCheck())
	case "remove":
		if !slices.Contains(t.Aliases, alias) {
			s.refuse(w, r, start, t, unchanged(t), "That isn't one of its aliases.")
			return
		}
		err = s.store.RemoveTagAlias(r.Context(), t.ID, alias, s.tagCheck())
	default:
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	s.tagChange(w, r, start, t, unchanged(t), err, t.Slug, "aliases")
}

// POST /admin/tags/{tag}/merge (into=<name>, confirm=1)
func (s *Server) handleAdminTagMerge(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	t, ok := s.adminTag(w, r)
	if !ok {
		return
	}
	problem, ok := s.adminForm(w, r)
	if !ok {
		return
	}
	if problem != "" {
		s.refuse(w, r, start, t, unchanged(t), problem)
		return
	}
	tree := s.tree()
	into, why := resolveTag(tree, r.PostForm.Get("into"), t.Facet)
	switch {
	case why != "":
		s.refuse(w, r, start, t, unchanged(t), why)
		return
	case into.ID == t.ID:
		s.refuse(w, r, start, t, unchanged(t), "A tag can't be merged into itself.")
		return
	case tree.IsAncestor(t.ID, into.ID):
		s.refuse(w, r, start, t, unchanged(t), fmt.Sprintf("%s is underneath %s. Move it out first, or merge the other way round.", into.Display, t.Display))
		return
	case r.PostForm.Get("confirm") != "1":
		s.refuse(w, r, start, t, unchanged(t), "Tick the box to confirm the merge: it can't be undone.")
		return
	}
	names := newNames(append([]string{t.Slug, t.Display}, t.Aliases...), into)
	err := s.store.MergeTag(r.Context(), t.ID, into.ID, names, s.tagCheck())
	s.tagChange(w, r, start, t, unchanged(t), err, into.Slug, "merged")
}

// POST /admin/tags/{tag}/delete (confirm=1)
func (s *Server) handleAdminTagDelete(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	t, ok := s.adminTag(w, r)
	if !ok {
		return
	}
	problem, ok := s.adminForm(w, r)
	if !ok {
		return
	}
	if problem != "" {
		s.refuse(w, r, start, t, unchanged(t), problem)
		return
	}
	if r.PostForm.Get("confirm") != "1" {
		s.refuse(w, r, start, t, unchanged(t), "Tick the box to confirm: deleting can't be undone.")
		return
	}
	err := s.store.DeleteTag(r.Context(), t.ID, s.tagCheck())
	if why, ok := rejected(err); ok {
		s.refuse(w, r, start, t, unchanged(t), why)
		return
	}
	if err == nil {
		err = s.afterTagChange(r)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/tags?done=deleted", http.StatusSeeOther)
}
