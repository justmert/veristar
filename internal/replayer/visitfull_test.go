package replayer_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/justmert/stellar-analytics/internal/events"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/umami"
)

// W-12: visit_state refuses writes once it holds natsx.VisitStateMaxBytes
// and keeps refusing until keys expire, up to 24 hours. before, the replayer
// retried the visit state and duplicate guard writes without end, so a full
// bucket stopped all web delivery. now it skips them, counts the skip, and
// delivers. the bucket is made full here by lowering its limit on the real
// server and filling it.
func TestFullVisitStateKeepsDelivering(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a dedicated umami")
	}
	env := startNATS(t)
	stack := startUmamiStack(t)
	s := newSite(t, stack.Admin)
	rp := startReplayerTo(t, env.srv.URL, []*url.URL{stack.URL}, "it-visitfull", 4, 64)
	waitFor(t, 30*time.Second, "replayer to lead", func() bool { return scrapeOK(rp.ops)["replayer_leader"] == 1 })

	ctx := testCtx(t, time.Minute)
	stream := "KV_" + natsx.BucketVisitState
	str, err := env.js.Stream(ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	cfg := str.CachedInfo().Config
	cfg.MaxBytes = 64 << 10
	if _, err := env.js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	kv, err := env.js.KeyValue(ctx, natsx.BucketVisitState)
	if err != nil {
		t.Fatal(err)
	}
	// large values first, then ever smaller ones, until not even a key with
	// a one byte value fits, which is what a duplicate guard key needs
	var fullErr error
	n := 0
	for _, size := range []int{4096, 256, 16, 1} {
		for {
			n++
			if _, err := kv.Put(ctx, fmt.Sprintf("f.%d", n), []byte(strings.Repeat("x", size))); err != nil {
				fullErr = err
				break
			}
		}
	}
	if fullErr == nil {
		t.Fatal("visit_state did not fill")
	}
	t.Logf("a write to the full bucket answers: %v", fullErr)

	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
	const visitors, each = 5, 4
	var evs []*events.WebEvent
	for v := range visitors {
		vt := newVT(t)
		for i := range each {
			evs = append(evs, pageview(t, s, vt, fmt.Sprintf("/v%d/%d", v, i), at.Add(time.Duration(v*each+i)*time.Second)))
		}
	}
	publish(t, env.js, evs...)
	waitFor(t, time.Minute, "stream drained with a full visit_state", func() bool { return drained(t, env.js) })
	if n := len(streamMsgs(t, env.js, natsx.StreamWebDLQ)); n != 0 {
		t.Fatalf("%d events dead-lettered", n)
	}
	r := umami.Range{Start: at.Add(-time.Minute), End: at.Add(time.Hour)}
	waitFor(t, 30*time.Second, "every pageview in umami", func() bool {
		st, err := stack.Admin.Stats(testCtx(t, time.Minute), s.ID, r)
		return err == nil && st.Pageviews == visitors*each
	})
	m := scrape(t, rp.ops)
	if m[`replayer_visit_state_writes_skipped_total{kind="sent"}`] < visitors*each {
		t.Fatalf("skipped duplicate guard writes: %v", m[`replayer_visit_state_writes_skipped_total{kind="sent"}`])
	}
	waitFor(t, 15*time.Second, "visit_state usage gauges", func() bool {
		m := scrape(t, rp.ops)
		return m["replayer_visit_state_max_bytes"] == 64<<10 && m["replayer_visit_state_bytes"] > 0
	})
}
