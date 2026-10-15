package web

// Request-level tests for moderation slice 3: cleanup and spam shadowbans.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShadowbannedBotSeesItsOwnPosts(t *testing.T) {
	s := newSite(t)
	bot := s.user(t, "blue-humming-otter")
	reader := s.user(t, "red-baking-hare")
	boss := admin(t, s.user(t, "silver-sleeping-fox"))
	real := s.post(t, reader, "A real post")

	rec := s.send("/admin/users/blue-humming-otter/shadowban", form("action", "on", "form_token", s.token(formModerate)), boss)
	expectContains(t, "needs confirming", rec, "Tick the box")
	rec = s.send("/admin/users/blue-humming-otter/shadowban", form("action", "on", "confirm", "1", "form_token", s.token(formModerate)), boss)
	expectRedirect(t, "shadowban", rec, "/admin/users/blue-humming-otter?done=shadowbanned")

	spam := s.post(t, bot, "Cheap watches")
	s.comment(t, real, bot, "Cheap watches in the comments")
	p := "/p/" + spam.PublicID

	// The bot sees its post and comment as if they were up...
	mine := s.get(p, bot)
	expectContains(t, "bot, own post", mine, "Cheap watches")
	expectMissing(t, "bot, own post", mine, "[removed")
	expectContains(t, "bot, own comment", s.get("/p/"+real.PublicID, bot), "Cheap watches in the comments")

	// ...and nobody else does.
	expectContains(t, "reader, the post", s.get(p, reader), "[removed: spam]")
	expectMissing(t, "reader, the comment", s.get("/p/"+real.PublicID, reader), "Cheap watches in the comments")
	expectMissing(t, "front page", s.get("/new", nil), "Cheap watches")
	expectContains(t, "admin sees why", s.get("/p/"+real.PublicID, boss), "silenced (shadowbanned account)")

	// Its votes and reports go nowhere, without it being told.
	vote := s.send("/vote", form("kind", "post", "id", real.PublicID, "dir", "up", "form_token", s.token(formVote)),
		bot, "HX-Request", "true")
	expectStatus(t, "bot votes", vote, http.StatusOK)
	expectContains(t, "bot votes", vote, `aria-pressed="true"`)
	if after, _, _ := testStore.PostByPublicID(ctx, real.PublicID); after.Score != 0 {
		t.Errorf("the bot's vote counted: score %d", after.Score)
	}
	expectContains(t, "bot reports", s.send("/p/"+real.PublicID+"/report",
		form("reason", "spam", "form_token", s.token(formReport)), bot), "Thanks, the admin will look at it.")
	var reports int
	if err := rawDB.QueryRow(ctx, `SELECT count(*) FROM reports`).Scan(&reports); err != nil || reports != 0 {
		t.Errorf("the bot's report was stored (%d, %v)", reports, err)
	}

	expectContains(t, "public log", s.get("/modlog", nil), "Spam account silenced")
}

func TestCleanupFromAdminPageAndQueue(t *testing.T) {
	s := newSite(t)
	spammer := s.user(t, "blue-humming-otter")
	other := s.user(t, "purple-reading-owl")
	reader := s.user(t, "red-baking-hare")
	boss := admin(t, s.user(t, "silver-sleeping-fox"))
	s.post(t, spammer, "Watches one")
	s.post(t, spammer, "Watches two")
	reported := s.post(t, other, "Watches three")
	s.post(t, other, "Watches four")

	page := "/admin/users/blue-humming-otter"
	send := func(path string, pairs ...string) *httptest.ResponseRecorder {
		return s.send(path, form(append(pairs, "form_token", s.token(formModerate))...), boss)
	}
	expectContains(t, "needs confirming", send(page+"/cleanup", "reason", "spam"), "Tick the box")
	expectRedirect(t, "cleanup", send(page+"/cleanup", "reason", "spam", "confirm", "1"), page+"?done=cleaned")
	front := s.get("/new", nil)
	expectMissing(t, "after cleanup", front, "Watches one")
	expectMissing(t, "after cleanup", front, "Watches two")
	expectContains(t, "public log", s.get("/modlog", nil), "Account cleaned up (spam): 2 posts, 0 comments, 0 votes withdrawn")

	// From the queue: remove one, and everything else its author posted.
	s.send("/p/"+reported.PublicID+"/report", form("reason", "spam", "form_token", s.token(formReport)), reader)
	rec := send("/admin/reports", "post", reported.PublicID, "comment", "", "author", "purple-reading-owl",
		"action", "remove", "reason", "spam", "cleanup", "1")
	expectRedirect(t, "queue cleanup", rec, "/admin/reports?done=cleaned")
	expectMissing(t, "after queue cleanup", s.get("/new", nil), "Watches four")
}
