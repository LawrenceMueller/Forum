package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMegaPollControls(t *testing.T) {
	s := newSite(t)
	boss := admin(t, s.user(t, "silver-sleeping-fox"))
	member := s.user(t, "blue-humming-otter")
	today := siteDay(time.Now())

	send := func(path string, pairs ...string) *httptest.ResponseRecorder {
		return s.send(path, form(append(pairs, "form_token", s.token(formModerate))...), boss)
	}
	for i, q := range []string{"Is a hot dog a sandwich?", "Cake or pie?", "Tabs or spaces?"} {
		rec := send("/admin/polls", "action", "save", "day", today.AddDate(0, 0, i).Format(dayFormat),
			"question", q, "option", "Yes", "option", "No", "topics", "technology")
		expectStatus(t, "queue "+q, rec, http.StatusSeeOther)
	}
	mt := s.srv.megaToday.Load()
	if mt == nil {
		t.Fatal("today's didn't go up")
	}
	post := "/p/" + mt.PublicID

	// The controls are the admin's alone.
	expectContains(t, "admin controls on the post", s.get(post, boss), "lock comments")
	expectContains(t, "admin controls on the post", s.get(post, boss), "close the poll now")
	expectMissing(t, "not for members", s.get(post, member), "lock comments")
	rec := s.send(post+"/lock", form("action", "lock", "form_token", s.token(formModerate)), member)
	expectStatus(t, "member locking", rec, http.StatusNotFound)
	expectContains(t, "today panel", s.get("/admin/polls", boss), "take it down")

	// Lock: readable, but no new comments.
	expectRedirect(t, "lock", send(post+"/lock", "action", "lock", "back", "/admin/polls"), "/admin/polls")
	locked := s.get(post, member)
	expectContains(t, "locked note", locked, "The admin has locked this post")
	expectMissing(t, "no comment box", locked, "<textarea")
	rec = s.send(post+"/comment", form("body", "Let me in!", "form_token", s.token(formComment)), member)
	expectContains(t, "comment refused", rec, "locked this post")
	expectContains(t, "logged", s.get("/modlog", nil), "Post locked: no new comments for now")
	expectRedirect(t, "unlock", send(post+"/lock", "action", "unlock"), post)
	expectContains(t, "comment box back", s.get(post, member), "<textarea")

	// Close the voting early: the results come out, and the box says so.
	expectRedirect(t, "close", send(post+"/poll/close", "back", "/admin/polls"), "/admin/polls")
	expectContains(t, "results out", s.get(post, member), "<progress")
	expectContains(t, "box", s.get("/", member), "Voting's closed")
	expectContains(t, "logged", s.get("/modlog", nil), "Poll closed early")

	// Take it down, then run tomorrow's.
	expectRedirect(t, "still up", send("/admin/polls/today", "action", "run-next"), "/admin/polls?done=still-up")
	expectRedirect(t, "take down", send("/admin/polls/today", "action", "takedown", "reason", "rules"),
		"/admin/polls?done=taken-down")
	if s.srv.megaToday.Load() != nil {
		t.Error("the front page box should go straight away")
	}
	expectMissing(t, "no box", s.get("/", member), "Today's mega poll")
	expectContains(t, "offer to run the next", s.get("/admin/polls", boss), "Run the next one now, “Cake or pie?”")
	expectRedirect(t, "run next", send("/admin/polls/today", "action", "run-next"), "/admin/polls?done=ran-next")
	if mt := s.srv.megaToday.Load(); mt == nil || mt.Title != "Cake or pie?" {
		t.Fatalf("today's is now %+v, want Cake or pie?", mt)
	}
	expectContains(t, "the rest moved up", s.get("/admin/polls/"+today.AddDate(0, 0, 1).Format(dayFormat), boss),
		`value="Tabs or spaces?"`)
}
