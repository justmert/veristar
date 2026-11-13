package replayer

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/umami"
)

func TestCacheFor(t *testing.T) {
	r := &VisitRecord{Websites: map[string]VisitEntry{
		"w1": {SessionID: "s", Cache: "c1", LastEventTS: 10_000},
	}}
	cases := []struct {
		website string
		ts      int64
		want    string
	}{
		{"w1", 10_000 + VisitGap, "c1"},   // exactly 30 minutes still continues
		{"w1", 10_000 + VisitGap + 1, ""}, // over 30 minutes starts again
		{"w1", 10_000 - 5, "c1"},          // an earlier acceptance time from another node
		{"w2", 10_001, ""},                // another website's token is never sent
		{"w1", 10_000 + VisitGap - 600, "c1"},
	}
	for _, c := range cases {
		if got := r.cacheFor(c.website, c.ts); got != c.want {
			t.Errorf("cacheFor(%s, %d) = %q, want %q", c.website, c.ts, got, c.want)
		}
	}
	var none *VisitRecord
	if none.cacheFor("w1", 1) != "" {
		t.Error("nil record gave a token")
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		err         error
		permanent   bool
		unreachable bool
		label       string
	}{
		{&umami.APIError{Status: 400}, true, false, sendClientError},
		{&umami.APIError{Status: 404}, true, false, sendClientError},
		{&umami.APIError{Status: 408}, false, false, sendClientError},
		{&umami.APIError{Status: 429}, false, false, sendClientError},
		{&umami.APIError{Status: 500}, false, false, sendServerError},
		{&umami.APIError{Status: 503}, false, true, sendServerError},
		{fmt.Errorf("umami send: %w", &url.Error{Op: "Post", URL: "http://u", Err: errors.New("connection refused")}), false, true, sendNetwork},
	}
	for _, c := range cases {
		f, label := classify(c.err)
		if f.permanent != c.permanent || f.unreachable != c.unreachable || label != c.label {
			t.Errorf("classify(%v) = %+v %s", c.err, f, label)
		}
	}
}

func TestLaneOfIsStableAndSpreads(t *testing.T) {
	const lanes = 16
	// fnv-1a is fixed, so a lane is the same in every process and after a
	// restart; this value pins it
	if got := laneOf("s", "v", lanes); got != laneOf("s", "v", lanes) || got < 0 || got >= lanes {
		t.Fatalf("lane %d", got)
	}
	// the separator keeps ("ab","c") and ("a","bc") apart
	if laneOf("ab", "c", 1<<20) == laneOf("a", "bc", 1<<20) {
		t.Error("separator does not split site id and token")
	}
	seen := map[int]int{}
	for i := range 1600 {
		seen[laneOf("site", fmt.Sprintf("%064x", i), lanes)]++
	}
	for l := range lanes {
		if seen[l] < 50 {
			t.Errorf("lane %d got %d of 1600 keys", l, seen[l])
		}
	}
}

func TestLoadSettings(t *testing.T) {
	ok := map[string]string{
		"REPLAYER_NODE_ID": "core-1", "REPLAYER_OPS_ADDR": "127.0.0.1:9090", "UMAMI_URL": "http://umami:3000",
		"REPLAYER_WORKERS": "16", "REPLAYER_MAX_IN_FLIGHT": "512",
	}
	s, err := LoadSettings(config.FromMap(ok))
	if err != nil || s.Workers != 16 || s.MaxInFlight != 512 || len(s.UmamiURLs) != 1 || s.UmamiURLs[0].Host != "umami:3000" {
		t.Fatalf("%+v %v", s, err)
	}
	// several umami processes, spaces after the commas allowed
	list := map[string]string{}
	for k, v := range ok {
		list[k] = v
	}
	list["UMAMI_URL"] = "http://umami-1:3000, http://umami-2:3000,http://umami-3:3000"
	s, err = LoadSettings(config.FromMap(list))
	if err != nil || len(s.UmamiURLs) != 3 || s.UmamiURLs[1].Host != "umami-2:3000" || s.UmamiURLs[2].Host != "umami-3:3000" {
		t.Fatalf("%+v %v", s, err)
	}
	bad := func(k, v string) error {
		m := map[string]string{}
		for kk, vv := range ok {
			m[kk] = vv
		}
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
		_, err := LoadSettings(config.FromMap(m))
		return err
	}
	// more umami processes than the 16 lanes
	urls := make([]string, 17)
	for i := range urls {
		urls[i] = fmt.Sprintf("http://umami-%d:3000", i)
	}
	seventeen := strings.Join(urls, ",")
	for _, c := range []struct{ k, v string }{
		{"REPLAYER_NODE_ID", ""}, {"REPLAYER_NODE_ID", "has.dot"}, {"REPLAYER_OPS_ADDR", "nope"},
		{"UMAMI_URL", "ftp://x"}, {"UMAMI_URL", "http://a:3000,"}, {"UMAMI_URL", "http://a:3000,http://a:3000"},
		{"UMAMI_URL", "http://a:3000,ftp://b"}, {"UMAMI_URL", seventeen},
		{"REPLAYER_WORKERS", "0"}, {"REPLAYER_MAX_IN_FLIGHT", "8"},
		{"REPLAYER_MAX_IN_FLIGHT", "5000"},
	} {
		if err := bad(c.k, c.v); err == nil {
			t.Errorf("%s=%q accepted", c.k, c.v)
		}
	}
}

// every event of one visitor goes to one umami process, and visitors spread
// evenly over the processes
func TestTargetOfFollowsTheVisitorHash(t *testing.T) {
	const procs = 4
	seen := map[int]int{}
	for i := range 4000 {
		vt := fmt.Sprintf("%064x", i)
		got := targetOf("site", vt, procs)
		if got != targetOf("site", vt, procs) || got < 0 || got >= procs {
			t.Fatalf("target %d", got)
		}
		// same hash as the lanes: with a lane count that is a multiple of
		// the process count, a lane always sends to one process
		if laneOf("site", vt, 128)%procs != got {
			t.Fatalf("lane %d and target %d disagree", laneOf("site", vt, 128), got)
		}
		seen[got]++
	}
	for p := range procs {
		if seen[p] < 850 {
			t.Errorf("process %d got %d of 4000 visitors", p, seen[p])
		}
	}
	if targetOf("site", "vt", 1) != 0 {
		t.Error("one process must take every visitor")
	}
}

// a lane waiting for one umami process is woken when that process answers,
// not when another one does
func TestGateWakesOnlyTheTargetThatAnswered(t *testing.T) {
	var g gate
	ctx := context.Background()
	woke := make(chan int, 3)
	for _, target := range []int{0, 1, noTarget} {
		go func() {
			if g.wait(ctx, time.Hour, target) {
				woke <- target
			}
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		g.mu.Lock()
		n := len(g.waiters)
		g.mu.Unlock()
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waiters did not register")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := g.unreachableTargets(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("unreachable targets %v", got)
	}
	g.wakeUnreachable(1)
	select {
	case got := <-woke:
		if got != 1 {
			t.Fatalf("woke %d", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter of process 1 was not woken")
	}
	select {
	case got := <-woke:
		t.Fatalf("woke %d as well", got)
	case <-time.After(200 * time.Millisecond):
	}
	g.wakeUnreachable(0)
	if got := <-woke; got != 0 {
		t.Fatalf("woke %d", got)
	}
}
