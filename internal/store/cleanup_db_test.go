package store

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanupRemovesEverythingAndWithdrawsVotes(t *testing.T) {
	st := freshDB(t)
	spammer := mustUser(t, st, "blue-humming-otter")
	other := mustUser(t, st, "red-baking-hare")
	boss := mustUser(t, st, "green-napping-puffin")
	exec(t, st, `UPDATE users SET is_admin = true WHERE id = $1`, boss)

	theirs := mustPost(t, st, other, "A real post")
	theirComment := mustComment(t, st, theirs, other, "", "A real comment")
	mine1 := mustPost(t, st, spammer, "Buy watches")
	mustPost(t, st, spammer, "Buy more watches")
	mustComment(t, st, theirs, spammer, "", "buy watches")
	held := mustComment(t, st, theirs, spammer, "", "more watches")
	mustComment(t, st, mine1, spammer, "", "watches here too")

	// The spammer's votes, under its fingerprint.
	if _, _, err := st.Vote(ctx, VotePost, theirs.PublicID, "fp-spammer", spammer, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Vote(ctx, VoteComment, theirComment, "fp-spammer", spammer, -1); err != nil {
		t.Fatal(err)
	}
	// Someone else's vote, which must stay.
	if _, _, err := st.Vote(ctx, VotePost, theirs.PublicID, "fp-other", other+100, 1); err != nil {
		t.Fatal(err)
	}
	// One comment already held by reports, and a report on a post.
	for i, who := range []string{"a", "b", "c"} {
		if _, err := st.Report(ctx, NewReport{Item: Item{PostID: theirs.PublicID, CommentID: held}, Reporter: who, Reason: "personal"}); err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
	}
	if _, err := st.Report(ctx, NewReport{Item: Item{PostID: mine1.PublicID}, Reporter: "a", Reason: "spam"}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.Cleanup(ctx, boss, "fp-boss", "pk-boss", "spam"); !errors.Is(err, ErrCantSuspendAdmin) {
		t.Errorf("cleaning up the admin gave %v", err)
	}
	res, err := st.Cleanup(ctx, spammer, "fp-spammer", "pk-spammer", "spam")
	if err != nil {
		t.Fatal(err)
	}
	if res.Posts != 2 || res.Comments != 3 || res.Votes != 2 {
		t.Errorf("cleanup = %+v, want 2 posts, 3 comments (the held one too), 2 votes", res)
	}
	if n := count(t, st, `SELECT count(*) FROM posts WHERE author_id = $1 AND (removed_at IS NULL OR removal_reason <> 'spam')`, spammer); n != 0 {
		t.Errorf("%d of the spammer's posts aren't removed as spam", n)
	}
	if n := count(t, st, `SELECT count(*) FROM comments WHERE author_id = $1 AND (removed_at IS NULL OR removal_reason <> 'spam')`, spammer); n != 0 {
		t.Errorf("%d of the spammer's comments aren't removed as spam", n)
	}
	p := mustLoadPost(t, st, theirs.PublicID)
	if p.CommentCount != 1 {
		t.Errorf("the real post counts %d comments, want 1 (its own)", p.CommentCount)
	}
	if p.Score != 1 {
		t.Errorf("the real post's score is %d, want 1 (the other vote stays)", p.Score)
	}
	if c := mustLoadComment(t, st, theirComment); c.Upvotes != 0 || c.Downvotes != 0 {
		t.Errorf("the real comment still has votes: %d up, %d down", c.Upvotes, c.Downvotes)
	}
	if n := count(t, st, `SELECT count(*) FROM post_votes WHERE voter = 'fp-spammer'`) +
		count(t, st, `SELECT count(*) FROM comment_votes WHERE voter = 'fp-spammer'`); n != 0 {
		t.Errorf("%d of the spammer's votes are left", n)
	}
	if n, _ := st.OpenReportItems(ctx); n != 0 {
		t.Errorf("%d reported items still open", n)
	}
	log, _ := st.ModLog(ctx, 0, 1, false)
	if len(log) != 1 || log[0].Action != "cleanup" || !strings.Contains(log[0].Detail, "2 posts, 3 comments, 2 votes") {
		t.Errorf("log = %+v", log)
	}
}

func TestShadowbans(t *testing.T) {
	st := freshDB(t)
	bot := mustUser(t, st, "blue-humming-otter")
	other := mustUser(t, st, "red-baking-hare")
	theirs := mustPost(t, st, other, "A real post")
	if _, _, err := st.Vote(ctx, VotePost, theirs.PublicID, "fp-bot", bot, 1); err != nil {
		t.Fatal(err)
	}

	if err := st.SetShadowban(ctx, bot, "fp-bot", "pk-bot", true); err != nil {
		t.Fatal(err)
	}
	if u := sessionUser(t, st, bot); !u.Shadowbanned {
		t.Error("the session should say the account is shadowbanned")
	}
	if p := mustLoadPost(t, st, theirs.PublicID); p.Score != 0 {
		t.Errorf("the bot's earlier vote should be withdrawn; score = %d", p.Score)
	}

	// What it posts from now on arrives removed, as "shadow", and counts for nothing.
	post := mustPost(t, st, bot, "Cheap watches")
	if p := mustLoadPost(t, st, post.PublicID); !p.Removed || p.RemovalReason != "shadow" {
		t.Errorf("the bot's post: removed %v, reason %q", p.Removed, p.RemovalReason)
	}
	cid := mustComment(t, st, theirs, bot, "", "watches!")
	if c := mustLoadComment(t, st, cid); !c.Removed || c.RemovalReason != "shadow" {
		t.Errorf("the bot's comment: removed %v, reason %q", c.Removed, c.RemovalReason)
	}
	if p := mustLoadPost(t, st, theirs.PublicID); p.CommentCount != 0 {
		t.Errorf("the bot's comment was counted: %d", p.CommentCount)
	}
	// It can still comment where only it can see: under its own shadowed post and comment.
	under := mustComment(t, st, post, bot, "", "first!")
	mustComment(t, st, post, bot, under, "second!")
	// Nobody else can comment on its shadowed post.
	if _, _, err := st.CreateComment(ctx, NewComment{PostID: post.ID, AuthorID: other, Body: "hm"}); !errors.Is(err, ErrPostLocked) {
		t.Errorf("someone else commenting on a shadowed post gave %v", err)
	}

	if err := st.SetShadowban(ctx, bot, "fp-bot", "pk-bot", false); err != nil {
		t.Fatal(err)
	}
	if mustLoadPost(t, st, mustPost(t, st, bot, "After").PublicID).Removed {
		t.Error("after lifting the shadowban, new posts should be up")
	}
	all, _ := st.ModLog(ctx, 0, 5, false)
	if len(all) != 2 || all[0].Action != "unshadowban" || all[1].Action != "shadowban" {
		t.Errorf("log = %+v", all)
	}
}
