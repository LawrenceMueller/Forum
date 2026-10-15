package store

import (
	"fmt"
	"testing"
)

func TestPurgeLetsGoAfter30Days(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	other := mustUser(t, st, "red-baking-hare")

	oldPost := mustPost(t, st, me, "Removed long ago")
	newPost := mustPost(t, st, me, "Removed yesterday")
	thread := mustPost(t, st, other, "A thread")
	oldComment := mustComment(t, st, thread, me, "", "an old removed comment")
	reply := mustComment(t, st, thread, other, oldComment, "a reply that keeps its place")
	heldComment := mustComment(t, st, thread, me, "", "held, never reviewed")
	upComment := mustComment(t, st, thread, me, "", "never removed")

	for _, it := range []Item{{PostID: oldPost.PublicID}, {PostID: newPost.PublicID}, {CommentID: oldComment}} {
		if err := st.Remove(ctx, it, "spam"); err != nil {
			t.Fatal(err)
		}
	}
	for i := range HoldAfter {
		if _, err := st.Report(ctx, NewReport{Item: Item{PostID: thread.PublicID, CommentID: heldComment},
			Reporter: fmt.Sprint("r", i), Reason: "illegal"}); err != nil {
			t.Fatal(err)
		}
	}
	// A handled report long ago, and one recently.
	if _, err := st.Report(ctx, NewReport{Item: Item{PostID: thread.PublicID}, Reporter: "x", Reason: "tags"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Report(ctx, NewReport{Item: Item{PostID: thread.PublicID, CommentID: upComment}, Reporter: "y", Reason: "tags"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Dismiss(ctx, Item{PostID: thread.PublicID}); err != nil {
		t.Fatal(err)
	}
	if err := st.Dismiss(ctx, Item{PostID: thread.PublicID, CommentID: upComment}); err != nil {
		t.Fatal(err)
	}
	// A suspension that ended long ago, with a note and an appeal.
	if err := st.Suspend(ctx, other, "spam", "a note to them", 1); err != nil {
		t.Fatal(err)
	}
	sus, _ := st.ActiveSuspension(ctx, other)
	if err := st.Appeal(ctx, other, sus.ID, "my appeal text"); err != nil {
		t.Fatal(err)
	}

	// Wind the clock back on the "long ago" ones.
	exec(t, st, `UPDATE posts SET removed_at = now() - interval '31 days' WHERE public_id = $1`, oldPost.PublicID)
	exec(t, st, `UPDATE comments SET removed_at = now() - interval '31 days' WHERE public_id IN ($1, $2)`, oldComment, heldComment)
	exec(t, st, `UPDATE reports SET handled_at = now() - interval '31 days' WHERE comment_id IS NULL`)
	exec(t, st, `UPDATE suspensions SET ends_at = now() - interval '31 days'`)

	res, err := st.Purge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Posts != 1 || res.Comments != 1 || res.Reports != 1 || res.Suspensions != 1 {
		t.Errorf("purge = %+v, want 1 of each", res)
	}

	p := mustLoadPost(t, st, oldPost.PublicID)
	if !p.Erased || p.Title != "[removed]" || p.Body != "" || p.URL != "" || !p.Removed || p.RemovalReason != "spam" {
		t.Errorf("old removed post: %+v", p)
	}
	if p := mustLoadPost(t, st, newPost.PublicID); p.Erased || p.Body == "" {
		t.Error("a recent removal should keep its text")
	}
	if c := mustLoadComment(t, st, oldComment); !c.Erased || c.Body != "" {
		t.Errorf("old removed comment: erased %v, body %q", c.Erased, c.Body)
	}
	if c := mustLoadComment(t, st, reply); c.Body == "" || c.ParentID == 0 {
		t.Error("the reply under an erased comment should be untouched, still in its place")
	}
	if c := mustLoadComment(t, st, heldComment); c.Erased {
		t.Error("an item still held for review mustn't be erased")
	}
	if n := count(t, st, `SELECT count(*) FROM reports WHERE handled_at IS NOT NULL`); n != 1 {
		t.Errorf("%d handled reports left, want the recent one", n)
	}
	if n := count(t, st, `SELECT count(*) FROM reports WHERE handled_at IS NULL`); n != HoldAfter {
		t.Errorf("open reports must never be purged: %d left", n)
	}
	var note, appeal string
	if err := st.db.QueryRow(ctx, `SELECT note, coalesce(appeal, '-') FROM suspensions`).Scan(&note, &appeal); err != nil {
		t.Fatal(err)
	}
	if note != "" || appeal != "" {
		t.Errorf("an old suspension kept note %q, appeal %q", note, appeal)
	}

	// Running again finds nothing new.
	if again, _ := st.Purge(ctx); again.Any() {
		t.Errorf("a second sweep did %+v", again)
	}
}
