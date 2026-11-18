package replayer_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// clusterNATS is a 3-node nats cluster with the full topology at 3 replicas.
func clusterNATS(t *testing.T) (*natsctr.Cluster, jetstream.JetStream) {
	t.Helper()
	c := natsctr.StartCluster(t)
	nc, err := nats.Connect(c.URLs(), nats.MaxReconnects(-1), nats.ReconnectWait(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := natsx.Topology(3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := natsx.Apply(testCtx(t, 2*time.Minute), js, spec); err != nil {
		t.Fatal(err)
	}
	return c, js
}

// node is one replayer process with its own umami proxies, as one core
// node runs it.
type node struct {
	name  string
	umami string
	p     *proc
}

func startNode(t *testing.T, bin, natsURLs, umamiURLs, name string, workers, maxInFlight int) *proc {
	t.Helper()
	ops := freeAddr(t)
	cmd := exec.Command(bin, "serve")
	cmd.Env = append(os.Environ(),
		"NATS_URL="+natsURLs, "NATS_REPLICAS=3", "REPLAYER_NODE_ID="+name, "REPLAYER_OPS_ADDR="+ops,
		"UMAMI_URL="+umamiURLs, fmt.Sprintf("REPLAYER_WORKERS=%d", workers), fmt.Sprintf("REPLAYER_MAX_IN_FLIGHT=%d", maxInFlight))
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
	waitFor(t, 30*time.Second, name+" ops endpoint", func() bool {
		code, err := httpGet(p.ops + "/healthz")
		return err == nil && code == 200
	})
	return p
}

// owned reads replayer_partitions_owned of each running node.
func owned(nodes []*node) map[string]int {
	out := map[string]int{}
	for _, n := range nodes {
		if n.p == nil || n.p.cmd.ProcessState != nil {
			continue
		}
		out[n.name] = int(scrapeOK(n.p.ops)["replayer_partitions_owned"])
	}
	return out
}

func balanced(got map[string]int, live int) bool {
	sum := 0
	for _, n := range got {
		if n < events.Partitions/live || n > (events.Partitions+live-1)/live {
			return false
		}
		sum += n
	}
	return len(got) == live && sum == events.Partitions
}

// three replayer processes on a 3-node nats cluster share the 32 partitions
// evenly and each delivers to its own umami proxy. while events keep
// arriving, one node is killed with SIGKILL and started again, and another
// is stopped with SIGTERM and started again, as a rolling deploy does. every
// visitor's events reach umami in order, no two nodes ever send for one
// visitor at the same time, nothing is lost, and the only repeats are sends
// the killed node had in flight (at most one per delivery slot).
func TestPartitionedReplayersOnCluster(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a nats cluster, umami and three replayer processes")
	}
	const workers = 16
	cluster, js := clusterNATS(t)
	st := startUmamiProcesses(t, 2, nil)
	s := newSite(t, st.Admin)
	bin := buildReplayer(t)
	log := &sendLog{}
	nodes := make([]*node, 3)
	for i := range nodes {
		name := fmt.Sprintf("core-%d", i+1)
		var urls []string
		for _, u := range st.URLs {
			urls = append(urls, newSendProxy(t, log, name, u).String())
		}
		nodes[i] = &node{name: name, umami: strings.Join(urls, ",")}
	}
	start := func(n *node) { n.p = startNode(t, bin, cluster.URLs(), n.umami, n.name, workers, 512) }
	for _, n := range nodes {
		start(n)
	}
	waitFor(t, 90*time.Second, "the partitions spread over three nodes", func() bool { return balanced(owned(nodes), 3) })
	t.Logf("partitions owned at start: %v", owned(nodes))

	// each visitor sends an event on two of three 500 ms ticks across the
	// run; each event's path names the visitor and its position
	const visitors = 60
	base := pastHour(3*time.Hour, 2*time.Hour)
	vts := make([]string, visitors)
	for i := range vts {
		vts[i] = newVT(t)
	}
	var published atomic.Int64
	next := make([]int, visitors)
	// the stop is seen between ticks only, so a batch is published whole
	// and the counts below match what is in the stream
	stop := make(chan struct{})
	stopPub := func() { close(stop) }
	pubCtx := context.Background()
	var pubWG sync.WaitGroup
	pubWG.Go(func() {
		tk := time.NewTicker(500 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
			}
			var evs []*events.WebEvent
			for i := range visitors {
				if rand.IntN(3) == 0 {
					continue
				}
				evs = append(evs, pageview(t, s, vts[i], visitorPath(i, next[i]), base.Add(time.Duration(next[i])*time.Second)))
				next[i]++
			}
			publishCtx(t, pubCtx, js, evs...)
			published.Add(int64(len(evs)))
		}
	})

	time.Sleep(10 * time.Second)
	killed := scrape(t, nodes[1].p.ops)
	if err := nodes[1].p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = nodes[1].p.cmd.Wait()
	t.Logf("killed %s holding %.0f messages in %.0f partitions", nodes[1].name, killed["replayer_in_flight_messages"], killed["replayer_partitions_owned"])
	waitFor(t, 2*time.Minute, "two nodes own every partition", func() bool { return balanced(owned(nodes), 2) })
	start(nodes[1])
	waitFor(t, 2*time.Minute, "three nodes own every partition again", func() bool { return balanced(owned(nodes), 3) })
	if err := nodes[2].p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := nodes[2].p.cmd.Wait(); err != nil {
		t.Errorf("%s did not stop cleanly: %v", nodes[2].name, err)
	}
	waitFor(t, time.Minute, "two nodes own every partition after the stop", func() bool { return balanced(owned(nodes), 2) })
	start(nodes[2])
	waitFor(t, 2*time.Minute, "three nodes own every partition after the restart", func() bool { return balanced(owned(nodes), 3) })
	stopPub()
	pubWG.Wait()
	waitFor(t, 5*time.Minute, "stream drained", func() bool { return drained(t, js) })

	rep := log.checkOrder()
	t.Logf("published %d; stored sends %d (by node %v), repeats %d, most nodes per visitor %d",
		published.Load(), rep.stored, rep.byNode, rep.repeats, rep.nodesPerVisitor)
	for _, o := range rep.outOfOrder {
		t.Error(o)
	}
	for _, o := range rep.overlaps {
		t.Error(o)
	}
	for _, r := range rep.repeated {
		t.Logf("sent twice: %s", r)
	}
	if rep.repeats > workers {
		t.Errorf("%d events stored twice, more than the %d delivery slots of the killed node", rep.repeats, workers)
	}
	paths := pathCounts(t, st.Admin, s.ID, umami.Range{Start: base.Add(-time.Hour), End: base.Add(3 * time.Hour)}, int(published.Load())+100)
	var missing, twice int64
	for i := range visitors {
		for j := range next[i] {
			c := paths[visitorPath(i, j)]
			if c == 0 {
				missing++
			}
			twice += max(c-1, 0)
		}
	}
	if missing != 0 {
		t.Errorf("%d events lost", missing)
	}
	// umami can never hold more copies than it answered 200 for
	if twice > int64(rep.repeats) {
		t.Errorf("umami stored %d events twice, the send log shows only %d repeats", twice, rep.repeats)
	}
	t.Logf("umami stored %d events twice", twice)
	if rep.nodesPerVisitor < 2 {
		t.Error("no visitor moved between nodes; the kill and restart did not move partitions")
	}
}

// publishCtx publishes events on their partition subjects.
func publishCtx(t *testing.T, ctx context.Context, js jetstream.JetStream, evs ...*events.WebEvent) {
	t.Helper()
	for _, ev := range evs {
		b, err := ev.Marshal()
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := natsx.PublishDedup(ctx, js, natsx.StreamWebEvents, ev.Subject(), ev.ID, b, nil); err != nil {
			t.Error(err)
			return
		}
	}
}
