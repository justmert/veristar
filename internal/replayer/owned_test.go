package replayer

import (
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// gauge reads one unlabelled gauge from reg.
func gauge(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf.GetMetric()[0].GetGauge().GetValue()
		}
	}
	t.Fatalf("%s not registered", name)
	return 0
}

// holds that start together each refresh the ownership gauges. counting the
// holds and setting the gauges must be one step: when a refresh that counted
// fewer holds sets the gauge after one that counted more, the gauge keeps
// the smaller count until the next hold starts or ends. that kept a node
// restarted in TestPartitionedReplayersOnCluster short of the partitions it
// consumed until the test gave up.
func TestSetOwnedKeepsTheLastCount(t *testing.T) {
	zero := func() float64 { return 0 }
	for round := range 300 {
		reg := prometheus.NewRegistry()
		m, err := newMetrics(reg, 1, zero, zero)
		if err != nil {
			t.Fatal(err)
		}
		r := &Replayer{m: m, leads: map[int]*leadRun{}}
		var wg sync.WaitGroup
		for p := range 32 {
			wg.Go(func() {
				r.mu.Lock()
				r.leads[p] = &leadRun{part: p}
				r.mu.Unlock()
				r.setOwned()
			})
		}
		wg.Wait()
		if got := gauge(t, reg, "replayer_partitions_owned"); got != 32 {
			t.Fatalf("round %d: replayer_partitions_owned is %v with 32 holds", round, got)
		}
		if got := gauge(t, reg, "replayer_leader"); got != 1 {
			t.Fatalf("round %d: replayer_leader is %v with 32 holds", round, got)
		}
	}
}
