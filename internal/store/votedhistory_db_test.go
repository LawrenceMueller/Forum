package store

// Database tests for MyVotedItems: the "upvoted" and "downvoted" views of
// the private history page.

import (
	"fmt"
	"slices"
	"testing"
)

func TestVotedHistoryShowsTheRightThings(t *testing.T) {
	st := freshDB(t)
	author := mustUser(t, st, "blue-humming-otter")
	me := mustUser(t, st, "red-baking-hare")
	other := mustUser(t, st, "green-napping-puffin")

	kept := mustPost(t, st, author, "Kept")
	removed := mustPost(t, st, author, "Removed by admin")
	deleted := mustPost(t, st, author, "Deleted by its author")
	disliked := mustPost(t, st, author, "Disliked")
	theirs := mustPost(t, st, author, "Only the other voter's")
	liked := mustComment(t, st, kept, author, "", "a liked comment")
	disliked2 := mustComment(t, st, kept, author, "", "a disliked comment")
	onRemoved := mustComment(t, st, removed, author, "", "on a removed post")
	removedC := mustComment(t, st, kept, author, "", "removed by admin")
	deletedC := mustComment(t, st, kept, author, "", "deleted by its author")

	vote := func(target VoteTarget, id, fp string, uid int64, dir int) {
		t.Helper()
		if _, _, err := st.Vote(ctx, target, id, fp, uid, dir); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{kept.PublicID, removed.PublicID, deleted.PublicID} {
		vote(VotePost, id, "fp-me", me, 1)
	}
	vote(VotePost, disliked.PublicID, "fp-me", me, -1)
	vote(VotePost, theirs.PublicID, "fp-other", other, 1)
	for _, id := range []string{liked, onRemoved, removedC, deletedC} {
		vote(VoteComment, id, "fp-me", me, 1)
	}
	vote(VoteComment, disliked2, "fp-me", me, -1)

	// Removals and deletions after the votes (closed items can't be voted on).
	if err := st.Remove(ctx, Item{PostID: removed.PublicID}, "spam"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeletePost(ctx, deleted.PublicID, author); err != nil {
		t.Fatal(err)
	}
	if err := st.Remove(ctx, Item{CommentID: removedC}, "spam"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteComment(ctx, deletedC, author); err != nil {
		t.Fatal(err)
	}

	describe := func(value int) []string {
		t.Helper()
		items, next, err := st.MyVotedItems(ctx, "fp-me", value, nil, 25)
		if err != nil || next != nil {
			t.Fatalf("MyVotedItems(%d): err %v, next %v", value, err, next)
		}
		var got []string
		for _, it := range items {
			s := it.Kind + ":" + it.Title
			if it.Kind == "comment" {
				s = it.Kind + ":" + it.Body
			}
			if it.Closed {
				s += " (closed)"
			}
			got = append(got, s)
		}
		slices.Sort(got)
		return got
	}

	wantUp := []string{
		"comment:a liked comment",
		"post:Deleted by its author (closed)", // its title stays, so it's listed
		"post:Kept",
		// not: the removed post, the comment on it, the removed comment,
		// the deleted comment, or the other voter's post
	}
	if got := describe(1); !slices.Equal(got, wantUp) {
		t.Errorf("upvoted:\n got  %q\n want %q", got, wantUp)
	}
	wantDown := []string{"comment:a disliked comment", "post:Disliked"}
	if got := describe(-1); !slices.Equal(got, wantDown) {
		t.Errorf("downvoted:\n got  %q\n want %q", got, wantDown)
	}

	// A comment carries the post it's on.
	items, _, _ := st.MyVotedItems(ctx, "fp-me", 1, nil, 25)
	for _, it := range items {
		if it.Kind == "comment" && (it.PostID != kept.PublicID || it.Title != "Kept") {
			t.Errorf("comment item points to post %q %q, want %q %q", it.PostID, it.Title, kept.PublicID, "Kept")
		}
	}
}

// Posts and comments are paged as one list. Even when every item was
// written at the same instant (so only the tiebreaker orders them), and a
// post and a comment share the same ID, paging must never skip or repeat.
func TestVotedHistoryPaging(t *testing.T) {
	st := freshDB(t)
	author := mustUser(t, st, "blue-humming-otter")
	me := mustUser(t, st, "red-baking-hare")
	post := mustPost(t, st, author, "Post 0")
	ids := []string{post.PublicID}
	if _, _, err := st.Vote(ctx, VotePost, post.PublicID, "fp-me", me, 1); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 8; i++ {
		p := mustPost(t, st, author, fmt.Sprintf("Post %d", i))
		c := mustComment(t, st, post, author, "", fmt.Sprintf("Comment %d", i))
		ids = append(ids, p.PublicID, c)
		if _, _, err := st.Vote(ctx, VotePost, p.PublicID, "fp-me", me, 1); err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.Vote(ctx, VoteComment, c, "fp-me", me, 1); err != nil {
			t.Fatal(err)
		}
	}
	exec(t, st, `UPDATE posts SET created_at = '2026-01-01 12:00:00+00'`)
	exec(t, st, `UPDATE comments SET created_at = '2026-01-01 12:00:00+00'`)

	var seen []string
	var after *Cursor
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("paging never ends")
		}
		items, next, err := st.MyVotedItems(ctx, "fp-me", 1, after, 4)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			seen = append(seen, it.PublicID)
		}
		if next == nil {
			break
		}
		after = next
	}

	slices.Sort(seen)
	slices.Sort(ids)
	if !slices.Equal(seen, ids) {
		t.Errorf("paging returned %d items %v,\nwant each of the %d exactly once %v", len(seen), seen, len(ids), ids)
	}
}
