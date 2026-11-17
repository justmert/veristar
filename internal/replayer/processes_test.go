package replayer_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/umami"
)

// several umami processes sharing one database and one APP_SECRET give a
// visitor the same session and visit wherever an event lands. umami derives
// the session id from website, address, user agent, the salt of the event's
// day and the client salt, and the visit id from the session and the event's
// utc hour, each hashed with APP_SECRET (src/lib/collection-session.ts,
// src/lib/crypto.ts); the cache token is a jwt signed with the same secret
// (src/lib/jwt.ts). nothing of it is process state.
func TestUmamiProcessesAgreeOnSessionAndVisit(t *testing.T) {
	st := startUmamiProcesses(t, 2, nil)
	s := newSite(t, st.Admin)
	a, err := umami.New(st.URLs[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := umami.New(st.URLs[1], nil)
	if err != nil {
		t.Fatal(err)
	}
	vt := newVT(t)
	// two hours back, so every timestamp below lies in the past, and the
	// five events (135 minutes) inside one utc day
	h := pastHour(2*time.Hour, 135*time.Minute)
	send := func(c *umami.Client, at time.Time, path, cache string) umami.SendResult {
		t.Helper()
		res, err := c.Send(testCtx(t, time.Minute), umami.SendRequest{
			Event: umami.Event{
				Website: s.ID, Hostname: "rp.example", URL: path, Title: "rp", Screen: "1920x1080",
				Language: "en-US", Timestamp: at.Unix(), Salt: vt,
			},
			UserAgent: desktopUA, Country: "DE", Cache: cache,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Bot || res.SessionID == "" || res.VisitID == "" || res.Cache == "" {
			t.Fatalf("answer %+v", res)
		}
		return res
	}

	first := send(a, h.Add(10*time.Minute), "/1", "")
	// another process, same hour, no cache token: the same session and visit
	second := send(b, h.Add(20*time.Minute), "/2", "")
	if second.SessionID != first.SessionID || second.VisitID != first.VisitID {
		t.Fatalf("process 2 derived session %s visit %s, process 1 %s %s", second.SessionID, second.VisitID, first.SessionID, first.VisitID)
	}
	// the next hour with process 1's cache token, sent to process 2: the
	// token verifies there and keeps the visit. a token process 2 could not
	// read would give the next hour's visit instead.
	third := send(b, h.Add(70*time.Minute), "/3", first.Cache)
	if third.SessionID != first.SessionID || third.VisitID != first.VisitID {
		t.Fatalf("process 2 did not continue process 1's visit: %+v, first %+v", third, first)
	}
	// the hour after without a token: a new visit, the same on both
	fourth := send(a, h.Add(130*time.Minute), "/4", "")
	fifth := send(b, h.Add(135*time.Minute), "/5", "")
	if fourth.SessionID != first.SessionID || fifth.SessionID != first.SessionID {
		t.Fatalf("session changed: %s %s, want %s", fourth.SessionID, fifth.SessionID, first.SessionID)
	}
	if fourth.VisitID == first.VisitID || fifth.VisitID != fourth.VisitID {
		t.Fatalf("visits %s %s, first %s", fourth.VisitID, fifth.VisitID, first.VisitID)
	}

	// umami's own counts agree: one visitor, two visits, five views
	stats, err := st.Admin.Stats(testCtx(t, time.Minute), s.ID, umami.Range{Start: h, End: h.Add(3 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Visitors != 1 || stats.Visits != 2 || stats.Pageviews != 5 {
		t.Errorf("visitors %d visits %d pageviews %d, want 1, 2, 5", stats.Visitors, stats.Visits, stats.Pageviews)
	}
}

// the replayer spreads visitors over the umami processes and the stored
// counts are exactly those of one process: per visitor one session, and the
// visits that the cache token and umami's hour rule give.
func TestReplayerSpreadsVisitorsOverUmamiProcesses(t *testing.T) {
	const visitors = 40
	st := startUmamiProcesses(t, 2, nil)
	env := startNATS(t)
	s := newSite(t, st.Admin)
	// the three events of a visitor span 140 minutes inside one utc day
	h := pastHour(4*time.Hour, 140*time.Minute)
	var evs []*events.WebEvent
	for i := range visitors {
		vt := newVT(t)
		evs = append(evs,
			// 15 minutes apart across the hour: the cache token keeps the visit
			pageview(t, s, vt, fmt.Sprintf("/v%d/1", i), h.Add(50*time.Minute)),
			pageview(t, s, vt, fmt.Sprintf("/v%d/2", i), h.Add(65*time.Minute)),
			// 75 minutes later: no token, a second visit
			pageview(t, s, vt, fmt.Sprintf("/v%d/3", i), h.Add(140*time.Minute)),
		)
	}
	publish(t, env.js, evs...)
	rp := startReplayerTo(t, env.srv.URL, st.URLs, "it-spread", 16, 512)
	waitFor(t, 2*time.Minute, "stream drained", func() bool { return drained(t, env.js) })

	stats, err := st.Admin.Stats(testCtx(t, time.Minute), s.ID, umami.Range{Start: h, End: h.Add(4 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Visitors != visitors || stats.Visits != 2*visitors || stats.Pageviews != 3*visitors {
		t.Errorf("visitors %d visits %d pageviews %d, want %d, %d, %d",
			stats.Visitors, stats.Visits, stats.Pageviews, visitors, 2*visitors, 3*visitors)
	}
	m := scrape(t, rp.ops)
	p0, p1 := m[`replayer_umami_sends_total{process="0"}`], m[`replayer_umami_sends_total{process="1"}`]
	if p0+p1 != 3*visitors || p0 == 0 || p1 == 0 {
		t.Errorf("sends per process %v and %v, want both above 0 and %d together", p0, p1, 3*visitors)
	}
	// whole visitors move: every count is a multiple of a visitor's 3 events
	if int(p0)%3 != 0 || int(p1)%3 != 0 {
		t.Errorf("a visitor's events were split: %v and %v", p0, p1)
	}
}

// TestDrainRateByUmamiProcesses measures how fast one replayer drains a
// backlog into 1, 2 and 3 umami processes on one postgres. each umami
// container is capped at one core so the result reflects the process count,
// not how idle the shared host happens to be. it is a measurement, not a
// check, and runs only with REPLAYER_DRAIN_MEASURE=<events per run>
// (docs/reference/drills/2026-11-13.md records the numbers).
func TestDrainRateByUmamiProcesses(t *testing.T) {
	raw := os.Getenv("REPLAYER_DRAIN_MEASURE")
	if raw == "" {
		t.Skip("set REPLAYER_DRAIN_MEASURE to the number of events per run to measure")
	}
	total, err := strconv.Atoi(raw)
	if err != nil || total < 600 {
		t.Fatalf("REPLAYER_DRAIN_MEASURE=%q: want an event count of at least 600", raw)
	}
	maxProcs := 3
	if v := os.Getenv("REPLAYER_DRAIN_PROCESSES"); v != "" {
		if maxProcs, err = strconv.Atoi(v); err != nil || maxProcs < 1 {
			t.Fatalf("REPLAYER_DRAIN_PROCESSES=%q", v)
		}
	}
	oneCore := 1.0
	st := startUmamiProcesses(t, maxProcs, &oneCore)
	env := startNATS(t)
	// six events per visitor, a minute apart, an hour back
	const perVisitor = 6
	h := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	backlog := func(n int) (site, []*events.WebEvent) {
		s := newSite(t, st.Admin)
		var evs []*events.WebEvent
		for range n / perVisitor {
			vt := newVT(t)
			for j := range perVisitor {
				evs = append(evs, pageview(t, s, vt, fmt.Sprintf("/p%d", j), h.Add(time.Duration(j)*time.Minute)))
			}
		}
		publish(t, env.js, evs...)
		return s, evs
	}
	// warm every process first: node compiles umami's hot paths during its
	// first few thousand requests, which would bill the newest process of
	// each run with that work
	backlog(600 * maxProcs)
	warm := startReplayerTo(t, env.srv.URL, st.URLs, "warmup", 128, 900)
	waitFor(t, 10*time.Minute, "warm-up drained", func() bool { return drained(t, env.js) })
	warm.stop(t)
	workers, inFlight := 128, 900
	for key, dst := range map[string]*int{"REPLAYER_DRAIN_WORKERS": &workers, "REPLAYER_DRAIN_IN_FLIGHT": &inFlight} {
		if v := os.Getenv(key); v != "" {
			if *dst, err = strconv.Atoi(v); err != nil || *dst < 1 {
				t.Fatalf("%s=%q", key, v)
			}
		}
	}
	if inFlight < workers {
		t.Fatalf("in flight %d is below workers %d", inFlight, workers)
	}
	t.Logf("replayer workers %d, in flight %d", workers, inFlight)
	// REPLAYER_DRAIN_COUNTS=2,6 measures only those process counts
	counts := make([]int, 0, maxProcs)
	for n := 1; n <= maxProcs; n++ {
		counts = append(counts, n)
	}
	if v := os.Getenv("REPLAYER_DRAIN_COUNTS"); v != "" {
		counts = counts[:0]
		for f := range strings.SplitSeq(v, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(f))
			if err != nil || n < 1 || n > maxProcs {
				t.Fatalf("REPLAYER_DRAIN_COUNTS=%q: each count must be 1 to REPLAYER_DRAIN_PROCESSES", v)
			}
			counts = append(counts, n)
		}
	}
	for _, n := range counts {
		s, evs := backlog(total)
		// the store a backlog takes on each server: short page views with a
		// path, a desktop user agent, a title, screen and language
		if sinfo, err := (func() (*jetstream.StreamInfo, error) {
			ws, err := env.js.Stream(testCtx(t, 10*time.Second), natsx.StreamWebEvents)
			if err != nil {
				return nil, err
			}
			return ws.Info(testCtx(t, 10*time.Second))
		})(); err == nil && sinfo.State.Msgs > 0 {
			t.Logf("WEB_EVENTS holds %d messages in %d bytes, %.0f bytes per message", sinfo.State.Msgs, sinfo.State.Bytes,
				float64(sinfo.State.Bytes)/float64(sinfo.State.Msgs))
		}
		cpu0 := cpuUsec(t, st.ums[:n])
		pg0 := cpuUsec(t, []*testcontainers.DockerContainer{st.pg})[0]
		start := time.Now()
		// the core role's lanes and in-flight bound, unless
		// REPLAYER_DRAIN_WORKERS and REPLAYER_DRAIN_IN_FLIGHT override them
		rp := startReplayerTo(t, env.srv.URL, st.URLs[:n], fmt.Sprintf("measure-%d", n), workers, inFlight)
		waitFor(t, 30*time.Minute, "stream drained", func() bool { return drained(t, env.js) })
		elapsed := time.Since(start)
		cpu := cpuUsec(t, st.ums[:n])
		pgCores := float64(cpuUsec(t, []*testcontainers.DockerContainer{st.pg})[0]-pg0) / float64(elapsed.Microseconds())
		m := scrape(t, rp.ops)
		rp.stop(t)
		stats, err := st.Admin.Stats(testCtx(t, time.Minute), s.ID, umami.Range{Start: h, End: h.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if stats.Pageviews != int64(len(evs)) {
			t.Fatalf("%d processes: umami holds %d pageviews, want %d", n, stats.Pageviews, len(evs))
		}
		var cores, sends []string
		for i := range n {
			cores = append(cores, fmt.Sprintf("%.2f", float64(cpu[i]-cpu0[i])/float64(elapsed.Microseconds())))
			sends = append(sends, fmt.Sprintf("%.0f", m[fmt.Sprintf(`replayer_umami_sends_total{process="%d"}`, i)]))
		}
		rate := float64(len(evs)) / elapsed.Seconds()
		// mean time of one send to umami, from the replayer's own histogram
		var sendSum, sendCount float64
		for k, v := range m {
			if strings.HasPrefix(k, "replayer_send_duration_seconds_sum") {
				sendSum += v
			}
			if strings.HasPrefix(k, "replayer_send_duration_seconds_count") {
				sendCount += v
			}
		}
		if sendCount > 0 {
			t.Logf("umami processes %d: mean send %.1f ms over %.0f sends", n, sendSum/sendCount*1000, sendCount)
		}
		t.Logf("umami processes %d: %d events in %.1f s = %.0f events/s; cpu per umami process (cores) %s; postgres %.2f cores, %.2f ms of cpu per event; sends per process %s",
			n, len(evs), elapsed.Seconds(), rate, strings.Join(cores, " "), pgCores, pgCores*1000/rate, strings.Join(sends, " "))
	}
}

// cpuUsec reads each container's cgroup cpu time in microseconds.
func cpuUsec(t *testing.T, cs []*testcontainers.DockerContainer) []int64 {
	t.Helper()
	out := make([]int64, len(cs))
	for i, c := range cs {
		code, r, err := c.Exec(context.Background(), []string{"cat", "/sys/fs/cgroup/cpu.stat"}, tcexec.Multiplexed())
		if err != nil || code != 0 {
			t.Fatalf("read cpu.stat: code %d, %v", code, err)
		}
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(string(b), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "usage_usec "); ok {
				if out[i], err = strconv.ParseInt(v, 10, 64); err != nil {
					t.Fatal(err)
				}
			}
		}
		if out[i] == 0 {
			t.Fatalf("no usage_usec in %q", b)
		}
	}
	return out
}
