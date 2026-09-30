package web

import (
	"context"
	"log"
	"strconv"
	"sync/atomic"
	"time"
)

// siteStats are numbers shown on every page (the footer's post count).
// Counting posts on every page load would be wasteful, so the count is
// refreshed once a minute in the background and read from memory.
// atomic.Int64 is safe to read and write from many requests at once.
type siteStats struct {
	posts atomic.Int64
}

func (s *Server) refreshStats() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := s.store.CountPosts(ctx)
	if err != nil {
		log.Printf("stats: %v", err)
		return
	}
	s.stats.posts.Store(n)
}

func (s *Server) statsLoop() {
	for range time.Tick(time.Minute) {
		s.refreshStats()
	}
}

// commas formats 12408 as "12,408", for templates: {{postCount | commas}}.
func commas(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := n < 0
	if neg {
		s = s[1:]
	}
	// Insert a comma every three digits, counting from the right.
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
