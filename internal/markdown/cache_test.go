package markdown

import (
	"fmt"
	"sync"
	"testing"
)

func TestCacheGivesWhatRenderGives(t *testing.T) {
	c := NewCache(100)
	for _, src := range []string{"**hi**", "[x](javascript:alert(1))", "<script>no</script>", "", "**hi**"} {
		if got, want := c.Render(src), Render(src); got != want {
			t.Errorf("Render(%q) from the cache = %q, want %q", src, got, want)
		}
	}
	if c.Len() != 4 {
		t.Errorf("holds %d texts, want 4 (one repeated)", c.Len())
	}
}

func TestCacheStaysBounded(t *testing.T) {
	c := NewCache(10)
	c.Render("keep me")
	for i := range 100 {
		c.Render(fmt.Sprintf("text %d", i))
		c.Render("keep me") // in use all along, so it should survive
	}
	if n := c.Len(); n > 20 {
		t.Errorf("holds %d texts, more than two generations of 10", n)
	}
	c.mu.Lock()
	_, inCur := c.cur["keep me"]
	_, inOld := c.old["keep me"]
	c.mu.Unlock()
	if !inCur && !inOld {
		t.Error("a text in constant use fell out of the cache")
	}
}

func TestCacheIsSafeToShare(t *testing.T) {
	c := NewCache(50)
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for i := range 500 {
				src := fmt.Sprintf("*%d*", (i*7+w)%120)
				if c.Render(src) != Render(src) {
					t.Errorf("wrong output for %q", src)
					return
				}
			}
		})
	}
	wg.Wait()
}
