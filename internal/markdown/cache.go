package markdown

// Cache remembers rendered Markdown, because rendering is the costliest
// part of showing a busy thread: on the large seed's biggest thread (2,400
// comments) it was about 40% of the page's time, and the same comments are
// rendered again on every view. A comment's HTML depends only on its text,
// so the text itself is the key: an edit changes the text, so it simply
// misses the cache, and there is nothing to invalidate.
//
// The size is bounded with two generations: new entries go into the
// current map; when it's full, it becomes the old one and the previous old
// one is dropped. Anything read from the old map moves back into the
// current one, so busy threads stay cached while forgotten ones fall out.
// Rendering happens outside the lock, so many requests can render at once.

import (
	"html/template"
	"sync"
)

// Cache is a bounded memo of Render. The zero value isn't usable; use NewCache.
type Cache struct {
	mu       sync.Mutex
	cur, old map[string]template.HTML
	max      int // entries per generation
}

// NewCache returns a cache holding up to about 2*perGeneration rendered texts.
func NewCache(perGeneration int) *Cache {
	return &Cache{cur: make(map[string]template.HTML), max: max(perGeneration, 1)}
}

// Render is markdown.Render, remembered.
func (c *Cache) Render(source string) template.HTML {
	c.mu.Lock()
	out, ok := c.cur[source]
	if !ok {
		if out, ok = c.old[source]; ok {
			c.put(source, out) // still in use: keep it
		}
	}
	c.mu.Unlock()
	if ok {
		return out
	}

	out = Render(source)
	c.mu.Lock()
	c.put(source, out)
	c.mu.Unlock()
	return out
}

// put stores one entry, starting a new generation when the current is full.
// The caller holds the lock.
func (c *Cache) put(source string, out template.HTML) {
	if len(c.cur) >= c.max {
		c.old, c.cur = c.cur, make(map[string]template.HTML, c.max)
	}
	c.cur[source] = out
}

// Len is how many rendered texts the cache holds (for tests).
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.cur) + len(c.old)
}
