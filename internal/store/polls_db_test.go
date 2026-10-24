package store

import (
	"errors"
	"testing"
	"time"
)

// mustPoll creates a post with a poll and returns the loaded post.
func mustPoll(t *testing.T, st *Store, author int64, title string, closes time.Duration, options ...string) Post {
	t.Helper()
	pid, err := st.CreatePost(ctx, NewPost{
		AuthorID: author, Title: title, TagIDs: []int{mustTag(t, st, "technology")},
		Poll: &NewPoll{Options: options, ClosesAt: time.Now().Add(closes)},
	})
	if err != nil {
		t.Fatalf("creating poll %q: %v", title, err)
	}
	return mustLoadPost(t, st, pid)
}

func mustPollState(t *testing.T, st *Store, postID int64) *Poll {
	t.Helper()
	p, err := st.PollForPost(ctx, postID)
	if err != nil || p == nil {
		t.Fatalf("loading poll: %v (nil: %v)", err, p == nil)
	}
	return p
}

func TestPollFingerprintsMatchInGoAndSQL(t *testing.T) {
	st := freshDB(t)
	var inSQL string
	if err := st.db.QueryRow(ctx, `SELECT `+pollFingerprintSQL+` FROM (SELECT 1234::bigint AS post_id) x`,
		"some-poll-key").Scan(&inSQL); err != nil {
		t.Fatal(err)
	}
	if want := PollVoter("some-poll-key", 1234); inSQL != want {
		t.Fatalf("SQL fingerprint %s, Go %s: withdrawals and deletions would miss votes", inSQL, want)
	}
	if PollVoter("k", 1) == PollVoter("k", 2) {
		t.Error("an account's fingerprint must differ between polls")
	}
}

func TestPollVoting(t *testing.T) {
	st := freshDB(t)
	asker := mustUser(t, st, "blue-humming-otter")
	a := mustUser(t, st, "red-baking-hare")
	b := mustUser(t, st, "green-napping-puffin")
	post := mustPoll(t, st, asker, "Cats or dogs?", time.Hour, "Cats", "Dogs")
	plain := mustPost(t, st, asker, "No poll here")

	if !post.IsPoll || plain.IsPoll {
		t.Fatalf("IsPoll: poll %v, plain %v", post.IsPoll, plain.IsPoll)
	}
	if p, _ := st.PollForPost(ctx, plain.ID); p != nil {
		t.Error("a post without a poll should have none")
	}
	if posts, _, err := st.ListPosts(ctx, ListOptions{Sort: SortNew}); err != nil || len(posts) != 2 || !posts[1].IsPoll {
		t.Errorf("lists should mark polls: %+v %v", posts, err)
	}

	// a commented before voting; their comment gets the tag once they vote.
	early := mustComment(t, st, post, a, "", "obviously cats")
	if c := mustLoadComment(t, st, early); c.PollPick != -1 {
		t.Errorf("no vote yet, pick %d", c.PollPick)
	}

	fa, fb := PollVoter("key-a", post.ID), PollVoter("key-b", post.ID)
	if err := st.PollVote(ctx, post.ID, fa, a, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.PollVote(ctx, post.ID, fa, a, 1); !errors.Is(err, ErrAlreadyVoted) {
		t.Errorf("second vote: %v, want ErrAlreadyVoted", err)
	}
	if err := st.PollVote(ctx, post.ID, fb, b, 5); !errors.Is(err, ErrNoSuchOption) {
		t.Errorf("option 5 of 2: %v", err)
	}
	if err := st.PollVote(ctx, plain.ID, fb, b, 0); !errors.Is(err, ErrPollNotFound) {
		t.Errorf("voting on a plain post: %v", err)
	}
	if err := st.PollVote(ctx, post.ID, fb, b, 1); err != nil {
		t.Fatal(err)
	}

	p := mustPollState(t, st, post.ID)
	if p.Total != 2 || p.Options[0].Votes != 1 || p.Options[1].Votes != 1 || p.Options[0].Label != "Cats" || !p.Open() {
		t.Errorf("poll after two votes: %+v", p)
	}
	if pick, voted, _ := st.MyPollPick(ctx, post.ID, fa); !voted || pick != 0 {
		t.Errorf("a's pick: %d %v", pick, voted)
	}
	if c := mustLoadComment(t, st, early); c.PollPick != 0 {
		t.Errorf("a's earlier comment should now show their pick, got %d", c.PollPick)
	}

	// A new comment picks up its author's vote straight away; others don't.
	late := func(author int64, voter string) Comment {
		cid, _, err := st.CreateComment(ctx, NewComment{PostID: post.ID, AuthorID: author, Body: "hm", PollVoter: voter})
		if err != nil {
			t.Fatal(err)
		}
		return mustLoadComment(t, st, cid)
	}
	if c := late(b, fb); c.PollPick != 1 {
		t.Errorf("b's comment pick %d, want 1", c.PollPick)
	}
	if c := late(asker, PollVoter("key-asker", post.ID)); c.PollPick != -1 {
		t.Errorf("the asker hasn't voted, pick %d", c.PollPick)
	}

	// Closed polls take no votes, and lose their fingerprints.
	exec(t, st, `UPDATE polls SET closes_at = now() - interval '1 minute' WHERE post_id = $1`, post.ID)
	if err := st.PollVote(ctx, post.ID, PollVoter("key-c", post.ID), asker, 0); !errors.Is(err, ErrPollClosed) {
		t.Errorf("closed poll: %v", err)
	}
	other := mustPoll(t, st, asker, "Tea or coffee?", time.Hour, "Tea", "Coffee")
	if err := st.PollVote(ctx, other.ID, PollVoter("key-a", other.ID), a, 1); err != nil {
		t.Fatal(err)
	}
	if n, err := st.ClosePolls(ctx); err != nil || n != 2 {
		t.Errorf("ClosePolls let go of %d (%v), want the closed poll's 2", n, err)
	}
	if n := count(t, st, `SELECT count(*) FROM poll_votes`); n != 1 {
		t.Errorf("%d fingerprints left, want the open poll's 1", n)
	}
	if p := mustPollState(t, st, post.ID); p.Total != 2 || p.Open() {
		t.Errorf("the totals must outlive the fingerprints: %+v", p)
	}

	// A removed post's poll is closed too.
	if err := st.Remove(ctx, Item{PostID: other.PublicID}, "spam"); err != nil {
		t.Fatal(err)
	}
	if err := st.PollVote(ctx, other.ID, fb, b, 0); !errors.Is(err, ErrPollClosed) {
		t.Errorf("removed post: %v", err)
	}
}

func TestPollOptionsAreCheckedByTheDatabase(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	_, err := st.CreatePost(ctx, NewPost{AuthorID: me, Title: "One option", TagIDs: []int{mustTag(t, st, "technology")},
		Poll: &NewPoll{Options: []string{"only"}, ClosesAt: time.Now().Add(time.Hour)}})
	if err == nil {
		t.Error("a one-option poll was saved")
	}
	post := mustPoll(t, st, me, "Q", time.Hour, "a", "b")
	expectConstraint(t, st, "poll_option_label", `INSERT INTO poll_options (post_id, position, label) VALUES ($1, 2, '')`, post.ID)
	expectConstraint(t, st, "poll_option_position", `INSERT INTO poll_options (post_id, position, label) VALUES ($1, 6, 'x')`, post.ID)
	if n := count(t, st, `SELECT count(*) FROM posts`); n != 1 {
		t.Errorf("the failed poll's post should not have been saved: %d posts", n)
	}
}

func TestPollVotesFollowModerationAndDeletion(t *testing.T) {
	st := freshDB(t)
	asker := mustUser(t, st, "blue-humming-otter")
	spammer := mustUser(t, st, "red-baking-hare")
	leaver := mustUser(t, st, "green-napping-puffin")
	post := mustPoll(t, st, asker, "Best season?", time.Hour, "Spring", "Summer", "Autumn")

	for _, v := range []struct {
		user int64
		key  string
		pick int
	}{{spammer, "pk-spammer", 0}, {leaver, "pk-leaver", 2}} {
		if err := st.PollVote(ctx, post.ID, PollVoter(v.key, post.ID), v.user, v.pick); err != nil {
			t.Fatal(err)
		}
	}

	// A cleanup withdraws the spammer's vote from the totals.
	res, err := st.Cleanup(ctx, spammer, "fp-spammer", "pk-spammer", "spam")
	if err != nil {
		t.Fatal(err)
	}
	if res.Votes != 1 {
		t.Errorf("cleanup withdrew %d votes, want 1", res.Votes)
	}
	if p := mustPollState(t, st, post.ID); p.Total != 1 || p.Options[0].Votes != 0 || p.Options[2].Votes != 1 {
		t.Errorf("after cleanup: %+v", p)
	}

	// A deleted account's vote keeps counting, but can't be traced back.
	if err := st.DeleteAccount(ctx, leaver, "fp-leaver", "pk-leaver", false); err != nil {
		t.Fatal(err)
	}
	if _, voted, _ := st.MyPollPick(ctx, post.ID, PollVoter("pk-leaver", post.ID)); voted {
		t.Error("the deleted account's fingerprint is still there")
	}
	if p := mustPollState(t, st, post.ID); p.Total != 1 {
		t.Errorf("the deleted account's vote should still count: %+v", p)
	}

	// Deleting the post erases the poll, options and all.
	if err := st.DeletePost(ctx, post.PublicID, asker); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.PollForPost(ctx, post.ID); p != nil || mustLoadPost(t, st, post.PublicID).IsPoll {
		t.Error("a deleted post's poll should be gone")
	}
	if n := count(t, st, `SELECT count(*) FROM poll_options`) + count(t, st, `SELECT count(*) FROM poll_votes`); n != 0 {
		t.Errorf("%d option and vote rows left behind", n)
	}

	// And retention erases a long-removed post's poll.
	old := mustPoll(t, st, asker, "Removed long ago", time.Hour, "x", "y")
	if err := st.Remove(ctx, Item{PostID: old.PublicID}, "rules"); err != nil {
		t.Fatal(err)
	}
	exec(t, st, `UPDATE posts SET removed_at = now() - interval '31 days' WHERE id = $1`, old.ID)
	if _, err := st.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.PollForPost(ctx, old.ID); p != nil {
		t.Error("an erased post's options are words too, and should be gone")
	}
}
