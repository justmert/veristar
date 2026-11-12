package gateway

import (
	"sync"
	"testing"
	"time"
)

// rlClock is a controlled clock: tests move it forward explicitly.
type rlClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *rlClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *rlClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestLimiter(t *testing.T, site, prefix Rate, maxKeys int) (*RateLimiter, *rlClock) {
	t.Helper()
	c := &rlClock{t: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)}
	l, err := NewRateLimiter(site, prefix, maxKeys, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return l, c
}

func countAllowed(l *RateLimiter, site string, p PrefixKey, n int) (allowed int, last RateOutcome) {
	for range n {
		last = l.Allow(site, p)
		if last == RateAllowed {
			allowed++
		}
	}
	return allowed, last
}

func TestRateLimitPrefixBurstAndRefill(t *testing.T) {
	l, c := newTestLimiter(t, DefaultSiteRate, DefaultPrefixRate, DefaultRateLimitKeys)
	p := PrefixKey{1}
	if n, last := countAllowed(l, "s", p, 100); n != 60 || last != RateLimitedPrefix {
		t.Fatalf("burst: allowed %d, last %v", n, last)
	}
	c.advance(500 * time.Millisecond) // 20 per second gives 10 tokens
	if n, _ := countAllowed(l, "s", p, 100); n != 10 {
		t.Fatalf("after 0.5s: allowed %d", n)
	}
	c.advance(time.Hour) // refill stops at the burst
	if n, _ := countAllowed(l, "s", p, 100); n != 60 {
		t.Fatalf("after an hour: allowed %d", n)
	}
	// another prefix, and the same prefix on another site, have their own
	// buckets.
	if n, _ := countAllowed(l, "s", PrefixKey{2}, 100); n != 60 {
		t.Fatalf("other prefix: allowed %d", n)
	}
	if n, _ := countAllowed(l, "t", p, 100); n != 60 {
		t.Fatalf("other site: allowed %d", n)
	}
}

func TestRateLimitSite(t *testing.T) {
	l, c := newTestLimiter(t, DefaultSiteRate, DefaultPrefixRate, DefaultRateLimitKeys)
	allowed := 0
	var last RateOutcome
	for i := range 1000 {
		last = l.Allow("s", PrefixKey{byte(i), byte(i >> 8)})
		if last == RateAllowed {
			allowed++
		}
	}
	if allowed != 400 || last != RateLimitedSite {
		t.Fatalf("site burst: allowed %d, last %v", allowed, last)
	}
	// a request refused by the site bucket never reaches a prefix bucket.
	if sites, prefixes := l.Len(); sites != 1 || prefixes != 400 {
		t.Fatalf("buckets: %d sites, %d prefixes", sites, prefixes)
	}
	c.advance(time.Second) // 200 site tokens
	if n, _ := countAllowed(l, "s", PrefixKey{0xff, 0xff}, 300); n != 60 {
		t.Fatalf("after refill: allowed %d", n)
	}
}

func TestRateLimitPrefixRefusalKeepsSiteTokens(t *testing.T) {
	l, _ := newTestLimiter(t, Rate{PerSecond: 1, Burst: 10}, Rate{PerSecond: 1, Burst: 2}, 10)
	countAllowed(l, "s", PrefixKey{1}, 50) // 2 allowed, 48 refused by prefix
	if n, _ := countAllowed(l, "s", PrefixKey{2}, 50); n != 2 {
		t.Fatalf("allowed %d", n)
	}
	if n, last := countAllowed(l, "s", PrefixKey{3}, 50); n != 2 || last != RateLimitedPrefix {
		t.Fatalf("allowed %d last %v", n, last)
	}
	// 6 site tokens spent, 4 left
	if n, last := countAllowed(l, "s", PrefixKey{4}, 1); n != 1 || last != RateAllowed {
		t.Fatalf("allowed %d last %v", n, last)
	}
}

func TestRateLimitClockBackwards(t *testing.T) {
	l, c := newTestLimiter(t, DefaultSiteRate, DefaultPrefixRate, DefaultRateLimitKeys)
	countAllowed(l, "s", PrefixKey{1}, 60)
	c.advance(-time.Hour)
	if n, _ := countAllowed(l, "s", PrefixKey{1}, 10); n != 0 {
		t.Fatalf("clock stepped back granted %d", n)
	}
	c.advance(time.Hour + time.Second)
	if n, _ := countAllowed(l, "s", PrefixKey{1}, 100); n != 20 {
		t.Fatalf("after the clock caught up: allowed %d", n)
	}
}

func TestRateLimitBoundedKeys(t *testing.T) {
	l, _ := newTestLimiter(t, Rate{PerSecond: 1e6, Burst: 1e6}, Rate{PerSecond: 1, Burst: 1}, 3)
	for i := range 10 {
		l.Allow("s", PrefixKey{byte(i)})
	}
	if s, p := l.Len(); s != 1 || p != 3 {
		t.Fatalf("buckets: %d sites, %d prefixes", s, p)
	}
	// prefixes 7, 8, 9 are held and empty; 0 was evicted and starts full.
	if l.Allow("s", PrefixKey{9}) != RateLimitedPrefix || l.Allow("s", PrefixKey{0}) != RateAllowed {
		t.Fatal("lru eviction did not keep the most recent keys")
	}
}

func TestRateLimitConfig(t *testing.T) {
	c := &rlClock{}
	bad := []Rate{{0, 1}, {-1, 1}, {1, 0.5}}
	for _, r := range bad {
		if _, err := NewRateLimiter(r, DefaultPrefixRate, 1, c.now); err == nil {
			t.Errorf("site rate %+v accepted", r)
		}
		if _, err := NewRateLimiter(DefaultSiteRate, r, 1, c.now); err == nil {
			t.Errorf("prefix rate %+v accepted", r)
		}
	}
	if _, err := NewRateLimiter(DefaultSiteRate, DefaultPrefixRate, 0, c.now); err == nil {
		t.Error("zero keys accepted")
	}
	if _, err := NewRateLimiter(DefaultSiteRate, DefaultPrefixRate, 1, nil); err == nil {
		t.Error("nil clock accepted")
	}
}

func TestRateLimitConcurrent(t *testing.T) {
	l, _ := newTestLimiter(t, DefaultSiteRate, DefaultPrefixRate, DefaultRateLimitKeys)
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for g := range 6 { // 6 x 60 fits in the site burst of 400
		wg.Go(func() {
			n, _ := countAllowed(l, "s", PrefixKey{byte(g)}, 100)
			mu.Lock()
			total += n
			mu.Unlock()
		})
	}
	wg.Wait()
	if total != 6*60 {
		t.Fatalf("allowed %d, want %d", total, 6*60)
	}
}
