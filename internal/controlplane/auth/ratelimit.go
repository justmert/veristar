package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// sign-in limits (contract 4.1.1 and 4.1.2 step 4 and
// N-2). each client has its own bucket, so one client running out cannot
// lock anyone else out; a global bucket per control plane instance stays as
// a ceiling, sized well above what clients within their own limits produce
// together, so it only bounds the load a large distributed flood puts on
// github. a request takes from its client's bucket first and only then
// from the global one, so a client already over its limit spends nothing
// of the shared budget.
//
// when the global bucket is empty, a client whose own bucket was full (one
// that had been idle, which is what a person starting a sign-in looks like)
// may still take from a small reserve. a flood keeps its own buckets below
// full, so it cannot reach the reserve with the keys it is using; it would
// need a fresh key per reserve token, and a key is a whole ipv4 address or
// a whole ipv6 /48. the per-client burst stays 20: keying by /48 and a
// shared ipv4 address (an office, a campus nat) puts several people behind
// one key, and their sign-ins arrive close together.
const (
	StartClientRatePerSecond  = 0.5
	StartClientBurst          = 20
	StartRatePerSecond        = 50
	StartBurst                = 200
	StartReserveRatePerSecond = 5
	StartReserveBurst         = 20

	ExchangeClientRatePerSecond  = 0.5
	ExchangeClientBurst          = 20
	ExchangeRatePerSecond        = 10
	ExchangeBurst                = 30
	ExchangeReserveRatePerSecond = 2
	ExchangeReserveBurst         = 10
)

// IPv6ClientPrefix is the ipv6 prefix length that keys one client (audit
// N-2). /48 is the largest assignment one end site normally gets (ripe-690
// recommends /48 or /56 per site, hosting providers route a /48 to one
// customer, and tunnel brokers hand a free /48 to anyone who signs up).
// keyed by /56 or /64, one such holder would get 256 or 65,536 buckets of
// its own and drain the global ceiling alone; keyed by /48 it gets one.
// the cost is that unrelated clients inside one /48 (a campus, or a mobile
// carrier's pool) share a bucket, which for sign-in, one start per person
// and visit, is at most a 2 second wait.
const IPv6ClientPrefix = 48

// ClientAddrHeader carries the client address from core caddy to the
// control plane (deploy/caddy/app.Caddyfile sets it from the connection,
// replacing any value the client sent). the control plane reads it only to
// derive the keyed client hash below; the address itself is never logged,
// stored or kept beyond the request.
const ClientAddrHeader = "X-Sa-Client-Addr"

// maxClients bounds the per-client buckets one limiter keeps. a bucket
// that has refilled is forgotten, so the map holds only clients seen in
// the last burst/rate seconds; a flood from more addresses than this is
// refused until buckets refill (rate_limited), never served unlimited.
const maxClients = 100_000

// bucket is a token bucket: up to burst tokens, refilled at rate per second.
type bucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newBucket(rate, burst float64, now time.Time) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst, last: now}
}

// take spends one token. when none is left it returns false and how long
// until the next token arrives.
func (b *bucket) take(now time.Time) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.takeLocked(now)
}

func (b *bucket) takeLocked(now time.Time) (bool, time.Duration) {
	b.refillLocked(now)
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
	return false, wait
}

func (b *bucket) refillLocked(now time.Time) {
	if el := now.Sub(b.last); el > 0 {
		b.tokens = min(b.burst, b.tokens+el.Seconds()*b.rate)
		b.last = now
	}
}

// fullLocked reports whether the bucket holds its whole burst at now, which
// a client's bucket does only after it was idle for burst/rate seconds.
func (b *bucket) fullLocked(now time.Time) bool {
	b.refillLocked(now)
	return b.tokens >= b.burst
}

// clientKey is the keyed hash of a client address prefix. the key is 32
// random bytes made at process start and held only in memory, so a key
// cannot be turned back into an address, and nothing outlives the process.
type clientKey [16]byte

type clientHasher struct{ key []byte }

func newClientHasher() (*clientHasher, error) {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return &clientHasher{key: k}, nil
}

// of returns the client of a request: the address caddy passed in
// ClientAddrHeader, or the connection's peer when the request did not come
// through caddy (the dev setup, where the browser connects directly). ipv4
// is used whole; ipv6 is cut to its IPv6ClientPrefix, because one holder of
// a prefix could otherwise take a new bucket per address or subnet. a value
// that is not an address is hashed as given, so it still gets a bucket of
// its own and never shares one with real clients.
func (c *clientHasher) of(r *http.Request) clientKey {
	raw := r.Header.Get(ClientAddrHeader)
	if raw == "" {
		raw = r.RemoteAddr
		if host, _, err := net.SplitHostPort(raw); err == nil {
			raw = host
		}
	}
	material := "raw:" + raw
	if a, err := netip.ParseAddr(raw); err == nil {
		a = a.Unmap()
		if a.Is6() {
			p, _ := a.Prefix(IPv6ClientPrefix)
			material = "v6:" + p.String()
		} else {
			material = "v4:" + a.String()
		}
	}
	m := hmac.New(sha256.New, c.key)
	m.Write([]byte(material))
	var k clientKey
	copy(k[:], m.Sum(nil))
	return k
}

// limiter is a per-client bucket set under a global ceiling, with a reserve
// beside the ceiling for clients that had been idle.
type limiter struct {
	mu          sync.Mutex
	rate, burst float64
	clients     map[clientKey]*bucket
	global      *bucket
	reserve     *bucket
}

func newLimiter(clientRate, clientBurst, globalRate, globalBurst, reserveRate, reserveBurst float64, now time.Time) *limiter {
	return &limiter{
		rate: clientRate, burst: clientBurst,
		clients: map[clientKey]*bucket{},
		global:  newBucket(globalRate, globalBurst, now),
		reserve: newBucket(reserveRate, reserveBurst, now),
	}
}

// take spends one token of the client's bucket and then one of the global
// bucket, or of the reserve when the global one is empty and the client's
// bucket was full before this request. it returns false and the wait when
// the request is refused.
func (l *limiter) take(k clientKey, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	b, ok := l.clients[k]
	if !ok {
		if len(l.clients) >= maxClients {
			l.forgetRefilled(now)
		}
		if len(l.clients) >= maxClients {
			l.mu.Unlock()
			return false, time.Duration(float64(time.Second) / l.rate)
		}
		b = newBucket(l.rate, l.burst, now)
		l.clients[k] = b
	}
	idle := b.fullLocked(now)
	ok, wait := b.takeLocked(now)
	l.mu.Unlock()
	if !ok {
		return false, wait
	}
	ok, wait = l.global.take(now)
	if ok || !idle {
		return ok, wait
	}
	if okReserve, _ := l.reserve.take(now); okReserve {
		return true, 0
	}
	return false, wait
}

// forgetRefilled drops the buckets that are full again: a new bucket
// starts full, so forgetting one changes nothing for its client.
func (l *limiter) forgetRefilled(now time.Time) {
	full := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.clients {
		if now.Sub(b.last) >= full {
			delete(l.clients, k)
		}
	}
}

// usedStates remembers the state values whose callback already ran, until
// their cookie would have expired. a sealed state cookie is single use in
// the browser, but a copy of it could be replayed with made-up codes; each
// state now reaches the code exchange once per control plane instance.
type usedStates struct {
	mu   sync.Mutex
	seen map[[32]byte]time.Time
}

// maxUsedStates bounds the set: the global start bucket lets at most
// StartRatePerSecond * stateCookieTTL states be issued per instance in
// one cookie lifetime, which this exceeds.
const maxUsedStates = 200_000

// use records state and reports whether it was new. when the set is full
// even after forgetting expired entries it refuses, rather than forget a
// live one.
func (u *usedStates) use(state string, exp, now time.Time) (fresh, full bool) {
	h := sha256.Sum256([]byte(state))
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.seen == nil {
		u.seen = map[[32]byte]time.Time{}
	}
	if _, ok := u.seen[h]; ok {
		return false, false
	}
	if len(u.seen) >= maxUsedStates {
		for k, e := range u.seen {
			if !now.Before(e) {
				delete(u.seen, k)
			}
		}
		if len(u.seen) >= maxUsedStates {
			return false, true
		}
	}
	u.seen[h] = exp
	return true, false
}
