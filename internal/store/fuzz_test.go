package store

// Fuzz test for pagination cursors, which arrive straight from URLs.
// This package's TestMain sets up the test database unless -short is given,
// and the cursor doesn't need one, so fuzz it with:
//
//	go test -short -run XXX -fuzz=FuzzParseCursor -fuzztime=60s ./internal/store

import "testing"

// Any string, however strange, must either be rejected or turn into a
// cursor that survives a round trip through its URL form.
func FuzzParseCursor(f *testing.F) {
	for _, s := range []string{"1790000000123456.42", "", "abc", "12.", ".5", "-1.-1", "+5.3", "9223372036854775807.1"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		c, err := ParseCursor(s)
		if err != nil {
			return
		}
		if c.ID < 1 {
			t.Fatalf("ParseCursor(%q) accepted ID %d; IDs start at 1", s, c.ID)
		}
		again, err := ParseCursor(c.String())
		if err != nil || again != c {
			t.Fatalf("round trip broke: %q -> %+v -> %q -> %+v (%v)", s, c, c.String(), again, err)
		}
	})
}
