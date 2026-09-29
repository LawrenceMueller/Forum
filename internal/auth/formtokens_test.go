package auth

import (
	"errors"
	"testing"
	"time"
)

func TestFormTokens(t *testing.T) {
	f, err := NewFormTokens()
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	token := f.Issue("signup", issued)

	// Checking at a chosen "now" instead of the real clock lets the test
	// jump through time instantly.
	cases := []struct {
		name  string
		token string
		form  string
		after time.Duration
		want  error
	}{
		{"on time", token, "signup", 10 * time.Second, nil},
		{"too fast", token, "signup", 500 * time.Millisecond, ErrFormTooFast},
		{"expired", token, "signup", 3 * time.Hour, ErrFormExpired},
		{"wrong form", token, "login", 10 * time.Second, ErrFormTokenInvalid},
		{"missing", "", "signup", 10 * time.Second, ErrFormTokenInvalid},
		{"tampered time", "1" + token, "signup", 10 * time.Second, ErrFormTokenInvalid},
		{"from the future", token, "signup", -time.Minute, ErrFormTokenInvalid},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := f.Check(c.token, c.form, issued.Add(c.after), 3*time.Second, 2*time.Hour)
			if !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestFormTokensFromAnotherServer(t *testing.T) {
	a, _ := NewFormTokens()
	b, _ := NewFormTokens() // a different key, like after a restart
	now := time.Now()

	err := b.Check(a.Issue("login", now), "login", now.Add(time.Minute), 0, time.Hour)
	if !errors.Is(err, ErrFormTokenInvalid) {
		t.Errorf("a token signed with another key should be invalid, got %v", err)
	}
}
