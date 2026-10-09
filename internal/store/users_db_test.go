package store

// Database tests for accounts, sessions, and recovery.
// Each follows the same rhythm: ARRANGE a known state, ACT by calling the
// store, ASSERT on the result and on what's now in the database.

import (
	"errors"
	"testing"
	"time"
)

func TestUsernamesAreUniqueForever(t *testing.T) {
	st := freshDB(t)
	id := mustUser(t, st, "blue-humming-otter")

	// A second account can't take the name...
	if _, err := st.CreateUser(ctx, "blue-humming-otter", "h", nil); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate name: want ErrUsernameTaken, got %v", err)
	}

	// ...and neither can anyone after the account is deleted.
	if err := st.DeleteAccount(ctx, id, "voter-x", false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(ctx, "blue-humming-otter", "h", nil); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("a deleted account's name must stay retired, got %v", err)
	}
	if taken, _ := st.UsernameTaken(ctx, "blue-humming-otter"); !taken {
		t.Error("UsernameTaken should report a retired name as taken")
	}
}

func TestUsernameFormatIsEnforced(t *testing.T) {
	st := freshDB(t)
	for _, bad := range []string{"Blue-Humming-Otter", "blue otter", "blue-otter", "blue-humming-otter-2", "<script>"} {
		if _, err := st.CreateUser(ctx, bad, "h", nil); err == nil {
			t.Errorf("username %q should have been refused", bad)
		}
	}
}

func TestCredentials(t *testing.T) {
	st := freshDB(t)
	id := mustUser(t, st, "blue-humming-otter")

	gotID, hash, found, err := st.Credentials(ctx, "blue-humming-otter")
	if err != nil || !found || gotID != id || hash != "hash-of-blue-humming-otter" {
		t.Fatalf("Credentials = (%d, %q, %v, %v)", gotID, hash, found, err)
	}
	if _, _, found, _ := st.Credentials(ctx, "red-baking-hare"); found {
		t.Error("a name nobody has should not be found")
	}
}

func TestSessions(t *testing.T) {
	st := freshDB(t)
	id := mustUser(t, st, "blue-humming-otter")

	if err := st.CreateSession(ctx, id, "live-token", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, id, "old-token", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	u, ok, err := st.UserBySession(ctx, "live-token")
	if err != nil || !ok || u.ID != id || u.Username != "blue-humming-otter" {
		t.Fatalf("live session: got (%+v, %v, %v)", u, ok, err)
	}
	if u.CreatedAt.IsZero() {
		t.Error("the session's user should carry the account's creation time (voting age needs it)")
	}

	if _, ok, _ := st.UserBySession(ctx, "old-token"); ok {
		t.Error("an expired session must not log anyone in")
	}

	n, err := st.DeleteExpiredSessions(ctx)
	if err != nil || n != 1 {
		t.Fatalf("cleanup removed %d sessions (err %v), want 1", n, err)
	}
	if count(t, st, `SELECT count(*) FROM sessions`) != 1 {
		t.Error("cleanup should leave the live session alone")
	}
}

func TestLogoutEverywhere(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	other := mustUser(t, st, "red-baking-hare")
	for _, tok := range []string{"phone", "laptop"} {
		if err := st.CreateSession(ctx, me, tok, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CreateSession(ctx, other, "theirs", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteUserSessions(ctx, me); err != nil {
		t.Fatal(err)
	}
	if n := count(t, st, `SELECT count(*) FROM sessions WHERE user_id = $1`, me); n != 0 {
		t.Errorf("%d of my sessions survived logout", n)
	}
	if _, ok, _ := st.UserBySession(ctx, "theirs"); !ok {
		t.Error("logging out must not touch other people's sessions")
	}
}

func TestChangePasswordLogsOutEverywhere(t *testing.T) {
	st := freshDB(t)
	id := mustUser(t, st, "blue-humming-otter")
	if err := st.CreateSession(ctx, id, "tok", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := st.ChangePassword(ctx, id, "new-hash"); err != nil {
		t.Fatal(err)
	}
	if h, _ := st.PasswordHash(ctx, id); h != "new-hash" {
		t.Errorf("password hash = %q, want new-hash", h)
	}
	if count(t, st, `SELECT count(*) FROM sessions`) != 0 {
		t.Error("changing the password must end every session, including the current one")
	}
}

func TestRecoveryCodeWorksExactlyOnce(t *testing.T) {
	st := freshDB(t)
	id := mustUser(t, st, "blue-humming-otter")
	mustUser(t, st, "red-baking-hare")
	if err := st.CreateSession(ctx, id, "stolen", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, err := st.RecoverAccount(ctx, "blue-humming-otter", "code-a", "recovered-hash")
	if err != nil || got != id {
		t.Fatalf("first use: got (%d, %v)", got, err)
	}
	if n, _ := st.RemainingRecoveryCodes(ctx, id); n != 1 {
		t.Errorf("%d codes left, want 1", n)
	}
	if h, _ := st.PasswordHash(ctx, id); h != "recovered-hash" {
		t.Error("recovery should set the new password")
	}
	if count(t, st, `SELECT count(*) FROM sessions`) != 0 {
		t.Error("recovery should throw out every session (someone may have been in the account)")
	}

	if _, err := st.RecoverAccount(ctx, "blue-humming-otter", "code-a", "x"); !errors.Is(err, ErrRecoveryFailed) {
		t.Errorf("a spent code must fail, got %v", err)
	}
	// Both accounts have a code stored as "code-b", but each only works on its own account.
	if _, err := st.RecoverAccount(ctx, "nobody-at-all", "code-b", "x"); !errors.Is(err, ErrRecoveryFailed) {
		t.Errorf("an unknown name must fail, got %v", err)
	}
	if _, err := st.RecoverAccount(ctx, "blue-humming-otter", "wrong", "x"); !errors.Is(err, ErrRecoveryFailed) {
		t.Errorf("a wrong code must fail, got %v", err)
	}
	if n, _ := st.RemainingRecoveryCodes(ctx, id); n != 1 {
		t.Errorf("failed attempts must not spend codes: %d left, want 1", n)
	}
}

func TestDeleteAccount(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	other := mustUser(t, st, "red-baking-hare")
	theirPost := mustPost(t, st, other, "Their post")
	myPost := mustPost(t, st, me, "My post")

	if err := st.CreateSession(ctx, me, "tok", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Vote(ctx, VotePost, theirPost.PublicID, "my-fingerprint", me, 1); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteAccount(ctx, me, "my-fingerprint", false); err != nil {
		t.Fatal(err)
	}

	if _, _, found, _ := st.Credentials(ctx, "blue-humming-otter"); found {
		t.Error("a deleted account must not be able to log in")
	}
	if count(t, st, `SELECT count(*) FROM users WHERE id = $1 AND password_hash IS NULL AND deleted_at IS NOT NULL`, me) != 1 {
		t.Error("the row should remain (name retired) with the password wiped")
	}
	if count(t, st, `SELECT count(*) FROM recovery_codes WHERE user_id = $1`, me) != 0 {
		t.Error("recovery codes should be gone")
	}
	if count(t, st, `SELECT count(*) FROM sessions WHERE user_id = $1`, me) != 0 {
		t.Error("sessions should be gone")
	}
	if _, err := st.RecoverAccount(ctx, "blue-humming-otter", "code-b", "x"); !errors.Is(err, ErrRecoveryFailed) {
		t.Error("a deleted account can't be recovered")
	}

	// THE PRIVACY PROMISE: the vote still counts, but nothing links it to
	// the account any more, not even its fingerprint.
	if n := count(t, st, `SELECT count(*) FROM post_votes WHERE voter = 'my-fingerprint'`); n != 0 {
		t.Errorf("%d votes still carry the deleted account's fingerprint", n)
	}
	if p := mustLoadPost(t, st, theirPost.PublicID); p.Score != 1 {
		t.Errorf("the vote should still count: score %d, want 1", p.Score)
	}

	// Without "also delete everything", posts stay, shown as [deleted] author.
	p := mustLoadPost(t, st, myPost.PublicID)
	if p.Deleted || p.Body == "" || !p.AuthorDeleted {
		t.Errorf("kept post: deleted=%v body=%q authorDeleted=%v", p.Deleted, p.Body, p.AuthorDeleted)
	}
}

func TestDeleteAccountWithEverything(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	other := mustUser(t, st, "red-baking-hare")
	myPost := mustPost(t, st, me, "My post")
	theirPost := mustPost(t, st, other, "Their post")
	mustComment(t, st, theirPost, me, "", "my comment on their post")
	mustComment(t, st, theirPost, other, "", "their own comment")

	if err := st.DeleteAccount(ctx, me, "my-fingerprint", true); err != nil {
		t.Fatal(err)
	}

	p := mustLoadPost(t, st, myPost.PublicID)
	if !p.Deleted || p.Body != "" || p.Title != "My post" {
		t.Errorf("my post: deleted=%v body=%q title=%q (title stays, body erased)", p.Deleted, p.Body, p.Title)
	}
	if count(t, st, `SELECT count(*) FROM comments WHERE author_id = $1 AND body <> ''`, me) != 0 {
		t.Error("my comments should be erased")
	}
	if count(t, st, `SELECT count(*) FROM comments WHERE author_id = $1 AND body <> ''`, other) != 1 {
		t.Error("other people's comments must be untouched")
	}
	if p := mustLoadPost(t, st, theirPost.PublicID); p.CommentCount != 1 {
		t.Errorf("their post's comment count = %d, want 1 (mine was erased)", p.CommentCount)
	}
}
