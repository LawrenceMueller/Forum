package web

import (
	"strings"
	"testing"
)

func TestSnippet(t *testing.T) {
	cases := []struct {
		body string
		max  int
		want string
	}{
		{"short and sweet", 160, "short and sweet"},
		{"line one\n\nline   two", 160, "line one line two"},
		{"abcdefghij", 5, "abcde…"},
		{"héllo wörld", 5, "héllo…"}, // counts characters, not bytes
	}
	for _, c := range cases {
		if got := snippet(c.body, c.max); got != c.want {
			t.Errorf("snippet(%q, %d) = %q, want %q", c.body, c.max, got, c.want)
		}
	}
	if long := snippet(strings.Repeat("a ", 200), 160); len([]rune(long)) > 161 {
		t.Errorf("snippet should stop at the limit (plus the ellipsis), got %d characters", len([]rune(long)))
	}
}
