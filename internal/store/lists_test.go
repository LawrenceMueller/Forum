package store

import "testing"

// These tests don't need a database: cursors are plain values.

func TestCursorRoundTrip(t *testing.T) {
	c := Cursor{Value: 1790000000123456, ID: 42}
	got, err := ParseCursor(c.String())
	if err != nil || got != c {
		t.Fatalf("round trip: got %+v, %v; want %+v", got, err, c)
	}
}

func TestParseCursorRejectsJunk(t *testing.T) {
	for _, bad := range []string{"", "abc", "12", "12.", ".5", "12.x", "x.5", "12.0", "12.-3"} {
		if _, err := ParseCursor(bad); err == nil {
			t.Errorf("ParseCursor(%q) should fail", bad)
		}
	}
}
