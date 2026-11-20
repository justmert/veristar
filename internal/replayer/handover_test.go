package replayer_test

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/umami"
)

// a hand-over under a backlog must not store an event twice. the old owner
// gets a while to deliver what it holds; at the end of that time sends are
// still in flight, because every send to the slow umami takes seconds.
// before the fix the hold then cancelled those sends: umami had stored the
// events, the replayer never saw the 200 and wrote no sent key, handed the
// messages back, and the next owner sent them again (production, v0.4.5,
// 59 duplicates in 24,000). now the old owner starts no new send once that
// time is up, lets the sends in flight finish and records them, and hands
// back only what it never sent.
func TestHandOverUnderBacklogStoresEveryEventOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a dedicated umami")
	}
	const (
		visitors = 40
		each     = 3
		workers  = 4
		// longer than a third of the hand-over time, so every delivery slot
		// has a send in flight when that time is up
		slow = 4 * time.Second
	)
	env := startNATS(t)
	stack := startUmamiStack(t)
	s := newSite(t, stack.Admin)
	log := &sendLog{}

	base := pastHour(3*time.Hour, time.Hour)
	var evs []*events.WebEvent
	for i := range visitors {
		vt := newVT(t)
		for j := range each {
			evs = append(evs, pageview(t, s, vt, visitorPath(i, j), base.Add(time.Duration(i*each+j)*time.Second)))
		}
	}
	publish(t, env.js, evs...)

	a := startReplayerTo(t, env.srv.URL, []*url.URL{newSlowSendProxy(t, log, "old", stack.URL, slow)}, "it-old", workers, 64)
	waitFor(t, time.Minute, "the old owner to store its first sends", func() bool { return log.checkOrder().stored >= workers })
	// a shutdown hands every partition over, as in a rolling deploy
	stopped := time.Now()
	a.stop(t)
	t.Logf("old owner stopped in %s after %d stored sends", time.Since(stopped).Round(time.Millisecond), log.checkOrder().byNode["old"])

	b := startReplayerTo(t, env.srv.URL, []*url.URL{newSendProxy(t, log, "new", stack.URL)}, "it-new", workers, 64)
	waitFor(t, 3*time.Minute, "stream drained by the new owner", func() bool { return drained(t, env.js) })
	if n := len(streamMsgs(t, env.js, natsx.StreamWebDLQ)); n != 0 {
		t.Fatalf("%d events dead-lettered", n)
	}

	rep := log.checkOrder()
	t.Logf("published %d; stored sends %d (by node %v), repeats %d", len(evs), rep.stored, rep.byNode, rep.repeats)
	if rep.byNode["new"] == 0 {
		t.Fatal("the new owner sent nothing; the old one delivered the whole backlog and nothing was handed over")
	}
	for _, r := range rep.repeated {
		t.Errorf("sent twice: %s", r)
	}
	for _, o := range rep.outOfOrder {
		t.Error(o)
	}
	for _, o := range rep.overlaps {
		t.Error(o)
	}
	r := umami.Range{Start: base.Add(-time.Hour), End: base.Add(2 * time.Hour)}
	paths := pathCounts(t, stack.Admin, s.ID, r, len(evs)+100)
	missing := 0
	for i := range visitors {
		for j := range each {
			if paths[visitorPath(i, j)] == 0 {
				missing++
			}
		}
	}
	if missing != 0 {
		t.Errorf("umami: %d events missing", missing)
	}
	// the path metric counts a path once per visit, so a copy stored in the
	// same visit shows only in the pageview total
	st, err := stack.Admin.Stats(testCtx(t, time.Minute), s.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pageviews != int64(len(evs)) {
		t.Errorf("umami holds %d pageviews for %d events", st.Pageviews, len(evs))
	}
	m := scrape(t, b.ops)
	if d := m[fmt.Sprintf("replayer_outcomes_total{outcome=%q}", "duplicate_skipped")]; d != 0 {
		t.Logf("the new owner skipped %.0f events by their sent key", d)
	}
}
