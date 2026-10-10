package store

// Concurrency tests: many requests hitting the same thing at the same moment.
//
// These are the bugs you can't find by clicking around, however carefully,
// because they need two requests to interleave within milliseconds. Each
// test fires many goroutines (Go's lightweight threads) through a "starting
// gate", so they really do arrive together, then checks the database still
// adds up exactly.
//
// Goroutines can't call t.Fatal (it must run on the test's own goroutine),
// so each one reports its result on a channel, and the test checks them all
// afterwards.

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// together runs fn n times at once, released by a single starting gate, and
// returns every result. Results come back in no particular order.
func together(n int, fn func(i int) error) []error {
	var (
		gate    = make(chan struct{})
		wg      sync.WaitGroup
		results = make(chan error, n)
	)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate // wait here until every goroutine is ready
			results <- fn(i)
		}()
	}
	close(gate) // and they're off
	wg.Wait()
	close(results)

	var errs []error
	for err := range results {
		errs = append(errs, err)
	}
	return errs
}

// nameFor turns a number into a valid generated-style username:
// 0 -> "test-voter-a", 25 -> "test-voter-z", 26 -> "test-voter-ba".
func nameFor(i int) string {
	s := ""
	for {
		s = string(rune('a'+i%26)) + s
		i /= 26
		if i == 0 {
			return "test-voter-" + s
		}
	}
}

// voteCountsMatchRows checks a post's stored counters against the actual
// vote rows: the invariant that concurrency bugs break.
func voteCountsMatchRows(t *testing.T, st *Store, postID int64) {
	t.Helper()
	up := count(t, st, `SELECT upvotes FROM posts WHERE id = $1`, postID)
	down := count(t, st, `SELECT downvotes FROM posts WHERE id = $1`, postID)
	rowsUp := count(t, st, `SELECT count(*) FROM post_votes WHERE post_id = $1 AND value = 1`, postID)
	rowsDown := count(t, st, `SELECT count(*) FROM post_votes WHERE post_id = $1 AND value = -1`, postID)
	if up != rowsUp || down != rowsDown {
		t.Errorf("counters say %d up / %d down, but the vote rows say %d / %d", up, down, rowsUp, rowsDown)
	}
}

// One person double-clicking (or a script hammering) the same arrow. Every
// click must succeed, and since each click toggles, an even number of clicks
// must leave no vote at all.
//
// This is the test the row lock exists for. Without "FOR UPDATE" in the
// vote's lock statement (store/votes.go), simultaneous clicks all read "no
// vote yet" and all try to insert one: every one after the first crashes
// with a duplicate-key error. Try it: remove "FOR UPDATE" from the post
// lock statement and run this test.
func TestRapidClicksByOneVoter(t *testing.T) {
	st := freshDB(t)
	author := mustUser(t, st, "blue-humming-otter")
	voter := mustUser(t, st, "red-baking-hare")
	post := mustPost(t, st, author, "Click me")

	const clicks = 20 // even
	errs := together(clicks, func(int) error {
		_, _, err := st.Vote(ctx, VotePost, post.PublicID, "fp-voter", voter, 1)
		return err
	})
	for _, err := range errs {
		if err != nil {
			t.Errorf("a click failed: %v", err)
		}
	}

	if n := count(t, st, `SELECT count(*) FROM post_votes`); n != 0 {
		t.Errorf("%d clicks should toggle the vote back off, but %d vote rows remain", clicks, n)
	}
	if p := mustLoadPost(t, st, post.PublicID); p.Score != 0 {
		t.Errorf("score %d, want 0", p.Score)
	}
	voteCountsMatchRows(t, st, post.ID)
}

// Many different people voting on the same post at once, some up, some
// down, some changing their minds. However it interleaves, the counters
// must match the votes actually stored.
func TestManyVotersAtOnce(t *testing.T) {
	st := freshDB(t)
	author := mustUser(t, st, "blue-humming-otter")
	post := mustPost(t, st, author, "Popular")

	const voters = 40
	ids := make([]int64, voters)
	for i := range voters {
		ids[i] = mustUser(t, st, nameFor(i))
	}

	// Each voter clicks twice at once: up then down, or up then up (taking
	// it back). Their two clicks race each other as well as everyone else's.
	errs := together(voters*2, func(i int) error {
		v := i / 2
		dir := 1
		if i%2 == 1 && v%2 == 0 {
			dir = -1
		}
		_, _, err := st.Vote(ctx, VotePost, post.PublicID, fmt.Sprintf("fp-%d", v), ids[v], dir)
		return err
	})
	for _, err := range errs {
		if err != nil {
			t.Errorf("a vote failed: %v", err)
		}
	}

	voteCountsMatchRows(t, st, post.ID)
	if n := count(t, st, `SELECT count(*) FROM post_votes`); n > voters {
		t.Errorf("%d vote rows for %d voters: someone has two votes", n, voters)
	}
	// score = upvotes - downvotes is enforced by the database itself
	// (score_matches_votes), so it can't be wrong if the update succeeded.
}

// Many comments arriving on one post at once: the post's comment count must
// equal the comments actually stored.
func TestManyCommentsAtOnce(t *testing.T) {
	st := freshDB(t)
	author := mustUser(t, st, "blue-humming-otter")
	post := mustPost(t, st, author, "Busy thread")
	parent := mustComment(t, st, post, author, "", "the first comment")

	const n = 40
	errs := together(n, func(i int) error {
		reply := ""
		if i%2 == 0 {
			reply = parent // half are replies, half top-level
		}
		_, _, err := st.CreateComment(ctx, NewComment{
			PostID: post.ID, ParentPublicID: reply, AuthorID: author, Body: fmt.Sprintf("comment %d", i),
		})
		return err
	})
	for _, err := range errs {
		if err != nil {
			t.Errorf("a comment failed: %v", err)
		}
	}

	stored := count(t, st, `SELECT count(*) FROM comments WHERE post_id = $1`, post.ID)
	if p := mustLoadPost(t, st, post.PublicID); p.CommentCount != stored || stored != n+1 {
		t.Errorf("comment count %d, comments stored %d, want both %d", p.CommentCount, stored, n+1)
	}
}

// Someone (or something) racing to use the same recovery code from many
// places at once. Exactly one attempt may win; every other must fail.
func TestRecoveryCodeRace(t *testing.T) {
	st := freshDB(t)
	id := mustUser(t, st, "blue-humming-otter")

	const attempts = 20
	errs := together(attempts, func(i int) error {
		_, err := st.RecoverAccount(ctx, "blue-humming-otter", "code-a", fmt.Sprintf("hash-%d", i))
		return err
	})

	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ErrRecoveryFailed):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Errorf("%d attempts succeeded with one code; exactly 1 may", wins)
	}
	if n, _ := st.RemainingRecoveryCodes(ctx, id); n != 1 {
		t.Errorf("%d codes left, want 1 (only the raced code spent)", n)
	}
}

// Two people signing up with the same name at the same instant (possible:
// the name was offered to both, and the "is it taken?" check passed for
// both). Exactly one gets it; the rest get ErrUsernameTaken, which the
// sign-up page turns into a fresh name.
func TestSameNameSignupRace(t *testing.T) {
	st := freshDB(t)

	const attempts = 20
	errs := together(attempts, func(int) error {
		_, err := st.CreateUser(ctx, "green-napping-puffin", "h", []string{"c1", "c2"})
		return err
	})

	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ErrUsernameTaken):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Errorf("%d accounts were created with one name; exactly 1 may", wins)
	}
	// The losers' recovery codes must not have been left behind.
	if n := count(t, st, `SELECT count(*) FROM recovery_codes`); n != 2 {
		t.Errorf("%d recovery codes stored, want 2 (only the winner's)", n)
	}
}
