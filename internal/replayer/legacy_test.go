package replayer_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// previousTopology creates WEB_EVENTS and its consumer as the releases
// before the partitions declared them: one subject per site and one
// unfiltered consumer, then applies the current topology over it, as the
// bootstrap step of an upgrade does.
func previousTopology(t *testing.T, js jetstream.JetStream) {
	t.Helper()
	ctx := testCtx(t, time.Minute)
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name: natsx.StreamWebEvents, Description: "accepted web events, subject token is the site id",
		Subjects: []string{natsx.SubjectWebEvents + ".*"}, Retention: jetstream.WorkQueuePolicy,
		Storage: jetstream.FileStorage, Replicas: 1, MaxAge: 72 * time.Hour, Duplicates: 2 * time.Minute,
		Discard: jetstream.DiscardOld, MaxMsgs: -1, MaxBytes: -1, MaxMsgSize: -1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateConsumer(ctx, natsx.StreamWebEvents, jetstream.ConsumerConfig{
		Durable: natsx.ConsumerReplayer, DeliverPolicy: jetstream.DeliverAllPolicy, AckPolicy: jetstream.AckExplicitPolicy,
		AckWait: natsx.AckWait, MaxDeliver: -1,
	}); err != nil {
		t.Fatal(err)
	}
}

func publishLegacy(t *testing.T, js jetstream.JetStream, evs ...*events.WebEvent) {
	t.Helper()
	ctx := testCtx(t, time.Minute)
	for _, ev := range evs {
		b, err := ev.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := natsx.PublishDedup(ctx, js, natsx.StreamWebEvents, ev.LegacySubject(), ev.ID, b, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// an upgrade from the single web subject: events queued by gateways of the
// earlier release wait on the legacy subject while gateways of this release
// queue the same visitors' later events on the partitions, interleaved.
// while a replayer of the earlier release still holds the legacy lock,
// nothing newer than its oldest legacy event reaches umami. once it is
// gone, two replayers of this release drain both kinds: every visitor's
// events reach umami in acceptance order, each exactly once, and the legacy
// subject can be retired afterwards.
func TestLegacyDrainKeepsOrder(t *testing.T) {
	st := startUmamiProcesses(t, 1, nil)
	srv := natsctr.StartSingle(t)
	env := natsOn(t, srv)
	previousTopology(t, env.js)
	spec, err := natsx.Topology(1)
	if err != nil {
		t.Fatal(err)
	}
	// the earlier release's events queued before the topology changed
	s := newSite(t, st.Admin)
	const visitors, perVisitor = 40, 12
	h := pastHour(3*time.Hour, time.Hour)
	vts := make([]string, visitors)
	for i := range vts {
		vts[i] = newVT(t)
	}
	ev := func(i, j int) *events.WebEvent {
		return pageview(t, s, vts[i], visitorPath(i, j), h.Add(time.Duration(j)*time.Minute))
	}
	for i := range visitors {
		publishLegacy(t, env.js, ev(i, 0), ev(i, 1))
	}
	if _, err := natsx.Apply(testCtx(t, time.Minute), env.js, spec); err != nil {
		t.Fatal(err)
	}
	// both kinds of gateway keep accepting: each later event goes to one
	// kind or the other, so a visitor's events alternate between them
	for j := 2; j < perVisitor; j++ {
		for i := range visitors {
			if (i+j)%3 == 0 {
				publishLegacy(t, env.js, ev(i, j))
			} else {
				publish(t, env.js, ev(i, j))
			}
		}
	}

	// a replayer of the earlier release holds the legacy lock
	locks, err := env.js.KeyValue(testCtx(t, time.Minute), natsx.BucketLocks)
	if err != nil {
		t.Fatal(err)
	}
	old, err := natsx.NewLock(locks, natsx.LockReplayer, "old-release")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Acquire(testCtx(t, time.Minute)); err != nil {
		t.Fatal(err)
	}
	keep := make(chan struct{})
	go func() {
		tk := time.NewTicker(natsx.LockRenewEvery)
		defer tk.Stop()
		for {
			select {
			case <-keep:
				return
			case <-tk.C:
				_, _ = old.Renew(testCtx(t, 10*time.Second))
			}
		}
	}()

	log := &sendLog{}
	var reps []*running
	for _, name := range []string{"core-1", "core-2"} {
		reps = append(reps, startReplayerTo(t, env.srv.URL, []*url.URL{newSendProxy(t, log, name, st.URL)}, name, 16, 512))
	}
	waitFor(t, time.Minute, "both replayers own partitions", func() bool {
		return scrape(t, reps[0].ops)["replayer_partitions_owned"]+scrape(t, reps[1].ops)["replayer_partitions_owned"] == events.Partitions
	})
	// every partition holds events newer than the oldest legacy one, so
	// nothing may be sent while the earlier release holds the legacy lock
	time.Sleep(5 * time.Second)
	log.mu.Lock()
	early := len(log.entries)
	log.mu.Unlock()
	if early != 0 {
		t.Fatalf("%d events sent while an earlier replayer held the legacy subject", early)
	}
	for _, r := range reps {
		if scrape(t, r.ops)["replayer_legacy_leader"] != 0 {
			t.Fatal("a replayer took the legacy lock from the earlier release")
		}
	}
	close(keep)
	if err := old.Release(testCtx(t, time.Minute)); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 3*time.Minute, "stream drained", func() bool { return drained(t, env.js) })
	rep := log.checkOrder()
	t.Logf("stored sends %d by node %v, repeats %d", rep.stored, rep.byNode, rep.repeats)
	for _, o := range rep.outOfOrder {
		t.Error(o)
	}
	for _, o := range rep.overlaps {
		t.Error(o)
	}
	if rep.stored != visitors*perVisitor || rep.repeats != 0 {
		t.Errorf("stored %d sends with %d repeats, want %d and none", rep.stored, rep.repeats, visitors*perVisitor)
	}
	paths := pathCounts(t, st.Admin, s.ID, umami.Range{Start: h.Add(-time.Hour), End: h.Add(3 * time.Hour)}, visitors*perVisitor+10)
	for i := range visitors {
		for j := range perVisitor {
			if c := paths[visitorPath(i, j)]; c != 1 {
				t.Errorf("%s stored %d times", visitorPath(i, j), c)
			}
		}
	}

	// the legacy subject is empty, so it retires; the replayers notice the
	// consumer is gone and keep delivering the partitions
	st2, err := natsx.RetireLegacyWeb(testCtx(t, time.Minute), env.js, 30*time.Second, 100*time.Millisecond)
	if err != nil || !st2.Retired() {
		t.Fatalf("retire: %+v %v", st2, err)
	}
	waitFor(t, 30*time.Second, "the legacy hold to end", func() bool {
		return scrape(t, reps[0].ops)["replayer_legacy_leader"]+scrape(t, reps[1].ops)["replayer_legacy_leader"] == 0
	})
	publish(t, env.js, ev(0, perVisitor))
	waitFor(t, time.Minute, "an event after the retirement", func() bool { return drained(t, env.js) })
	if c := pathCounts(t, st.Admin, s.ID, umami.Range{Start: h.Add(-time.Hour), End: h.Add(4 * time.Hour)}, visitors*perVisitor+10)[visitorPath(0, perVisitor)]; c != 1 {
		t.Errorf("the event after the retirement was stored %d times", c)
	}
}

// natsOn connects to a nats server whose topology the test sets up itself.
func natsOn(t *testing.T, srv *natsctr.Server) *natsEnv {
	t.Helper()
	nc, err := nats.Connect(srv.URL, nats.MaxReconnects(-1), nats.ReconnectWait(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return &natsEnv{srv: srv, nc: nc, js: js}
}
