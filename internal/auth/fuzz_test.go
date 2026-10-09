package auth

// Fuzz tests. In a normal "go test", each one only runs its seed inputs (and
// any saved failures in testdata/fuzz), so they're quick. To actually fuzz:
//
//	go test -run XXX -fuzz=FuzzParseHash -fuzztime=60s ./internal/auth
//
// (-run XXX skips the ordinary tests, so the fuzzer starts right away.)
// Any input that breaks a rule is saved under testdata/fuzz/ and becomes a
// permanent test: commit it.

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// A stored hash string, however damaged, must never crash the server, never
// ask argon2 for absurd amounts of memory, and never accept an empty hash.
func FuzzParseHash(f *testing.F) {
	f.Add(mustHash("Real-Password-1!")) // a genuine hash
	f.Add("$argon2id$v=19$m=0,t=0,p=0$$")
	f.Add("$argon2id$v=19$m=4294967295,t=1,p=1$c29tZXNhbHRzb21lc2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2g")
	f.Add("$argon2id$v=19$m=19456,t=2,p=1$c29tZXNhbHRzb21lc2FsdA$")
	f.Add("")

	f.Fuzz(func(t *testing.T, encoded string) {
		p, salt, key, err := parseHash(encoded)
		if err != nil {
			if !errors.Is(err, ErrBadHash) {
				t.Fatalf("parseHash(%q): unexpected error %v", encoded, err)
			}
			return
		}
		if p.time < 1 || p.threads < 1 || p.memory > maxHashMemory || p.memory < 8*uint32(p.threads) {
			t.Fatalf("parseHash(%q) accepted unsafe settings %+v", encoded, p)
		}
		if len(salt) < minSaltLen || len(key) < minKeyLen {
			t.Fatalf("parseHash(%q) accepted a too-short salt or hash", encoded)
		}
	})
}

// Damaged hashes that used to be dangerous: argon2 panics on t=0 or p=0, a
// huge m would exhaust the server's memory, and an empty hash part would
// have matched EVERY password.
func TestVerifyRefusesUnsafeHashes(t *testing.T) {
	salt := "c29tZXNhbHRzb21lc2FsdA" // 16 bytes
	key := "aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g"
	for _, bad := range []string{
		"$argon2id$v=19$m=19456,t=0,p=1$" + salt + "$" + key,
		"$argon2id$v=19$m=19456,t=2,p=0$" + salt + "$" + key,
		"$argon2id$v=19$m=4294967295,t=2,p=1$" + salt + "$" + key,
		"$argon2id$v=19$m=19456,t=2,p=1$" + salt + "$",
		"$argon2id$v=19$m=19456,t=2,p=1$$" + key,
	} {
		ok, err := VerifyPassword("any password at all", bad)
		if ok || !errors.Is(err, ErrBadHash) {
			t.Errorf("VerifyPassword with %q = (%v, %v), want (false, ErrBadHash)", bad, ok, err)
		}
	}
}

// The password rules must never crash, and anything they accept must really
// be within the length limits.
func FuzzPasswordProblems(f *testing.F) {
	f.Add("Tr0ub4dor&3", "")
	f.Add("P@ssw0rd!", "")
	f.Add("Blue-Humming-Otter1!", "blue-humming-otter")
	f.Add("Café-Kr8!", "")
	f.Add("\xff\xfe\x00", "")

	f.Fuzz(func(t *testing.T, password, username string) {
		problems := PasswordProblems(password, username)
		if len(problems) > 0 {
			return
		}
		n := utf8.RuneCountInString(normalizePassword(password))
		if n < MinPasswordLen || n > MaxPasswordLen {
			t.Fatalf("accepted %q with %d characters", password, n)
		}
		if username != "" && strings.Contains(strings.ToLower(normalizePassword(password)), strings.ToLower(username)) {
			t.Fatalf("accepted %q, which contains the username %q", password, username)
		}
	})
}
