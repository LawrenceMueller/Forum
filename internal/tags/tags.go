// Package tags is the tag system's logic: the tree of tags, matching what
// people type to a tag, and the rules for choosing a post's tags. Like
// internal/auth, it knows nothing about HTTP or the database: the store
// loads the rows, New builds a Tree from them, and the web package asks the
// Tree questions. That keeps every rule here testable with plain values.
//
// The ideas (from the tag system design, section 11.1 of the context doc):
//
//   - A tag is a concept: one slug ("sm64-blj"), a display name, a
//     description, and any number of aliases. Whatever spelling someone
//     types is normalized and matched against all of them.
//   - Tags form a tree in which a tag may have several parents (a directed
//     graph with no loops). Choosing a tag implies all of its ancestors.
//   - Three facets: Topic (1 to 3 per post), Format (0 or 1), Level (0 or 1).
package tags

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Facet is the kind of tag.
type Facet string

const (
	Topic  Facet = "topic"  // what a post is about
	Format Facet = "format" // what kind of post it is (question, guide, ...)
	Level  Facet = "level"  // who it's for (beginner, intermediate, advanced)
)

// MaxTopics is how many topic tags a post may have.
const MaxTopics = 3

// Tag is one tag, as the rest of the app sees it.
type Tag struct {
	ID          int
	Slug        string // canonical name, used in URLs: "sourdough"
	Display     string // what people see: "Sourdough"
	Description string // one line, shown under the name
	Facet       Facet
	Parents     []int    // IDs of its direct parents (none for a top-level tag)
	Children    []int    // IDs of its direct children, by display name
	Aliases     []string // other names that lead here, as typed by the admin
}

// Edge says Parent is a direct parent of Tag.
type Edge struct{ Tag, Parent int }

// Alias is another name for a tag.
type Alias struct {
	Tag  int
	Text string
}

// Tree is the whole tag system in memory. It's built once from the
// database rows and only read afterwards, so many requests can share one
// without locking. (Changing tags means building a new Tree.)
type Tree struct {
	byID      map[int]*Tag
	bySlug    map[string]*Tag
	keys      map[string][]int // normalized name -> the tags it can mean
	ancestors map[int][]int    // every ancestor of each tag, not including itself
	roots     map[Facet][]int  // top-level tags per facet, by display name
	order     []int            // every tag ID, by display name
}

// ErrLoop means the parents would make a tag its own ancestor.
var ErrLoop = errors.New("a tag can't be its own ancestor")

// New builds a Tree. It refuses edges or aliases pointing at unknown tags,
// and any loop in the parents.
func New(list []Tag, edges []Edge, aliases []Alias) (*Tree, error) {
	t := &Tree{
		byID:      make(map[int]*Tag, len(list)),
		bySlug:    make(map[string]*Tag, len(list)),
		keys:      make(map[string][]int),
		ancestors: make(map[int][]int, len(list)),
		roots:     make(map[Facet][]int),
	}
	for i := range list {
		tag := list[i] // a copy: the tree owns its tags
		tag.Parents, tag.Children, tag.Aliases = nil, nil, nil
		if _, dup := t.byID[tag.ID]; dup {
			return nil, fmt.Errorf("tag id %d appears twice", tag.ID)
		}
		t.byID[tag.ID] = &tag
		t.bySlug[tag.Slug] = &tag
		t.order = append(t.order, tag.ID)
	}
	byDisplay := func(a, b int) int {
		return strings.Compare(strings.ToLower(t.byID[a].Display), strings.ToLower(t.byID[b].Display))
	}
	slices.SortFunc(t.order, byDisplay)

	for _, e := range edges {
		child, parent := t.byID[e.Tag], t.byID[e.Parent]
		if child == nil || parent == nil {
			return nil, fmt.Errorf("edge %d -> %d names an unknown tag", e.Tag, e.Parent)
		}
		if child.Facet != parent.Facet {
			return nil, fmt.Errorf("%s and its parent %s are different facets", child.Slug, parent.Slug)
		}
		child.Parents = append(child.Parents, parent.ID)
		parent.Children = append(parent.Children, child.ID)
	}
	for _, a := range aliases {
		tag := t.byID[a.Tag]
		if tag == nil {
			return nil, fmt.Errorf("alias %q names an unknown tag", a.Text)
		}
		tag.Aliases = append(tag.Aliases, a.Text)
	}

	for _, id := range t.order {
		tag := t.byID[id]
		slices.SortFunc(tag.Children, byDisplay)
		if len(tag.Parents) == 0 {
			t.roots[tag.Facet] = append(t.roots[tag.Facet], id)
		}
		// Every name a tag answers to: its slug, its display name, its aliases.
		for _, name := range append([]string{tag.Slug, tag.Display}, tag.Aliases...) {
			k := Normalize(name)
			if k != "" && !slices.Contains(t.keys[k], id) {
				t.keys[k] = append(t.keys[k], id)
			}
		}
	}

	// Work out every tag's ancestors, refusing loops. "visiting" marks the
	// tags on the current path: meeting one again means we went in a circle.
	visiting := make(map[int]bool)
	var walk func(id int) error
	walk = func(id int) error {
		if _, done := t.ancestors[id]; done {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("%w (%s)", ErrLoop, t.byID[id].Slug)
		}
		visiting[id] = true
		var all []int
		for _, p := range t.byID[id].Parents {
			if err := walk(p); err != nil {
				return err
			}
			all = append(all, p)
			all = append(all, t.ancestors[p]...)
		}
		slices.Sort(all)
		t.ancestors[id] = slices.Compact(all)
		visiting[id] = false
		return nil
	}
	for _, id := range t.order {
		if err := walk(id); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// Normalize turns a typed name into its lookup key: Unicode-normalized,
// lowercase, with spaces, hyphens and punctuation removed. So "Sour Dough",
// "sour-dough" and "SOURDOUGH" all become "sourdough". Plus and hash signs
// stay, so "C++" and "C#" don't collapse into "C".
//
// Plurals are NOT handled here: stripping a trailing "s" breaks words like
// "news" and "glass". Plural spellings are aliases instead.
func Normalize(s string) string {
	s = norm.NFKC.String(s)
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+' || r == '#' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---- reading the tree ----

// ByID returns a tag by its ID.
func (t *Tree) ByID(id int) (*Tag, bool) {
	tag, ok := t.byID[id]
	return tag, ok
}

// BySlug returns a tag by its exact slug.
func (t *Tree) BySlug(slug string) (*Tag, bool) {
	tag, ok := t.bySlug[slug]
	return tag, ok
}

// Lookup finds the tags a typed name could mean, any facet. An exact slug
// wins outright; otherwise every tag the normalized name belongs to is
// returned (more than one means the name is ambiguous, like "football").
func (t *Tree) Lookup(name string) []*Tag {
	if tag, ok := t.bySlug[strings.TrimSpace(name)]; ok {
		return []*Tag{tag}
	}
	var out []*Tag
	for _, id := range t.keys[Normalize(name)] {
		out = append(out, t.byID[id])
	}
	slices.SortFunc(out, func(a, b *Tag) int { return strings.Compare(a.Display, b.Display) })
	return out
}

// Ancestors returns the IDs of every ancestor of a tag (not the tag itself).
func (t *Tree) Ancestors(id int) []int { return t.ancestors[id] }

// IsAncestor reports whether a is an ancestor of b.
func (t *Tree) IsAncestor(a, b int) bool {
	_, found := slices.BinarySearch(t.ancestors[b], a)
	return found
}

// Path is the chain from a top-level tag down to this one, following each
// tag's first parent (alphabetically): Food & drink > Baking > Bread.
func (t *Tree) Path(id int) []*Tag {
	var path []*Tag
	for tag, ok := t.byID[id]; ok; {
		path = append(path, tag)
		if len(tag.Parents) == 0 || len(path) > 64 {
			break
		}
		first := slices.MinFunc(tag.Parents, func(a, b int) int {
			return strings.Compare(t.byID[a].Display, t.byID[b].Display)
		})
		tag = t.byID[first]
	}
	slices.Reverse(path)
	return path
}

// Roots returns the top-level tags of one facet, by display name.
func (t *Tree) Roots(f Facet) []*Tag { return t.tagsFor(t.roots[f]) }

// Children returns a tag's direct children, by display name.
func (t *Tree) Children(id int) []*Tag {
	if tag, ok := t.byID[id]; ok {
		return t.tagsFor(tag.Children)
	}
	return nil
}

// Parents returns a tag's direct parents, by display name.
func (t *Tree) Parents(id int) []*Tag {
	if tag, ok := t.byID[id]; ok {
		out := t.tagsFor(tag.Parents)
		slices.SortFunc(out, func(a, b *Tag) int { return strings.Compare(a.Display, b.Display) })
		return out
	}
	return nil
}

// All returns every tag of one facet, by display name.
func (t *Tree) All(f Facet) []*Tag {
	var out []*Tag
	for _, id := range t.order {
		if tag := t.byID[id]; tag.Facet == f {
			out = append(out, tag)
		}
	}
	return out
}

// Len is how many tags there are.
func (t *Tree) Len() int { return len(t.byID) }

func (t *Tree) tagsFor(ids []int) []*Tag {
	out := make([]*Tag, 0, len(ids))
	for _, id := range ids {
		out = append(out, t.byID[id])
	}
	return out
}

// ---- a post's tags ----

// Chip is one tag as shown on a post.
type Chip struct {
	Slug, Display string
	Facet         Facet
}

// Chips turns a post's tag slugs into what's shown: topics first (by
// display name), then the format, then the level. Unknown slugs are skipped.
func (t *Tree) Chips(slugs []string) []Chip {
	var out []Chip
	for _, f := range []Facet{Topic, Format, Level} {
		var group []Chip
		for _, s := range slugs {
			if tag, ok := t.bySlug[s]; ok && tag.Facet == f {
				group = append(group, Chip{tag.Slug, tag.Display, tag.Facet})
			}
		}
		slices.SortFunc(group, func(a, b Chip) int { return strings.Compare(a.Display, b.Display) })
		out = append(out, group...)
	}
	return out
}

// Selection is a post's tags as the forms show them: topics as typed text,
// format and level as slugs ("" for none).
type Selection struct {
	Topics string
	Format string
	Level  string
}

// SelectionFor turns a post's stored tag slugs back into form values.
func (t *Tree) SelectionFor(slugs []string) Selection {
	var sel Selection
	var topics []string
	for _, c := range t.Chips(slugs) {
		switch c.Facet {
		case Topic:
			topics = append(topics, c.Display)
		case Format:
			sel.Format = c.Slug
		case Level:
			sel.Level = c.Slug
		}
	}
	sel.Topics = strings.Join(topics, ", ")
	return sel
}

// Choose turns what someone filled in into tag IDs to store, or explains
// what's wrong. The rules:
//
//   - Topics are typed, separated by commas, and matched forgivingly
//     (Normalize, aliases). Unknown, ambiguous or non-topic names are errors
//     that say what to do instead.
//   - Picking a tag and one of its ancestors keeps only the deeper one:
//     the ancestor is implied anyway, so it shouldn't use up a slot.
//   - 1 to MaxTopics topics; at most one format and one level, which come
//     from fixed lists (so a bad value means a tampered form).
func (t *Tree) Choose(sel Selection) (ids []int, problems []string) {
	var topics []int
	for _, raw := range strings.FieldsFunc(sel.Topics, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		matches := t.Lookup(name)
		var asTopic []*Tag
		for _, m := range matches {
			if m.Facet == Topic {
				asTopic = append(asTopic, m)
			}
		}
		switch {
		case len(asTopic) == 1:
			if !slices.Contains(topics, asTopic[0].ID) {
				topics = append(topics, asTopic[0].ID)
			}
		case len(asTopic) > 1:
			problems = append(problems, fmt.Sprintf("“%s” could mean %s. Type the one you mean.", name, orList(asTopic)))
		case len(matches) > 0:
			problems = append(problems, fmt.Sprintf("“%s” is a %s, not a topic. Pick it from the %s list instead.",
				name, matches[0].Facet, facetLabel[matches[0].Facet]))
		default:
			problems = append(problems, fmt.Sprintf("There's no topic called “%s”. Try a broader one, and ask for it under Meta › Site tags.", name))
		}
	}

	// Drop any topic that's an ancestor of another chosen topic.
	kept := topics[:0:0]
	for _, a := range topics {
		implied := false
		for _, b := range topics {
			if a != b && t.IsAncestor(a, b) {
				implied = true
				break
			}
		}
		if !implied {
			kept = append(kept, a)
		}
	}

	if len(problems) == 0 {
		switch {
		case len(kept) == 0:
			problems = append(problems, "Add at least one topic.")
		case len(kept) > MaxTopics:
			problems = append(problems, fmt.Sprintf("Pick at most %d topics. (A topic already includes everything above it.)", MaxTopics))
		}
	}

	ids = kept
	for _, pick := range []struct {
		slug  string
		facet Facet
	}{{sel.Format, Format}, {sel.Level, Level}} {
		if pick.slug == "" {
			continue
		}
		tag, ok := t.bySlug[pick.slug]
		if !ok || tag.Facet != pick.facet {
			problems = append(problems, fmt.Sprintf("Pick a %s from the list.", pick.facet))
			continue
		}
		ids = append(ids, tag.ID)
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return ids, nil
}

// facetLabel is a facet's name as a form label.
var facetLabel = map[Facet]string{Topic: "Topics", Format: "Format", Level: "Level"}

// orList writes "A, B or C".
func orList(list []*Tag) string {
	names := make([]string, len(list))
	for i, tag := range list {
		names[i] = tag.Display
	}
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// ---- type-ahead ----

// splitTopics breaks the topics box into its comma-separated parts, keeping
// empty ones out. The last part is what's being typed right now.
func splitTopics(text string) []string {
	var out []string
	for _, raw := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if s := strings.TrimSpace(raw); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Typing splits the topics box into the topics already settled (every part
// before the last comma) and the one still being typed (after it, which may
// be empty when the box ends with a comma).
func Typing(text string) (settled []string, current string) {
	if i := strings.LastIndexAny(text, ",;\n"); i >= 0 {
		return splitTopics(text[:i]), strings.TrimSpace(text[i+1:])
	}
	return nil, strings.TrimSpace(text)
}

// Suggest finds the topics that what's being typed could become, best first:
//
//  1. a name that matches exactly ("nfl" -> American football)
//  2. a display name or slug that starts with it ("sour" -> Sourdough)
//  3. an alias that starts with it ("homebr" -> Beer & brewing)
//  4. a word inside the display name that starts with it ("network" -> Home networking)
//  5. a display name containing it anywhere (from three letters on)
//
// Ties go to the busier tag (count gives a tag's post count; nil for none),
// then alphabetical. Tags in exclude (already chosen) are left out.
func (t *Tree) Suggest(typed string, limit int, count func(id int) int64, exclude []int) []*Tag {
	q := Normalize(typed)
	if q == "" || limit < 1 {
		return nil
	}
	type hit struct {
		tag  *Tag
		rank int
	}
	var hits []hit
	for _, id := range t.order {
		tag := t.byID[id]
		if tag.Facet != Topic || slices.Contains(exclude, id) {
			continue
		}
		display, slug := Normalize(tag.Display), Normalize(tag.Slug)
		rank := 0
		switch {
		case display == q || slug == q || slices.ContainsFunc(tag.Aliases, func(a string) bool { return Normalize(a) == q }):
			rank = 1
		case strings.HasPrefix(display, q) || strings.HasPrefix(slug, q):
			rank = 2
		case slices.ContainsFunc(tag.Aliases, func(a string) bool { return strings.HasPrefix(Normalize(a), q) }):
			rank = 3
		case slices.ContainsFunc(strings.FieldsFunc(tag.Display, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }),
			func(w string) bool { return strings.HasPrefix(Normalize(w), q) }):
			rank = 4
		case len([]rune(q)) >= 3 && strings.Contains(display, q):
			// Only from three letters on: "b" is inside half the tree.
			rank = 5
		default:
			continue
		}
		hits = append(hits, hit{tag, rank})
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		if a.rank != b.rank {
			return a.rank - b.rank
		}
		if count != nil {
			if ca, cb := count(a.tag.ID), count(b.tag.ID); ca != cb {
				if ca > cb {
					return -1
				}
				return 1
			}
		}
		return strings.Compare(a.tag.Display, b.tag.Display)
	})
	out := make([]*Tag, 0, min(limit, len(hits)))
	for _, h := range hits[:min(limit, len(hits))] {
		out = append(out, h.tag)
	}
	return out
}

// Recognized returns the topics in the box that already match exactly one
// topic, in the order typed, each once. (Unknown and ambiguous names are
// skipped here; Choose explains those when the form is sent.)
func (t *Tree) Recognized(text string) []*Tag {
	var out []*Tag
	for _, name := range splitTopics(text) {
		var only *Tag
		for _, m := range t.Lookup(name) {
			if m.Facet == Topic {
				if only != nil {
					only = nil
					break
				}
				only = m
			}
		}
		if only != nil && !slices.Contains(out, only) {
			out = append(out, only)
		}
	}
	return out
}

// WithPick puts a chosen topic in place of the one being typed, keeping the
// settled ones, and ends with ", " ready for the next. Picking a topic that's
// already there just tidies the box.
func WithPick(text string, picked *Tag) string {
	settled, _ := Typing(text)
	names := slices.Clone(settled)
	if !slices.ContainsFunc(names, func(n string) bool { return Normalize(n) == Normalize(picked.Display) }) {
		names = append(names, picked.Display)
	}
	return strings.Join(names, ", ") + ", "
}
