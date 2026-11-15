package replayer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/replayer"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

func dayRange(d time.Time) umami.Range {
	return umami.Range{Start: d, End: d.Add(24*time.Hour - time.Millisecond)}
}

// the replayer against the shared dev umami: time buckets, visit rule,
// bands, bots, the dlq and the duplicate guard. one nats server and one
// replayer serve every subtest; each subtest uses its own site.
func TestDevUmami(t *testing.T) {
	umamiURL, admin := devUmami(t)
	env := startNATS(t)
	rp := startReplayer(t, env.srv.URL, umamiURL, "it-a")
	waitFor(t, 30*time.Second, "replayer to lead", func() bool { return scrapeOK(rp.ops)["replayer_leader"] == 1 })
	today := time.Now().UTC().Truncate(24 * time.Hour)

	t.Run("acceptance times on three days land in their days", func(t *testing.T) {
		s := newSite(t, admin)
		want := map[time.Time]int64{}
		for n := 1; n <= 3; n++ {
			day := today.AddDate(0, 0, -n-2)
			want[day] = int64(n)
			for i := range n {
				publish(t, env.js, pageview(t, s, newVT(t), fmt.Sprintf("/day/%d/%d", n, i), day.Add(12*time.Hour+time.Duration(i)*time.Minute)))
			}
		}
		waitFor(t, time.Minute, "stream drained", func() bool { return drained(t, env.js) })
		for day, n := range want {
			st, err := admin.Stats(testCtx(t, time.Minute), s.ID, dayRange(day))
			if err != nil {
				t.Fatal(err)
			}
			if st.Pageviews != n || st.Visitors != n {
				t.Errorf("%s: pageviews %d visitors %d, want %d", day.Format(time.DateOnly), st.Pageviews, st.Visitors, n)
			}
		}
		// the days between and around hold nothing
		for _, day := range []time.Time{today.AddDate(0, 0, -6), today.AddDate(0, 0, -2)} {
			st, err := admin.Stats(testCtx(t, time.Minute), s.ID, dayRange(day))
			if err != nil {
				t.Fatal(err)
			}
			if st.Pageviews != 0 {
				t.Errorf("%s holds %d pageviews, want 0", day.Format(time.DateOnly), st.Pageviews)
			}
		}
	})

	t.Run("visits follow the 1800 s gap and umami's hour rule", func(t *testing.T) {
		s := newSite(t, admin)
		h := today.AddDate(0, 0, -2).Add(10 * time.Hour)
		a, b, c := newVT(t), newVT(t), newVT(t)
		publish(t, env.js,
			// 15 min apart across the hour: the cache token keeps one visit
			pageview(t, s, a, "/a/1", h.Add(50*time.Minute)),
			pageview(t, s, a, "/a/2", h.Add(65*time.Minute)),
			// 45 min apart in one utc hour: no cache token, but umami derives
			// the same visit from session and hour (its own behaviour)
			pageview(t, s, b, "/b/1", h.Add(5*time.Minute)),
			pageview(t, s, b, "/b/2", h.Add(50*time.Minute)),
			// 31 min apart across the hour: two visits
			pageview(t, s, c, "/c/1", h.Add(40*time.Minute)),
			pageview(t, s, c, "/c/2", h.Add(71*time.Minute)),
		)
		waitFor(t, time.Minute, "stream drained", func() bool { return drained(t, env.js) })
		st, err := admin.Stats(testCtx(t, time.Minute), s.ID, dayRange(h.Truncate(24*time.Hour)))
		if err != nil {
			t.Fatal(err)
		}
		if st.Pageviews != 6 || st.Visitors != 3 || st.Visits != 4 {
			t.Errorf("pageviews %d visitors %d visits %d, want 6, 3, 4", st.Pageviews, st.Visitors, st.Visits)
		}
		kv, err := env.js.KeyValue(testCtx(t, time.Minute), natsx.BucketVisitState)
		if err != nil {
			t.Fatal(err)
		}
		e, err := kv.Get(testCtx(t, time.Minute), replayer.VisitKey(s.ID, a))
		if err != nil {
			t.Fatal(err)
		}
		var rec replayer.VisitRecord
		if err := json.Unmarshal(e.Value(), &rec); err != nil {
			t.Fatal(err)
		}
		got := rec.Websites[s.ID]
		if got.SessionID == "" || got.Cache == "" || got.LastEventTS != h.Add(65*time.Minute).Unix() {
			t.Errorf("visit state %+v", got)
		}
	})

	t.Run("quarantine band goes to the quarantine website", func(t *testing.T) {
		s := newSite(t, admin)
		at := today.AddDate(0, 0, -1).Add(9 * time.Hour)
		ev := pageview(t, s, newVT(t), "/q", at)
		ev.Band, ev.WebsiteID = events.BandQuarantine, s.Quarantine
		publish(t, env.js, ev, pageview(t, s, newVT(t), "/p", at))
		waitFor(t, time.Minute, "stream drained", func() bool { return drained(t, env.js) })
		q := pathCounts(t, admin, s.Quarantine, dayRange(at.Truncate(24*time.Hour)), 10)
		p := pathCounts(t, admin, s.ID, dayRange(at.Truncate(24*time.Hour)), 10)
		if len(q) != 1 || q["/q"] != 1 || len(p) != 1 || p["/p"] != 1 {
			t.Errorf("quarantine website %v, primary website %v", q, p)
		}
	})

	t.Run("a bot user agent stores nothing and counts bot", func(t *testing.T) {
		s := newSite(t, admin)
		at := today.AddDate(0, 0, -1).Add(8 * time.Hour)
		ev := pageview(t, s, newVT(t), "/bot", at)
		ev.UA = botUA
		publish(t, env.js, ev)
		waitFor(t, time.Minute, "stream drained", func() bool { return drained(t, env.js) })
		st, err := admin.Stats(testCtx(t, time.Minute), s.ID, dayRange(at.Truncate(24*time.Hour)))
		if err != nil {
			t.Fatal(err)
		}
		if st.Pageviews != 0 || st.Visitors != 0 {
			t.Errorf("bot stored pageviews %d visitors %d", st.Pageviews, st.Visitors)
		}
		// the stats window is flushed every 10 s
		waitFor(t, 30*time.Second, "bot count on EDGE_STATS", func() bool { return edgeStats(t, env.js, s.ID)["bot"] == 1 })
	})

	t.Run("a 4xx from umami goes to WEB_DLQ with the reason", func(t *testing.T) {
		// a site umami does not know: /api/send answers 400
		s := site{ID: uuid.NewString()}
		ev := pageview(t, s, newVT(t), "/gone", today.Add(-time.Hour))
		publish(t, env.js, ev)
		waitFor(t, time.Minute, "stream drained", func() bool { return drained(t, env.js) })
		var found bool
		for _, m := range streamMsgs(t, env.js, natsx.StreamWebDLQ) {
			if m.Subject != natsx.Subject(natsx.SubjectWebDLQ, s.ID) {
				continue
			}
			found = true
			reason := m.Header.Get(natsx.HeaderDLQReason)
			if !strings.Contains(reason, "http 400") || m.Header.Get(nats.MsgIdHdr) != ev.ID {
				t.Errorf("dlq reason %q, msg id %q", reason, m.Header.Get(nats.MsgIdHdr))
			}
			if _, err := events.DecodeWebEvent(m.Data); err != nil {
				t.Errorf("dlq copy is not the event: %v", err)
			}
		}
		if !found {
			t.Fatal("event not in WEB_DLQ")
		}
		waitFor(t, 30*time.Second, "dlq count on EDGE_STATS", func() bool { return edgeStats(t, env.js, s.ID)["dlq"] == 1 })
	})

	t.Run("an undecodable message goes to WEB_DLQ", func(t *testing.T) {
		siteID := uuid.NewString()
		if _, err := natsx.PublishDedup(testCtx(t, time.Minute), env.js, natsx.StreamWebEvents,
			natsx.Subject(natsx.SubjectWebEvents, siteID), uuid.NewString(), []byte("not json"), nil); err != nil {
			t.Fatal(err)
		}
		waitFor(t, time.Minute, "stream drained", func() bool { return drained(t, env.js) })
		var reason string
		for _, m := range streamMsgs(t, env.js, natsx.StreamWebDLQ) {
			if m.Subject == natsx.Subject(natsx.SubjectWebDLQ, siteID) {
				reason = m.Header.Get(natsx.HeaderDLQReason)
			}
		}
		if !strings.HasPrefix(reason, "not a valid web event") {
			t.Errorf("dlq reason %q", reason)
		}
	})

	t.Run("the duplicate guard skips an event already sent", func(t *testing.T) {
		s := newSite(t, admin)
		at := today.AddDate(0, 0, -1).Add(7 * time.Hour)
		ev := pageview(t, s, newVT(t), "/dup", at)
		kv, err := env.js.KeyValue(testCtx(t, time.Minute), natsx.BucketVisitState)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := kv.Put(testCtx(t, time.Minute), replayer.SentKey(ev.ID), []byte{'1'}); err != nil {
			t.Fatal(err)
		}
		before := scrape(t, rp.ops)[`replayer_outcomes_total{outcome="duplicate_skipped"}`]
		publish(t, env.js, ev)
		waitFor(t, time.Minute, "stream drained", func() bool { return drained(t, env.js) })
		st, err := admin.Stats(testCtx(t, time.Minute), s.ID, dayRange(at.Truncate(24*time.Hour)))
		if err != nil {
			t.Fatal(err)
		}
		after := scrape(t, rp.ops)[`replayer_outcomes_total{outcome="duplicate_skipped"}`]
		if st.Pageviews != 0 || after != before+1 {
			t.Errorf("pageviews %d, duplicate_skipped %v -> %v", st.Pageviews, before, after)
		}
	})

	t.Run("lag gauges return to zero", func(t *testing.T) {
		waitFor(t, 15*time.Second, "lag gauges at zero", func() bool {
			m := scrape(t, rp.ops)
			return m["replayer_consumer_pending_messages"] == 0 && m["replayer_consumer_ack_pending_messages"] == 0 &&
				m["replayer_in_flight_messages"] == 0 && m["replayer_oldest_pending_age_seconds"] == 0 &&
				m["replayer_consumer_info_timestamp_seconds"] > 0
		})
	})
}

// minuteSeries returns umami's pageviews per utc minute.
func minuteSeries(t *testing.T, a *umami.Admin, website string, r umami.Range) map[time.Time]int64 {
	t.Helper()
	pv, err := a.Pageviews(testCtx(t, time.Minute), website, umami.PageviewsQuery{Range: r, Unit: umami.UnitMinute, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	out := map[time.Time]int64{}
	for _, p := range pv.Pageviews {
		var ts time.Time
		var err error
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if ts, err = time.Parse(layout, p.X); err == nil {
				break
			}
		}
		if err != nil {
			t.Fatalf("pageviews bucket %q is in no known time format", p.X)
		}
		if p.Y > 0 {
			out[ts.UTC()] += p.Y
		}
	}
	return out
}

// outage: events published while umami (or its database) is down all
// arrive once it is back, with their original minute buckets, and nothing
// is stored twice. a dedicated umami and postgres are stopped, never the
// shared dev stack.
func TestOutage(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a dedicated umami")
	}
	env := startNATS(t)
	stack := startUmamiStack(t)
	rp := startReplayer(t, env.srv.URL, stack.URL, "it-outage")
	waitFor(t, 30*time.Second, "replayer to lead", func() bool { return scrapeOK(rp.ops)["replayer_leader"] == 1 })

	run := func(t *testing.T, stop, start func(), down string) {
		s := newSite(t, stack.Admin)
		const n = 500
		// the 58 minutes of events inside one utc day, so umami counts each
		// visitor once (pastHour)
		base := pastHour(3*time.Hour, time.Hour)
		want := map[time.Time]int64{}
		vts := make([]string, 50)
		for i := range vts {
			vts[i] = newVT(t)
		}
		stop()
		var evs []*events.WebEvent
		for i := range n {
			// 7 s apart: 500 events over 58 minutes, several per minute
			at := base.Add(time.Duration(i) * 7 * time.Second)
			want[at.Truncate(time.Minute)]++
			evs = append(evs, pageview(t, s, vts[i%len(vts)], fmt.Sprintf("/o/%d", i), at))
		}
		publish(t, env.js, evs...)
		// the events wait: held by the replayer, nothing acked
		time.Sleep(10 * time.Second)
		m := scrape(t, rp.ops)
		if m["replayer_in_flight_messages"] == 0 || m["replayer_oldest_pending_age_seconds"] < 5 {
			t.Errorf("while %s is down: in flight %v, oldest age %v", down, m["replayer_in_flight_messages"], m["replayer_oldest_pending_age_seconds"])
		}
		if drained(t, env.js) {
			t.Fatalf("stream drained while %s was down", down)
		}
		retriesBefore := m["replayer_retries_total"]
		if retriesBefore == 0 {
			t.Errorf("no retries while %s was down", down)
		}
		restarted := time.Now()
		start()
		waitFor(t, 4*time.Minute, "stream drained after "+down+" came back", func() bool { return drained(t, env.js) })
		t.Logf("%s down: %d events, drained %.1f s after restart, %v retries", down, n, time.Since(restarted).Seconds(), scrape(t, rp.ops)["replayer_retries_total"])

		r := umami.Range{Start: base, End: base.Add(time.Hour)}
		got := minuteSeries(t, stack.Admin, s.ID, r)
		if len(got) != len(want) {
			t.Errorf("%d minute buckets, want %d", len(got), len(want))
		}
		for minute, c := range want {
			if got[minute] != c {
				t.Errorf("minute %s: %d pageviews, want %d", minute.Format(time.TimeOnly), got[minute], c)
			}
		}
		paths := pathCounts(t, stack.Admin, s.ID, r, n+10)
		var dup int64
		for i := range n {
			c := paths[fmt.Sprintf("/o/%d", i)]
			if c == 0 {
				t.Errorf("/o/%d missing", i)
			}
			dup += max(c-1, 0)
		}
		if dup != 0 {
			t.Errorf("%d duplicates after the %s outage", dup, down)
		}
		st, err := stack.Admin.Stats(testCtx(t, time.Minute), s.ID, r)
		if err != nil {
			t.Fatal(err)
		}
		if st.Pageviews != n || st.Visitors != int64(len(vts)) {
			t.Errorf("pageviews %d visitors %d, want %d and %d", st.Pageviews, st.Visitors, n, len(vts))
		}
	}

	t.Run("umami stopped", func(t *testing.T) {
		run(t, func() { stopContainer(t, stack.um) }, func() {
			startContainer(t, stack.um)
			waitHeartbeat(t, stack.URL, 3*time.Minute)
		}, "umami")
	})
	t.Run("postgres stopped", func(t *testing.T) {
		run(t, func() { stopContainer(t, stack.pg) }, func() { startContainer(t, stack.pg) }, "postgres")
	})
}

// buildReplayer compiles cmd/replayer, so the restart test can kill a real
// process with SIGKILL.
func buildReplayer(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "replayer")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/replayer")
	cmd.Dir = natsctr.RepoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build replayer: %v\n%s", err, out)
	}
	return bin
}

type proc struct {
	cmd *exec.Cmd
	ops string
}

func startProc(t *testing.T, bin, natsURL, umamiURL, node string) *proc {
	t.Helper()
	ops := freeAddr(t)
	cmd := exec.Command(bin, "serve")
	cmd.Env = append(os.Environ(),
		"NATS_URL="+natsURL, "NATS_REPLICAS=1", "REPLAYER_NODE_ID="+node, "REPLAYER_OPS_ADDR="+ops,
		"UMAMI_URL="+umamiURL, "REPLAYER_WORKERS=16", "REPLAYER_MAX_IN_FLIGHT=512")
	cmd.Stdout = testWriter{t}
	cmd.Stderr = testWriter{t}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &proc{cmd: cmd, ops: "http://" + ops}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			_ = cmd.Wait()
		}
	})
	waitFor(t, 30*time.Second, node+" ops endpoint", func() bool {
		resp, err := httpGet(p.ops + "/healthz")
		return err == nil && resp == 200
	})
	return p
}

// restart: the active replayer is killed with SIGKILL mid-run. the standby
// takes the lock, nothing is lost, and events stored twice are at most the
// messages the killed process held in flight.
func TestRestartStandbyTakesOver(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and kills replayer processes")
	}
	umamiURL, admin := devUmami(t)
	env := startNATS(t)
	bin := buildReplayer(t)
	a := startProc(t, bin, env.srv.URL, umamiURL.String(), "it-a")
	waitFor(t, 30*time.Second, "a to lead", func() bool { return scrapeOK(a.ops)["replayer_leader"] == 1 })
	b := startProc(t, bin, env.srv.URL, umamiURL.String(), "it-b")

	s := newSite(t, admin)
	const n = 2000
	base := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Minute)
	vts := make([]string, 200)
	for i := range vts {
		vts[i] = newVT(t)
	}
	var evs []*events.WebEvent
	for i := range n {
		evs = append(evs, pageview(t, s, vts[i%len(vts)], fmt.Sprintf("/r/%d", i), base.Add(time.Duration(i)*time.Second)))
	}
	publish(t, env.js, evs...)

	var atKill map[string]float64
	waitFor(t, 2*time.Minute, "a to deliver a quarter", func() bool {
		atKill = scrapeOK(a.ops)
		return atKill[`replayer_outcomes_total{outcome="delivered"}`] >= n/4
	})
	if err := a.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	killed := time.Now()
	_ = a.cmd.Wait()
	inFlight := atKill["replayer_in_flight_messages"]
	deliveredByA := atKill[`replayer_outcomes_total{outcome="delivered"}`]

	waitFor(t, time.Minute, "b to lead", func() bool { return scrapeOK(b.ops)["replayer_leader"] == 1 })
	takeover := time.Since(killed)
	waitFor(t, 3*time.Minute, "stream drained by b", func() bool { return drained(t, env.js) })
	drainedAfter := time.Since(killed)

	r := umami.Range{Start: base, End: base.Add(time.Hour)}
	paths := pathCounts(t, admin, s.ID, r, n+100)
	var missing, dup int64
	for i := range n {
		c := paths[fmt.Sprintf("/r/%d", i)]
		if c == 0 {
			missing++
		}
		dup += max(c-1, 0)
	}
	mb := scrapeOK(b.ops)
	count, sum := mb[`replayer_send_duration_seconds_count{result="ok"}`], mb[`replayer_send_duration_seconds_sum{result="ok"}`]
	t.Logf("restart: %d events; a delivered %.0f before SIGKILL with %.0f in flight; b led %.1f s after the kill and drained %.1f s after it; b delivered %.0f, skipped %.0f as duplicates; missing %d, stored twice %d; b mean send latency %.1f ms over %.0f sends",
		n, deliveredByA, inFlight, takeover.Seconds(), drainedAfter.Seconds(),
		mb[`replayer_outcomes_total{outcome="delivered"}`], mb[`replayer_outcomes_total{outcome="duplicate_skipped"}`],
		missing, dup, 1000*sum/math.Max(count, 1), count)
	if missing != 0 {
		t.Errorf("%d events lost", missing)
	}
	if float64(dup) > inFlight {
		t.Errorf("%d events stored twice, more than the %.0f in flight at the kill", dup, inFlight)
	}
}

func httpGet(u string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}
