package store

import (
	"errors"
	"testing"
	"time"
)

func TestLockingAPost(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	other := mustUser(t, st, "red-baking-hare")
	post := mustPost(t, st, me, "A heated thread")
	cid := mustComment(t, st, post, other, "", "a fresh comment")

	if err := st.SetLocked(ctx, post.PublicID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLocked(ctx, post.PublicID, true); err != nil { // already locked: nothing to do
		t.Fatal(err)
	}
	if !mustLoadPost(t, st, post.PublicID).Locked {
		t.Fatal("not locked")
	}
	if _, _, err := st.CreateComment(ctx, NewComment{PostID: post.ID, AuthorID: me, Body: "more"}); !errors.Is(err, ErrCommentsLocked) {
		t.Errorf("commenting on a locked post: %v", err)
	}
	if _, err := st.EditComment(ctx, cid, other, "edited"); !errors.Is(err, ErrEditClosed) {
		t.Errorf("editing in a locked thread: %v", err)
	}
	if err := st.DeleteComment(ctx, cid, other); err != nil {
		t.Errorf("deleting your own comment should still work: %v", err)
	}
	if err := st.SetLocked(ctx, post.PublicID, false); err != nil {
		t.Fatal(err)
	}
	mustComment(t, st, post, me, "", "back open")
	if err := st.SetLocked(ctx, "aaaaaaaa", true); !errors.Is(err, ErrItemNotFound) {
		t.Errorf("no such post: %v", err)
	}
	if n := count(t, st, `SELECT count(*) FROM mod_log WHERE action IN ('lock', 'unlock')`); n != 2 {
		t.Errorf("%d lock entries in the log, want 2 (the repeat logs nothing)", n)
	}
}

func TestClosingAPollEarly(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	voter := mustUser(t, st, "red-baking-hare")
	post := mustPoll(t, st, me, "Q?", time.Hour, "yes", "no")
	if err := st.PollVote(ctx, post.ID, PollVoter("k", post.ID), voter, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.ClosePollNow(ctx, post.PublicID); err != nil {
		t.Fatal(err)
	}
	p := mustPollState(t, st, post.ID)
	if p.Open() || p.Total != 1 {
		t.Errorf("after closing: %+v", p)
	}
	if n := count(t, st, `SELECT count(*) FROM poll_votes`); n != 0 {
		t.Errorf("%d fingerprints kept after closing", n)
	}
	if err := st.ClosePollNow(ctx, post.PublicID); !errors.Is(err, ErrPollNotOpen) {
		t.Errorf("closing twice: %v", err)
	}
	if err := st.ClosePollNow(ctx, mustPost(t, st, me, "no poll").PublicID); !errors.Is(err, ErrPollNotFound) {
		t.Errorf("closing a post without a poll: %v", err)
	}
}

func TestRunNextNow(t *testing.T) {
	st := freshDB(t)
	boss := mustUser(t, st, "silver-sleeping-fox")
	tech := mustTag(t, st, "technology")
	today := day("2026-11-10")
	queue := func(d, q string) {
		t.Helper()
		if err := st.SaveMegaPoll(ctx, nil, MegaPoll{Day: day(d), Question: q, Options: []string{"a", "b"},
			Topics: "technology", CreatedBy: boss}); err != nil {
			t.Fatal(err)
		}
	}
	queue("2026-11-09", "yesterday")
	queue("2026-11-10", "today")
	queue("2026-11-11", "tomorrow")
	queue("2026-11-12", "the day after")
	queue("2026-11-14", "after a gap")

	if err := st.RunNextNow(ctx, today); !errors.Is(err, ErrMegaTodayWait) {
		t.Errorf("today's not up yet: %v", err)
	}
	pid, err := st.PublishMegaPoll(ctx, today, []int{tech}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RunNextNow(ctx, today); !errors.Is(err, ErrMegaTodayLive) {
		t.Errorf("today's still up: %v", err)
	}
	if err := st.Remove(ctx, Item{PostID: pid}, "rules"); err != nil {
		t.Fatal(err)
	}
	if err := st.RunNextNow(ctx, today); err != nil {
		t.Fatal(err)
	}

	list, err := st.MegaPollsFrom(ctx, day("2026-11-01"), 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range list {
		got[m.Day.Format("2006-01-02")] = m.Question
	}
	want := map[string]string{
		"2026-11-09": "yesterday", // the past is left alone
		"2026-11-10": "tomorrow", "2026-11-11": "the day after", "2026-11-13": "after a gap",
	}
	if len(got) != len(want) {
		t.Errorf("queue after: %v", got)
	}
	for d, q := range want {
		if got[d] != q {
			t.Errorf("%s: %q, want %q (whole queue %v)", d, got[d], q, got)
		}
	}
	if p := mustLoadPost(t, st, pid); !p.Removed {
		t.Error("the taken-down post should stay removed")
	}

	// With nothing queued after today, there's nothing to run.
	st2 := freshDB(t)
	if err := st2.RunNextNow(ctx, today); !errors.Is(err, ErrMegaQueueDry) {
		t.Errorf("empty queue: %v", err)
	}
}
