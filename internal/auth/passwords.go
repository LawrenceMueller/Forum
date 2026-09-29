package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

const (
	MinPasswordLen = 6
	MaxPasswordLen = 128 // hashing is deliberately slow, so cap what we'll hash
)

// normalizePassword puts a password into one standard Unicode form (NFKC).
// The same visible password can arrive as different bytes: "é" might be one
// character, or "e" plus a combining accent, depending on the keyboard and
// operating system. Normalizing first means it always hashes the same.
// Plain ASCII passwords come out unchanged.
func normalizePassword(password string) string {
	return norm.NFKC.String(password)
}

// PasswordProblems returns what a password is missing, in plain words that
// read after "Your password needs to ...". An empty result means it passes.
// username is the account's name, which a password mustn't contain.
func PasswordProblems(password, username string) []string {
	password = normalizePassword(password)

	// Count characters, not bytes, so "passwörd" isn't penalized.
	n := utf8.RuneCountInString(password)
	if n > MaxPasswordLen {
		return []string{fmt.Sprintf("be at most %d characters", MaxPasswordLen)}
	}

	var hasUpper, hasDigit, hasSpecial bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSpecial = true
		}
	}

	var missing []string
	if n < MinPasswordLen {
		missing = append(missing, fmt.Sprintf("be at least %d characters", MinPasswordLen))
	}
	if !hasUpper {
		missing = append(missing, "have an uppercase letter")
	}
	if !hasDigit {
		missing = append(missing, "have a number")
	}
	if !hasSpecial {
		missing = append(missing, "have a symbol, like ! or #")
	}
	if isCommonPattern(password) {
		missing = append(missing, "not be a common word with numbers or symbols added")
	}
	// Usernames are public, so a password containing one is half given away.
	if username != "" && strings.Contains(strings.ToLower(password), strings.ToLower(username)) {
		missing = append(missing, "not contain your username")
	}
	return missing
}

// commonWords are among the most-guessed password bases. Attackers try them
// first, with every obvious decoration, so they're no protection at all.
var commonWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		password passwd pass qwerty qwertyuiop asdf asdfgh asdfghjkl zxcvbn
		letmein welcome admin administrator login iloveyou love lovely
		monkey dragon football baseball soccer hockey master shadow sunshine
		princess superman batman trustno starwars whatever freedom hello
		secret summer winter spring autumn snowball snow abc abcd abcdef
		test tester user changeme flower charlie michael jordan hunter
		killer pokemon minecraft google facebook forum anonymous anon`) {
		commonWords[w] = true
	}
}

// Undo the usual letter swaps: p@ssw0rd reads as password.
var leetSpeak = strings.NewReplacer(
	"@", "a", "4", "a", "3", "e", "1", "i", "!", "i",
	"0", "o", "$", "s", "5", "s", "7", "t",
)

// isCommonPattern catches a common word dressed up to pass the rules:
// "Password1!", "P@ssw0rd!", "Summer2024!". It trims digits and symbols off
// both ends, undoes letter swaps in what's left, and checks the word list.
func isCommonPattern(password string) bool {
	core := strings.TrimFunc(strings.ToLower(password), func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	return commonWords[leetSpeak.Replace(core)]
}

// argon2id is deliberately slow and memory-hungry, which is what makes
// stolen hashes expensive to crack. These are OWASP's recommended minimums,
// sized to stay gentle on a small server.
type argonParams struct {
	memory  uint32 // KiB
	time    uint32 // passes over the memory
	threads uint8
	saltLen uint32
	keyLen  uint32
}

var currentArgon = argonParams{memory: 19 * 1024, time: 2, threads: 1, saltLen: 16, keyLen: 32}

var ErrBadHash = errors.New("stored password hash is malformed")

// Each argon2 hash uses about 19 MB of memory for a moment. A flood of
// simultaneous logins could exhaust a small server's memory, so at most
// maxConcurrentHashes run at once. Everyone else waits for a free slot,
// and gives up with ErrBusy if none frees up within busyTimeout.
const maxConcurrentHashes = 4

var (
	hashSlots   = make(chan struct{}, maxConcurrentHashes) // a semaphore: one token per running hash
	busyTimeout = 10 * time.Second                         // a var so tests can shorten it
)

var ErrBusy = errors.New("too many password checks in progress")

// idKey runs argon2 once a slot is free.
func idKey(password string, salt []byte, p argonParams, keyLen uint32) ([]byte, error) {
	select {
	case hashSlots <- struct{}{}: // took a slot
	case <-time.After(busyTimeout):
		return nil, ErrBusy
	}
	defer func() { <-hashSlots }() // give the slot back when done

	return argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, keyLen), nil
}

// HashPassword returns a self-describing string like
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
//
// The settings travel with each hash, so they can be raised later
// without breaking existing accounts.
func HashPassword(password string) (string, error) {
	p := currentArgon

	// A random salt per password means identical passwords get different
	// hashes, so attackers can't crack everyone at once.
	salt := make([]byte, p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	key, err := idKey(normalizePassword(password), salt, p, p.keyLen)
	if err != nil {
		return "", err
	}

	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword re-hashes the attempt with the stored salt and settings
// and compares the results. It can fail with ErrBusy when the server is
// flooded, or ErrBadHash if the stored hash is damaged.
func VerifyPassword(password, encoded string) (bool, error) {
	// "", "argon2id", "v=19", "m=...,t=...,p=...", salt, hash
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrBadHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrBadHash
	}

	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return false, ErrBadHash
	}

	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, ErrBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, ErrBadHash
	}

	got, err := idKey(normalizePassword(password), salt, p, uint32(len(want)))
	if err != nil {
		return false, err
	}

	// Constant-time comparison, so response timing can't reveal how
	// many bytes of a guess were right.
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash lets a login for a nonexistent user take as long as a real one.
var dummyHash = mustHash("snowball-timing-dummy")

func mustHash(pw string) string {
	h, err := HashPassword(pw)
	if err != nil {
		panic(err)
	}
	return h
}

// VerifyDummy does the work of a password check and throws the answer away.
// Call it when a username doesn't exist, so the speed of a failed login
// doesn't reveal whether the name was real.
func VerifyDummy(password string) {
	_, _ = VerifyPassword(password, dummyHash)
}
