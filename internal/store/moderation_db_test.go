package store

import (
	"errors"
	"fmt"
	"testing"
)

func TestReportRules(t *testing.T) {
	st := freshDB(t)
	author := mustUser(t, st, "blue-humming-otter")
	post := mustPost(t, st, author, "Something")
	it := Item{PostID: post.PublicID}

	if _, err := st.Report(ctx, NewReport{Item: it, Reporter: "fp-a", Reason: "spam"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Report(ctx, NewReport{Item: it, Reporter: "fp-a", Reason: "harassment"}); !errors.Is(err, ErrAlreadyReported) {
		t.Errorf("a second report from the same person gave %v, want ErrAlreadyReported", err)
	}
	if _, err := st.Report(ctx, NewReport{Item: it, Reporter: "fp-b", Reason: "made-up"}); err == nil {
		t.Error("an unknown reason was accepted")
	}
	if _, err := st.Report(ctx, NewReport{Item: Item{PostID: "aaaaaaaa"}, Reporter: "fp-b", Reason: "spam"}); !errors.Is(err, ErrItemNotFound) {
		t.Errorf("reporting a missing post gave %v", err)
	}

	// The daily cap, per reporter.
	for i := range ReportsPerDay {
		p := mustPost(t, st, author, fmt.Sprintf("Post %d", i))
		_, err := st.Report(ctx, NewReport{Item: Item{PostID: p.PublicID}, Reporter: "fp-busy", Reason: "spam"})
		if err != nil {
			t.Fatalf("report %d: %v", i+1, err)
		}
	}
	extra := mustPost(t, st, author, "One too many")
	if _, err := st.Report(ctx, NewReport{Item: Item{PostID: extra.PublicID}, Reporter: "fp-busy", Reason: "spam"}); !errors.Is(err, ErrReportLimit) {
		t.Errorf("report %d in a day gave %v, want ErrReportLimit", ReportsPerDay+1, err)
	}
}

func TestSeriousReportsHideThenDismissRestores(t *testing.T) {
	st := freshDB(t)
	author := mustUser(t, st, "blue-humming-otter")
	post := mustPost(t, st, author, "A thread")
	cid := mustComment(t, st, post, author, "", "my neighbour lives at ...")
	it := Item{PostID: post.PublicID, CommentID: cid}

	// Lots of ordinary reports never hide anything.
	for i := range 5 {
		if _, err := st.Report(ctx, NewReport{Item: it, Reporter: fmt.Sprint("spam-", i), Reason: "spam"}); err != nil {
			t.Fatal(err)
		}
	}
	// Serious ones do, at the third.
	for i := range HoldAfter {
		held, err := st.Report(ctx, NewReport{Item: it, Reporter: fmt.Sprint("p-", i), Reason: "personal"})
		if err != nil {
			t.Fatal(err)
		}
		if want := i == HoldAfter-1; held != want {
			t.Errorf("serious report %d: held = %v, want %v", i+1, held, want)
		}
	}
	c := mustLoadComment(t, st, cid)
	if !c.Removed || c.RemovalReason != "held" {
		t.Fatalf("after %d serious reports: removed %v, reason %q", HoldAfter, c.Removed, c.RemovalReason)
	}
	if p := mustLoadPost(t, st, post.PublicID); p.CommentCount != 0 {
		t.Errorf("a held comment still counts: comment_count = %d", p.CommentCount)
	}
	if n, _ := st.OpenReportItems(ctx); n != 1 {
		t.Errorf("open report items = %d, want 1", n)
	}

	// The admin finds it fine: it comes back, and the count with it.
	if err := st.Dismiss(ctx, it); err != nil {
		t.Fatal(err)
	}
	c = mustLoadComment(t, st, cid)
	if c.Removed || c.RemovalReason != "" {
		t.Errorf("after dismissing: removed %v, reason %q", c.Removed, c.RemovalReason)
	}
	if p := mustLoadPost(t, st, post.PublicID); p.CommentCount != 1 {
		t.Errorf("after restoring: comment_count = %d, want 1", p.CommentCount)
	}
	if n, _ := st.OpenReportItems(ctx); n != 0 {
		t.Errorf("open report items = %d after dismissing, want 0", n)
	}

	// The log: hold, dismiss, restore; the public view leaves out the dismissal.
	all, err := st.ModLog(ctx, 0, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range all {
		actions = append(actions, e.Action)
	}
	if fmt.Sprint(actions) != "[restore dismiss hold]" {
		t.Errorf("log, newest first = %v", actions)
	}
	if all[1].Reports != 8 || all[2].Reports != HoldAfter {
		t.Errorf("reports settled: dismiss %d (want 8), hold %d (want %d)", all[1].Reports, all[2].Reports, HoldAfter)
	}
	public, _ := st.ModLog(ctx, 0, 10, false)
	if len(public) != 2 {
		t.Errorf("the public log has %d entries, want 2 (no dismissal)", len(public))
	}
}

func TestRemoveWithReason(t *testing.T) {
	st := freshDB(t)
	author := mustUser(t, st, "blue-humming-otter")
	post := mustPost(t, st, author, "A thread")
	cid := mustComment(t, st, post, author, "", "buy cheap watches")
	it := Item{PostID: post.PublicID, CommentID: cid}

	for i := range HoldAfter {
		if _, err := st.Report(ctx, NewReport{Item: it, Reporter: fmt.Sprint("r-", i), Reason: "illegal"}); err != nil {
			t.Fatal(err)
		}
	}
	// Held, then removed for real: the reason changes, the count drops once.
	if err := st.Remove(ctx, Item{CommentID: cid}, "spam"); err != nil {
		t.Fatal(err)
	}
	c := mustLoadComment(t, st, cid)
	if !c.Removed || c.RemovalReason != "spam" {
		t.Errorf("removed %v, reason %q", c.Removed, c.RemovalReason)
	}
	if p := mustLoadPost(t, st, post.PublicID); p.CommentCount != 0 {
		t.Errorf("comment_count = %d, want 0", p.CommentCount)
	}
	if n, _ := st.OpenReportItems(ctx); n != 0 {
		t.Error("removing should close the item's reports")
	}
	if err := st.Remove(ctx, it, "made-up"); err == nil {
		t.Error("an unknown removal reason was accepted")
	}
	if err := st.Remove(ctx, Item{PostID: post.PublicID}, "rules"); err != nil {
		t.Fatal(err)
	}
	if p := mustLoadPost(t, st, post.PublicID); !p.Removed || p.RemovalReason != "rules" {
		t.Errorf("post: removed %v, reason %q", p.Removed, p.RemovalReason)
	}
	// A comment named with the wrong post isn't found.
	other := mustPost(t, st, author, "Another")
	if err := st.Remove(ctx, Item{PostID: other.PublicID, CommentID: cid}, "spam"); !errors.Is(err, ErrItemNotFound) {
		t.Errorf("a comment under the wrong post gave %v", err)
	}
}

func TestReportQueueOrder(t *testing.T) {
	st := freshDB(t)
	author := mustUser(t, st, "blue-humming-otter")
	busy := mustPost(t, st, author, "Reported a lot")
	serious := mustPost(t, st, author, "Reported once, seriously")
	quiet := mustPost(t, st, author, "Reported once")

	report := func(p Post, who, reason, note string) {
		t.Helper()
		if _, err := st.Report(ctx, NewReport{Item: Item{PostID: p.PublicID}, Reporter: who, Reason: reason, Note: note}); err != nil {
			t.Fatal(err)
		}
	}
	report(quiet, "a", "tags", "")
	report(busy, "a", "spam", "selling things")
	report(busy, "b", "spam", "")
	report(busy, "c", "other", "weird")
	report(serious, "a", "illegal", "")

	q, err := st.ReportQueue(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, item := range q {
		order = append(order, item.PostTitle)
	}
	want := "[Reported once, seriously Reported a lot Reported once]"
	if fmt.Sprint(order) != want {
		t.Errorf("queue order = %v, want %v", order, want)
	}
	b := q[1]
	if b.Reports != 3 || b.Reasons["spam"] != 2 || b.Reasons["other"] != 1 || len(b.Notes) != 2 || b.Author != "blue-humming-otter" {
		t.Errorf("busy item: %+v", b)
	}
	if !q[0].Serious || q[1].Serious {
		t.Error("only the illegal-content report is serious")
	}
}
