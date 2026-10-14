package web

import (
	"strings"
	"testing"
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
