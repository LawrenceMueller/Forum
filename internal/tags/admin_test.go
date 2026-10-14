package tags

import (
	"strings"
	"testing"
)

func TestNameRules(t *testing.T) {
	for slug, ok := range map[string]bool{
		"sourdough": true, "home-networking": true, "c4": true,
		"": false, "Sourdough": false, "home--networking": false, "-bread": false,
		"bread-": false, "sour dough": false, strings.Repeat("a", MaxSlugLen+1): false,
	} {
		if got := SlugProblem(slug) == ""; got != ok {
			t.Errorf("SlugProblem(%q) accepted = %v, want %v", slug, got, ok)
		}
	}
	for alias, ok := range map[string]bool{
		"sour dough": true, "c++": true, "c#": true, "3d printing": true, "café": true,
		"": false, "   ": false, ".net": false, "#hashtag": false, "!!!": false,
	} {
		if got := AliasProblem(alias) == ""; got != ok {
			t.Errorf("AliasProblem(%q) accepted = %v, want %v", alias, got, ok)
		}
	}
	if p := AliasProblem(".net"); !strings.Contains(p, "“net”") {
		t.Errorf("the .net message should say what it would really mean: %q", p)
	}
	for display, ok := range map[string]bool{"Sourdough": true, "C#": true, "": false, " ": false, "&&": false} {
		if got := DisplayProblem(display) == ""; got != ok {
			t.Errorf("DisplayProblem(%q) accepted = %v, want %v", display, got, ok)
		}
	}
}

func TestSlugify(t *testing.T) {
	for display, want := range map[string]string{
		"Food & drink":    "food-and-drink",
		"Home networking": "home-networking",
		"C++":             "c-plus-plus",
		"C#":              "c-sharp",
		"  Spaced  out  ": "spaced-out",
		"Café":            "caf", // only a starting point; the admin checks it
	} {
		if got := Slugify(display); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", display, got, want)
		}
		if got := Slugify(display); SlugProblem(got) != "" {
			t.Errorf("Slugify(%q) = %q, which isn't a valid slug", display, got)
		}
	}
}

func TestCheck(t *testing.T) {
	if problems := sample(t).Check(); len(problems) != 0 {
		t.Fatalf("the sample tree should be healthy, got %q", problems)
	}

	// Each kind of problem, added to an otherwise healthy tree.
	base := []Tag{
		{ID: 1, Slug: "networking", Display: "Networking", Facet: Topic},
		{ID: 2, Slug: "csharp", Display: "C#", Facet: Topic},
		{ID: 3, Slug: "question", Display: "Question", Facet: Format},
		{ID: 4, Slug: "news", Display: "News", Facet: Format},
	}
	for _, c := range []struct {
		what    string
		list    []Tag
		edges   []Edge
		aliases []Alias
		want    string
	}{
		{"punctuation alias", base, nil, []Alias{{2, ".net"}}, "starts with punctuation"},
		{"alias is another tag's name", base, nil, []Alias{{2, "networking"}}, "networking's own name"},
		{"same name twice", append(base[:4:4], Tag{ID: 5, Slug: "net-working", Display: "Net working", Facet: Topic}),
			nil, nil, "have the same name"},
		{"format with a parent", base, []Edge{{3, 4}}, nil, "only topics have parents"},
	} {
		tree, err := New(c.list, c.edges, c.aliases)
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		problems := tree.Check()
		if len(problems) != 1 || !strings.Contains(problems[0], c.want) {
			t.Errorf("%s: Check() = %q, want one problem mentioning %q", c.what, problems, c.want)
		}
	}
}
