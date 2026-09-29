package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

const RecoveryCodeCount = 8

// Base32 uses A-Z and 2-7: no 0, 1, 8 or 9, so nobody mixes up O and 0
// or I and 1 when copying a code by hand.
var codeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newRecoveryCode returns something like "K7QM-3XAT-RB2W":
// 12 random characters, 60 bits of randomness.
func newRecoveryCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s := codeEncoding.EncodeToString(b)[:12]
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12], nil
}

// normalizeRecoveryCode makes "k7qm 3xat-rb2w" and "K7QM-3XAT-RB2W" match.
func normalizeRecoveryCode(code string) string {
	code = strings.ToUpper(code)
	return strings.NewReplacer("-", "", " ", "").Replace(code)
}

// HashRecoveryCode uses plain SHA-256, not argon2. Slow hashing protects
// passwords because people choose guessable ones; these codes are fully
// random, so there's nothing to guess and a fast hash is enough.
func HashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}

// GenerateRecoveryCodes returns the codes to show the user once,
// and the hashes to store.
func GenerateRecoveryCodes() (codes, hashes []string, err error) {
	for range RecoveryCodeCount {
		code, err := newRecoveryCode()
		if err != nil {
			return nil, nil, err
		}
		codes = append(codes, code)
		hashes = append(hashes, HashRecoveryCode(code))
	}
	return codes, hashes, nil
}
