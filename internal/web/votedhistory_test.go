package web

// Request-level tests for the "upvoted" and "downvoted" views of the
// private history page.

import (
	"net/http"
	"testing"
)

func TestVotedHistory(t *testing.T) {
	s := newSite(t)
	author := s.user(t, "blue-humming-otter")
	me := s.user(t, "red-baking-hare")
	post := s.post(t, author, "A post I liked")
	cid := s.comment(t, post, author, "A comment I disliked")

	vote := func(kind, id, dir string) {
		t.Helper()
		rec := s.send("/vote", form("kind", kind, "id", id, "dir", dir,
			"back", "/", "form_token", s.token(formVote)), me)
		expectRedirect(t, "vote", rec, "/")
	}
	vote("post", post.PublicID, "up")
	vote("comment", cid, "down")

	up := s.get("/account/history?show=upvoted", me)
	expectStatus(t, "upvoted", up, http.StatusOK)
	expectContains(t, "upvoted", up, "A post I liked")
	expectMissing(t, "upvoted", up, "A comment I disliked")
	// The arrows come along, showing the vote, so it can be taken back.
	expectContains(t, "upvoted", up, `aria-label="Upvote"
            aria-pressed="true"`)

	down := s.get("/account/history?show=downvoted", me)
	expectContains(t, "downvoted", down, "A comment I disliked")
	expectContains(t, "downvoted", down, "A post I liked") // as the post the comment is on
	expectContains(t, "downvoted", down, "comment on")

	// Votes are private to the voter: the author's own lists are empty.
	expectContains(t, "author's upvoted", s.get("/account/history?show=upvoted", author), "You haven't upvoted anything yet.")
	expectMissing(t, "author's downvoted", s.get("/account/history?show=downvoted", author), "A comment I disliked")

	// Taking the vote back removes it from the list.
	vote("post", post.PublicID, "up")
	expectMissing(t, "after unvoting", s.get("/account/history?show=upvoted", me), "A post I liked")

	// An unknown view falls back to posts; logged out, it's the login page.
	expectContains(t, "unknown view", s.get("/account/history?show=everything", me), "You haven't posted anything yet.")
	expectRedirect(t, "logged out", s.get("/account/history?show=upvoted", nil), "/login")
}
