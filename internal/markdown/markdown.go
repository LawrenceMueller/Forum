// Package markdown turns what people write into safe HTML.
//
// Two layers, on purpose:
//
//  1. goldmark converts Markdown to HTML with raw HTML switched off, so
//     anything like <script> typed into a post is dropped, not passed on.
//  2. bluemonday then sanitizes that output against a short allowlist of
//     tags. Even if a goldmark bug let something through, anything not on
//     the list is stripped.
//
// Rendering happens every time a post is shown (not once at save), so if
// the rules here are ever tightened, every old post benefits immediately.
package markdown

import (
	"bytes"
	"html/template"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// goldmark's defaults already leave raw HTML out. Extensions add
// ~~strikethrough~~ and turn bare URLs into links.
var converter = goldmark.New(
	goldmark.WithExtensions(extension.Strikethrough, extension.Linkify),
)

var policy = newPolicy()

// newPolicy is the allowlist: the only HTML a post can ever contain.
//
// Notably absent:
//   - headings (h1-h6): stripped, keeping their text, since big headings in
//     posts and comments mostly get used for shouting;
//   - images: an image from another site would send every reader's IP
//     address to whoever runs that site. Uploads come later, done safely;
//   - anything with style, class, or event attributes.
func newPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "strong", "em", "del", "ul", "ol", "li",
		"blockquote", "code", "pre", "hr")
	p.AllowAttrs("start").Matching(bluemonday.Integer).OnElements("ol")

	// Links: only http and https (so no javascript: tricks), and always
	// rel="nofollow", so search engines give spam links no credit.
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https")
	p.RequireParseableURLs(true)
	p.RequireNoFollowOnLinks(true)
	return p
}

// Render converts Markdown source into sanitized HTML, ready to put in a
// template. template.HTML tells html/template "this is already safe, don't
// escape it". It's the only place in Snowball that makes that promise, which
// is why it lives behind the sanitizer.
func Render(source string) template.HTML {
	var buf bytes.Buffer
	if err := converter.Convert([]byte(source), &buf); err != nil {
		// Should never happen; fall back to showing the text, escaped.
		return template.HTML("<p>" + template.HTMLEscapeString(source) + "</p>")
	}
	return template.HTML(policy.SanitizeBytes(buf.Bytes()))
}
