package store

import (
	"errors"
	"testing"
	"time"
)

func day(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestMegaPollQueue(t *testing.T) {
	st := freshDB(t)
	boss := mustUser(t, st, "silver-sleeping-fox")
	member := mustUser(t, st, "blue-humming-otter")
	tech := mustTag(t, st, "technology")

	entry := MegaPoll{Day: day("2026-11-02"), Question: "Cats or dogs?", Options: []string{"Cats", "Dogs"},
		Topics: "technology", CreatedBy: boss}
	if err := st.SaveMegaPoll(ctx, nil, entry); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveMegaPoll(ctx, nil, entry); !errors.Is(err, ErrMegaDayTaken) {
		t.Errorf("same day twice: %v", err)
	}
	other := entry
	other.Day, other.Question = day("2026-11-03"), "Tea or coffee?"
	if err := st.SaveMegaPoll(ctx, nil, other); err != nil {
		t.Fatal(err)
	}

	// Moving one onto a taken day is refused; onto a free one, fine.
	moved := entry
	moved.Day = day("2026-11-03")
	if err := st.SaveMegaPoll(ctx, &entry.Day, moved); !errors.Is(err, ErrMegaDayTaken) {
		t.Errorf("moving onto a taken day: %v", err)
	}
	moved.Day, moved.Question = day("2026-11-01"), "Cats or dogs, really?"
	if err := st.SaveMegaPoll(ctx, &entry.Day, moved); err != nil {
		t.Fatal(err)
	}
	list, err := st.MegaPollsFrom(ctx, day("2026-10-01"), 10)
	if err != nil || len(list) != 2 || list[0].Question != "Cats or dogs, really?" || !list[0].Day.Equal(day("2026-11-01")) {
		t.Fatalf("queue: %+v %v", list, err)
	}

	// Publishing.
	closes := time.Now().Add(time.Hour)
	pid, err := st.PublishMegaPoll(ctx, day("2026-11-01"), []int{tech}, closes)
	if err != nil || pid == "" {
		t.Fatalf("publish: %q %v", pid, err)
	}
	if again, err := st.PublishMegaPoll(ctx, day("2026-11-01"), []int{tech}, closes); again != "" || err != nil {
		t.Errorf("publishing twice: %q %v", again, err)
	}
	if none, err := st.PublishMegaPoll(ctx, day("2026-12-25"), []int{tech}, closes); none != "" || err != nil {
		t.Errorf("publishing an empty day: %q %v", none, err)
	}
	p := mustLoadPost(t, st, pid)
	if !p.BySite || !p.IsPoll || !p.IsMega || p.Title != "Cats or dogs, really?" || len(p.Tags) != 1 {
		t.Errorf("published post: %+v", p)
	}
	poll := mustPollState(t, st, p.ID)
	if !poll.Mega || len(poll.Options) != 2 || !poll.ClosesAt.Equal(closes.Truncate(time.Microsecond)) {
		t.Errorf("published poll: %+v", poll)
	}
	if got, found, _ := st.MegaPollPost(ctx, day("2026-11-01")); !found || got.ID != p.ID {
		t.Error("MegaPollPost should find the published post")
	}
	if posts, _, _ := st.ListPosts(ctx, ListOptions{Sort: SortNew}); len(posts) != 1 || !posts[0].IsMega || !posts[0].BySite {
		t.Errorf("lists should mark it: %+v", posts)
	}

	// Once published, the queue entry can't be changed or deleted.
	if err := st.SaveMegaPoll(ctx, &moved.Day, moved); !errors.Is(err, ErrMegaPublished) {
		t.Errorf("changing a published one: %v", err)
	}
	if err := st.DeleteMegaPoll(ctx, moved.Day); !errors.Is(err, ErrMegaPublished) {
		t.Errorf("deleting a published one: %v", err)
	}
	if err := st.DeleteMegaPoll(ctx, day("2026-11-03")); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteMegaPoll(ctx, day("2026-11-03")); !errors.Is(err, ErrMegaNotFound) {
		t.Errorf("deleting twice: %v", err)
	}

	// Comments on Snowball's own posts don't land in the admin's inbox.
	mustComment(t, st, p, member, "", "Dogs, obviously.")
	if n := count(t, st, `SELECT unread_replies FROM users WHERE id = $1`, boss); n != 0 {
		t.Errorf("the admin was notified of %d comments on a mega poll", n)
	}
	if replies, _, _ := st.Replies(ctx, boss, nil, 10); len(replies) != 0 {
		t.Errorf("the admin's inbox has %d mega poll comments", len(replies))
	}
}

func TestMegaPollArchive(t *testing.T) {
	st := freshDB(t)
	boss := mustUser(t, st, "silver-sleeping-fox")
	tech := mustTag(t, st, "technology")
	var voters []int64
	for _, name := range []string{"blue-humming-otter", "red-baking-hare", "green-napping-puffin"} {
		voters = append(voters, mustUser(t, st, name))
	}

	publish := func(d string, votes ...int) Post {
		t.Helper()
		if err := st.SaveMegaPoll(ctx, nil, MegaPoll{Day: day(d), Question: "Q " + d,
			Options: []string{"A", "B", "C"}, Topics: "technology", CreatedBy: boss}); err != nil {
			t.Fatal(err)
		}
		pid, err := st.PublishMegaPoll(ctx, day(d), []int{tech}, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		p := mustLoadPost(t, st, pid)
		for i, pick := range votes {
			if err := st.PollVote(ctx, p.ID, PollVoter("k", int64(i)+p.ID*10), voters[i], pick); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	publish("2026-10-01", 0, 0, 1)
	publish("2026-10-02", 0, 1)
	gone := publish("2026-10-03")
	publish("2026-10-04")
	if err := st.Remove(ctx, Item{PostID: gone.PublicID}, "rules"); err != nil {
		t.Fatal(err)
	}

	list, next, err := st.MegaPollArchive(ctx, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].Day.Equal(day("2026-10-04")) || !list[1].Day.Equal(day("2026-10-02")) || next == nil {
		t.Fatalf("first page (the removed one left out): %+v next %v", list, next)
	}
	if w := list[1].Winners; len(w) != 2 || list[1].WinnerVotes != 1 || list[1].Total != 2 {
		t.Errorf("a tie: %+v", list[1])
	}
	if len(list[0].Winners) != 0 || list[0].Total != 0 {
		t.Errorf("no votes, no winner: %+v", list[0])
	}
	list, next, err = st.MegaPollArchive(ctx, next, 2)
	if err != nil || len(list) != 1 || next != nil {
		t.Fatalf("second page: %+v %v %v", list, next, err)
	}
	if w := list[0].Winners; len(w) != 1 || w[0] != "A" || list[0].WinnerVotes != 2 || list[0].Total != 3 {
		t.Errorf("a clear winner: %+v", list[0])
	}
}
