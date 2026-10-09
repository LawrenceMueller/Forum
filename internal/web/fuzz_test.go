package web

// Fuzz test for the vote form's return address. Run it with:
//
//	go test -run XXX -fuzz=FuzzSafeBack -fuzztime=60s ./internal/web

import (
	"strings"
	"testing"
)

// asBrowserSeesIt applies what browsers do to a URL before following it:
// trim surrounding spaces and control characters, delete tabs and newlines.
func asBrowserSeesIt(u string) string {
	u = strings.TrimFunc(u, func(r rune) bool { return r <= ' ' })
	return strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(u)
}

// Whatever it's given, safeBack must return a path on Snowball itself, as a
// browser would read it. This fuzzer found the tab/newline hole on its first
// run, in under half a second.
func FuzzSafeBack(f *testing.F) {
	for _, s := range []string{"/", "/p/abcdefgh?sort=new", "//evil.example", `/\evil.example`,
		"https://evil.example", "/\t/evil.example", ""} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, in string) {
		out := safeBack(in)
		seen := asBrowserSeesIt(out)
		if !strings.HasPrefix(seen, "/") || strings.HasPrefix(seen, "//") || strings.HasPrefix(seen, `/\`) {
			t.Fatalf("safeBack(%q) = %q, which a browser follows as %q: off-site", in, out, seen)
		}
		if out != "/" && out != in {
			t.Fatalf("safeBack(%q) = %q: it should return the input unchanged, or \"/\"", in, out)
		}
	})
}
