package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"snowball/internal/store"
)

// pollPost makes a post with a poll directly in the store.
func (s *site) pollPost(t *testing.T, author *testUser, title string, closes time.Duration, options ...string) store.Post {
	t.Helper()
	pid, err := testStore.CreatePost(ctx, store.NewPost{
		AuthorID: author.id, Title: title, TagIDs: []int{tagID(t, "technology")},
		Poll: &store.NewPoll{Options: options, ClosesAt: time.Now().Add(closes)},
	})
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := testStore.PostByPublicID(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSubmitAPoll(t *testing.T) {
	s := newSite(t)
	me := s.user(t, "blue-humming-otter")
	submit := func(pairs ...string) *httptest.ResponseRecorder {
		return s.send("/submit", form(append(pairs, "topics", "technology", "form_token", s.token(formSubmit))...), me)
	}
	expectContains(t, "form", s.get("/submit", me), "Add a poll")

	for name, c := range map[string]struct {
		pairs []string
		want  string
	}{
		"one option":  {[]string{"option", "Only", "option", "", "closes", "1d"}, "A poll needs at least two options."},
		"same twice":  {[]string{"option", "Cats", "option", "cats", "closes", "1d"}, "Each poll option needs to be different"},
		"too long":    {[]string{"option", strings.Repeat("x", 81), "option", "b", "closes", "1d"}, "up to 80 characters"},
		"no duration": {[]string{"option", "a", "option", "b", "closes", "forever"}, "Pick how long the poll stays open."},
	} {
		rec := submit(append([]string{"title", "Q " + name}, c.pairs...)...)
		expectContains(t, name, rec, c.want)
		expectContains(t, name+" keeps what was typed", rec, `value="`+c.pairs[1]+`"`)
	}

	rec := submit("title", "Cats or dogs?", "option", " Cats ", "option", "", "option", "Dogs", "closes", "3h")
	expectRedirect(t, "poll", rec, "/p/")
	post, _, _ := testStore.PostByPublicID(ctx, strings.TrimPrefix(rec.Header().Get("Location"), "/p/"))
	poll, err := testStore.PollForPost(ctx, post.ID)
	if err != nil || poll == nil {
		t.Fatalf("no poll saved: %v", err)
	}
	if len(poll.Options) != 2 || poll.Options[0].Label != "Cats" || poll.Options[1].Label != "Dogs" {
		t.Errorf("options %+v: want the two filled ones, trimmed", poll.Options)
	}
	if left := time.Until(poll.ClosesAt); left < 2*time.Hour || left > 3*time.Hour {
		t.Errorf("closes in %v, want 3 hours", left)
	}
	expectContains(t, "listed", s.get("/new", me), "[poll]")

	rec = submit("title", "Just a post", "body", "words")
	expectRedirect(t, "no options, no poll", rec, "/p/")
	plain, _, _ := testStore.PostByPublicID(ctx, strings.TrimPrefix(rec.Header().Get("Location"), "/p/"))
	if plain.IsPoll {
		t.Error("a post with no options filled in shouldn't get a poll")
	}
}

func TestVotingInAPoll(t *testing.T) {
	s := newSite(t)
	asker := s.user(t, "blue-humming-otter")
	voter := s.user(t, "red-baking-hare")
	other := s.user(t, "green-napping-puffin")
	newbie := young(t, s.user(t, "purple-reading-owl"))
	post := s.pollPost(t, asker, "Cats or dogs?", 26*time.Hour, "Cats", "Dogs")
	page := "/p/" + post.PublicID

	// Before voting: options, no results.
	before := s.get(page, voter)
	expectContains(t, "form", before, `name="option" value="1"`)
	expectContains(t, "form", before, "closes in 1 day 1 hour")
	expectMissing(t, "no results yet", before, "<progress")
	expectContains(t, "logged out", s.get(page, nil), "to vote and see the results")
	expectContains(t, "new account", s.get(page, newbie), "New accounts can vote after their first day.")

	// Someone comments before voting.
	early := s.comment(t, post, voter, "I'll think about it")

	vote := func(u *testUser, option string, headers ...string) *httptest.ResponseRecorder {
		return s.send(page+"/poll", form("option", option, "form_token", s.token(formPoll)), u, headers...)
	}
	rec := vote(voter, "0", "HX-Request", "true")
	expectStatus(t, "htmx vote", rec, http.StatusOK)
	body := rec.Body.String()
	if strings.Contains(body, "<html") || !strings.Contains(body, "<progress") || !strings.Contains(body, "your pick") {
		t.Errorf("htmx vote should return just the results, with the pick marked: %s", body)
	}
	expectContains(t, "live", rec, `hx-trigger="every 15s, poll-refresh"`)
	expectContains(t, "count", rec, "100% <span")

	expectContains(t, "again", vote(voter, "1", "HX-Request", "true"), "You've already voted in this poll.")
	expectContains(t, "no pick", vote(other, "", "HX-Request", "true"), "Pick an option first.")
	expectContains(t, "newbie", vote(newbie, "0", "HX-Request", "true"), "New accounts can vote after their first day.")
	expectRedirect(t, "without JavaScript", vote(other, "1"), page+"#poll")

	// The live box, and the comment tags.
	box := s.get(page+"/poll", voter, "HX-Request", "true")
	expectContains(t, "box", box, "2 votes")
	expectRedirect(t, "box without htmx", s.get(page+"/poll", voter), page+"#poll")
	s.comment(t, post, other, "dogs, obviously") // made directly, so tag it the way the handler would
	rec = s.send(page+"/comment", form("body", "Cats, every time.", "form_token", s.token(formComment)), voter)
	expectRedirect(t, "comment", rec, page)
	thread := s.get(page, asker)
	expectContains(t, "pick tag", thread, `class="pick pick-0" title="voted for this in the poll">▸ Cats</span>`)
	if c, _, _ := testStore.CommentByPublicID(ctx, early); c.PollPick != 0 {
		t.Errorf("the comment written before voting should carry the pick too, got %d", c.PollPick)
	}
	expectContains(t, "asker hasn't voted", thread, `name="option"`)

	// Closed: final results for everyone, the winner marked.
	sqlExec(t, `UPDATE polls SET closes_at = now() - interval '1 minute' WHERE post_id = $1`, post.ID)
	closed := s.get(page, nil)
	expectContains(t, "closed", closed, "poll · closed")
	expectContains(t, "results for everyone", closed, "<progress")
	expectContains(t, "tie: both win", closed, "pick-1 winner")
	expectMissing(t, "not live", closed, "hx-trigger")
	expectContains(t, "closed vote", vote(asker, "0", "HX-Request", "true"), "This poll is closed.")
}

func TestShadowbannedPollVotesDontCount(t *testing.T) {
	s := newSite(t)
	asker := s.user(t, "blue-humming-otter")
	bot := s.user(t, "red-baking-hare")
	sqlExec(t, `UPDATE users SET shadowbanned_at = now() WHERE id = $1`, bot.id)
	post := s.pollPost(t, asker, "Q?", time.Hour, "Yes", "No")

	rec := s.send("/p/"+post.PublicID+"/poll", form("option", "1", "form_token", s.token(formPoll)), bot, "HX-Request", "true")
	expectContains(t, "looks counted", rec, "your pick")
	expectMissing(t, "but doesn't refresh", rec, "hx-trigger")
	if p, _ := testStore.PollForPost(ctx, post.ID); p.Total != 0 {
		t.Errorf("a shadowbanned vote was counted: %+v", p)
	}
}
