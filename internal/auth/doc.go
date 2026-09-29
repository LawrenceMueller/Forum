// Package auth holds Snowball's security-critical logic: password hashing
// and rules, recovery codes, session tokens, and generated usernames.
//
// Everything here takes plain values and returns plain values. Nothing
// touches HTTP or the database, which keeps it easy to read, review, and test.
package auth
