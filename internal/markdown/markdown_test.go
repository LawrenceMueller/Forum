package markdown

import (
	"strings"
	"testing"
)

func TestRenderAllowsFormatting(t *testing.T) {
	cases := []struct {
		name, source, want string
	}{
		{"bold", "**hi**", "<strong>hi</strong>"},
		{"italic", "*hi*", "<em>hi</em>"},
		{"strikethrough", "~~gone~~", "<del>gone</del>"},
		{"list", "- one\n- two", "<li>one</li>"},
		{"quote", "> wise words", "<blockquote>"},
		{"inline code", "use `go test`", "<code>go test</code>"},
		{"code block", "```\nfmt.Println()\n```", "<pre><code>"},
		{"link", "[snow](https://example.org)", `href="https://example.org"`},
		{"bare URL becomes a link", "see https://example.org", `href="https://example.org"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(Render(c.source)); !strings.Contains(got, c.want) {
				t.Errorf("Render(%q) = %q, want it to contain %q", c.source, got, c.want)
			}
		})
	}
}

func TestRenderBlocksDangerousThings(t *testing.T) {
	cases := []struct {
		name, source, mustNotContain string
	}{
		{"script tag", "<script>alert(1)</script>", "<script"},
		{"raw HTML", `<div onclick="alert(1)">x</div>`, "onclick"},
		{"javascript link", "[click](javascript:alert(1))", "javascript"},
		{"external image", "![x](https://tracker.example/pixel.png)", "<img"},
		{"iframe", `<iframe src="https://example.org"></iframe>`, "<iframe"},
		{"heading", "# SHOUTING", "<h1"},
		{"style attribute", `<p style="color:red">x</p>`, "style"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(Render(c.source)); strings.Contains(got, c.mustNotContain) {
				t.Errorf("Render(%q) = %q, must not contain %q", c.source, got, c.mustNotContain)
			}
		})
	}
}

func TestLinksAreNofollow(t *testing.T) {
	got := string(Render("[x](https://example.org)"))
	if !strings.Contains(got, "nofollow") {
		t.Errorf("links should carry rel=nofollow, got %q", got)
	}
}

func TestHeadingsKeepTheirText(t *testing.T) {
	got := string(Render("# Hello there"))
	if !strings.Contains(got, "Hello there") {
		t.Errorf("a stripped heading should keep its words, got %q", got)
	}
}
