package replayer

import (
	"errors"
	"fmt"
	"net/url"
	"testing"

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
	if err != nil || s.Workers != 16 || s.MaxInFlight != 512 || s.UmamiURL.Host != "umami:3000" {
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
	for _, c := range []struct{ k, v string }{
		{"REPLAYER_NODE_ID", ""}, {"REPLAYER_NODE_ID", "has.dot"}, {"REPLAYER_OPS_ADDR", "nope"},
		{"UMAMI_URL", "ftp://x"}, {"REPLAYER_WORKERS", "0"}, {"REPLAYER_MAX_IN_FLIGHT", "8"},
		{"REPLAYER_MAX_IN_FLIGHT", "5000"},
	} {
		if err := bad(c.k, c.v); err == nil {
			t.Errorf("%s=%q accepted", c.k, c.v)
		}
	}
}
