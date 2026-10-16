package web

// Tests for notes from the admin.

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestAdminNotes(t *testing.T) {
	s := newSite(t)
	member := s.user(t, "blue-humming-otter")
	boss := admin(t, s.user(t, "silver-sleeping-fox"))

	expectMissing(t, "no note, no box", s.get("/", nil), "From the admin")
	expectContains(t, "empty archive", s.get("/notes", nil), "No notes yet.")
	expectStatus(t, "members can't", s.get("/admin/notes", member), http.StatusNotFound)
	expectStatus(t, "members can't post", s.send("/admin/notes", form("action", "add", "body", "hi",
		"form_token", s.token(formModerate)), member), http.StatusNotFound)

	change := func(pairs ...string) *httptest.ResponseRecorder {
		return s.send("/admin/notes", form(append(pairs, "form_token", s.token(formModerate))...), boss)
	}
	expectContains(t, "empty", change("action", "add", "body", "  "), "Write something first.")
	expectRedirect(t, "first", change("action", "add", "body", "Threading is **live**."), "/admin/notes?done=added")
	expectRedirect(t, "second", change("action", "add", "body", "Search is coming."), "/admin/notes?done=added")

	// The newest is on the front page, rendered; both are at /notes.
	front := s.get("/", nil)
	expectContains(t, "front page", front, "From the admin,")
	expectContains(t, "front page", front, "Search is coming.")
	expectMissing(t, "only the newest", front, "Threading is")
	archive := s.get("/notes", nil)
	expectContains(t, "archive", archive, "Threading is <strong>live</strong>.")
	expectContains(t, "archive", archive, "Search is coming.")

	notes, err := testStore.Notes(ctx, true, 10)
	if err != nil || len(notes) != 2 {
		t.Fatalf("notes = %v, %v", notes, err)
	}
	newest := strconv.FormatInt(notes[0].ID, 10)

	// Hiding the newest brings the older one back to the front page.
	expectRedirect(t, "hide", change("action", "hide", "id", newest), "/admin/notes?done=hidden")
	front = s.get("/", nil)
	expectContains(t, "older note back", front, "Threading is")
	expectMissing(t, "hidden note", s.get("/notes", nil), "Search is coming.")
	expectContains(t, "admin still sees it", s.get("/admin/notes", boss), "Search is coming.")

	expectRedirect(t, "show", change("action", "show", "id", newest), "/admin/notes?done=shown")
	expectRedirect(t, "edit", change("action", "edit", "id", newest, "body", "Search is here."), "/admin/notes?done=edited")
	expectContains(t, "edited", s.get("/", nil), "Search is here.")

	expectContains(t, "delete needs confirming", change("action", "delete", "id", newest), "Tick the box")
	expectRedirect(t, "delete", change("action", "delete", "id", newest, "confirm", "1"), "/admin/notes?done=deleted")
	expectMissing(t, "deleted", s.get("/notes", nil), "Search is here.")
	expectContains(t, "missing note", change("action", "hide", "id", newest), "doesn't exist any more")
	expectContains(t, "footer link", s.get("/rules", nil), `href="/notes"`)
}
