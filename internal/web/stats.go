package web

import (
	"context"
	"log"
	"strconv"
	"sync/atomic"
	"time"
)

// siteStats are numbers shown on every page: the footer's post count and
// the post count under each tag. Counting on every page load would be
// wasteful (the tag counts read every tag link), so they're refreshed once
// a minute in the background and read from memory. The atomic types are
// safe to read and write from many requests at once.
type siteStats struct {
	posts     atomic.Int64
	tagCounts atomic.Pointer[map[int]int64] // tag ID -> visible posts at or below it
}

func (s *Server) refreshStats() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n, err := s.store.CountPosts(ctx)
	if err != nil {
		log.Printf("stats: %v", err)
		return
	}
	s.stats.posts.Store(n)

	counts, err := s.store.TagPostCounts(ctx)
	if err != nil {
		log.Printf("stats: tag counts: %v", err)
		return
	}
	s.stats.tagCounts.Store(&counts)
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
