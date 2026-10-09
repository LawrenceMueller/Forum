package store

import "testing"

func TestVoteChange(t *testing.T) {
	cases := []struct {
		name             string
		old, dir         int
		next, upD, downD int
	}{
		{"first upvote", 0, 1, 1, 1, 0},
		{"first downvote", 0, -1, -1, 0, 1},
		{"click up again: take it back", 1, 1, 0, -1, 0},
		{"click down again: take it back", -1, -1, 0, 0, -1},
		{"switch up to down", 1, -1, -1, -1, 1},
		{"switch down to up", -1, 1, 1, 1, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			next, up, down := voteChange(c.old, c.dir)
			if next != c.next || up != c.upD || down != c.downD {
				t.Errorf("voteChange(%d, %d) = (%d, %d, %d), want (%d, %d, %d)",
					c.old, c.dir, next, up, down, c.next, c.upD, c.downD)
			}
		})
	}
}
