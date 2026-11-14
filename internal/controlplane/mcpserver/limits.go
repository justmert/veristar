package mcpserver

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// bucket is a token bucket. the caller holds the lock of whatever owns it.
type bucket struct {
	rate, burst float64
	tokens      float64
	last        time.Time
}

func newBucket(rate, burst float64, now time.Time) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst, last: now}
}

// take spends one token, or says how long until the next one.
func (b *bucket) take(now time.Time) (bool, time.Duration) {
	if el := now.Sub(b.last); el > 0 {
		b.tokens = min(b.burst, b.tokens+el.Seconds()*b.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
}

// limiter holds one bucket per credential. buckets are per process: two
// control plane instances behind the load balancer allow twice the rate,
// which the operator guide states.
type limiter struct {
	mu        sync.Mutex
	rate      float64
	burst     float64
	buckets   map[uuid.UUID]*bucket
	lastPrune time.Time
}

func newLimiter(rate, burst float64) *limiter {
	return &limiter{rate: rate, burst: burst, buckets: map[uuid.UUID]*bucket{}}
}

func (l *limiter) take(id uuid.UUID, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// at most once a minute, forget buckets that have refilled: a new
	// bucket starts full, so nothing changes and the map stays small
	if now.Sub(l.lastPrune) >= time.Minute {
		l.lastPrune = now
		full := time.Duration(l.burst / l.rate * float64(time.Second))
		for k, b := range l.buckets {
			if now.Sub(b.last) >= full {
				delete(l.buckets, k)
			}
		}
	}
	b, ok := l.buckets[id]
	if !ok {
		b = newBucket(l.rate, l.burst, now)
		l.buckets[id] = b
	}
	return b.take(now)
}
