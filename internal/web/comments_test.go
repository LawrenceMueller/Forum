package web

import (
	"fmt"
	"math"
	"testing"
	"time"

	"snowball/internal/store"
)

func TestWilson(t *testing.T) {
	cases := []struct {
		up, down int
		want     float64
	}{
		{0, 0, 0},
		{1, 0, 0.2065},
		{10, 1, 0.6226},
		{40, 20, 0.5406},
	}
	for _, c := range cases {
		if got := wilson(c.up, c.down); math.Abs(got-c.want) > 0.001 {
			t.Errorf("wilson(%d, %d) = %.4f, want about %.4f", c.up, c.down, got, c.want)
		}
	}
	// The whole point of "best": fewer votes but a better ratio can win.
	if wilson(10, 1) <= wilson(40, 20) {
		t.Error("10 up / 1 down should rank above 40 up / 20 down")
	}
}

// ---- helpers for building test threads ----

var base = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func cmt(id, parent int64, minutes int) store.Comment {
	return store.Comment{
		ID:        id,
		ParentID:  parent,
		PublicID:  fmt.Sprintf("c%07d", id), // not the real format, but unique
		Author:    "blue-humming-otter",
		Body:      "hello",
		CreatedAt: base.Add(time.Duration(minutes) * time.Minute),
	}
}

func ids(nodes []*commentNode) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.PublicID)
	}
	return out
}

func opts(sort string) treeOptions {
	return treeOptions{sort: sort, postID: "abcdefgh"}
}

// ---- tests ----

func TestTreeNestsAndSortsByTime(t *testing.T) {
	comments := []store.Comment{cmt(1, 0, 0), cmt(2, 0, 5), cmt(3, 1, 10)}

	old := buildTree(comments, opts("old"))
	if got := fmt.Sprint(ids(old)); got != "[c0000001 c0000002]" {
		t.Errorf("old: top level = %s", got)
	}
	if len(old[0].Children) != 1 || old[0].Children[0].PublicID != "c0000003" {
		t.Error("comment 3 should be nested under comment 1")
	}

	newest := buildTree(comments, opts("new"))
	if got := fmt.Sprint(ids(newest)); got != "[c0000002 c0000001]" {
		t.Errorf("new: top level = %s", got)
	}
}

func TestTreeSortsByBest(t *testing.T) {
	a, b, c := cmt(1, 0, 0), cmt(2, 0, 1), cmt(3, 0, 2)
	a.Upvotes = 1                   // one lonely upvote
	b.Upvotes, b.Downvotes = 10, 1 // well liked
	// c has no votes and is newest

	tree := buildTree([]store.Comment{a, b, c}, opts("best"))
	if got := fmt.Sprint(ids(tree)); got != "[c0000002 c0000001 c0000003]" {
		t.Errorf("best order = %s, want the well-liked comment first", got)
	}
}

func TestTreeDropsEmptyDeletedComments(t *testing.T) {
	gone := cmt(1, 0, 0)
	gone.Deleted = true // no replies: should vanish

	holder := cmt(2, 0, 1)
	holder.Removed = true // has a reply: should stay as a placeholder
	reply := cmt(3, 2, 2)

	tree := buildTree([]store.Comment{gone, holder, reply}, opts("old"))
	if len(tree) != 1 || tree[0].PublicID != "c0000002" {
		t.Fatalf("want only the removed comment with a reply, got %v", ids(tree))
	}
	if tree[0].Placeholder != "[removed]" || tree[0].Author != "" || tree[0].BodyHTML != "" {
		t.Error("a removed comment must show a placeholder and hide its author and body")
	}
	if len(tree[0].Children) != 1 {
		t.Error("the reply under a removed comment must survive")
	}
}

func TestAdminSeesRemovedComments(t *testing.T) {
	c := cmt(1, 0, 0)
	c.Removed = true
	o := opts("old")
	o.isAdmin = true

	tree := buildTree([]store.Comment{c}, o)
	if len(tree) != 1 || !tree[0].AdminSeesGone || tree[0].BodyHTML == "" {
		t.Error("the admin should still see a removed comment's content")
	}
}

// chain builds a thread where each comment replies to the one before.
func chain(n int) []store.Comment {
	var out []store.Comment
	for i := 1; i <= n; i++ {
		out = append(out, cmt(int64(i), int64(i-1), i))
	}
	return out
}

func TestDepthRules(t *testing.T) {
	tree := buildTree(chain(10), opts("old"))

	// Walk down the single branch, recording each level.
	var levels []*commentNode
	for n := tree[0]; n != nil; {
		levels = append(levels, n)
		if len(n.Children) == 0 {
			break
		}
		n = n.Children[0]
	}

	if len(levels) != maxThreadDepth {
		t.Fatalf("the main page should show %d levels, got %d", maxThreadDepth, len(levels))
	}
	last := levels[maxThreadDepth-1]
	if last.ContinueURL != "/p/abcdefgh/c/"+last.PublicID {
		t.Errorf("the deepest shown comment should link onward, got %q", last.ContinueURL)
	}

	if !levels[maxIndentDepth-2].Indent || levels[maxIndentDepth-1].Indent {
		t.Error("replies should stop indenting at the indent limit")
	}
	if levels[maxIndentDepth-1].ReplyingTo != "" || levels[maxIndentDepth].ReplyingTo == "" {
		t.Error("only replies past the indent limit should say who they reply to")
	}
}

func TestContinueThreadStartsFromItsRoot(t *testing.T) {
	comments := chain(10)
	o := opts("old")
	o.rootID = comments[7].ID // the comment that had "continue this thread"

	tree := buildTree(comments, o)
	if len(tree) != 1 || tree[0].PublicID != comments[7].PublicID {
		t.Fatalf("the thread page should start at its root, got %v", ids(tree))
	}
	if len(tree[0].Children) != 1 {
		t.Error("the replies hidden on the main page should appear here")
	}
}
