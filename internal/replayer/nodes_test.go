package replayer_test

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/umami"
)

// TestDrainRateByReplayerNodes measures how fast 1, 2 and 3 replayer nodes
// drain one backlog on a 3-node nats cluster. each node delivers to its own
// umami processes (REPLAYER_NODES_UMAMI per node, default 1, each capped at
// one core) and all of them share one postgres, as the core nodes share the
// patroni leader. the nodes start with their member keys already in the
// locks bucket, as nodes that were running before, so they split the
// partitions from the first second and the time is the drain alone. it is a
// measurement, not a check, and runs only with
// REPLAYER_NODES_MEASURE=<events per run>.
func TestDrainRateByReplayerNodes(t *testing.T) {
	raw := os.Getenv("REPLAYER_NODES_MEASURE")
	if raw == "" {
		t.Skip("set REPLAYER_NODES_MEASURE to the number of events per run to measure")
	}
	total, err := strconv.Atoi(raw)
	if err != nil || total < 600 {
		t.Fatalf("REPLAYER_NODES_MEASURE=%q: want an event count of at least 600", raw)
	}
	perNode := 1
	if v := os.Getenv("REPLAYER_NODES_UMAMI"); v != "" {
		if perNode, err = strconv.Atoi(v); err != nil || perNode < 1 {
			t.Fatalf("REPLAYER_NODES_UMAMI=%q", v)
		}
	}
	counts := []int{1, 2, 3}
	if v := os.Getenv("REPLAYER_NODES_COUNTS"); v != "" {
		counts = counts[:0]
		for f := range strings.SplitSeq(v, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(f))
			if err != nil || n < 1 || n > 3 {
				t.Fatalf("REPLAYER_NODES_COUNTS=%q: each count must be 1 to 3", v)
			}
			counts = append(counts, n)
		}
	}
	const workers, inFlight = 128, 900
	oneCore := 1.0
	st := startUmamiProcesses(t, 3*perNode, &oneCore)
	cluster, js := clusterNATS(t)
	nodeURLs := func(i int) []*url.URL { return st.URLs[i*perNode : (i+1)*perNode] }
	const perVisitor = 6
	h := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	backlog := func(n int) (site, int) {
		s := newSite(t, st.Admin)
		var evs []*events.WebEvent
		for range n / perVisitor {
			vt := newVT(t)
			for j := range perVisitor {
				evs = append(evs, pageview(t, s, vt, fmt.Sprintf("/p%d", j), h.Add(time.Duration(j)*time.Minute)))
			}
		}
		publish(t, js, evs...)
		return s, len(evs)
	}
	locks, err := js.KeyValue(testCtx(t, time.Minute), natsx.BucketLocks)
	if err != nil {
		t.Fatal(err)
	}
	register := func(names []string) {
		for _, n := range names {
			if _, err := locks.Put(testCtx(t, 10*time.Second), natsx.LockReplayer+"-member."+n, []byte("measure")); err != nil {
				t.Fatal(err)
			}
		}
	}
	run := func(n int, label string) (time.Duration, []map[string]float64) {
		var names []string
		for i := range n {
			names = append(names, fmt.Sprintf("%s-%d", label, i+1))
		}
		register(names)
		start := time.Now()
		var rps []*running
		for i, name := range names {
			rps = append(rps, startReplayerWith(t, cluster.URLs(), nodeURLs(i), name, workers, inFlight, 3))
		}
		waitFor(t, 60*time.Minute, "stream drained", func() bool { return drained(t, js) })
		elapsed := time.Since(start)
		var ms []map[string]float64
		for _, rp := range rps {
			ms = append(ms, scrape(t, rp.ops))
			rp.stop(t)
		}
		return elapsed, ms
	}
	// warm every umami process first: node compiles umami's hot paths during
	// its first few thousand requests
	backlog(600 * 3 * perNode)
	run(3, "warm")
	t.Logf("replayer workers %d and in flight %d per node, %d umami process(es) per node at one core each, %d partitions",
		workers, inFlight, perNode, events.Partitions)
	for _, n := range counts {
		s, sent := backlog(total)
		load0 := loadAvg()
		used := st.ums[:n*perNode]
		cpu0 := cpuUsec(t, used)
		pg0 := cpuUsec(t, []*testcontainers.DockerContainer{st.pg})[0]
		elapsed, ms := run(n, fmt.Sprintf("measure%d", n))
		cpu := cpuUsec(t, used)
		pgCores := float64(cpuUsec(t, []*testcontainers.DockerContainer{st.pg})[0]-pg0) / float64(elapsed.Microseconds())
		var cores []string
		for i := range used {
			cores = append(cores, fmt.Sprintf("%.2f", float64(cpu[i]-cpu0[i])/float64(elapsed.Microseconds())))
		}
		stats, err := st.Admin.Stats(testCtx(t, time.Minute), s.ID, umami.Range{Start: h, End: h.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if stats.Pageviews != int64(sent) {
			t.Fatalf("%d nodes: umami holds %d pageviews, want %d", n, stats.Pageviews, sent)
		}
		var perNodeSends []string
		for _, m := range ms {
			perNodeSends = append(perNodeSends, fmt.Sprintf("%.0f", m[`replayer_outcomes_total{outcome="delivered"}`]))
		}
		var sendSum, sendCount float64
		for _, m := range ms {
			sendSum += m[`replayer_send_duration_seconds_sum{result="ok"}`]
			sendCount += m[`replayer_send_duration_seconds_count{result="ok"}`]
		}
		t.Logf("replayer nodes %d: %d events in %.1f s = %.0f events/s; delivered per node %s; umami cpu per process (cores) %s; postgres %.2f cores; mean send %.0f ms; host load average %s at the start, %s at the end",
			n, sent, elapsed.Seconds(), float64(sent)/elapsed.Seconds(), strings.Join(perNodeSends, " "), strings.Join(cores, " "),
			pgCores, 1000*sendSum/max(sendCount, 1), load0, loadAvg())
	}
}

// loadAvg is the host's 1, 5 and 15 minute load average.
func loadAvg() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "unknown"
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return "unknown"
	}
	return strings.Join(f[:3], " ")
}
