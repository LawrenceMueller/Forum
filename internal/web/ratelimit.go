package web

import (
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Rate limiting works on token buckets: each visitor (keyed by IP) gets a
// bucket of tokens, every request spends one, and tokens refill at a steady
// rate. A full bucket allows a short burst; sustained hammering drains it.
//
// Buckets live only in this process's memory. They're never written to disk,
// the database, or the logs, and idle ones are forgotten after a few minutes.

const (
	// Past this many tracked visitors, new ones share one "overflow" bucket,
	// so someone spraying requests from endless addresses can't make the
	// map (and the server's memory) grow without limit.
	maxTrackedKeys = 50_000
	overflowKey    = "overflow"

	// Idle buckets are forgotten to save memory, but never sooner than this.
	bucketIdleTimeout = 15 * time.Minute
)

type limiter struct {
	name  string
	every time.Duration // one token refills every this long
	burst int           // bucket size

	// forgetAfter is how long a bucket must sit unused before it's dropped.
	// A forgotten visitor comes back with a FULL bucket, so this must be at
	// least the time an empty bucket takes to refill completely. Otherwise
	// slow limits (like 3 accounts, then one per 20 minutes) could be reset
	// just by waiting a while.
	forgetAfter time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket
	warned  bool
}

type bucket struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

func newLimiter(name string, every time.Duration, burst int) *limiter {
	l := &limiter{
		name: name, every: every, burst: burst,
		forgetAfter: max(bucketIdleTimeout, every*time.Duration(burst)),
		buckets:     make(map[string]*bucket),
	}
	go l.cleanupLoop()
	return l
}

// allow spends a token for key. If the bucket is empty it returns false and
// how long until a token is available.
func (l *limiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok && len(l.buckets) >= maxTrackedKeys {
		if !l.warned {
			log.Printf("rate limit %q: tracking %d visitors, new ones now share one bucket", l.name, maxTrackedKeys)
			l.warned = true
		}
		key = overflowKey
		b, ok = l.buckets[key]
	}
	if !ok {
		b = &bucket{lim: rate.NewLimiter(rate.Every(l.every), l.burst)}
		l.buckets[key] = b
	}
	b.lastSeen = time.Now()

	// Reserve a token. If it isn't available yet, hand it back and say how
	// long the wait would be.
	res := b.lim.Reserve()
	if wait := res.Delay(); wait > 0 {
		res.Cancel()
		return false, wait
	}
	return true, 0
}

// status peeks at key's bucket without spending anything: how many whole
// tokens are left, and if none, how long until the next one.
func (l *limiter) status(key string) (left int, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		return l.burst, 0 // never seen: a full bucket
	}
	if left = int(b.lim.Tokens()); left >= 1 {
		return left, 0
	}
	// Ask when a token would be ready, then hand the reservation back.
	res := b.lim.Reserve()
	wait = res.Delay()
	res.Cancel()
	return 0, wait
}

func (l *limiter) cleanupLoop() {
	for range time.Tick(time.Minute) {
		l.mu.Lock()
		for key, b := range l.buckets {
			if time.Since(b.lastSeen) > l.forgetAfter {
				delete(l.buckets, key)
			}
		}
		if len(l.buckets) < maxTrackedKeys/2 {
			l.warned = false
		}
		l.mu.Unlock()
	}
}

// clientIP works out who's asking, for rate limiting only.
//
// Normally that's the address of the connection. Behind a reverse proxy
// like Caddy, every connection comes from the proxy, so with trustProxy set
// we read the X-Forwarded-For header instead. We take only the LAST entry:
// that's the one the proxy appended itself. Anything before it came from the
// client and can be made up.
//
// IPv6 addresses are grouped by their /64 prefix, since one household or
// server usually holds a whole /64 (billions of addresses) and could
// otherwise dodge limits just by switching.
func clientIP(r *http.Request, trustProxy bool) string {
	raw := r.RemoteAddr
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}

	if trustProxy {
		if headers := r.Header.Values("X-Forwarded-For"); len(headers) > 0 {
			entries := strings.Split(headers[len(headers)-1], ",")
			raw = strings.TrimSpace(entries[len(entries)-1])
		}
	}

	ip, err := netip.ParseAddr(raw)
	if err != nil {
		return raw
	}
	ip = ip.Unmap() // "::ffff:1.2.3.4" is really the IPv4 address 1.2.3.4
	if ip.Is6() {
		if prefix, err := ip.WithZone("").Prefix(64); err == nil {
			return prefix.String()
		}
	}
	return ip.String()
}

// limit wraps a handler so each visitor is held to l's rate.
func (s *Server) limit(l *limiter, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := l.allow(clientIP(r, s.trustProxy)); !ok {
			tooManyRequests(w, wait)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// tooManyRequests is the standard "slow down" response. Retry-After tells
// well-behaved clients (and scripts) how long to wait.
func tooManyRequests(w http.ResponseWriter, wait time.Duration) {
	secs := int(math.Ceil(wait.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	http.Error(w, "Slow down a little. Try again in "+friendlyDuration(wait)+".",
		http.StatusTooManyRequests)
}

// friendlyDuration turns a wait into words: "40 seconds", "3 minutes".
func friendlyDuration(d time.Duration) string {
	if d < time.Minute {
		n := int(math.Ceil(d.Seconds()))
		if n <= 1 {
			return "1 second"
		}
		return fmt.Sprintf("%d seconds", n)
	}
	mins := int(math.Ceil(d.Minutes()))
	if mins < 60 {
		return plural(mins, "minute")
	}
	hours, mins := mins/60, mins%60
	if mins == 0 {
		return plural(hours, "hour")
	}
	return plural(hours, "hour") + " " + plural(mins, "minute")
}

// plural writes "1 minute", "2 minutes", and so on.
func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
