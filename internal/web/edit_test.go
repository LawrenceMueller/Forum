package web

import (
	"testing"
	"time"

	"snowball/internal/store"
)

func TestEditTimeLeft(t *testing.T) {
	created := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		after    time.Duration
		wantOK   bool
		wantLeft time.Duration
	}{
		{"just posted", 0, true, store.EditWindow},
		{"halfway", 5 * time.Minute, true, 5 * time.Minute},
		{"one second before the end", store.EditWindow - time.Second, true, time.Second},
		{"exactly at the end", store.EditWindow, false, 0},
		{"long after", time.Hour, false, 0},
		{"clock a little behind", -time.Second, true, store.EditWindow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			left, ok := editTimeLeft(created, created.Add(c.after))
			if ok != c.wantOK || left != c.wantLeft {
				t.Errorf("editTimeLeft = (%v, %v), want (%v, %v)", left, ok, c.wantLeft, c.wantOK)
			}
		})
	}
}

func TestCanEdit(t *testing.T) {
	me := voteViewer{user: store.User{Username: "blue-humming-otter"}, loggedIn: true}
	fresh := time.Now().Add(-time.Minute)
	stale := time.Now().Add(-time.Hour)

	cases := []struct {
		name    string
		viewer  voteViewer
		author  string
		deleted bool
		gone    bool
		created time.Time
		want    bool
	}{
		{"my fresh comment", me, "blue-humming-otter", false, false, fresh, true},
		{"my old comment", me, "blue-humming-otter", false, false, stale, false},
		{"someone else's", me, "red-baking-hare", false, false, fresh, false},
		{"removed", me, "blue-humming-otter", false, true, fresh, false},
		{"logged out", voteViewer{}, "blue-humming-otter", false, false, fresh, false},
		{"hidden author (placeholder)", me, "", false, false, fresh, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, left := canEdit(c.viewer, c.author, c.deleted, c.gone, c.created)
			if got != c.want {
				t.Errorf("canEdit = %v, want %v", got, c.want)
			}
			if got && left == "" {
				t.Error("an editable item should say how long is left")
			}
		})
	}
}
