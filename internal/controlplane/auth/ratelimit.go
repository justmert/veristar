package auth

import (
	"sync"
	"time"
)

// sign-in start limit (contract 4.1.1): one token bucket per control plane
// instance, shared by every client. core caddy has no rate limiter, so the
// control plane holds the limit. it is global rather than per client
// because the control plane never sees client addresses in a usable form.
const (
	StartRatePerSecond = 10
	StartBurst         = 30
)

// bucket is a token bucket: up to burst tokens, refilled at rate per second.
type bucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newBucket(rate, burst int, now time.Time) *bucket {
	return &bucket{rate: float64(rate), burst: float64(burst), tokens: float64(burst), last: now}
}

// take spends one token. when none is left it returns false and how long
// until the next token arrives.
func (b *bucket) take(now time.Time) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if el := now.Sub(b.last); el > 0 {
		b.tokens = min(b.burst, b.tokens+el.Seconds()*b.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
	return false, wait
}
