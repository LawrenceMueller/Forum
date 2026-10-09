package web

import (
	"testing"
	"time"

	"snowball/internal/store"
)

func TestSafeBack(t *testing.T) {
	cases := map[string]string{
		"/":                    "/",
		"/p/abcdefgh?sort=new": "/p/abcdefgh?sort=new",
		"/new?after=123.4":     "/new?after=123.4",
		"":                     "/",
		"https://evil.example": "/",
		"//evil.example/path":  "/",
		`/\evil.example`:       "/",
		"javascript:alert(1)":  "/",
	}
	for in, want := range cases {
		if got := safeBack(in); got != want {
			t.Errorf("safeBack(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVoteWidgetRules(t *testing.T) {
	old := voteViewer{
		user:     store.User{Username: "blue-humming-otter", CreatedAt: time.Now().Add(-48 * time.Hour)},
		loggedIn: true,
		eligible: true,
	}
	young := old
	young.eligible = false

	cases := []struct {
		name    string
		viewer  voteViewer
		author  string
		deleted bool
		closed  bool
		canVote bool
	}{
		{"someone else's post", old, "red-baking-hare", false, false, true},
		{"your own post", old, "blue-humming-otter", false, false, false},
		{"a deleted account's post", old, "blue-humming-otter", true, false, true},
		{"removed content", old, "red-baking-hare", false, true, false},
		{"brand-new account", young, "red-baking-hare", false, false, false},
		{"logged out", voteViewer{}, "red-baking-hare", false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := c.viewer.widget("post", "abcdefgh", 3, 0, c.author, c.deleted, c.closed, false)
			if w.CanVote != c.canVote {
				t.Errorf("CanVote = %v, want %v (reason %q)", w.CanVote, c.canVote, w.Reason)
			}
		})
	}
}
