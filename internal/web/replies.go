package web

// The reply inbox (/account/replies): replies to your comments and
// top-level comments on your posts, newest first, with the new ones marked.
// The header shows how many arrived since you last looked; opening the
// inbox resets that. See store/replies.go for what counts as a reply, and
// why nothing about replies is stored apart from the comments themselves.

import (
	"net/http"
	"time"

	"snowball/internal/store"
)

const repliesPageSize = 30

type replyRow struct {
	store.Reply
	Snippet string
	Link    string // the reply in its thread
	New     bool   // arrived since the inbox was last opened
	OnPost  bool   // a top-level comment on the viewer's post (else a reply to their comment)
	Color   string
}

type repliesPage struct {
	basePage
	Replies  []replyRow
	NextURL  string
	FirstURL string
}

// GET /account/replies (?after=<cursor> for older)
func (s *Server) handleReplies(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	start := time.Now()
	var after *store.Cursor
	if raw := r.URL.Query().Get("after"); raw != "" {
		c, err := store.ParseCursor(raw)
		if err != nil {
			http.Redirect(w, r, "/account/replies", http.StatusSeeOther)
			return
		}
		after = &c
	}
	replies, next, err := s.store.Replies(r.Context(), u.ID, after, repliesPageSize)
	if err != nil {
		serverError(w, err)
		return
	}

	data := repliesPage{basePage: newBasePage(r, "replies to you").withUp("/account", "your account")}
	data.UnreadReplies = 0 // this page is where they're read
	for _, rep := range replies {
		row := replyRow{
			Reply:   rep,
			Snippet: snippet(rep.Body, 200),
			New:     rep.CreatedAt.After(u.RepliesSeenAt),
			OnPost:  rep.ParentID == "",
			Link:    "/p/" + rep.PostID + "#c-" + rep.PublicID,
		}
		if rep.Depth >= maxThreadDepth {
			// Too deep for the post's page: it lives on a thread page of its own.
			row.Link = "/p/" + rep.PostID + "/c/" + rep.PublicID
		}
		if !rep.AuthorDeleted {
			row.Color = colorClass(rep.Author)
		}
		data.Replies = append(data.Replies, row)
	}
	if after != nil {
		data.FirstURL = "/account/replies"
	}
	if next != nil {
		data.NextURL = "/account/replies?after=" + next.String()
	}

	// Opening the inbox (its first page) marks everything as seen. The page
	// above was worked out first, so what was new still shows as new.
	if after == nil && (u.UnreadReplies > 0 || len(replies) > 0) {
		if err := s.store.MarkRepliesSeen(r.Context(), u.ID); err != nil {
			serverError(w, err)
			return
		}
	}
	data.Generated = time.Since(start)
	s.render(w, "replies.html", data)
}
