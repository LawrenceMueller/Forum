package store

// Database tests for votes, editing, deletion, history, and the rules the
// database itself enforces.

import (
	"errors"
	"slices"
	"testing"
)

// ---- votes ----

func TestVoteTogglingKeepsCountsRight(t *testing.T) {
	st := freshDB(t)
	author := mustUser(t, st, "blue-humming-otter")
	voter := mustUser(t, st, "red-baking-hare")
	post := mustPost(t, st, author, "Vote on me")

	steps := []struct {
		name             string
		dir              int
		wantScore        int
		wantMine         int
		wantUp, wantDown int
	}{
		{"upvote", 1, 1, 1, 1, 0},
		{"click up again: take it back", 1, 0, 0, 0, 0},
		{"downvote", -1, -1, -1, 0, 1},
		{"switch to up", 1, 1, 1, 1, 0},
	}
	for _, s := range steps {
		score, mine, err := st.Vote(ctx, VotePost, post.PublicID, "fp-voter", voter, s.dir)
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if score != s.wantScore || mine != s.wantMine {
			t.Errorf("%s: got score %d, mine %d; want %d, %d", s.name, score, mine, s.wantScore, s.wantMine)
		}
		up := count(t, st, `SELECT upvotes FROM posts WHERE id = $1`, post.ID)
		down := count(t, st, `SELECT downvotes FROM posts WHERE id = $1`, post.ID)
		if up != s.wantUp || down != s.wantDown {
			t.Errorf("%s: stored %d up / %d down, want %d / %d", s.name, up, down, s.wantUp, s.wantDown)
		}
	}
	if n := count(t, st, `SELECT count(*) FROM post_votes`); n != 1 {
		t.Errorf("one voter should have exactly one vote row, got %d", n)
	}
}

func TestVoteRules(t *testing.T) {
	st := freshDB(t)
	author := mustUser(t, st, "blue-humming-otter")
	voter := mustUser(t, st, "red-baking-hare")
	post := mustPost(t, st, author, "A post")
	comment := mustComment(t, st, post, author, "", "a comment")

	if _, _, err := st.Vote(ctx, VotePost, post.PublicID, "fp-author", author, 1); !errors.Is(err, ErrOwnContent) {
		t.Errorf("voting on your own post: want ErrOwnContent, got %v", err)
	}
	if _, _, err := st.Vote(ctx, VoteComment, comment, "fp-author", author, 1); !errors.Is(err, ErrOwnContent) {
		t.Errorf("voting on your own comment: want ErrOwnContent, got %v", err)
	}
	if _, _, err := st.Vote(ctx, VotePost, "zzzzzzzz", "fp-voter", voter, 1); !errors.Is(err, ErrVoteNotFound) {
		t.Errorf("voting on nothing: want ErrVoteNotFound, got %v", err)
	}

	if err := st.Remove(ctx, Item{PostID: post.PublicID}, "spam"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Vote(ctx, VotePost, post.PublicID, "fp-voter", voter, 1); !errors.Is(err, ErrVoteLocked) {
		t.Errorf("voting on a removed post: want ErrVoteLocked, got %v", err)
	}
	if _, _, err := st.Vote(ctx, VoteComment, comment, "fp-voter", voter, 1); !errors.Is(err, ErrVoteLocked) {
		t.Errorf("voting on a comment under a removed post: want ErrVoteLocked, got %v", err)
	}
}

func TestCommentVotesAndMyVotes(t *testing.T) {
	st := freshDB(t)
	author := mustUser(t, st, "blue-humming-otter")
	a := mustUser(t, st, "red-baking-hare")
	b := mustUser(t, st, "green-napping-puffin")
	post := mustPost(t, st, author, "A post")
	c1 := mustComment(t, st, post, author, "", "one")
	c2 := mustComment(t, st, post, author, "", "two")

	mustVote := func(target VoteTarget, id, fp string, uid int64, dir int) {
		t.Helper()
		if _, _, err := st.Vote(ctx, target, id, fp, uid, dir); err != nil {
			t.Fatal(err)
		}
	}
	mustVote(VoteComment, c1, "fp-a", a, 1)
	mustVote(VoteComment, c1, "fp-b", b, -1)
	mustVote(VoteComment, c2, "fp-a", a, -1)

	if c := mustLoadComment(t, st, c1); c.Upvotes != 1 || c.Downvotes != 1 {
		t.Errorf("c1 has %d up / %d down, want 1 / 1", c.Upvotes, c.Downvotes)
	}

	ids := []int64{mustLoadComment(t, st, c1).ID, mustLoadComment(t, st, c2).ID}
	mine, err := st.MyVotes(ctx, VoteComment, "fp-a", ids)
	if err != nil {
		t.Fatal(err)
	}
	if mine[ids[0]] != 1 || mine[ids[1]] != -1 {
		t.Errorf("my votes = %v, want up on c1 and down on c2", mine)
	}
	if theirs, _ := st.MyVotes(ctx, VoteComment, "fp-b", ids); len(theirs) != 1 {
		t.Errorf("b voted once, but MyVotes returned %v", theirs)
	}

	authorID, current, err := st.VoteAuthor(ctx, VoteComment, c1, "fp-b")
	if err != nil || authorID != author || current != -1 {
		t.Errorf("VoteAuthor = (%d, %d, %v), want (%d, -1, nil)", authorID, current, err, author)
	}
}

// The votes tables must never grow an account ID or a timestamp column.
// If someone adds one in a future migration, this test fails and asks
// them to think again.
func TestVotesTablesStayPrivate(t *testing.T) {
	st := freshDB(t)
	for table, want := range map[string][]string{
		"post_votes":    {"post_id", "value", "voter"},
		"comment_votes": {"comment_id", "value", "voter"},
	} {
		rows, err := st.db.Query(ctx, `SELECT column_name FROM information_schema.columns
			WHERE table_name = $1 ORDER BY column_name`, table)
		if err != nil {
			t.Fatal(err)
		}
		var cols []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, c)
		}
		rows.Close()
		if !slices.Equal(cols, want) {
			t.Errorf("%s columns are %v; want exactly %v (no account IDs, no timestamps)", table, cols, want)
		}
	}
}

// ---- editing ----

func TestEditPostWindowAndGrace(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	other := mustUser(t, st, "red-baking-hare")
	post := mustPost(t, st, me, "Fixed title")
	books := mustTag(t, st, "books")

	// Within the grace period: saved, but no "edited" marker.
	if err := st.EditPost(ctx, post.PublicID, me, post.Title, post.URL, "typo fixed", []int{books}); err != nil {
		t.Fatal(err)
	}
	p := mustLoadPost(t, st, post.PublicID)
	if p.Body != "typo fixed" || p.EditedAt != nil {
		t.Errorf("grace edit: body %q, edited %v; want the new body and no marker", p.Body, p.EditedAt)
	}
	if !slices.Equal(p.Tags, []string{"books"}) {
		t.Errorf("tags = %v, want [books] (tags are replaced, not added to)", p.Tags)
	}

	// After the grace period: marked.
	exec(t, st, `UPDATE posts SET created_at = now() - interval '3 minutes' WHERE id = $1`, post.ID)
	if err := st.EditPost(ctx, post.PublicID, me, post.Title, post.URL, "later edit", []int{books}); err != nil {
		t.Fatal(err)
	}
	if p := mustLoadPost(t, st, post.PublicID); p.EditedAt == nil {
		t.Error("an edit after 2 minutes should set edited_at")
	}

	// Someone else, even inside the window: refused.
	if err := st.EditPost(ctx, post.PublicID, other, post.Title, post.URL, "hijack", []int{books}); !errors.Is(err, ErrEditClosed) {
		t.Errorf("editing someone else's post: want ErrEditClosed, got %v", err)
	}

	// After 10 minutes: final.
	exec(t, st, `UPDATE posts SET created_at = now() - interval '11 minutes' WHERE id = $1`, post.ID)
	if err := st.EditPost(ctx, post.PublicID, me, post.Title, post.URL, "too late", []int{books}); !errors.Is(err, ErrEditClosed) {
		t.Errorf("editing after the window: want ErrEditClosed, got %v", err)
	}
	if p := mustLoadPost(t, st, post.PublicID); p.Body != "later edit" || p.Title != "Fixed title" {
		t.Errorf("a refused edit must change nothing: body %q, title %q", p.Body, p.Title)
	}
}

func TestEditComment(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	post := mustPost(t, st, me, "A post")
	cid := mustComment(t, st, post, me, "", "first draft")

	c, err := st.EditComment(ctx, cid, me, "second draft")
	if err != nil || c.Body != "second draft" || c.EditedAt != nil {
		t.Fatalf("grace edit: body %q, edited %v, err %v", c.Body, c.EditedAt, err)
	}

	exec(t, st, `UPDATE comments SET created_at = now() - interval '11 minutes' WHERE public_id = $1`, cid)
	if _, err := st.EditComment(ctx, cid, me, "too late"); !errors.Is(err, ErrEditClosed) {
		t.Errorf("editing after the window: want ErrEditClosed, got %v", err)
	}
}

// ---- deleting your own content ----

func TestDeletePostErasesButKeepsThread(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	other := mustUser(t, st, "red-baking-hare")
	pid, err := st.CreatePost(ctx, NewPost{AuthorID: me, Title: "Keep me", URL: "https://example.org", Body: "secret",
		TagIDs: []int{mustTag(t, st, "technology")}})
	if err != nil {
		t.Fatal(err)
	}
	post := mustLoadPost(t, st, pid)
	mustComment(t, st, post, other, "", "a reply that must survive")

	// Someone else can't delete it (silently does nothing).
	if err := st.DeletePost(ctx, pid, other); err != nil {
		t.Fatal(err)
	}
	if p := mustLoadPost(t, st, pid); p.Deleted {
		t.Fatal("someone else deleted my post")
	}

	if err := st.DeletePost(ctx, pid, me); err != nil {
		t.Fatal(err)
	}
	p := mustLoadPost(t, st, pid)
	if !p.Deleted || p.Title != "Keep me" || p.Body != "" || p.URL != "" {
		t.Errorf("deleted post: deleted=%v title=%q body=%q url=%q; want title kept, the rest erased",
			p.Deleted, p.Title, p.Body, p.URL)
	}
	if count(t, st, `SELECT count(*) FROM comments WHERE post_id = $1 AND body <> ''`, p.ID) != 1 {
		t.Error("the discussion under a deleted post must survive")
	}
}

func TestDeleteCommentCountsOnce(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	post := mustPost(t, st, me, "A post")
	mustComment(t, st, post, me, "", "keep")
	gone := mustComment(t, st, post, me, "", "delete me")
	removedFirst := mustComment(t, st, post, me, "", "removed, then deleted")

	if err := st.Remove(ctx, Item{CommentID: removedFirst}, "spam"); err != nil { // count 3 -> 2
		t.Fatal(err)
	}
	if err := st.DeleteComment(ctx, gone, me); err != nil { // 2 -> 1
		t.Fatal(err)
	}
	if err := st.DeleteComment(ctx, removedFirst, me); err != nil { // already uncounted: stays 1
		t.Fatal(err)
	}
	if err := st.DeleteComment(ctx, gone, me); err != nil { // twice: no change
		t.Fatal(err)
	}

	if p := mustLoadPost(t, st, post.PublicID); p.CommentCount != 1 {
		t.Errorf("comment count = %d, want 1 (only %q is still visible)", p.CommentCount, "keep")
	}
	if c := mustLoadComment(t, st, gone); c.Body != "" || !c.Deleted {
		t.Errorf("deleted comment: body %q, deleted %v; want it erased", c.Body, c.Deleted)
	}
}

func TestHistory(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	other := mustUser(t, st, "red-baking-hare")
	visible := mustPost(t, st, me, "Visible")
	removed := mustPost(t, st, me, "Removed by admin")
	deleted := mustPost(t, st, me, "Deleted by me")
	mustPost(t, st, other, "Not mine")
	mustComment(t, st, visible, me, "", "my comment")

	if err := st.Remove(ctx, Item{PostID: removed.PublicID}, "spam"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeletePost(ctx, deleted.PublicID, me); err != nil {
		t.Fatal(err)
	}

	posts, next, err := st.MyPosts(ctx, me, nil, 25)
	if err != nil || next != nil {
		t.Fatalf("MyPosts: err %v, next %v", err, next)
	}
	var titles []string
	for _, p := range posts {
		titles = append(titles, p.Title)
		if p.Title == "Removed by admin" && !p.Removed {
			t.Error("my removed post should be labeled as removed")
		}
	}
	slices.Sort(titles)
	if !slices.Equal(titles, []string{"Removed by admin", "Visible"}) {
		t.Errorf("history shows %v; want my visible and removed posts, not deleted or other people's", titles)
	}

	comments, _, err := st.MyComments(ctx, me, nil, 25)
	if err != nil || len(comments) != 1 || comments[0].PostTitle != "Visible" {
		t.Errorf("MyComments: %+v, err %v", comments, err)
	}
}

// ---- the database's own rules ----

// These try to break the rules directly in SQL, bypassing all the Go code.
// They pass only if Postgres refuses.
func TestDatabaseRefusesBrokenData(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	post := mustPost(t, st, me, "A post")
	cid := mustComment(t, st, post, me, "", "a comment")

	expectConstraint(t, st, "score_matches_votes",
		`UPDATE posts SET score = 99 WHERE id = $1`, post.ID)
	expectConstraint(t, st, "password_only_while_active",
		`UPDATE users SET password_hash = NULL WHERE id = $1`, me)

	if err := st.DeletePost(ctx, post.PublicID, me); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteComment(ctx, cid, me); err != nil {
		t.Fatal(err)
	}
	expectConstraint(t, st, "deleted_post_is_erased",
		`UPDATE posts SET body = 'it came back' WHERE id = $1`, post.ID)
	expectConstraint(t, st, "deleted_comment_is_erased",
		`UPDATE comments SET body = 'it came back' WHERE public_id = $1`, cid)
	expectConstraint(t, st, "comment_body_length",
		`INSERT INTO comments (public_id, post_id, author_id, body, content_hash)
		 VALUES ('abcdefgh', $1, $2, '', 'h')`, post.ID, me)
}
