package replayer

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/umami"
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
		err       error
		permanent bool
		label     string
	}{
		{&umami.APIError{Status: 400}, true, sendClientError},
		{&umami.APIError{Status: 404}, true, sendClientError},
		{&umami.APIError{Status: 408}, false, sendClientError},
		{&umami.APIError{Status: 429}, false, sendClientError},
		{&umami.APIError{Status: 500}, false, sendServerError},
		{&umami.APIError{Status: 503}, false, sendServerError},
		{fmt.Errorf("umami send: %w", &url.Error{Op: "Post", URL: "http://u", Err: errors.New("connection refused")}), false, sendNetwork},
		// an undecodable 200 is a send failure like any other, so
		// umami's health decides whether it is retried without limit
		{errors.New("umami send: 200 answer without cache, sessionId or visitId"), false, sendNetwork},
	}
	for _, c := range cases {
		f, label := classify(c.err)
		if f.permanent != c.permanent || !f.send || label != c.label {
			t.Errorf("classify(%v) = %+v %s", c.err, f, label)
		}
	}
}

func TestVisitorHashIsStableAndSpreads(t *testing.T) {
	const n = 16
	// fnv-1a is fixed, so a visitor's process is the same in every replayer
	// and after a restart
	if got := targetOf("s", "v", n); got != targetOf("s", "v", n) || got < 0 || got >= n {
		t.Fatalf("target %d", got)
	}
	// the separator keeps ("ab","c") and ("a","bc") apart
	if visitorHash("ab", "c") == visitorHash("a", "bc") {
		t.Error("separator does not split site id and token")
	}
}

func TestSlotsPerTarget(t *testing.T) {
	for _, c := range []struct{ workers, n int }{{16, 1}, {16, 3}, {128, 5}, {3, 3}, {1, 1}} {
		sum := 0
		for i := range c.n {
			s := slotsPerTarget(c.workers, c.n, i)
			if s < 1 {
				t.Errorf("%d workers over %d: process %d gets %d", c.workers, c.n, i, s)
			}
			sum += s
		}
		if sum != c.workers {
			t.Errorf("%d workers over %d processes give %d slots", c.workers, c.n, sum)
		}
	}
}

// retries on a healthy umami are bounded and spaced 1 s, 5 s, 30 s
func TestHealthyRetriesAreBounded(t *testing.T) {
	var total time.Duration
	for n := 1; n < maxHealthyFailures; n++ {
		total += healthyRetryDelay(n)
	}
	if maxHealthyFailures != 4 || total != 36*time.Second {
		t.Fatalf("%d attempts over %s", maxHealthyFailures, total)
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
	// more umami processes than the 16 workers
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

// a visitor waiting for one umami process is woken when that process answers,
// not when another one does
func TestGateWakesOnlyTheTargetThatAnswered(t *testing.T) {
	var g gate
	ctx := context.Background()
	woke := make(chan int, 3)
	for _, target := range []int{0, 1, noTarget} {
		go func() {
			if g.wait(ctx, nil, time.Hour, target) {
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

// failing sends of several visitors on a process whose health checks
// pass are a write fault; one visitor failing among stored sends is not.
func TestWriteWatch(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	var w writeWatch
	// one poison visitor among normal traffic
	for i := range 20 {
		w.succeed(fmt.Sprintf("ok%d", i), now)
	}
	w.fail("poison", now)
	if w.faulted(now) {
		t.Fatal("one failing visitor was taken for a write fault")
	}
	// every send fails: a fault from the third visitor on
	var x writeWatch
	x.fail("a", now)
	x.fail("b", now)
	if x.faulted(now) {
		t.Fatal("two visitors are below the minimum")
	}
	x.fail("c", now)
	if !x.faulted(now) {
		t.Fatal("three failing visitors and no stored send is a write fault")
	}
	// the first stored send says waiters should retry; more stored sends
	// than failing visitors end the fault
	if !x.succeed("a", now.Add(time.Second)) {
		t.Fatal("a stored send must wake the visitors that wait out the fault")
	}
	x.succeed("d", now.Add(time.Second))
	if x.faulted(now.Add(time.Second)) {
		t.Fatal("as many stored sends as failing visitors end the fault")
	}
	// failures leave the window
	var y writeWatch
	for _, k := range []string{"a", "b", "c"} {
		y.fail(k, now)
	}
	if y.faulted(now.Add(writeFaultWindow)) {
		t.Fatal("failures older than the window still count")
	}
	// write-fault waiters are woken by a stored send, never by the probe
	if k := writeFaultWaiter(0); k >= 0 || k == noTarget {
		t.Fatalf("write fault key %d collides with a process or noTarget", k)
	}
	g := gate{waiters: map[chan struct{}]int{make(chan struct{}, 1): writeFaultWaiter(0)}}
	if got := g.unreachableTargets(); len(got) != 0 {
		t.Fatalf("the health probe would wake write-fault waiters: %v", got)
	}
	if writeFaultDelay(10) != writeFaultRetryMax {
		t.Fatalf("write fault delay %s", writeFaultDelay(10))
	}
}
