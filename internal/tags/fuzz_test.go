package tags

// Fuzz test for the topics box. Go's fuzzer feeds random text through what
// the type-ahead does with it, checking promises that must hold for ANY
// text, not just the examples in tags_test.go.
//
// Run it for a while with:  go test -fuzz=FuzzTopicsBox ./internal/tags
// (Plain "go test" just runs the seeds below.)

import (
	"slices"
	"strings"
	"testing"
)

func FuzzTopicsBox(f *testing.F) {
	for _, seed := range []string{
		"", ",", "sour", "sourdough, so", "football", "c++, c#,", " , ,sour dough ;bread\nnfl",
		"Sourdough, Soccer, Bread, ", "ünïcödé, ＳＯＵＲＤＯＵＧＨ", "\x00,\xff", strings.Repeat("a,", 200),
	} {
		f.Add(seed)
	}
	tree := sample(f)
	topics := tree.All(Topic)

	f.Fuzz(func(t *testing.T, text string) {
		settled, current := Typing(text)
		for _, part := range settled {
			if part == "" || part != strings.TrimSpace(part) || strings.ContainsAny(part, ",;\n") {
				t.Fatalf("Typing(%q): settled part %q isn't a clean name", text, part)
			}
		}
		if strings.ContainsAny(current, ",;\n") {
			t.Fatalf("Typing(%q): the current part %q holds a separator", text, current)
		}

		// Suggestions are topics, never excluded ones, never too many.
		exclude := []int{4}
		got := tree.Suggest(current, 5, nil, exclude)
		if len(got) > 5 {
			t.Fatalf("Suggest(%q) gave %d, over the limit", current, len(got))
		}
		for _, tag := range got {
			if tag.Facet != Topic || slices.Contains(exclude, tag.ID) {
				t.Fatalf("Suggest(%q) offered %s", current, tag.Slug)
			}
		}

		// Recognized only returns topics, each once.
		seen := map[int]bool{}
		for _, tag := range tree.Recognized(text) {
			if tag.Facet != Topic || seen[tag.ID] {
				t.Fatalf("Recognized(%q) gave %s wrongly", text, tag.Slug)
			}
			seen[tag.ID] = true
		}

		// Picking any topic leaves the box ready for the next one, with the
		// pick recognized as exactly that topic.
		for _, pick := range topics {
			after := WithPick(text, pick)
			if _, cur := Typing(after); cur != "" {
				t.Fatalf("WithPick(%q, %s) = %q: something is still being typed", text, pick.Slug, after)
			}
			if !slices.Contains(tree.Recognized(after), pick) {
				t.Fatalf("WithPick(%q, %s) = %q, which doesn't name %s", text, pick.Slug, after, pick.Slug)
			}
		}
	})
}
