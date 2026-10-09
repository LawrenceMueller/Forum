package markdown

// Fuzz test for the Markdown renderer: the one place Snowball tells the
// browser "this HTML is safe", so the place an attacker would aim. Run with:
//
//	go test -run XXX -fuzz=FuzzRender -fuzztime=5m ./internal/markdown

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The only elements Render may ever produce (the bluemonday allowlist).
var allowedElements = map[string]bool{
	"p": true, "br": true, "strong": true, "em": true, "del": true,
	"ul": true, "ol": true, "li": true, "blockquote": true,
	"code": true, "pre": true, "hr": true, "a": true,
}

// The only attributes, per element.
var allowedAttrs = map[string]map[string]bool{
	"a":  {"href": true, "rel": true},
	"ol": {"start": true},
}

// FuzzRender throws mutated Markdown at Render, then reads the output with a
// real HTML5 parser (the same rules browsers follow, so tricks that confuse
// a simple text search can't hide) and checks every element and attribute.
func FuzzRender(f *testing.F) {
	for _, s := range []string{
		"**bold** and *em* and ~~gone~~",
		"[link](https://example.org) and https://example.org",
		"<script>alert(1)</script>",
		`<img src=x onerror=alert(1)>`,
		"[x](javascript:alert(1))",
		"[x](JaVaScRiPt:alert(1))",
		"[x](java\tscript:alert(1))",
		"[x](&#106;avascript:alert(1))",
		"![tracker](https://evil.example/pixel.png)",
		"<a href=\"https://ok\" onclick=\"x\">a</a>",
		"```\n</code><script>x</script>\n```",
		"> quote\n> <style>body{}</style>",
		"1. one\n2. two",
		"<svg><script>alert(1)</script></svg>",
		"<!-- --><script>x</script>",
		"\x00<scr\x00ipt>",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, source string) {
		out := string(Render(source))

		body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
		nodes, err := html.ParseFragment(strings.NewReader(out), body)
		if err != nil {
			t.Fatalf("output isn't parseable HTML: %v\nsource: %q\noutput: %q", err, source, out)
		}

		var check func(n *html.Node)
		check = func(n *html.Node) {
			if n.Type == html.ElementNode {
				if !allowedElements[n.Data] {
					t.Fatalf("forbidden element <%s>\nsource: %q\noutput: %q", n.Data, source, out)
				}
				for _, a := range n.Attr {
					if !allowedAttrs[n.Data][a.Key] {
						t.Fatalf("forbidden attribute %s on <%s>\nsource: %q\noutput: %q", a.Key, n.Data, source, out)
					}
					if a.Key == "href" {
						u, err := url.Parse(a.Val)
						if err != nil {
							t.Fatalf("unparseable link %q\nsource: %q", a.Val, source)
						}
						if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
							t.Fatalf("link with scheme %q: %q\nsource: %q", u.Scheme, a.Val, source)
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				check(c)
			}
		}
		for _, n := range nodes {
			check(n)
		}
	})
}
