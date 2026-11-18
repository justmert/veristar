package replayer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// while only one kind of web event is queued, as while the legacy consumer
// drains during the core tier of an upgrade, the barrier clears a whole
// fetch at once instead of one message per read: a read per message at the
// barrier's pace capped the legacy drain at 40 events a second.
func TestSeqBarrierClearsABatchWhenTheOtherKindIsEmpty(t *testing.T) {
	srv := natsctr.StartSingle(t)
	nc, err := nats.Connect(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	// the earlier release's stream, then this release's topology over it
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name: natsx.StreamWebEvents, Subjects: []string{natsx.WebEventsLegacy}, Retention: jetstream.WorkQueuePolicy,
		Storage: jetstream.FileStorage, Replicas: 1, MaxAge: 72 * time.Hour, Duplicates: 2 * time.Minute,
		Discard: jetstream.DiscardOld, MaxMsgs: -1, MaxBytes: -1, MaxMsgSize: -1,
	}); err != nil {
		t.Fatal(err)
	}
	spec, err := natsx.Topology(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := natsx.Apply(ctx, js, spec); err != nil {
		t.Fatal(err)
	}
	const n = fetchBatch * 3
	seqs := make([]uint64, 0, n)
	for i := range n {
		ack, err := natsx.PublishDedup(ctx, js, natsx.StreamWebEvents, "web.events.site-a", fmt.Sprintf("old-%d", i), []byte("x"), nil)
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, ack.Sequence)
	}

	// the legacy hold clears its messages against the partitions, which
	// hold nothing, one message at a time as fetchLoop does
	b := newSeqBarrier(js, natsx.WebEventsPartitioned, false)
	start := time.Now()
	for _, seq := range seqs {
		b.fetched(seq)
		if !b.wait(ctx, nil, seq) {
			t.Fatal("wait ended without clearing")
		}
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("clearing %d messages took %s, %s per message", n, d, d/n)
	}

	// a partitioned event queued later than the legacy ones holds back
	// nothing below it, and a legacy one after it waits
	ack, err := natsx.PublishDedup(ctx, js, natsx.StreamWebEvents, "web.events.site-a.p03", "new-0", []byte("x"), nil)
	if err != nil {
		t.Fatal(err)
	}
	late, err := natsx.PublishDedup(ctx, js, natsx.StreamWebEvents, "web.events.site-a", "old-late", []byte("x"), nil)
	if err != nil {
		t.Fatal(err)
	}
	b.fetched(late.Sequence)
	wctx, wcancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer wcancel()
	if b.wait(wctx, nil, late.Sequence) {
		t.Fatalf("legacy event %d cleared while partitioned event %d before it is queued", late.Sequence, ack.Sequence)
	}
}
