package store

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// sessionUser logs a user in and loads them the way every request does.
func sessionUser(t *testing.T, st *Store, id int64) User {
	t.Helper()
	token := fmt.Sprint("token-", id, "-", time.Now().UnixNano())
	if err := st.CreateSession(ctx, id, token, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	u, ok, err := st.UserBySession(ctx, token)
	if err != nil || !ok {
		t.Fatalf("loading the session: %v, %v", ok, err)
	}
	return u
}

func TestSuspensionLifecycle(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")

	if u := sessionUser(t, st, me); u.Suspension != nil {
		t.Fatal("a new account shouldn't be suspended")
	}
	if err := st.Suspend(ctx, me, "harassment", "Please cool off.", 7); err != nil {
		t.Fatal(err)
	}
	u := sessionUser(t, st, me)
	if u.Suspension == nil || u.Suspension.Reason != "harassment" || u.Suspension.Note != "Please cool off." ||
		u.Suspension.Permanent() || !u.Suspension.CanAppeal() {
		t.Fatalf("the session should carry the suspension, got %+v", u.Suspension)
	}
	if left := time.Until(*u.Suspension.EndsAt); left < 6*24*time.Hour || left > 7*24*time.Hour {
		t.Errorf("a 7-day suspension ends in %s", left)
	}
	id := u.Suspension.ID

	// One appeal, then no more.
	if err := st.Appeal(ctx, me, id, "I was quoting someone else."); err != nil {
		t.Fatal(err)
	}
	if err := st.Appeal(ctx, me, id, "Again!"); !errors.Is(err, ErrAlreadyAppealed) {
		t.Errorf("a second appeal gave %v", err)
	}
	if err := st.Appeal(ctx, me+1, id, "Not mine"); !errors.Is(err, ErrNotSuspended) {
		t.Errorf("appealing someone else's suspension gave %v", err)
	}
	appeals, err := st.OpenAppeals(ctx)
	if err != nil || len(appeals) != 1 || appeals[0].Username != "blue-humming-otter" {
		t.Fatalf("open appeals = %+v, %v", appeals, err)
	}
	if n, _ := st.OpenAppealCount(ctx); n != 1 {
		t.Errorf("open appeal count = %d", n)
	}

	// Turned down: it stands, and the appeal is no longer open.
	if err := st.KeepSuspension(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := st.KeepSuspension(ctx, id); !errors.Is(err, ErrAppealNotPossible) {
		t.Errorf("deciding twice gave %v", err)
	}
	if n, _ := st.OpenAppealCount(ctx); n != 0 {
		t.Errorf("open appeal count = %d after deciding", n)
	}
	if u := sessionUser(t, st, me); u.Suspension == nil || u.Suspension.AppealHandledAt == nil {
		t.Error("the suspension should stand, with its appeal marked as read")
	}

	// Lifted early: gone.
	if err := st.LiftSuspension(ctx, id); err != nil {
		t.Fatal(err)
	}
	if u := sessionUser(t, st, me); u.Suspension != nil {
		t.Error("a lifted suspension is still in force")
	}
	if err := st.LiftSuspension(ctx, id); !errors.Is(err, ErrNotSuspended) {
		t.Errorf("lifting twice gave %v", err)
	}

	// The log: suspend, keep, lift; the public view leaves out the appeal decision.
	all, _ := st.ModLog(ctx, 0, 10, true)
	var got []string
	for _, e := range all {
		got = append(got, e.Action+":"+e.Reason+":"+e.Detail)
	}
	if want := "[lift:harassment: keep:harassment: suspend:harassment:7 days]"; fmt.Sprint(got) != want {
		t.Errorf("log = %v, want %v", got, want)
	}
	if public, _ := st.ModLog(ctx, 0, 10, false); len(public) != 2 {
		t.Errorf("the public log has %d entries, want 2", len(public))
	}
	for _, e := range all {
		if e.PostID != "" || e.CommentID != "" {
			t.Error("account actions name no post or comment")
		}
	}
}

func TestSuspensionRules(t *testing.T) {
	st := freshDB(t)
	me := mustUser(t, st, "blue-humming-otter")
	boss := mustUser(t, st, "green-napping-puffin")
	exec(t, st, `UPDATE users SET is_admin = true WHERE id = $1`, boss)

	if err := st.Suspend(ctx, boss, "spam", "", 1); !errors.Is(err, ErrCantSuspendAdmin) {
		t.Errorf("suspending the admin gave %v", err)
	}
	if err := st.Suspend(ctx, me, "made-up", "", 1); err == nil {
		t.Error("an unknown reason was accepted")
	}
	if err := st.Suspend(ctx, me, "spam", "", 3); err == nil {
		t.Error("an unsupported length was accepted")
	}
	if err := st.Suspend(ctx, 999999, "spam", "", 1); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("suspending a missing account gave %v", err)
	}

	// A new suspension replaces the one in force.
	if err := st.Suspend(ctx, me, "spam", "", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Suspend(ctx, me, "harassment", "", 0); err != nil {
		t.Fatal(err)
	}
	if u := sessionUser(t, st, me); u.Suspension == nil || !u.Suspension.Permanent() || u.Suspension.Reason != "harassment" {
		t.Errorf("after replacing: %+v", u.Suspension)
	}
	if n := count(t, st, `SELECT count(*) FROM suspensions WHERE user_id = $1 AND lifted_at IS NULL`, me); n != 1 {
		t.Errorf("%d suspensions in force, want 1", n)
	}

	// One that has run out isn't in force any more.
	exec(t, st, `UPDATE suspensions SET ends_at = now() - interval '1 minute' WHERE user_id = $1`, me)
	if u := sessionUser(t, st, me); u.Suspension != nil {
		t.Error("an expired suspension is still in force")
	}

	info, err := st.AccountByName(ctx, "blue-humming-otter")
	if err != nil || info.ID != me || info.IsAdmin {
		t.Errorf("AccountByName = %+v, %v", info, err)
	}
	if _, err := st.AccountByName(ctx, "nobody-at-all"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("a missing account gave %v", err)
	}
}
