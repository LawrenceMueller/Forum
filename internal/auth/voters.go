package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
)

// VoterKeys turns an account ID into the fingerprint stored with its votes.
//
// The votes tables never hold account IDs. They hold HMAC(secret, id): the
// same account always gets the same fingerprint, so one-vote-per-account
// still works, but without the secret there's no way to tell which account
// a fingerprint belongs to. The secret lives outside the database (an
// environment variable), so a leaked database or backup alone reveals
// nobody's votes.
//
// Unlike Snowball's other keys, this one must stay the same across restarts:
// if it changed, everyone's past votes would stop being recognized as
// theirs, and they could vote on everything again.
type VoterKeys struct {
	key []byte
}

// MinVoteSecretLen keeps the secret long enough to be unguessable.
const MinVoteSecretLen = 32

var ErrVoteSecretTooShort = errors.New("vote secret must be at least 32 characters")

func NewVoterKeys(secret string) (*VoterKeys, error) {
	if len(secret) < MinVoteSecretLen {
		return nil, ErrVoteSecretTooShort
	}
	return &VoterKeys{key: []byte(secret)}, nil
}

// Reporter returns the fingerprint an account's reports are filed under.
// It's a different value from Voter, so the stored reports and votes of one
// account can't be matched up with each other either.
func (v *VoterKeys) Reporter(userID int64) string {
	mac := hmac.New(sha256.New, v.key)
	mac.Write([]byte("reporter:" + strconv.FormatInt(userID, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Voter returns the fingerprint for an account.
func (v *VoterKeys) Voter(userID int64) string {
	mac := hmac.New(sha256.New, v.key)
	mac.Write([]byte("voter:" + strconv.FormatInt(userID, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}
