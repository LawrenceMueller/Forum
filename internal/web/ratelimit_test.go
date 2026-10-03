package web

import (
	"net/http/httptest"
	"testing"
	"time"
)

// Run with: go test ./...
// Go finds tests by name: files ending in _test.go, functions starting with Test.

func TestClientIP(t *testing.T) {
	// A "table-driven" test: a list of cases, each checked the same way.
	// Adding a case is one line, which makes edge cases cheap to cover.
	cases := []struct {
		name       string
		remoteAddr string
		forwarded  string // X-Forwarded-For header, if any
		trustProxy bool
		want       string
	}{
		{"plain IPv4", "203.0.113.7:5555", "", false, "203.0.113.7"},
		{"IPv6 grouped by /64", "[2001:db8:abcd:12:1:2:3:4]:443", "", false, "2001:db8:abcd:12::/64"},
		{"same /64, same key", "[2001:db8:abcd:12:ffff::9]:443", "", false, "2001:db8:abcd:12::/64"},
		{"IPv4 written as IPv6", "[::ffff:203.0.113.7]:80", "", false, "203.0.113.7"},
		{"header ignored when not trusted", "203.0.113.7:1", "198.51.100.1", false, "203.0.113.7"},
		{"trusted proxy: last entry wins", "127.0.0.1:1", "6.6.6.6, 203.0.113.50", true, "203.0.113.50"},
		{"trusted proxy, no header", "203.0.113.7:1", "", true, "203.0.113.7"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = c.remoteAddr
			if c.forwarded != "" {
				r.Header.Set("X-Forwarded-For", c.forwarded)
			}
			if got := clientIP(r, c.trustProxy); got != c.want {
				t.Errorf("clientIP() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestLimiterBurstThenBlock(t *testing.T) {
	l := newLimiter("test", time.Hour, 3)

	for i := range 3 {
		if ok, _ := l.allow("a"); !ok {
			t.Fatalf("request %d should fit in the burst", i+1)
		}
	}
	ok, wait := l.allow("a")
	if ok {
		t.Fatal("4th request should be refused")
	}
	if wait <= 0 {
		t.Errorf("refusal should say how long to wait, got %v", wait)
	}

	if ok, _ := l.allow("b"); !ok {
		t.Error("a different visitor should have their own bucket")
	}
}

func TestFriendlyDuration(t *testing.T) {
	cases := map[time.Duration]string{
		500 * time.Millisecond:  "1 second",
		40 * time.Second:        "40 seconds",
		90 * time.Second:        "2 minutes",
		time.Hour:               "1 hour",
		144 * time.Minute:       "2 hours 24 minutes",
		61 * time.Minute:        "1 hour 1 minute",
		5 * time.Hour:           "5 hours",
	}
	for d, want := range cases {
		if got := friendlyDuration(d); got != want {
			t.Errorf("friendlyDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestForgetAfterCoversARefill(t *testing.T) {
	// A slow limit must not be forgotten (and so reset to full) before its
	// bucket would have refilled anyway.
	slow := newLimiter("slow", 20*time.Minute, 3)
	if slow.forgetAfter != time.Hour {
		t.Errorf("3 tokens at 20 minutes each refill in 1h, but forgetAfter = %v", slow.forgetAfter)
	}
	daily := newLimiter("daily", 24*time.Hour/250, 250)
	if daily.forgetAfter < 24*time.Hour-time.Minute {
		t.Errorf("a daily budget must be remembered for a day, got %v", daily.forgetAfter)
	}
	fast := newLimiter("fast", time.Second, 5)
	if fast.forgetAfter != bucketIdleTimeout {
		t.Errorf("fast limits keep the minimum idle time, got %v", fast.forgetAfter)
	}
}

func TestLimiterStatus(t *testing.T) {
	l := newLimiter("test", time.Hour, 3)

	if left, _ := l.status("a"); left != 3 {
		t.Fatalf("a new visitor should see a full bucket, got %d", left)
	}
	l.allow("a")
	if left, _ := l.status("a"); left != 2 {
		t.Errorf("after one request, want 2 left, got %d", left)
	}
	l.allow("a")
	l.allow("a")
	left, wait := l.status("a")
	if left != 0 || wait <= 0 {
		t.Errorf("empty bucket: want 0 left and a wait, got %d and %v", left, wait)
	}
	if ok, _ := l.allow("a"); ok {
		t.Error("peeking shouldn't have put a token back")
	}
}

func TestLoginGuard(t *testing.T) {
	g := newLoginGuard()

	for range freeFailures {
		g.fail("blue-humming-otter")
	}
	if w := g.wait("blue-humming-otter"); w != 0 {
		t.Fatalf("the first %d failures should be free, got a wait of %v", freeFailures, w)
	}

	g.fail("blue-humming-otter")
	if w := g.wait("blue-humming-otter"); w <= 0 {
		t.Fatal("a failure past the free ones should block")
	}
	if w := g.wait("red-baking-hare"); w != 0 {
		t.Error("other accounts shouldn't be affected")
	}

	g.succeed("blue-humming-otter")
	if w := g.wait("blue-humming-otter"); w != 0 {
		t.Error("a successful login should clear the block")
	}
}

func TestBlockForDoublesAndCaps(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	for i, w := range want {
		if got := blockFor(i + 1); got != w {
			t.Errorf("blockFor(%d) = %v, want %v", i+1, got, w)
		}
	}
	if got := blockFor(500); got != maxBlock {
		t.Errorf("huge failure counts should stay capped, got %v", got)
	}
}
