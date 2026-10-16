package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestReplyInbox(t *testing.T) {
	s := newSite(t)
	me := s.user(t, "blue-humming-otter")
	other := s.user(t, "red-baking-hare")
	post := s.post(t, me, "My question")

	expectRedirect(t, "logged out", s.get("/account/replies", nil), "/login")
	expectContains(t, "nothing yet", s.get("/account/replies", me), "No replies yet.")
	expectContains(t, "quiet header", s.get("/", me), `href="/account/replies">replies</a>`)

	// Someone answers, through the real comment form.
	rec := s.send("/p/"+post.PublicID+"/comment", form("body", "Here's an answer for you", "form_token", s.token(formComment)), other)
	expectRedirect(t, "comment", rec, "/p/")
	cid := strings.TrimPrefix(rec.Header().Get("Location"), "/p/"+post.PublicID+"#c-")
	rec = s.send("/p/"+post.PublicID+"/comment", form("body", "thanks!", "parent", cid, "form_token", s.token(formComment)), me)
	expectRedirect(t, "my reply", rec, "/p/")
	rec = s.send("/p/"+post.PublicID+"/comment", form("body", "You're welcome, glad it helped", "parent",
		strings.TrimPrefix(rec.Header().Get("Location"), "/p/"+post.PublicID+"#c-"), "form_token", s.token(formComment)), other)
	expectRedirect(t, "their reply", rec, "/p/")

	expectContains(t, "header count", s.get("/", me), "2 new replies")
	expectContains(t, "they got my thanks", s.get("/", other), "1 new reply")

	inbox := s.get("/account/replies", me)
	expectStatus(t, "inbox", inbox, http.StatusOK)
	expectContains(t, "inbox", inbox, "Here's an answer for you")
	expectContains(t, "inbox", inbox, "commented on your post")
	expectContains(t, "inbox", inbox, "You're welcome, glad it helped")
	expectContains(t, "inbox", inbox, "replied to your comment")
	if n := strings.Count(inbox.Body.String(), `class="new-badge"`); n != 2 {
		t.Errorf("%d replies marked new, want 2", n)
	}
	expectMissing(t, "the inbox page itself shows no count", inbox, "2 new replies")

	// Seen now: the count is gone, and nothing is new any more.
	expectContains(t, "header after", s.get("/", me), `href="/account/replies">replies</a>`)
	if again := s.get("/account/replies", me); strings.Contains(again.Body.String(), `class="new-badge"`) {
		t.Error("replies still marked new after the inbox was opened")
	}
}
