package tags

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// A small tree with the interesting shapes: depth, a second parent, a
// shared alias, and the other two facets.
//
//	food-and-drink > baking > bread > sourdough
//	cycling > bike-racing < sports
//	sports > soccer, american-football   ("football" means both)
//	formats: question, news; levels: beginner
func sample(t testing.TB) *Tree {
	t.Helper()
	list := []Tag{
		{ID: 1, Slug: "food-and-drink", Display: "Food & drink", Facet: Topic},
		{ID: 2, Slug: "baking", Display: "Baking", Facet: Topic},
		{ID: 3, Slug: "bread", Display: "Bread", Facet: Topic},
		{ID: 4, Slug: "sourdough", Display: "Sourdough", Facet: Topic},
		{ID: 5, Slug: "cycling", Display: "Cycling", Facet: Topic},
		{ID: 6, Slug: "sports", Display: "Sports", Facet: Topic},
		{ID: 7, Slug: "bike-racing", Display: "Bike racing", Facet: Topic},
		{ID: 8, Slug: "soccer", Display: "Soccer", Facet: Topic},
		{ID: 9, Slug: "american-football", Display: "American football", Facet: Topic},
		{ID: 10, Slug: "question", Display: "Question", Facet: Format},
		{ID: 11, Slug: "news", Display: "News", Facet: Format},
		{ID: 12, Slug: "beginner", Display: "Beginner", Facet: Level},
		{ID: 13, Slug: "c-and-cpp", Display: "C & C++", Facet: Topic},
	}
	edges := []Edge{{2, 1}, {3, 2}, {4, 3}, {7, 5}, {7, 6}, {8, 6}, {9, 6}}
	aliases := []Alias{
		{4, "sour dough"}, {4, "wild yeast bread"},
		{8, "football"}, {9, "football"}, {9, "nfl"},
		{10, "ask"}, {13, "c++"}, {13, "c"},
	}
	tree, err := New(list, edges, aliases)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Sour Dough":    "sourdough",
		"sour-dough":    "sourdough",
		"  SOURDOUGH  ": "sourdough",
		"PointOfView":   "pointofview",
		"point of view": "pointofview",
		"C++":           "c++",
		"C#":            "c#",
		"Café":          "café",
		"ｆｕｌｌｗｉｄｔｈ":     "fullwidth", // NFKC folds look-alike forms
		"d&d":           "dd",
		"":              "",
		"--- !!":        "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAncestorsAndPath(t *testing.T) {
	tree := sample(t)
	if got := tree.Ancestors(4); !slices.Equal(got, []int{1, 2, 3}) {
		t.Errorf("sourdough's ancestors = %v, want [1 2 3]", got)
	}
	if got := tree.Ancestors(7); !slices.Equal(got, []int{5, 6}) {
		t.Errorf("bike racing's ancestors = %v, want both parents [5 6]", got)
	}
	if !tree.IsAncestor(1, 4) || tree.IsAncestor(4, 1) || tree.IsAncestor(4, 4) {
		t.Error("IsAncestor is wrong")
	}
	var names []string
	for _, tag := range tree.Path(4) {
		names = append(names, tag.Slug)
	}
	if strings.Join(names, ">") != "food-and-drink>baking>bread>sourdough" {
		t.Errorf("path = %v", names)
	}
	// Two parents: the path follows the first alphabetically (Cycling).
	if p := tree.Path(7); len(p) != 2 || p[0].Slug != "cycling" {
		t.Errorf("bike racing's path should start at cycling, got %v", p)
	}
	if roots := tree.Roots(Topic); len(roots) != 4 {
		t.Errorf("want 4 topic roots (food, cycling, sports, c), got %d", len(roots))
	}
}

func TestLoopsAreRefused(t *testing.T) {
	list := []Tag{{ID: 1, Slug: "a", Display: "A", Facet: Topic}, {ID: 2, Slug: "b", Display: "B", Facet: Topic}}
	_, err := New(list, []Edge{{1, 2}, {2, 1}}, nil)
	if !errors.Is(err, ErrLoop) {
		t.Errorf("a two-tag loop should be refused, got %v", err)
	}
	_, err = New(list, []Edge{{1, 1}}, nil)
	if !errors.Is(err, ErrLoop) {
		t.Errorf("a tag as its own parent should be refused, got %v", err)
	}
	_, err = New(append(list, Tag{ID: 3, Slug: "q", Display: "Q", Facet: Format}), []Edge{{3, 1}}, nil)
	if err == nil {
		t.Error("a format under a topic should be refused")
	}
}

func TestLookup(t *testing.T) {
	tree := sample(t)
	for in, want := range map[string]string{
		"sourdough":        "sourdough",
		"Sour-Dough":       "sourdough",
		"wild yeast bread": "sourdough",
		"Food & Drink":     "food-and-drink", // the display name works too
		"NFL":              "american-football",
		"c++":              "c-and-cpp",
	} {
		got := tree.Lookup(in)
		if len(got) != 1 || got[0].Slug != want {
			t.Errorf("Lookup(%q) = %v, want %s", in, got, want)
		}
	}
	if got := tree.Lookup("football"); len(got) != 2 {
		t.Errorf("football should be ambiguous, got %v", got)
	}
	if got := tree.Lookup("nothing-like-this"); len(got) != 0 {
		t.Errorf("unknown name matched %v", got)
	}
}

func TestChoose(t *testing.T) {
	tree := sample(t)
	cases := []struct {
		name    string
		sel     Selection
		want    []int  // IDs, in order
		problem string // a word the problem must contain; "" for none
	}{
		{"one topic", Selection{Topics: "sourdough"}, []int{4}, ""},
		{"forgiving spelling", Selection{Topics: "Sour Dough"}, []int{4}, ""},
		{"commas and spaces", Selection{Topics: " sourdough ,, soccer , "}, []int{4, 8}, ""},
		{"duplicates count once", Selection{Topics: "sourdough, sour dough"}, []int{4}, ""},
		{"an ancestor is implied", Selection{Topics: "baking, sourdough, food & drink"}, []int{4}, ""},
		{"format and level", Selection{Topics: "soccer", Format: "question", Level: "beginner"}, []int{8, 10, 12}, ""},
		{"no topic", Selection{Topics: "  "}, nil, "at least one"},
		{"too many", Selection{Topics: "sourdough, soccer, american football, cycling"}, nil, "at most 3"},
		{"unknown", Selection{Topics: "underwater basket weaving"}, nil, "no topic called"},
		{"ambiguous", Selection{Topics: "football"}, nil, "American football or Soccer"},
		{"a format typed as a topic", Selection{Topics: "ask"}, nil, "Format list"},
		{"tampered format", Selection{Topics: "soccer", Format: "sourdough"}, nil, "Pick a format"},
		{"tampered level", Selection{Topics: "soccer", Level: "nope"}, nil, "Pick a level"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ids, problems := tree.Choose(c.sel)
			if c.problem == "" {
				if len(problems) > 0 || !slices.Equal(ids, c.want) {
					t.Errorf("got ids %v, problems %q; want ids %v", ids, problems, c.want)
				}
				return
			}
			if ids != nil || !strings.Contains(strings.Join(problems, " "), c.problem) {
				t.Errorf("got ids %v, problems %q; want a problem mentioning %q", ids, problems, c.problem)
			}
		})
	}
}

func TestChipsAndSelection(t *testing.T) {
	tree := sample(t)
	chips := tree.Chips([]string{"beginner", "soccer", "question", "bread", "gone-tag"})
	var got []string
	for _, c := range chips {
		got = append(got, c.Slug)
	}
	if !slices.Equal(got, []string{"bread", "soccer", "question", "beginner"}) {
		t.Errorf("chips = %v: topics by name, then format, then level", got)
	}
	sel := tree.SelectionFor([]string{"beginner", "soccer", "question", "bread"})
	if sel != (Selection{Topics: "Bread, Soccer", Format: "question", Level: "beginner"}) {
		t.Errorf("selection = %+v", sel)
	}
	// What the edit form shows must choose the same tags again.
	ids, problems := tree.Choose(sel)
	if len(problems) > 0 || !slices.Equal(ids, []int{3, 8, 10, 12}) {
		t.Errorf("round trip: ids %v, problems %v", ids, problems)
	}
}

func TestTyping(t *testing.T) {
	cases := []struct {
		in      string
		settled []string
		current string
	}{
		{"", nil, ""},
		{"sour", nil, "sour"},
		{"bread, sou", []string{"bread"}, "sou"},
		{"bread, soccer, ", []string{"bread", "soccer"}, ""},
		{" , bread ,, so", []string{"bread"}, "so"},
	}
	for _, c := range cases {
		settled, current := Typing(c.in)
		if !slices.Equal(settled, c.settled) || current != c.current {
			t.Errorf("Typing(%q) = %q, %q; want %q, %q", c.in, settled, current, c.settled, c.current)
		}
	}
}

func TestSuggest(t *testing.T) {
	tree := sample(t)
	slugs := func(list []*Tag) string {
		var s []string
		for _, tag := range list {
			s = append(s, tag.Slug)
		}
		return strings.Join(s, ",")
	}
	counts := map[int]int64{8: 50, 9: 10} // soccer is busier than american football

	cases := []struct{ typed, want string }{
		{"sour", "sourdough"},                // display prefix
		{"wild yea", "sourdough"},            // alias prefix
		{"nfl", "american-football"},         // exact alias wins
		{"foot", "soccer,american-football"}, // alias prefix for both; busier first
		{"drink", "food-and-drink"},          // a word inside the display name
		{"ead", "bread"},                     // anywhere inside
		{"b", "baking,bike-racing,bread"},    // prefixes, then alphabetical (no counts)
		{"question", ""},                     // formats are never suggested as topics
		{"   ", ""},                          // nothing typed
		{"zzz", ""},                          // nothing matches
	}
	for _, c := range cases {
		if got := slugs(tree.Suggest(c.typed, 8, func(id int) int64 { return counts[id] }, nil)); got != c.want {
			t.Errorf("Suggest(%q) = %q, want %q", c.typed, got, c.want)
		}
	}
	if got := slugs(tree.Suggest("b", 2, nil, nil)); got != "baking,bike-racing" {
		t.Errorf("limit 2 gave %q", got)
	}
	if got := slugs(tree.Suggest("b", 8, nil, []int{2})); got != "bike-racing,bread" {
		t.Errorf("excluding baking gave %q", got)
	}
}

func TestRecognizedAndWithPick(t *testing.T) {
	tree := sample(t)
	var got []string
	for _, tag := range tree.Recognized("sour dough, football, nope, sourdough, nfl, question") {
		got = append(got, tag.Slug)
	}
	// football is ambiguous, nope is unknown, question isn't a topic, and
	// sourdough counts once.
	if strings.Join(got, ",") != "sourdough,american-football" {
		t.Errorf("Recognized = %v", got)
	}

	bread, _ := tree.BySlug("bread")
	for in, want := range map[string]string{
		"":                 "Bread, ",
		"bre":              "Bread, ",
		"soccer, bre":      "soccer, Bread, ",
		"soccer, ":         "soccer, Bread, ",
		"Bread, soccer, b": "Bread, soccer, ",
	} {
		if got := WithPick(in, bread); got != want {
			t.Errorf("WithPick(%q) = %q, want %q", in, got, want)
		}
	}
}
