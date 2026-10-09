package store

// Database tests for posts, lists, and comments.

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var publicIDFormat = regexp.MustCompile(`^[a-z2-7]{8}$`)

func TestCreatePost(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")

	pid, err := st.CreatePost(ctx, NewPost{
		AuthorID: me, Title: "A link", URL: "https://example.org/x", Body: "**hi**",
		TagIDs: []int{mustTag(t, st, "tech"), mustTag(t, st, "ask")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !publicIDFormat.MatchString(pid) {
		t.Errorf("public ID %q isn't 8 characters of a-z2-7", pid)
	}

	p := mustLoadPost(t, st, pid)
	if p.Title != "A link" || p.URL != "https://example.org/x" || p.Body != "**hi**" || p.Author != "blue-humming-otter" {
		t.Errorf("loaded post doesn't match what was saved: %+v", p)
	}
	if !slices.Equal(p.Tags, []string{"ask", "tech"}) {
		t.Errorf("tags = %v, want [ask tech] (alphabetical)", p.Tags)
	}

	// A text post stores no link at all (NULL), not an empty string.
	text := mustPost(t, st, me, "Just text")
	if count(t, st, `SELECT count(*) FROM posts WHERE id = $1 AND url IS NULL`, text.ID) != 1 {
		t.Error("a text post's url should be NULL")
	}
}

func TestPostLimitsAreEnforcedByTheDatabase(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	if _, err := st.CreatePost(ctx, NewPost{AuthorID: me, Title: strings.Repeat("x", 201)}); err == nil {
		t.Error("a 201-character title should be refused by the database, even if Go's checks were skipped")
	}
	if _, err := st.CreatePost(ctx, NewPost{AuthorID: me, Title: ""}); err == nil {
		t.Error("an empty title should be refused by the database")
	}
}

func TestListsHideRemovedAndDeletedPosts(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	keep := mustPost(t, st, me, "Visible")
	removed := mustPost(t, st, me, "Removed")
	deleted := mustPost(t, st, me, "Deleted")

	if err := st.RemovePost(ctx, removed.PublicID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeletePost(ctx, deleted.PublicID, me); err != nil {
		t.Fatal(err)
	}

	for _, sort := range []Sort{SortNew, SortActive, SortTop, SortPopular} {
		posts, _, err := st.ListPosts(ctx, ListOptions{Sort: sort, Limit: 30})
		if err != nil {
			t.Fatal(err)
		}
		if len(posts) != 1 || posts[0].PublicID != keep.PublicID {
			t.Errorf("sort %d: got %d posts, want only the visible one", sort, len(posts))
		}
	}

	if n, _ := st.CountPosts(ctx); n != 1 {
		t.Errorf("CountPosts = %d, want 1", n)
	}
	counts, _ := st.TagCounts(ctx)
	for _, tc := range counts {
		if tc.Name == "tech" && tc.Count != 1 {
			t.Errorf("tech tag count = %d, want 1", tc.Count)
		}
	}

	// Tag pages filter by tag.
	if posts, _, _ := st.ListPosts(ctx, ListOptions{Sort: SortNew, Tag: "books", Limit: 30}); len(posts) != 0 {
		t.Errorf("no post is tagged books, but the tag list returned %d", len(posts))
	}
}

func TestPaginationNeverSkipsOrRepeats(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")

	// 25 posts, with several sharing exact timestamps: the case where a
	// naive cursor would skip or repeat posts.
	for i := 1; i <= 25; i++ {
		p := mustPost(t, st, me, fmt.Sprintf("post %d", i))
		// i/3 is whole-number division, so posts come in groups of three
		// sharing a timestamp. (Both columns get the same expression: within
		// one UPDATE, every SET reads the row as it was BEFORE the update.)
		exec(t, st, `UPDATE posts
			SET created_at       = '2026-10-01 12:00'::timestamptz - ($1::int / 3) * interval '1 hour',
			    last_activity_at = '2026-10-01 12:00'::timestamptz - ($1::int / 3) * interval '1 hour'
			WHERE id = $2`, i, p.ID)
	}

	for _, sort := range []Sort{SortNew, SortActive} {
		seen := map[string]bool{}
		var after *Cursor
		pages := 0
		for {
			posts, next, err := st.ListPosts(ctx, ListOptions{Sort: sort, After: after, Limit: 7})
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range posts {
				if seen[p.PublicID] {
					t.Fatalf("sort %d: %s appeared twice", sort, p.Title)
				}
				seen[p.PublicID] = true
			}
			pages++
			if next == nil {
				break
			}
			after = next
			if pages > 10 {
				t.Fatal("pagination never ended")
			}
		}
		if len(seen) != 25 || pages != 4 {
			t.Errorf("sort %d: saw %d posts over %d pages, want 25 over 4", sort, len(seen), pages)
		}
	}
}

func TestComments(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	post := mustPost(t, st, me, "A post")

	top := mustComment(t, st, post, me, "", "top level")
	reply, depth, err := st.CreateComment(ctx, NewComment{PostID: post.ID, ParentPublicID: top, AuthorID: me, Body: "a reply"})
	if err != nil || depth != 1 {
		t.Fatalf("reply: depth %d, err %v", depth, err)
	}
	if c := mustLoadComment(t, st, reply); c.Depth != 1 || c.ParentID == 0 {
		t.Errorf("reply stored with depth %d, parent %d", c.Depth, c.ParentID)
	}

	after := mustLoadPost(t, st, post.PublicID)
	if after.CommentCount != 2 {
		t.Errorf("comment count = %d, want 2", after.CommentCount)
	}
	// PostByPublicID doesn't load last_activity_at, so check the column directly.
	if count(t, st, `SELECT count(*) FROM posts WHERE id = $1 AND last_activity_at > created_at`, post.ID) != 1 {
		t.Error("commenting should bump the post's activity time (that's what moves it up 'active')")
	}

	all, err := st.CommentsForPost(ctx, post.ID)
	if err != nil || len(all) != 2 {
		t.Fatalf("CommentsForPost: %d comments, err %v", len(all), err)
	}
}

func TestCommentRules(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	postA := mustPost(t, st, me, "Post A")
	postB := mustPost(t, st, me, "Post B")
	onA := mustComment(t, st, postA, me, "", "on A")

	// A reply must be on the same post as its parent.
	_, _, err := st.CreateComment(ctx, NewComment{PostID: postB.ID, ParentPublicID: onA, AuthorID: me, Body: "sneaky"})
	if !errors.Is(err, ErrParentGone) {
		t.Errorf("replying across posts: want ErrParentGone, got %v", err)
	}

	// Removed parents take no replies.
	if err := st.RemoveComment(ctx, onA); err != nil {
		t.Fatal(err)
	}
	_, _, err = st.CreateComment(ctx, NewComment{PostID: postA.ID, ParentPublicID: onA, AuthorID: me, Body: "late"})
	if !errors.Is(err, ErrParentGone) {
		t.Errorf("replying to a removed comment: want ErrParentGone, got %v", err)
	}
	if p := mustLoadPost(t, st, postA.PublicID); p.CommentCount != 0 {
		t.Errorf("removing the only comment should leave a count of 0, got %d", p.CommentCount)
	}

	// Removed posts take no comments.
	if err := st.RemovePost(ctx, postB.PublicID); err != nil {
		t.Fatal(err)
	}
	_, _, err = st.CreateComment(ctx, NewComment{PostID: postB.ID, AuthorID: me, Body: "hello?"})
	if !errors.Is(err, ErrPostLocked) {
		t.Errorf("commenting on a removed post: want ErrPostLocked, got %v", err)
	}
}
