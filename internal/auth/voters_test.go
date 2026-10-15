package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestVoterFingerprints(t *testing.T) {
	a, err := NewVoterKeys(strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewVoterKeys(strings.Repeat("b", 32))

	// Two separate calls, compared: written as a.Voter(7) != a.Voter(7),
	// staticcheck flags it as a typo, since identical expressions on both
	// sides usually are one.
	first, again := a.Voter(7), a.Voter(7)
	if first != again {
		t.Error("the same account must always get the same fingerprint")
	}
	if a.Voter(7) == a.Voter(8) {
		t.Error("different accounts must get different fingerprints")
	}
	if a.Voter(7) == b.Voter(7) {
		t.Error("a different secret must give different fingerprints")
	}
	if len(a.Voter(7)) != 64 {
		t.Errorf("fingerprints should be 64 hex characters, got %d", len(a.Voter(7)))
	}
}

func TestVoteSecretTooShort(t *testing.T) {
	if _, err := NewVoterKeys("short"); !errors.Is(err, ErrVoteSecretTooShort) {
		t.Errorf("want ErrVoteSecretTooShort, got %v", err)
	}
}

func TestReporterFingerprints(t *testing.T) {
	k, _ := NewVoterKeys(strings.Repeat("a", 32))
	first, again := k.Reporter(7), k.Reporter(7)
	if first != again {
		t.Error("the same account must always get the same reporter fingerprint")
	}
	if k.Reporter(7) == k.Reporter(8) {
		t.Error("different accounts must get different reporter fingerprints")
	}
	// Reports and votes are stored under different fingerprints, so the two
	// can't be matched up to each other.
	if k.Reporter(7) == k.Voter(7) {
		t.Error("an account's reporter and voter fingerprints must differ")
	}
}
