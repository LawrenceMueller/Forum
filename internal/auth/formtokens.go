package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// FormTokens issues and checks signed timestamps for forms. Each form carries
// a hidden token saying when it was handed out, signed so it can't be forged.
// On submit, that tells us two things a bot gets wrong:
//
//   - whether the form was loaded at all (bots often post blind), and
//   - how long it was open (people take seconds; scripts take milliseconds).
//
// Tokens are also bound to the form's name, so a login token can't be
// reused to submit the sign-up form.
type FormTokens struct {
	key []byte
}

var (
	ErrFormTokenInvalid = errors.New("form token missing or invalid")
	ErrFormTooFast      = errors.New("form submitted too quickly")
	ErrFormExpired      = errors.New("form token expired")
)

// NewFormTokens makes a signer with a random secret. A restart makes a new
// one, which invalidates forms that were open at the time.
func NewFormTokens() (*FormTokens, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &FormTokens{key: key}, nil
}

// Issue returns a token for the named form, issued at now:
// "<milliseconds since 1970>.<signature>".
func (f *FormTokens) Issue(form string, now time.Time) string {
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	return ts + "." + f.sign(form, ts)
}

func (f *FormTokens) sign(form, ts string) string {
	mac := hmac.New(sha256.New, f.key)
	mac.Write([]byte(form + "|" + ts))
	return hex.EncodeToString(mac.Sum(nil))
}

// Check verifies a token for the named form: genuine, at least minAge old,
// and no older than maxAge.
func (f *FormTokens) Check(token, form string, now time.Time, minAge, maxAge time.Duration) error {
	ts, sig, ok := strings.Cut(token, ".")
	if !ok || !hmac.Equal([]byte(f.sign(form, ts)), []byte(sig)) {
		return ErrFormTokenInvalid
	}
	ms, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ErrFormTokenInvalid
	}

	age := now.Sub(time.UnixMilli(ms))
	switch {
	case age < 0:
		return ErrFormTokenInvalid // issued in the future: not one of ours
	case age < minAge:
		return ErrFormTooFast
	case age > maxAge:
		return ErrFormExpired
	}
	return nil
}
