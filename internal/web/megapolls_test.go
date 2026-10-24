package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSiteDays(t *testing.T) {
	// 07:30 UTC on Oct 24 is still Oct 23 in Los Angeles (UTC-7).
	at := time.Date(2026, 10, 24, 6, 30, 0, 0, time.UTC)
	if d := siteDay(at).Format(dayFormat); d != "2026-10-23" {
		t.Errorf("siteDay = %s, want 2026-10-23", d)
	}
	end := dayEnd(siteDay(at))
	if want := time.Date(2026, 10, 24, 7, 0, 0, 0, time.UTC); !end.Equal(want) {
		t.Errorf("dayEnd = %v, want midnight Pacific (%v)", end, want)
	}
	// The day the clocks go back is 25 hours long; it still ends at midnight.
	if end := dayEnd(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); !end.Equal(time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("Nov 1 ends %v, want 08:00 UTC Nov 2", end)
	}
}

func TestMegaPolls(t *testing.T) {
	s := newSite(t)
	boss := admin(t, s.user(t, "silver-sleeping-fox"))
	member := s.user(t, "blue-humming-otter")
	other := s.user(t, "red-baking-hare")
	today := siteDay(time.Now())
	tomorrow := today.AddDate(0, 0, 1).Format(dayFormat)

	// Admins only.
	for _, path := range []string{"/admin", "/admin/polls", "/admin/polls/" + tomorrow} {
		expectStatus(t, "member "+path, s.get(path, member), http.StatusNotFound)
	}
	expectContains(t, "account links the hub", s.get("/account", boss), `href="/admin"`)
	expectContains(t, "hub, nothing queued", s.get("/admin", boss), "Nothing for today")

	queue := func(path string, pairs ...string) *httptest.ResponseRecorder {
		return s.send(path, form(append(pairs, "form_token", s.token(formModerate))...), boss)
	}
	newEntry := func(day, question string, more ...string) *httptest.ResponseRecorder {
		return queue("/admin/polls", append([]string{"action", "save", "day", day, "question", question,
			"option", "Cats", "option", "Dogs", "topics", "technology"}, more...)...)
	}

	// Mistakes.
	expectContains(t, "past day", newEntry(today.AddDate(0, 0, -1).Format(dayFormat), "Q"), "That day has already been")
	expectContains(t, "no question", newEntry(tomorrow, ""), "Write the question.")
	rec := queue("/admin/polls", "action", "save", "day", tomorrow, "question", "Q", "topics", "technology")
	expectContains(t, "no options", rec, "A poll needs at least two options.")
	expectContains(t, "bad topic", queue("/admin/polls", "action", "save", "day", tomorrow, "question", "Q",
		"option", "a", "option", "b", "topics", "no-such-topic-at-all"), "There's no topic called")

	// Queue tomorrow's, then change it.
	expectRedirect(t, "queued", newEntry(tomorrow, "Tea or coffee?"), "/admin/polls?done=added")
	expectContains(t, "taken", newEntry(tomorrow, "Again?"), "There's already a mega poll queued for "+tomorrow)
	page := s.get("/admin/polls", boss)
	expectContains(t, "queue", page, "Tea or coffee?")
	expectContains(t, "next free day suggested", page, `value="`+today.Format(dayFormat)+`"`)
	expectContains(t, "edit form", s.get("/admin/polls/"+tomorrow, boss), `value="Tea or coffee?"`)
	expectRedirect(t, "saved", queue("/admin/polls/"+tomorrow, "action", "save", "day", tomorrow,
		"question", "Tea, coffee, or neither?", "option", "Tea", "option", "Coffee", "option", "Neither",
		"topics", "technology"), "/admin/polls?done=saved")
	expectContains(t, "delete needs confirming", queue("/admin/polls/"+tomorrow, "action", "delete"), "Tick the box")

	// Today's goes up straight away.
	expectRedirect(t, "today", newEntry(today.Format(dayFormat), "Cats or dogs?", "body", "Settle it."),
		"/admin/polls?done=published")
	mt := s.srv.megaToday.Load()
	if mt == nil {
		t.Fatal("today's mega poll wasn't published")
	}
	post := "/p/" + mt.PublicID
	hub := s.get("/admin", boss)
	expectContains(t, "hub shows today", hub, `<a href="`+post+`">Cats or dogs?</a>`)
	expectContains(t, "hub shows the queue", hub, "1 more queued")

	// It's from "Snowball", everywhere, never the admin's name.
	for name, page := range map[string]*httptest.ResponseRecorder{
		"post": s.get(post, member), "list": s.get("/new", member), "front": s.get("/", member),
	} {
		expectContains(t, name, page, "Snowball")
		expectMissing(t, name+" hides the admin", page, "silver-sleeping-fox")
	}
	expectContains(t, "marker", s.get("/new", member), "[mega poll]")

	// The front page box: vote there, land on the post.
	front := s.get("/", member)
	expectContains(t, "box", front, "Today's mega poll")
	expectContains(t, "box form", front, `action="`+post+`/poll"`)
	expectContains(t, "logged out box", s.get("/", nil), "to vote.")
	rec = s.send(post+"/poll", form("option", "1", "form_token", s.token(formPoll)), member)
	expectRedirect(t, "vote from the box", rec, post+"#poll")

	// After voting: the pick, no results until it closes.
	voted := s.get(post, member)
	expectContains(t, "pick", voted, "your pick")
	expectContains(t, "no results yet", voted, "the results come out when it closes")
	expectMissing(t, "no bars", voted, "<progress")
	expectContains(t, "box after voting", s.get("/", member), "You picked")
	rec = s.send(post+"/poll", form("option", "0", "form_token", s.token(formPoll)), other, "HX-Request", "true")
	expectMissing(t, "htmx vote, still no bars", rec, "<progress")
	expectContains(t, "running total", rec, "2 votes so far")
	expectContains(t, "admin sees results", s.get(post, boss), "<progress")

	// Comments don't go to the admin's inbox.
	s.send(post+"/comment", form("body", "Dogs, and I'll die on this hill.", "form_token", s.token(formComment)), member)
	expectMissing(t, "no reply badge for the admin", s.get("/", boss), "new repl")

	// The archive: open now, then the result.
	expectContains(t, "archive open", s.get("/polls", nil), "open now")
	expectMissing(t, "no result while open", s.get("/polls", nil), "a tie")
	sqlExec(t, `UPDATE polls SET closes_at = now() - interval '1 minute'`)
	archive := s.get("/polls", nil)
	expectContains(t, "a tie", archive, "a tie:")
	expectContains(t, "share", archive, "with 50% of 2 votes")
	expectContains(t, "results after close", s.get(post, member), "<progress")

	// Published ones can't be changed from the queue.
	expectRedirect(t, "edit a published one", s.get("/admin/polls/"+today.Format(dayFormat), boss), post)
}
