package auth

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBucket(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	b := newBucket(10, 30, now)
	for i := range 30 {
		ok, _ := b.take(now)
		require.True(t, ok, i)
	}
	ok, wait := b.take(now)
	require.False(t, ok)
	require.Equal(t, 100*time.Millisecond, wait)
	// one tenth of a second gives one token back
	ok, _ = b.take(now.Add(100 * time.Millisecond))
	require.True(t, ok)
	ok, _ = b.take(now.Add(100 * time.Millisecond))
	require.False(t, ok)
	// a long pause refills to the burst, not beyond
	later := now.Add(time.Hour)
	for range 30 {
		ok, _ = b.take(later)
		require.True(t, ok)
	}
	ok, _ = b.take(later)
	require.False(t, ok)
}

func requestFrom(header, remote string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	if header != "" {
		r.Header.Set(ClientAddrHeader, header)
	}
	return r
}

func TestClientKeys(t *testing.T) {
	h, err := newClientHasher()
	require.NoError(t, err)
	// caddy's header wins over the peer, which is caddy itself
	require.Equal(t, h.of(requestFrom("203.0.113.5", "10.0.0.2:5000")), h.of(requestFrom("203.0.113.5", "10.0.0.3:6000")))
	require.NotEqual(t, h.of(requestFrom("203.0.113.5", "10.0.0.2:5000")), h.of(requestFrom("203.0.113.6", "10.0.0.2:5000")))
	// without caddy (dev) the peer is the client
	require.Equal(t, h.of(requestFrom("", "127.0.0.1:1")), h.of(requestFrom("127.0.0.1", "")))
	// ipv6 by /48, ipv4-mapped ipv6 as ipv4
	require.Equal(t, h.of(requestFrom("2001:db8::1", "")), h.of(requestFrom("2001:db8::ffff:1", "")))
	require.Equal(t, h.of(requestFrom("2001:db8::1", "")), h.of(requestFrom("2001:db8:0:1::1", "")))
	require.Equal(t, h.of(requestFrom("2001:db8::1", "")), h.of(requestFrom("2001:db8:0:ff00::1", "")))
	require.NotEqual(t, h.of(requestFrom("2001:db8::1", "")), h.of(requestFrom("2001:db8:1::1", "")))
	require.Equal(t, h.of(requestFrom("::ffff:192.0.2.1", "")), h.of(requestFrom("192.0.2.1", "")))
	// another process has another key, so keys mean nothing outside it
	h2, err := newClientHasher()
	require.NoError(t, err)
	require.NotEqual(t, h.of(requestFrom("192.0.2.1", "")), h2.of(requestFrom("192.0.2.1", "")))
}

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	l := newLimiter(1, 2, 100, 3, 0.001, 0, now)
	a, b, c := clientKey{1}, clientKey{2}, clientKey{3}
	for range 2 {
		ok, _ := l.take(a, now)
		require.True(t, ok)
	}
	ok, wait := l.take(a, now)
	require.False(t, ok, "a's own bucket is empty")
	require.Equal(t, time.Second, wait)
	ok, _ = l.take(b, now)
	require.True(t, ok, "b has its own bucket")
	// the global ceiling (3) is reached by a and b together; a refused
	// request of a did not spend from it
	ok, _ = l.take(c, now)
	require.False(t, ok, "the global ceiling holds")
	// refilled buckets are forgotten when the map is full
	l.clients = map[clientKey]*bucket{a: newBucket(1, 2, now)}
	l.forgetRefilled(now.Add(2 * time.Second))
	require.Empty(t, l.clients)
}

func TestUsedStates(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	var u usedStates
	fresh, full := u.use("s1", now.Add(time.Minute), now)
	require.True(t, fresh)
	require.False(t, full)
	fresh, _ = u.use("s1", now.Add(time.Minute), now)
	require.False(t, fresh, "a state is used once")
	fresh, _ = u.use("s2", now.Add(time.Minute), now)
	require.True(t, fresh)
}

// review: one holder of an ipv6 /48 (a hosting customer, a tunnel
// broker's free allocation) cycles through its /56 and /64 subnets as fast
// as it likes. it gets one client bucket, so it neither drains the global
// ceiling nor stops a person elsewhere from starting a sign-in.
func TestOneIPv6SiteCannotDrainTheCeiling(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	h, err := newClientHasher()
	require.NoError(t, err)
	l := newLimiter(StartClientRatePerSecond, StartClientBurst, StartRatePerSecond, StartBurst,
		StartReserveRatePerSecond, StartReserveBurst, now)
	admitted := 0
	for i := range 65536 {
		addr := fmt.Sprintf("2001:db8:77:%x::1", i)
		if ok, _ := l.take(h.of(requestFrom(addr, "")), now); ok {
			admitted++
		}
	}
	require.Equal(t, StartClientBurst, admitted, "a /48 is one client")
	for i := range 50 {
		ok, _ := l.take(h.of(requestFrom(fmt.Sprintf("198.51.100.%d", i+1), "")), now)
		require.True(t, ok, "client %d elsewhere", i)
	}
}

// review: a flood from 150 keys (150 ipv4 addresses or ipv6 /48s), each
// at its own client rate, asks for 75 starts a second and so keeps the
// global ceiling of 50 a second empty. people who had been idle still
// start: the reserve serves only clients whose own bucket was full, and the
// flood's keys never are, because every refusal by the ceiling still spent
// their own token.
func TestIdleClientsKeepAReserveUnderAFlood(t *testing.T) {
	start := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	h, err := newClientHasher()
	require.NoError(t, err)
	l := newLimiter(StartClientRatePerSecond, StartClientBurst, StartRatePerSecond, StartBurst,
		StartReserveRatePerSecond, StartReserveBurst, start)
	flood := make([]clientKey, 150)
	for i := range flood {
		flood[i] = h.of(requestFrom(fmt.Sprintf("2001:db8:%x::1", 0x100+i), ""))
	}
	const step = 20 * time.Millisecond
	const run = 60 * time.Second
	floodOK, people, peopleOK := 0, 0, 0
	for at := time.Duration(0); at < run; at += step {
		now := start.Add(at)
		for _, k := range flood {
			if ok, _ := l.take(k, now); ok {
				floodOK++
			}
		}
		// one new person every 200 ms, after the first 10 s of flood
		if at >= 10*time.Second && at%(200*time.Millisecond) == 0 {
			people++
			addr := fmt.Sprintf("10.%d.%d.%d", people/65536, people/256%256, people%256)
			if ok, _ := l.take(h.of(requestFrom(addr, "")), now); ok {
				peopleOK++
			}
		}
	}
	require.Equal(t, people, peopleOK, "every idle person starts during the flood")
	// the flood got no more than the ceiling's burst and rate
	require.LessOrEqual(t, floodOK, StartBurst+int(run.Seconds())*StartRatePerSecond)
}
