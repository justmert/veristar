package tenantguardprocessor

import (
	"sync"
	"time"
)

// bucket is a token bucket. tokens refill continuously at rate per second up
// to burst.
type bucket struct {
	tokens float64
	last   time.Time
}

func (b *bucket) take(now time.Time, n, rate float64, burst int) bool {
	b.tokens += now.Sub(b.last).Seconds() * rate
	if b.tokens > float64(burst) {
		b.tokens = float64(burst)
	}
	b.last = now
	if b.tokens < n {
		return false
	}
	b.tokens -= n
	return true
}

type tenantBuckets struct {
	requests bucket
	items    bucket
	seen     time.Time
}

// limiter keeps the two buckets of every tenant. it is per collector
// process; three edge nodes therefore allow up to three times the limit,
// which docs/reference/collector.md states.
type limiter struct {
	cfg *Config
	now func() time.Time

	mu        sync.Mutex
	tenants   map[string]*tenantBuckets
	lastSweep time.Time
}

func newLimiter(cfg *Config, now func() time.Time) *limiter {
	return &limiter{cfg: cfg, now: now, tenants: map[string]*tenantBuckets{}, lastSweep: now()}
}

// allow takes one request token and n item tokens, or nothing. a request
// larger than the item burst can never pass, so it is refused at once.
func (l *limiter) allow(tenant string, n int) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) > l.cfg.IdleBucketTTL {
		for k, t := range l.tenants {
			if now.Sub(t.seen) > l.cfg.IdleBucketTTL {
				delete(l.tenants, k)
			}
		}
		l.lastSweep = now
	}
	t, ok := l.tenants[tenant]
	if !ok {
		t = &tenantBuckets{
			requests: bucket{tokens: float64(l.cfg.RequestsBurst), last: now},
			items:    bucket{tokens: float64(l.cfg.ItemsBurst), last: now},
		}
		l.tenants[tenant] = t
	}
	t.seen = now
	// check both before taking from either, so a refused request costs nothing
	probeReq, probeItems := t.requests, t.items
	if !probeReq.take(now, 1, l.cfg.RequestsPerSecond, l.cfg.RequestsBurst) ||
		!probeItems.take(now, float64(n), l.cfg.ItemsPerSecond, l.cfg.ItemsBurst) {
		// refill time still counts, so the refused attempt is not penalised
		// but the refill up to now is kept
		t.requests.take(now, 0, l.cfg.RequestsPerSecond, l.cfg.RequestsBurst)
		t.items.take(now, 0, l.cfg.ItemsPerSecond, l.cfg.ItemsBurst)
		return false
	}
	t.requests, t.items = probeReq, probeItems
	return true
}
