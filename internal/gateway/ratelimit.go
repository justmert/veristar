package gateway

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// Rate is a token bucket setting: tokens refill at PerSecond up to Burst,
// and one request takes one token.
type Rate struct {
	PerSecond float64
	Burst     float64
}

// starting values from this stage of the plan, recorded in
// docs/reference/gateway-limits.md. change them only with a recorded reason.
var (
	DefaultSiteRate   = Rate{PerSecond: 200, Burst: 400}
	DefaultPrefixRate = Rate{PerSecond: 20, Burst: 60}
)

// DefaultRateLimitKeys bounds the number of buckets held per kind, so a
// flood of distinct prefixes cannot grow memory without limit.
const DefaultRateLimitKeys = 200_000

func (r Rate) validate() error {
	if !(r.PerSecond > 0) || math.IsInf(r.PerSecond, 0) {
		return fmt.Errorf("gateway: rate %v per second must be positive and finite", r.PerSecond)
	}
	if !(r.Burst >= 1) || math.IsInf(r.Burst, 0) {
		return fmt.Errorf("gateway: burst %v must be at least 1 and finite", r.Burst)
	}
	return nil
}

// RateOutcome is the result of RateLimiter.Allow.
type RateOutcome uint8

// outcomes. both refusals count as ReasonRateLimited in the stats; String
// tells them apart for logs and metrics.
const (
	RateAllowed RateOutcome = iota
	RateLimitedSite
	RateLimitedPrefix
)

func (o RateOutcome) String() string {
	switch o {
	case RateAllowed:
		return "allowed"
	case RateLimitedSite:
		return "site"
	case RateLimitedPrefix:
		return "prefix"
	}
	return "unknown"
}

// PrefixKey identifies a truncated client address. it must be the keyed
// hash of the truncated address, never the
// address itself, so the limiter's memory holds no ip.
type PrefixKey [32]byte

type rlPrefixID struct {
	site   string
	prefix PrefixKey
}

// rlBucket is one token bucket and its place in the table's lru list.
type rlBucket[K comparable] struct {
	key        K
	tokens     float64
	last       time.Time
	prev, next *rlBucket[K]
}

// rlTable is a set of buckets bounded by an lru: when full, the bucket used
// least recently is evicted. eviction hands that key a fresh burst on its
// next request, which a key spraying attacker cannot turn into more than the
// site bucket allows, because every request also takes a site token. the
// list is intrusive so that no interface conversion sits on the hot path.
type rlTable[K comparable] struct {
	rate    Rate
	max     int
	buckets map[K]*rlBucket[K]
	head    rlBucket[K] // sentinel: head.next is most recent, head.prev least
}

func newRLTable[K comparable](r Rate, max int) *rlTable[K] {
	t := &rlTable[K]{rate: r, max: max, buckets: make(map[K]*rlBucket[K])}
	t.head.next, t.head.prev = &t.head, &t.head
	return t
}

func (t *rlTable[K]) unlink(b *rlBucket[K]) {
	b.prev.next, b.next.prev = b.next, b.prev
}

func (t *rlTable[K]) pushFront(b *rlBucket[K]) {
	b.prev, b.next = &t.head, t.head.next
	t.head.next.prev = b
	t.head.next = b
}

// get returns the refilled bucket for k, creating it full.
func (t *rlTable[K]) get(k K, now time.Time) *rlBucket[K] {
	b, ok := t.buckets[k]
	if !ok {
		if len(t.buckets) >= t.max {
			oldest := t.head.prev
			t.unlink(oldest)
			delete(t.buckets, oldest.key)
		}
		b = &rlBucket[K]{key: k, tokens: t.rate.Burst, last: now}
		t.pushFront(b)
		t.buckets[k] = b
		return b
	}
	t.unlink(b)
	t.pushFront(b)
	// a clock that steps back refills nothing instead of draining tokens.
	if el := now.Sub(b.last); el > 0 {
		b.tokens = math.Min(t.rate.Burst, b.tokens+el.Seconds()*t.rate.PerSecond)
		b.last = now
	}
	return b
}

// RateLimiter holds token buckets per site and per site and truncated
// address prefix, in memory only. each gateway node limits on its own, so
// the effective cluster limit is the node count times the configured rate.
// the prefix bucket is keyed by site too, so a busy office or carrier nat
// visiting one site does not use up its budget on another.
type RateLimiter struct {
	now    func() time.Time
	mu     sync.Mutex
	site   *rlTable[string]
	prefix *rlTable[rlPrefixID]
}

// NewRateLimiter builds a limiter. now is the clock (time.Now in production,
// a controlled clock in tests). maxKeys bounds each bucket table.
func NewRateLimiter(site, prefix Rate, maxKeys int, now func() time.Time) (*RateLimiter, error) {
	if err := site.validate(); err != nil {
		return nil, fmt.Errorf("site rate: %w", err)
	}
	if err := prefix.validate(); err != nil {
		return nil, fmt.Errorf("prefix rate: %w", err)
	}
	if maxKeys < 1 {
		return nil, errors.New("gateway: rate limiter needs room for at least one key")
	}
	if now == nil {
		return nil, errors.New("gateway: rate limiter needs a clock")
	}
	return &RateLimiter{
		now:    now,
		site:   newRLTable[string](site, maxKeys),
		prefix: newRLTable[rlPrefixID](prefix, maxKeys),
	}, nil
}

// Allow takes one token from the site bucket and one from the prefix
// bucket, or none when either is empty. a request refused by one bucket
// does not drain the other, so a flood from one prefix does not also eat
// the site budget beyond what was admitted.
func (l *RateLimiter) Allow(siteID string, prefix PrefixKey) RateOutcome {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.site.get(siteID, now)
	if s.tokens < 1 {
		return RateLimitedSite
	}
	p := l.prefix.get(rlPrefixID{site: siteID, prefix: prefix}, now)
	if p.tokens < 1 {
		return RateLimitedPrefix
	}
	s.tokens--
	p.tokens--
	return RateAllowed
}

// Len reports how many site and prefix buckets are held, for metrics.
func (l *RateLimiter) Len() (sites, prefixes int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.site.buckets), len(l.prefix.buckets)
}
