package web

import (
	"strings"
	"testing"

	"snowball/internal/store"
)

func TestValidLink(t *testing.T) {
	cases := []struct {
		link string
		want bool
	}{
		{"https://example.org", true},
		{"http://example.org/some/page?x=1", true},
		{"javascript:alert(1)", false},
		{"data:text/html,<script>alert(1)</script>", false},
		{"example.org", false}, // no scheme: ambiguous, so refused
		{"/p/abcdefgh", false},
		{"https://", false},
		{"https://example.org/" + strings.Repeat("a", maxURLLen), false},
	}
	for _, c := range cases {
		if got := validLink(c.link); got != c.want {
			t.Errorf("validLink(%q) = %v, want %v", c.link, got, c.want)
		}
	}
}

func TestValidatePost(t *testing.T) {
	if errs := validatePost("A fine title", "", "Some text."); len(errs) != 0 {
		t.Errorf("a normal post should pass, got %v", errs)
	}
	if errs := validatePost("", "", ""); len(errs) == 0 {
		t.Error("an empty title should fail")
	}
	if errs := validatePost(strings.Repeat("x", maxTitleLen+1), "", ""); len(errs) == 0 {
		t.Error("an over-long title should fail")
	}
	if errs := validatePost("ok", "", strings.Repeat("x", maxBodyLen+1)); len(errs) == 0 {
		t.Error("an over-long body should fail")
	}
}

func TestPickTags(t *testing.T) {
	all := []store.Tag{{ID: 1, Name: "art"}, {ID: 2, Name: "ask"}, {ID: 3, Name: "books"}, {ID: 4, Name: "diy"}}

	cases := []struct {
		name        string
		values      []string
		wantIDs     int
		wantProblem bool
	}{
		{"one tag", []string{"1"}, 1, false},
		{"three tags", []string{"1", "2", "3"}, 3, false},
		{"none", nil, 0, true},
		{"too many", []string{"1", "2", "3", "4"}, 4, true},
		{"duplicates count once", []string{"2", "2", "2"}, 1, false},
		{"unknown and junk ignored", []string{"99", "abc", "1"}, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ids, problem := pickTags(c.values, all)
			if len(ids) != c.wantIDs {
				t.Errorf("got %d tags, want %d", len(ids), c.wantIDs)
			}
			if (problem != "") != c.wantProblem {
				t.Errorf("problem = %q, wanted a problem: %v", problem, c.wantProblem)
			}
		})
	}
}
