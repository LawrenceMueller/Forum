package web

import (
	"net/http"
	"strings"
)

// contentSecurityPolicy tells the browser exactly what this site is allowed
// to load. If an attacker ever slipped a <script> tag into a post, the
// browser would refuse to run it, because only files from Snowball itself
// are allowed. Each part:
//
//	default-src 'self'       anything not listed below: only from this site
//	script-src 'self'        scripts only from our own files, never inline
//	style-src 'self'         styles only from our own files, never inline
//	img-src 'self'           images only from this site
//	object-src 'none'        no plugins (Flash and friends)
//	base-uri 'none'          nobody can change what relative URLs point at
//	form-action 'self'       forms can only submit back to Snowball
//	frame-ancestors 'none'   no other site can embed Snowball in a frame
var contentSecurityPolicy = strings.Join([]string{
	"default-src 'self'",
	"script-src 'self'",
	"style-src 'self'",
	"img-src 'self'",
	"object-src 'none'",
	"base-uri 'none'",
	"form-action 'self'",
	"frame-ancestors 'none'",
}, "; ")

// securityHeaders is middleware that stamps every response with headers
// telling the browser to be strict with this site.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)

		// Don't guess file types: a file served as text is text, never
		// reinterpreted as a script.
		h.Set("X-Content-Type-Options", "nosniff")

		// When someone clicks a link off Snowball, don't tell the other site
		// which page they came from. Privacy, and it keeps URLs private.
		h.Set("Referrer-Policy", "no-referrer")

		// Older browsers' version of frame-ancestors 'none' (clickjacking).
		h.Set("X-Frame-Options", "DENY")

		// Other windows can't reach into ours through window.opener.
		h.Set("Cross-Origin-Opener-Policy", "same-origin")

		// Stay out of search engines and archives. The header covers every
		// response, not just HTML pages; the layout's <meta> tag says the same.
		h.Set("X-Robots-Tag", "noindex, nofollow, noarchive")

		// Snowball never needs these, so switch them off entirely.
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")

		next.ServeHTTP(w, r)
	})
}

// revalidate is for static files. "no-cache" doesn't mean "don't cache": it
// means "keep a copy, but check with the server before using it." The file
// server answers that check with a tiny "304 Not Modified" when nothing
// changed, so it stays fast, and an edited CSS or JS file shows up on the
// very next page load instead of whenever the browser's guess expires.
func revalidate(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

// robotsTxt asks every crawler to stay out. Polite crawlers, including
// the AI-training ones, check this file before anything else.
//
// A subtlety: a crawler that obeys "Disallow" never fetches the pages, so
// it never sees their noindex header either. If another site links to a
// Snowball page, a search engine may still list the bare URL (with no
// content). Both together are the usual compromise for a site that wants
// to stay private.
func robotsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("User-agent: *\nDisallow: /\n"))
}

// noStore marks a handler's responses as never to be cached. Used on the
// account pages, so a shared computer's back button or cache can't show
// someone else's form or name.
func noStore(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h(w, r)
	})
}
