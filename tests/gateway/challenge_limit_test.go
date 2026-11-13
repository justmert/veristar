//go:build gatewayit

package gatewayit

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/justmert/stellar-analytics/tests/gateway/gwclient"
)

// frozenClock stands still until a test moves it, so token buckets refill
// by exactly the amount the test asks for.
type frozenClock struct{ ns atomic.Int64 }

func (c *frozenClock) Now() time.Time          { return time.Unix(0, c.ns.Load()) }
func (c *frozenClock) advance(d time.Duration) { c.ns.Add(int64(d)) }
func newFrozenClock(at time.Time) *frozenClock {
	c := &frozenClock{}
	c.ns.Store(at.UnixNano())
	return c
}

// challengeStatus asks for one challenge and returns the status.
func challengeStatus(t testing.TB, c *gwclient.Client, site string) int {
	t.Helper()
	status, body, _, err := c.Get(context.Background(), "/v1/c?w="+url.QueryEscape(site))
	if err != nil {
		t.Fatal(err)
	}
	if status == http.StatusTooManyRequests && string(body) != "rejected_rate_limited\n" {
		t.Fatalf("429 body %q", body)
	}
	return status
}

// issued asks for n challenges and returns how many were issued and the
// last status.
func issued(t testing.TB, c *gwclient.Client, site string, n int) (ok, last int) {
	t.Helper()
	for range n {
		last = challengeStatus(t, c, site)
		switch last {
		case http.StatusOK:
			ok++
		case http.StatusTooManyRequests:
		default:
			t.Fatalf("challenge status %d", last)
		}
	}
	return ok, last
}

// TestChallengeRateLimits checks the token buckets of GET /v1/c on a node
// with a stopped clock, so every count is exact: the prefix bucket (keyed by
// site and truncated address), the site bucket, refill, and that challenge
// and event buckets are separate. the values are the starting values of
// docs/reference/gateway-limits.md (gatewayEnv).
func TestChallengeRateLimits(t *testing.T) {
	s := newStack(t)
	siteA := s.PutSite(t, "", "https://rl-a.sa-tenant.example", "active")
	siteB := s.PutSite(t, "", "https://rl-b.sa-tenant.example", "active")
	siteC := s.PutSite(t, "", "https://rl-c.sa-tenant.example", "active")
	clk := newFrozenClock(time.Now())
	p := startInProcess(t, s, "challenge-limit", clk.Now)
	ctx := context.Background()

	a := inProcessClient(p, siteA.Origin, firefoxUA, "198.51.100.70")
	// waitSite polls until the registry has the site; unknown-site answers
	// take no token, the one challenge it gets does
	proof := proofFor(t, a, siteA.ID)

	t.Run("prefix burst then 429", func(t *testing.T) {
		if ok, last := issued(t, a, siteA.ID, 70); ok != 59 || last != http.StatusTooManyRequests {
			t.Fatalf("issued %d of 70 after the first, last %d; want 59 and 429", ok, last)
		}
		// another address in the same /24 shares the bucket
		same := inProcessClient(p, siteA.Origin, firefoxUA, "198.51.100.71")
		if st := challengeStatus(t, same, siteA.ID); st != http.StatusTooManyRequests {
			t.Fatalf("same /24: status %d, want 429", st)
		}
		// another /24 has its own bucket
		other := inProcessClient(p, siteA.Origin, firefoxUA, "198.51.101.70")
		if st := challengeStatus(t, other, siteA.ID); st != http.StatusOK {
			t.Fatalf("other /24: status %d, want 200", st)
		}
		// the prefix bucket is per site, so the same address on another
		// site is not limited
		onB := inProcessClient(p, siteB.Origin, firefoxUA, "198.51.100.70")
		if st := challengeStatus(t, onB, siteB.ID); st != http.StatusOK {
			t.Fatalf("same address, other site: status %d, want 200", st)
		}
	})

	t.Run("event buckets are separate", func(t *testing.T) {
		// the challenge buckets of this prefix are empty; its events are not
		// limited by that
		status, err := a.PostEvent(ctx, gwclient.Pageview(siteA.ID, siteA.Host, "/after-challenge-limit", proof), nil)
		if err != nil || status != http.StatusAccepted {
			t.Fatalf("event: status %d, %v, want 202", status, err)
		}
	})

	t.Run("prefix refill", func(t *testing.T) {
		clk.advance(time.Second) // 20 per second
		if ok, last := issued(t, a, siteA.ID, 25); ok != 20 || last != http.StatusTooManyRequests {
			t.Fatalf("after 1 s: issued %d of 25, last %d; want 20 and 429", ok, last)
		}
	})

	t.Run("site burst then 429 across prefixes", func(t *testing.T) {
		// 40 per /24 stays under the prefix burst of 60, so only the site
		// bucket (burst 400) can refuse
		total := 0
		var last int
		for i := 0; i < 11 && last != http.StatusTooManyRequests; i++ {
			c := inProcessClient(p, siteC.Origin, firefoxUA, fmt.Sprintf("10.20.%d.1", i))
			var ok int
			ok, last = issued(t, c, siteC.ID, 40)
			total += ok
		}
		if total != 400 || last != http.StatusTooManyRequests {
			t.Fatalf("site C issued %d, last %d; want 400 and 429", total, last)
		}
		fresh := inProcessClient(p, siteC.Origin, firefoxUA, "10.21.0.1")
		if st := challengeStatus(t, fresh, siteC.ID); st != http.StatusTooManyRequests {
			t.Fatalf("fresh prefix on an exhausted site: status %d, want 429", st)
		}
		// another site is unaffected
		onA := inProcessClient(p, siteA.Origin, firefoxUA, "10.21.0.1")
		if st := challengeStatus(t, onA, siteA.ID); st != http.StatusOK {
			t.Fatalf("other site: status %d, want 200", st)
		}
		clk.advance(time.Second) // 200 per second for the site
		total = 0
		for i := range 6 {
			c := inProcessClient(p, siteC.Origin, firefoxUA, fmt.Sprintf("10.22.%d.1", i))
			ok, _ := issued(t, c, siteC.ID, 40)
			total += ok
		}
		if total != 200 {
			t.Fatalf("after 1 s site C issued %d of 240, want 200", total)
		}
	})

	t.Run("refused requests take no token", func(t *testing.T) {
		// a foreign origin and an unknown site are refused before the limit
		d := inProcessClient(p, "https://evil.example", firefoxUA, "198.51.102.70")
		for range 100 {
			if st := challengeStatus(t, d, siteA.ID); st != http.StatusForbidden {
				t.Fatalf("foreign origin: status %d, want 403", st)
			}
		}
		good := inProcessClient(p, siteA.Origin, firefoxUA, "198.51.102.70")
		if ok, _ := issued(t, good, siteA.ID, 60); ok != 60 {
			t.Fatalf("after 100 refused requests the prefix issued %d of 60", ok)
		}
	})
}
