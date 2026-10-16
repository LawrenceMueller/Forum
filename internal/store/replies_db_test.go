package store

import (
	"fmt"
	"testing"
)

func TestReplies(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	other := mustUser(t, st, "red-baking-hare")
	bot := mustUser(t, st, "green-napping-puffin")

	unread := func() int {
		t.Helper()
		return count(t, st, `SELECT unread_replies FROM users WHERE id = $1`, me)
	}
	mine := mustPost(t, st, me, "My post")
	theirs := mustPost(t, st, other, "Their post")

	onMyPost := mustComment(t, st, mine, other, "", "a comment on your post")
	myComment := mustComment(t, st, theirs, me, "", "my comment")
	toMyComment := mustComment(t, st, theirs, other, myComment, "a reply to your comment")
	mustComment(t, st, mine, me, onMyPost, "me replying in my own thread") // not a reply TO me
	mustComment(t, st, theirs, other, toMyComment, "deeper down: not direct")
	if n := unread(); n != 2 {
		t.Fatalf("unread = %d, want 2", n)
	}

	replies, next, err := st.Replies(ctx, me, nil, 10)
	if err != nil || next != nil {
		t.Fatal(err, next)
	}
	var got []string
	for _, r := range replies {
		got = append(got, fmt.Sprintf("%s/%v", r.Body, r.ParentID == ""))
	}
	if want := "[a reply to your comment/false a comment on your post/true]"; fmt.Sprint(got) != want {
		t.Errorf("replies = %v, want %v", got, want)
	}

	// A spam bot's replies count for nothing and never show.
	if err := st.SetShadowban(ctx, bot, "fp-bot", true); err != nil {
		t.Fatal(err)
	}
	mustComment(t, st, mine, bot, "", "cheap watches")
	if n := unread(); n != 2 {
		t.Errorf("a shadowbanned reply counted: unread = %d", n)
	}

	// Removed replies, and replies under something I deleted, drop out.
	if err := st.Remove(ctx, Item{CommentID: onMyPost}, "spam"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteComment(ctx, myComment, me); err != nil {
		t.Fatal(err)
	}
	if replies, _, _ := st.Replies(ctx, me, nil, 10); len(replies) != 0 {
		t.Errorf("%d replies left, want none", len(replies))
	}

	// Paging, and seeing.
	for i := range 3 {
		mustComment(t, st, mine, other, "", fmt.Sprint("comment ", i))
	}
	page, next, _ := st.Replies(ctx, me, nil, 2)
	if len(page) != 2 || next == nil {
		t.Fatalf("first page: %d, next %v", len(page), next)
	}
	rest, next, _ := st.Replies(ctx, me, next, 2)
	if len(rest) != 1 || next != nil || rest[0].Body != "comment 0" {
		t.Errorf("second page: %+v, next %v", rest, next)
	}
	if err := st.MarkRepliesSeen(ctx, me); err != nil {
		t.Fatal(err)
	}
	if n := unread(); n != 0 {
		t.Errorf("unread = %d after seeing", n)
	}
	if u := sessionUser(t, st, me); u.UnreadReplies != 0 || u.RepliesSeenAt.IsZero() {
		t.Errorf("the session should carry the counter and seen time: %+v", u)
	}
}
