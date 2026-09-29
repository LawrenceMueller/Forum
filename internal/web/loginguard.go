package web

import (
	"sync"
	"time"
)

// loginGuard slows down password guessing against a single account, from
// any number of IPs. The first few failures are free (everyone mistypes),
// then each further failure blocks that account's logins for a growing
// delay: 30s, 1m, 2m, 4m, then 5m at most.
//
// The cap is deliberately low. A long block would let anyone lock a real
// user out of their own account just by failing on purpose. Per-IP limits
// do the heavy lifting; this stops a guesser spread across many IPs.
//
// Only accounts that exist are tracked, so junk usernames can't fill memory.
// Like rate limits, it's memory only and forgotten on restart.

const (
	freeFailures  = 3
	firstBlock    = 30 * time.Second
	maxBlock      = 5 * time.Minute
	forgetAfter   = time.Hour // failures older than this are wiped
)

type loginGuard struct {
	mu       sync.Mutex
	accounts map[string]*failures
}

type failures struct {
	count        int
	blockedUntil time.Time
	last         time.Time
}

func newLoginGuard() *loginGuard {
	g := &loginGuard{accounts: make(map[string]*failures)}
	go g.cleanupLoop()
	return g
}

// wait reports how long until this account can try logging in again.
// Zero means go ahead.
func (g *loginGuard) wait(username string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	f, ok := g.accounts[username]
	if !ok {
		return 0
	}
	return max(time.Until(f.blockedUntil), 0)
}

// fail records a wrong password for an existing account.
func (g *loginGuard) fail(username string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	f, ok := g.accounts[username]
	if !ok {
		f = &failures{}
		g.accounts[username] = f
	}
	f.count++
	f.last = time.Now()

	if f.count > freeFailures {
		f.blockedUntil = f.last.Add(blockFor(f.count - freeFailures))
	}
}

// blockFor is the delay after the nth failure beyond the free ones:
// doubling from firstBlock, never above maxBlock.
func blockFor(n int) time.Duration {
	if n > 10 { // doubling this many times would pass the cap anyway
		return maxBlock
	}
	return min(firstBlock<<(n-1), maxBlock)
}

// succeed clears the record after a correct password.
func (g *loginGuard) succeed(username string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.accounts, username)
}

func (g *loginGuard) cleanupLoop() {
	for range time.Tick(time.Minute) {
		g.mu.Lock()
		for name, f := range g.accounts {
			if time.Since(f.last) > forgetAfter && time.Now().After(f.blockedUntil) {
				delete(g.accounts, name)
			}
		}
		g.mu.Unlock()
	}
}
