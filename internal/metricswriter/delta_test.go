package metricswriter

import (
	"errors"
	"testing"
	"time"
)

const sec = int64(time.Second)

// bk builds a bucket array from leading values.
func bk(v ...uint64) Buckets {
	var b Buckets
	copy(b[:], v)
	return b
}

func cum(start, at int64, counts Buckets, sum float64, attrs ...Attr) Point {
	return Point{
		Tenant: "t1", ResourceHash: "r1", Metric: "http.server.request.duration",
		Attrs: attrs, Temporality: Cumulative, Start: start, Time: at,
		Count: counts.Total(), Sum: sum, Counts: counts,
	}
}

var (
	routeA = Attr{"http.route", "/a"}
	routeB = Attr{"http.route", "/b"}
	get    = Attr{"http.request.method", "GET"}
	s200   = Attr{"http.response.status_code", "200"}
	s500   = Attr{"http.response.status_code", "500"}
)

type step struct {
	name    string
	p       Point
	outcome Outcome
	want    Contribution
}

func runSteps(t *testing.T, s *State, steps []step) {
	t.Helper()
	for _, st := range steps {
		got, out, err := s.Apply(&st.p)
		if err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		if out != st.outcome {
			t.Errorf("%s: outcome %s, want %s", st.name, out, st.outcome)
		}
		if got != st.want {
			t.Errorf("%s: contribution %+v, want %+v", st.name, got, st.want)
		}
	}
}

// newState is a state with the writer's cap and the stream counts of the
// test tenants loaded, as loadState does.
func newState() *State {
	s := NewState(DefaultFirstPointThreshold, MaxSeriesPerTenant)
	s.LoadSeriesCount("t1", 0)
	s.LoadSeriesCount("t2", 0)
	return s
}

func contrib(counts Buckets, sum float64) Contribution {
	return Contribution{Count: counts.Total(), Sum: sum, Counts: counts}
}

func TestEngine(t *testing.T) {
	now := int64(1_800_000_000) * sec
	cases := []struct {
		name  string
		setup func(*State)
		steps []step
	}{
		{
			name: "cumulative sequence of a fresh process",
			steps: []step{
				{"first", cum(now, now+10*sec, bk(1, 2), 0.5, get, routeA), OutcomeNew, contrib(bk(1, 2), 0.5)},
				{"second", cum(now, now+20*sec, bk(3, 2, 1), 1.5, get, routeA), OutcomeIncrement, contrib(bk(2, 0, 1), 1.0)},
				{"unchanged", cum(now, now+30*sec, bk(3, 2, 1), 1.5, get, routeA), OutcomeIncrement, Contribution{}},
				{"third", cum(now, now+40*sec, bk(3, 2, 1, 0, 4), 2.5, get, routeA), OutcomeIncrement, contrib(bk(0, 0, 0, 0, 4), 1.0)},
			},
		},
		{
			name: "restart with a new start time is a new stream of a new resource",
			steps: []step{
				{"before", cum(now, now+10*sec, bk(5), 1, routeA), OutcomeNew, contrib(bk(5), 1)},
				{"later", cum(now, now+20*sec, bk(8), 2, routeA), OutcomeIncrement, contrib(bk(3), 1)},
				{"restarted", cum(now+25*sec, now+30*sec, bk(2), 0.25, routeA), OutcomeNew, contrib(bk(2), 0.25)},
				{"restarted later", cum(now+25*sec, now+40*sec, bk(4), 0.5, routeA), OutcomeIncrement, contrib(bk(2), 0.25)},
			},
		},
		{
			name: "lower count with the same start time is a reset and counts in full",
			steps: []step{
				{"before", cum(now, now+10*sec, bk(5, 5), 1, routeA), OutcomeNew, contrib(bk(5, 5), 1)},
				{"reset", cum(now, now+20*sec, bk(1, 2), 0.25, routeA), OutcomeReset, contrib(bk(1, 2), 0.25)},
				{"after", cum(now, now+30*sec, bk(1, 3), 0.5, routeA), OutcomeIncrement, contrib(bk(0, 1), 0.25)},
			},
		},
		{
			name: "a bucket going down while the total grows is a reset for exact data",
			steps: []step{
				{"before", cum(now, now+10*sec, bk(5, 5), 1, routeA), OutcomeNew, contrib(bk(5, 5), 1)},
				{"shifted", cum(now, now+20*sec, bk(4, 7), 2, routeA), OutcomeReset, contrib(bk(4, 7), 2)},
			},
		},
		{
			name: "interleaved replicas with equal attributes and different start times",
			steps: []step{
				{"a1", cum(now, now+10*sec, bk(3), 1, get, routeA, s200), OutcomeNew, contrib(bk(3), 1)},
				{"b1", cum(now+1, now+11*sec, bk(7), 2, get, routeA, s200), OutcomeNew, contrib(bk(7), 2)},
				{"a2", cum(now, now+20*sec, bk(4), 1.5, get, routeA, s200), OutcomeIncrement, contrib(bk(1), 0.5)},
				{"b2", cum(now+1, now+21*sec, bk(9), 3, get, routeA, s200), OutcomeIncrement, contrib(bk(2), 1)},
				{"a3", cum(now, now+30*sec, bk(10), 2, get, routeA, s200), OutcomeIncrement, contrib(bk(6), 0.5)},
			},
		},
		{
			name: "first point of a long-running unknown process is a baseline",
			steps: []step{
				{"history", cum(now-3600*sec, now, bk(900, 100), 50, routeA), OutcomeBaseline, Contribution{}},
				{"other series same process", cum(now-3600*sec, now, bk(10), 5, routeB), OutcomeBaseline, Contribution{}},
				{"next", cum(now-3600*sec, now+60*sec, bk(905, 101), 52, routeA), OutcomeIncrement, contrib(bk(5, 1), 2)},
			},
		},
		{
			name: "new route and new status of a followed process count in full",
			steps: []step{
				{"followed", cum(now-3600*sec, now, bk(900), 50, get, routeA, s200), OutcomeBaseline, Contribution{}},
				{"followed next", cum(now-3600*sec, now+60*sec, bk(910), 51, get, routeA, s200), OutcomeIncrement, contrib(bk(10), 1)},
				{"new route", cum(now-3600*sec, now+120*sec, bk(4), 0.5, get, routeB, s200), OutcomeNew, contrib(bk(4), 0.5)},
				{"new status", cum(now-3600*sec, now+180*sec, bk(0, 1), 0.1, get, routeA, s500), OutcomeNew, contrib(bk(0, 1), 0.1)},
			},
		},
		{
			name: "resource seen in an earlier batch makes a new series count",
			setup: func(s *State) {
				s.LoadResource(ResourceKey{Tenant: "t1", ResourceHash: "r1", Start: now - 3600*sec}, now-60*sec)
			},
			steps: []step{
				{"new route", cum(now-3600*sec, now, bk(2), 0.2, routeB), OutcomeNew, contrib(bk(2), 0.2)},
			},
		},
		{
			name: "resource first seen in the same export stays under the age rule",
			setup: func(s *State) {
				s.LoadResource(ResourceKey{Tenant: "t1", ResourceHash: "r1", Start: now - 3600*sec}, now)
			},
			steps: []step{
				{"same export, other series", cum(now-3600*sec, now, bk(2), 0.2, routeB), OutcomeBaseline, Contribution{}},
			},
		},
		{
			name: "first point within the threshold counts in full",
			steps: []step{
				{"young", cum(now, now+120*sec, bk(6), 1, routeA), OutcomeNew, contrib(bk(6), 1)},
			},
		},
		{
			name: "first point just past the threshold is a baseline",
			steps: []step{
				{"old", cum(now, now+120*sec+1, bk(6), 1, routeA), OutcomeBaseline, Contribution{}},
			},
		},
		{
			name: "stored series continues across batches",
			setup: func(s *State) {
				p := cum(now, now+10*sec, bk(5, 1), 1, routeA)
				s.LoadSeries(p.Key(), SeriesState{Start: now, Last: now + 10*sec, Count: 6, Sum: 1, Counts: bk(5, 1)})
			},
			steps: []step{
				{"next", cum(now, now+20*sec, bk(6, 1), 1.5, routeA), OutcomeIncrement, contrib(bk(1), 0.5)},
			},
		},
		{
			name: "a redelivered older point adds nothing",
			steps: []step{
				{"p1", cum(now, now+10*sec, bk(1), 1, routeA), OutcomeNew, contrib(bk(1), 1)},
				{"p2", cum(now, now+20*sec, bk(3), 2, routeA), OutcomeIncrement, contrib(bk(2), 1)},
				{"p1 again", cum(now, now+10*sec, bk(1), 1, routeA), OutcomeOld, Contribution{}},
				{"p2 again", cum(now, now+20*sec, bk(3), 2, routeA), OutcomeOld, Contribution{}},
				{"p3", cum(now, now+30*sec, bk(4), 3, routeA), OutcomeIncrement, contrib(bk(1), 1)},
			},
		},
		{
			name: "stale markers contribute nothing and keep the state",
			steps: []step{
				{"p1", cum(now, now+10*sec, bk(4), 1, routeA), OutcomeNew, contrib(bk(4), 1)},
				{"stale", func() Point {
					p := cum(now, now+20*sec, bk(), 0, routeA)
					p.NoRecordedValue = true
					return p
				}(), OutcomeStale, Contribution{}},
				{"back", cum(now, now+30*sec, bk(6), 2, routeA), OutcomeIncrement, contrib(bk(2), 1)},
			},
		},
		{
			name: "delta points pass through",
			steps: []step{
				{"d1", Point{Tenant: "t1", Metric: "m", Temporality: Delta, Start: now, Time: now + sec, Count: 3, Sum: 1, Counts: bk(1, 2)}, OutcomeDelta, contrib(bk(1, 2), 1)},
				{"d1 same values", Point{Tenant: "t1", Metric: "m", Temporality: Delta, Start: now + sec, Time: now + 2*sec, Count: 3, Sum: 1, Counts: bk(1, 2)}, OutcomeDelta, contrib(bk(1, 2), 1)},
				{"span derived, no start time", Point{Tenant: "t1", Metric: "m", Temporality: Delta, Time: now + 3*sec, Count: 1, Sum: 0.1, Counts: bk(0, 0, 1)}, OutcomeDelta, contrib(bk(0, 0, 1), 0.1)},
			},
		},
		{
			name: "lossy points rebalance buckets instead of resetting",
			steps: []step{
				{"p1", func() Point { p := cum(now, now+10*sec, bk(5, 5, 0), 1, routeA); p.Lossy = true; return p }(), OutcomeNew, contrib(bk(5, 5, 0), 1)},
				// total +3; bucket 0 lost one to the split, bucket 1 gained 4
				{"p2", func() Point { p := cum(now, now+20*sec, bk(4, 9, 0), 2, routeA); p.Lossy = true; return p }(), OutcomeIncrement, contrib(bk(0, 3, 0), 1)},
			},
		},
		{
			name: "attribute order does not change the stream",
			steps: []step{
				{"p1", cum(now, now+10*sec, bk(1), 1, get, routeA, s200), OutcomeNew, contrib(bk(1), 1)},
				{"p2 reordered", cum(now, now+20*sec, bk(2), 2, s200, routeA, get), OutcomeIncrement, contrib(bk(1), 1)},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newState()
			if c.setup != nil {
				c.setup(s)
			}
			runSteps(t, s, c.steps)
		})
	}
}

func TestEngineTenantsAndResourcesAreSeparate(t *testing.T) {
	now := int64(1_800_000_000) * sec
	a := cum(now, now+10*sec, bk(1), 1, routeA)
	b := a
	b.Tenant = "t2"
	c := a
	c.ResourceHash = "r2"
	d := a
	d.Metric = "scf.http.server.request.duration.from_spans"
	keys := map[SeriesKey]bool{a.Key(): true, b.Key(): true, c.Key(): true, d.Key(): true}
	if len(keys) != 4 {
		t.Fatalf("tenant, resource or metric does not separate streams: %d keys", len(keys))
	}
	// an attribute value containing the separator of another field cannot
	// collide with a different split
	x := cum(now, now, bk(), 0, Attr{"a", "bc"})
	y := cum(now, now, bk(), 0, Attr{"ab", "c"})
	if x.Key() == y.Key() {
		t.Fatal("length prefixes do not separate fields")
	}
}

func TestEngineRejectsInconsistentPoints(t *testing.T) {
	s := newState()
	p := cum(0, sec, bk(1, 1), 0)
	p.Count = 3
	if _, _, err := s.Apply(&p); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("count mismatch accepted: %v", err)
	}
	q := cum(0, sec, bk(1), 0)
	q.Temporality = 0
	if _, _, err := s.Apply(&q); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("missing temporality accepted: %v", err)
	}
	if len(s.Changed()) != 0 || len(s.Touched()) != 0 {
		t.Fatal("a rejected point changed the state")
	}
}

func TestEngineRecordsChanges(t *testing.T) {
	now := int64(1_800_000_000) * sec
	s := newState()
	p1 := cum(now, now+10*sec, bk(1), 1, routeA)
	p2 := cum(now, now+20*sec, bk(2), 2, routeA)
	p3 := cum(now, now+15*sec, bk(1), 1, routeB)
	for _, p := range []*Point{&p1, &p2, &p3} {
		if _, _, err := s.Apply(p); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(s.Changed()); n != 2 {
		t.Fatalf("%d changed series, want 2", n)
	}
	st, ok := s.Series(p1.Key())
	if !ok || st.Count != 2 || st.Last != now+20*sec || st.Start != now {
		t.Fatalf("state %+v", st)
	}
	tr := s.Touched()
	if len(tr) != 1 || tr[0].LastSeen != now+20*sec || tr[0].Key.Start != now {
		t.Fatalf("touched %+v", tr)
	}
}

func TestRebalanceKeepsTheSum(t *testing.T) {
	cases := [][NumBuckets]int64{
		{-1, 2},
		{3, -2, 0, -1, 1},
		{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 5, -5},
		{-3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4},
	}
	for _, d := range cases {
		var before int64
		for _, v := range d {
			before += v
		}
		rebalance(&d)
		var after int64
		for _, v := range d {
			if v < 0 {
				t.Fatalf("negative entry left: %v", d)
			}
			after += v
		}
		if after != before {
			t.Fatalf("sum changed %d to %d: %v", before, after, d)
		}
	}
}

// a process exporting every 5 minutes is 300 s old at its first
// export, past the 120 s threshold, so that point is a baseline. its next
// export shows from the cadence that the process had only just started, and
// then counts in full: the first interval's requests are not lost.
func TestEngineLongExportIntervalKeepsFirstInterval(t *testing.T) {
	now := int64(1_800_000_000) * sec
	s := newState()
	runSteps(t, s, []step{
		{"first export", cum(now, now+300*sec, bk(3), 0.25, get, routeA), OutcomeBaseline, Contribution{}},
		{"second export", cum(now, now+600*sec, bk(7), 0.75, get, routeA), OutcomeCatchUp, contrib(bk(7), 0.75)},
		{"third export", cum(now, now+900*sec, bk(9), 1, get, routeA), OutcomeIncrement, contrib(bk(2), 0.25)},
	})

	// the baseline is stored, so the catch-up also works across batches
	p := cum(now, now+300*sec, bk(3), 0.25, get, routeB)
	s = newState()
	s.LoadSeries(p.Key(), SeriesState{Start: now, Last: now + 300*sec, Count: 3, Sum: 0.25, Counts: bk(3), Baseline: true})
	runSteps(t, s, []step{{"next batch", cum(now, now+600*sec, bk(5), 0.5, get, routeB), OutcomeCatchUp, contrib(bk(5), 0.5)}})
	if st, _ := s.Series(p.Key()); st.Baseline {
		t.Error("baseline flag kept after the catch-up")
	}

	// a process that ran an hour before its first export seen here stays a
	// baseline even with a 5 minute cadence: 3600 s is more than two
	// intervals
	s = newState()
	runSteps(t, s, []step{
		{"history", cum(now-3600*sec, now, bk(900), 50, routeA), OutcomeBaseline, Contribution{}},
		{"next", cum(now-3600*sec, now+300*sec, bk(905), 51, routeA), OutcomeIncrement, contrib(bk(5), 1)},
	})
}

// the writer's per-stream state is capped per tenant. a new
// cumulative stream beyond the cap is refused; known streams and delta
// points carry on.
func TestEngineSeriesCap(t *testing.T) {
	now := int64(1_800_000_000) * sec
	s := NewState(DefaultFirstPointThreshold, 2)
	s.LoadSeriesCount("t1", 1)
	a := cum(now, now+10*sec, bk(1), 0.1, routeA)
	if _, out, err := s.Apply(&a); err != nil || out != OutcomeNew {
		t.Fatalf("first new stream: %s %v", out, err)
	}
	b := cum(now, now+10*sec, bk(1), 0.1, routeB)
	if _, _, err := s.Apply(&b); !errors.Is(err, ErrSeriesCap) {
		t.Fatalf("stream beyond the cap: %v", err)
	}
	if len(s.Touched()) != 1 {
		t.Errorf("a refused stream touched its resource")
	}
	a2 := cum(now, now+20*sec, bk(3), 0.3, routeA)
	if c, out, err := s.Apply(&a2); err != nil || out != OutcomeIncrement || c.Count != 2 {
		t.Fatalf("known stream: %s %+v %v", out, c, err)
	}
	d := Point{Tenant: "t1", Metric: "m", Temporality: Delta, Time: now, Count: 1, Counts: bk(1)}
	if _, out, err := s.Apply(&d); err != nil || out != OutcomeDelta {
		t.Fatalf("delta point: %s %v", out, err)
	}
	// a tenant whose count was not loaded is a writer bug, not a silent pass
	o := cum(now, now+10*sec, bk(1), 0.1, routeA)
	o.Tenant = "t2"
	if _, _, err := s.Apply(&o); err == nil || errors.Is(err, ErrSeriesCap) {
		t.Fatalf("unloaded tenant: %v", err)
	}
}
