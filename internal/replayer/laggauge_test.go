package replayer

import (
	"context"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
)

// blockingInfo is a consumer whose Info waits until release is closed.
type blockingInfo struct {
	jetstream.Consumer
	reading chan struct{}
	release chan struct{}
}

func (c *blockingInfo) Info(context.Context) (*jetstream.ConsumerInfo, error) {
	close(c.reading)
	<-c.release
	return &jetstream.ConsumerInfo{NumPending: 7, NumAckPending: 2}, nil
}

// a hold that ends while its consumer info is read leaves no partition
// gauge behind: a stale one would stay for good and count the partition
// twice once another instance owns it.
func TestLagSampleAfterHoldEndedLeavesNoGauge(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := newMetrics(reg, 1, func() float64 { return 0 }, func() float64 { return 0 })
	if err != nil {
		t.Fatal(err)
	}
	r := &Replayer{m: m, log: slog.New(slog.DiscardHandler), leads: map[int]*leadRun{}}
	cons := &blockingInfo{reading: make(chan struct{}), release: make(chan struct{})}
	lr := &leadRun{r: r, part: 5, name: "replayer-p05", label: "p05", cons: cons, held: map[uint64]*held{}}
	r.leads[lr.part] = lr

	done := make(chan struct{})
	go func() {
		r.sampleConsumers(t.Context())
		close(done)
	}()
	<-cons.reading
	r.endLead(lr)
	close(cons.release)
	<-done

	if n := seriesCount(m.partitionPending); n != 0 {
		t.Fatalf("%d partition pending series left after the hold ended", n)
	}
	if n := seriesCount(m.partitionOldest); n != 0 {
		t.Fatalf("%d partition age series left after the hold ended", n)
	}

	// a hold that is still on keeps its gauges
	cons2 := &blockingInfo{reading: make(chan struct{}), release: make(chan struct{})}
	close(cons2.release)
	lr2 := &leadRun{r: r, part: 6, name: "replayer-p06", label: "p06", cons: cons2, held: map[uint64]*held{}}
	r.leads[lr2.part] = lr2
	r.sampleConsumers(t.Context())
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := -1.0
	for _, mf := range mfs {
		if mf.GetName() == "replayer_partition_pending_messages" && len(mf.GetMetric()) == 1 {
			got = mf.GetMetric()[0].GetGauge().GetValue()
		}
	}
	if got != 7 {
		t.Fatalf("pending gauge of the held partition %v, want 7", got)
	}
}

func seriesCount(c prometheus.Collector) int {
	ch := make(chan prometheus.Metric, 64)
	c.Collect(ch)
	close(ch)
	return len(ch)
}
