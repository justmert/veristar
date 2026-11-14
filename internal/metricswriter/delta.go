// Package metricswriter turns the histogram points on API_METRICS into exact
// per-minute rows in the api schema.
//
// delta.go is the delta engine: a pure function of the points and the state
// loaded for them, with no nats and no sql, so its rules are tested on their
// own. writer.go feeds it from jetstream and stores its output.
package metricswriter

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/justmert/veristar/internal/histo"
)

// NumBuckets is the length of every bucket array the engine handles: the
// canonical layout (internal/histo), 14 boundaries and 15 buckets.
const NumBuckets = histo.Buckets

// Buckets holds counts on the canonical layout.
type Buckets [NumBuckets]uint64

// Total is the sum of all buckets.
func (b *Buckets) Total() uint64 {
	var n uint64
	for _, c := range b {
		n += c
	}
	return n
}

// Temporality is the aggregation temporality of a point.
type Temporality uint8

const (
	// Delta points carry only what happened since the previous export.
	Delta Temporality = iota + 1
	// Cumulative points carry everything since their start time.
	Cumulative
)

// Attr is one data point attribute that is part of a stream's identity.
type Attr struct {
	Key, Value string
}

// Point is one histogram data point, already re-bucketed onto the canonical
// layout.
type Point struct {
	Tenant       string
	ResourceHash string
	Metric       string
	// Attrs is the identity of the stream inside its resource: every data
	// point attribute except the lossy marker, in any order.
	Attrs       []Attr
	Temporality Temporality
	// Start and Time are unix nanoseconds, as on the wire.
	Start, Time int64
	Count       uint64
	Sum         float64
	Counts      Buckets
	// Lossy marks a point that is approximate: converted from an exponential
	// histogram, or re-bucketed from boundaries that are not a subset of the
	// canonical ones.
	Lossy bool
	// NoRecordedValue is the otlp flag of a stale marker: the series ended
	// and the point carries no data.
	NoRecordedValue bool
}

// SeriesKey identifies one cumulative stream: the series hash over tenant,
// resource hash, metric name and attributes, and the start time. two
// replicas of one service without an instance id share a series hash but
// not a start time, so they stay separate streams. the start time is kept
// apart from the hash because it is its own key column in api.series_state.
type SeriesKey struct {
	Hash  [sha256.Size]byte
	Start int64
}

func compareKeys(a, b SeriesKey) int {
	if c := compareBytes(a.Hash[:], b.Hash[:]); c != 0 {
		return c
	}
	switch {
	case a.Start < b.Start:
		return -1
	case a.Start > b.Start:
		return 1
	}
	return 0
}

// Key returns the stream identity of p.
func (p *Point) Key() SeriesKey {
	attrs := slices.Clone(p.Attrs)
	slices.SortFunc(attrs, func(a, b Attr) int {
		if c := compareStrings(a.Key, b.Key); c != 0 {
			return c
		}
		return compareStrings(a.Value, b.Value)
	})
	h := sha256.New()
	// every field is length-prefixed, so no two different identities can
	// produce the same byte string
	field := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	field(p.Tenant)
	field(p.ResourceHash)
	field(p.Metric)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(attrs)))
	h.Write(n[:])
	for _, a := range attrs {
		field(a.Key)
		field(a.Value)
	}
	k := SeriesKey{Start: p.Start}
	copy(k.Hash[:], h.Sum(nil))
	return k
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// ResourceKey identifies one process of a tenant's service as the writer
// sees it: the resource hash and the start time of its cumulative points.
type ResourceKey struct {
	Tenant       string
	ResourceHash string
	Start        int64
}

// SeriesState is what the writer remembers of a cumulative stream: the last
// point it applied. times are unix nanoseconds.
type SeriesState struct {
	Start  int64
	Last   int64
	Count  uint64
	Sum    float64
	Counts Buckets
	// Baseline is set while the last point is the baseline of a stream
	// first met past the threshold.
	Baseline bool
}

// Outcome says what a point contributed.
type Outcome uint8

const (
	// OutcomeDelta: a delta point, added as it is.
	OutcomeDelta Outcome = iota + 1
	// OutcomeNew: the first point of a stream of a known resource, or of a
	// new resource within the threshold. it counts in full.
	OutcomeNew
	// OutcomeBaseline: the first point of a stream of a resource the writer
	// has never seen, older than the threshold. it contributes nothing and
	// is the base of the next difference.
	OutcomeBaseline
	// OutcomeIncrement: a later point of a known stream; contributes the
	// difference to the previous one.
	OutcomeIncrement
	// OutcomeReset: a known stream whose count went down. the process kept
	// its start time but started counting again, so the point counts in full.
	OutcomeReset
	// OutcomeStale: a stale marker. nothing is contributed and the state is
	// kept, so a stream that comes back continues from where it was.
	OutcomeStale
	// OutcomeOld: a point not newer than the last applied point of its
	// stream. its requests are already contained in that point.
	OutcomeOld
	// OutcomeCatchUp: the second point of a stream whose first point became
	// a baseline, when the export cadence shows the process had only just
	// started at that first point. it counts in full.
	OutcomeCatchUp
)

var outcomeNames = map[Outcome]string{
	OutcomeDelta: "delta", OutcomeNew: "new", OutcomeBaseline: "baseline", OutcomeIncrement: "increment",
	OutcomeReset: "reset", OutcomeStale: "stale", OutcomeOld: "old", OutcomeCatchUp: "catch_up",
}

func (o Outcome) String() string {
	if s, ok := outcomeNames[o]; ok {
		return s
	}
	return fmt.Sprintf("outcome(%d)", o)
}

// Outcomes lists every outcome, for metrics that create each label up front.
func Outcomes() []Outcome {
	return []Outcome{OutcomeDelta, OutcomeNew, OutcomeBaseline, OutcomeIncrement, OutcomeReset, OutcomeStale, OutcomeOld, OutcomeCatchUp}
}

// Contribution is what one point adds to its minute row.
type Contribution struct {
	Count  uint64
	Sum    float64
	Counts Buckets
}

// ErrInvalidPoint marks a point the engine refuses: its buckets do not add
// up to its count, or it has no temporality. such a point cannot be counted
// exactly, so the writer reports it instead of guessing.
var ErrInvalidPoint = errors.New("metricswriter: invalid point")

// DefaultFirstPointThreshold is the default for the first-point rule.
const DefaultFirstPointThreshold = 120 * time.Second

// State is the engine's view of the stored state for one batch. the writer
// loads the rows for every key of the batch first, the engine then applies
// the points in stream order and records what changed, and the writer
// stores the changes in the same transaction.
type State struct {
	// Threshold is the first-point rule's age limit.
	Threshold time.Duration

	series map[SeriesKey]*SeriesState
	// resources maps a resource to the time of the first point the writer
	// applied for it, in microseconds: the database keeps that time as a
	// timestamp, and the engine compares the value it holds in memory exactly
	// as it would compare the one read back
	resources map[ResourceKey]int64
	// changed series and touched resources, with the latest point time
	dirty   map[SeriesKey]bool
	touched map[ResourceKey]int64
	// fresh holds the resources whose first point was applied in this batch
	fresh map[ResourceKey]bool
	// owners is the tenant of every changed series
	owners map[SeriesKey]string
	// seriesCap bounds the streams the writer follows per tenant
	seriesCap int
	// series counts the followed streams per tenant, for SeriesCap
	seriesN map[string]int
}

// ErrSeriesCap marks a new cumulative stream of a tenant that already has
// the cap's number of active streams.
var ErrSeriesCap = errors.New("metricswriter: tenant stream cap reached")

// LoadSeriesCount records how many active streams a tenant has (stored a
// point within ActiveSeriesWindow). the writer loads it for every tenant
// that has a stream the batch does not know yet.
func (s *State) LoadSeriesCount(tenant string, n int) {
	s.seriesN[tenant] = n
}

// errSeriesCountNotLoaded means the writer did not load a tenant's stream
// count before a new stream of it arrived: a bug in the writer, reported
// rather than letting the stream pass the cap unchecked.
var errSeriesCountNotLoaded = errors.New("metricswriter: stream count of the tenant was not loaded")

// MaxSeriesPerTenant is the cap on a tenant's active streams, the ones the
// writer stored a point of within ActiveSeriesWindow. the route, service,
// method and status caps bound what a tenant's rows hold, not how many
// streams the writer must follow: every process restart is a new resource
// and every distinct error.type a new stream. 100,000 is about 20 running
// processes of a service with the full 300 routes, 9 methods and 10
// statuses. only active streams count: the streams of a process
// that restarted stop counting an hour after its last export, so restarts,
// deploys and autoscaling never reach the cap; only that many streams alive
// at once do. their rows are bounded separately (MaxStoredSeriesPerTenant).
const MaxSeriesPerTenant = 100_000

// ActiveSeriesWindow is how long after its last stored point a stream still
// counts towards MaxSeriesPerTenant. an hour covers every common export
// interval (60 s default, 5 minutes at most in practice) many times over,
// so a live stream never drops out between two exports.
const ActiveSeriesWindow = time.Hour

// MaxStoredSeriesPerTenant bounds the api.series_state rows of a tenant,
// active or idle: api-retention evicts the longest idle streams (idle
// longer than ActiveSeriesWindow) above it every hour. at about 250 bytes a
// row a tenant holds at most about 125 mb of writer state, plus what it
// adds within one hour.
const MaxStoredSeriesPerTenant = 500_000

// NewState returns an empty state with the given threshold and per-tenant
// stream cap.
func NewState(threshold time.Duration, seriesCap int) *State {
	return &State{
		Threshold: threshold,
		seriesCap: seriesCap,
		series:    map[SeriesKey]*SeriesState{},
		resources: map[ResourceKey]int64{},
		dirty:     map[SeriesKey]bool{},
		touched:   map[ResourceKey]int64{},
		fresh:     map[ResourceKey]bool{},
		owners:    map[SeriesKey]string{},
		seriesN:   map[string]int{},
	}
}

// LoadSeries records a stored series state.
func (s *State) LoadSeries(k SeriesKey, st SeriesState) {
	c := st
	s.series[k] = &c
}

// LoadResource records that a resource was first seen at firstSeen (unix
// nanoseconds).
func (s *State) LoadResource(k ResourceKey, firstSeen int64) {
	s.resources[k] = Micro(firstSeen)
}

// Micro truncates unix nanoseconds to microseconds, the precision of a
// postgresql timestamp.
func Micro(ns int64) int64 { return ns - ns%1000 }

// Series returns the state of a stream, for tests and for storing.
func (s *State) Series(k SeriesKey) (SeriesState, bool) {
	st, ok := s.series[k]
	if !ok {
		return SeriesState{}, false
	}
	return *st, true
}

// Changed returns the streams whose state changed, sorted so the writes
// take row locks in a fixed order.
func (s *State) Changed() []SeriesKey {
	out := make([]SeriesKey, 0, len(s.dirty))
	for k := range s.dirty {
		out = append(out, k)
	}
	slices.SortFunc(out, compareKeys)
	return out
}

// Touched returns every resource a cumulative point referred to, with the
// time of its latest point, sorted for the same reason as Changed.
func (s *State) Touched() []TouchedResource {
	out := make([]TouchedResource, 0, len(s.touched))
	for k, t := range s.touched {
		out = append(out, TouchedResource{Key: k, LastSeen: t, FirstSeen: s.resources[k], New: s.fresh[k]})
	}
	slices.SortFunc(out, func(a, b TouchedResource) int {
		if c := compareStrings(a.Key.Tenant, b.Key.Tenant); c != 0 {
			return c
		}
		if c := compareStrings(a.Key.ResourceHash, b.Key.ResourceHash); c != 0 {
			return c
		}
		switch {
		case a.Key.Start < b.Key.Start:
			return -1
		case a.Key.Start > b.Key.Start:
			return 1
		}
		return 0
	})
	return out
}

// TouchedResource is a resource with the time of its latest point, and of
// its first point when that was applied in this batch (New).
type TouchedResource struct {
	Key       ResourceKey
	LastSeen  int64
	FirstSeen int64
	New       bool
}

func compareBytes(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return len(a) - len(b)
}

// Apply applies one point and returns what it contributes. points of one
// stream must be applied in the order they were queued.
func (s *State) Apply(p *Point) (Contribution, Outcome, error) {
	if p.Temporality != Delta && p.Temporality != Cumulative {
		return Contribution{}, 0, fmt.Errorf("%w: temporality %d", ErrInvalidPoint, p.Temporality)
	}
	if p.NoRecordedValue {
		return Contribution{}, OutcomeStale, nil
	}
	if p.Counts.Total() != p.Count {
		return Contribution{}, 0, fmt.Errorf("%w: buckets add up to %d, count is %d", ErrInvalidPoint, p.Counts.Total(), p.Count)
	}
	full := Contribution{Count: p.Count, Sum: p.Sum, Counts: p.Counts}
	if p.Temporality == Delta {
		return full, OutcomeDelta, nil
	}

	key := p.Key()
	at := Micro(p.Time)
	rk := ResourceKey{Tenant: p.Tenant, ResourceHash: p.ResourceHash, Start: p.Start}
	st, known := s.series[key]
	if !known {
		// the state the writer keeps per stream is bounded per tenant:
		// a tenant that keeps inventing streams (a random
		// resource attribute per export, ids normalization misses) is
		// refused new ones, and nothing of a refused one is stored
		n, loaded := s.seriesN[p.Tenant]
		if !loaded {
			return Contribution{}, 0, errSeriesCountNotLoaded
		}
		if n >= s.seriesCap {
			return Contribution{}, 0, fmt.Errorf("%w: %d streams", ErrSeriesCap, n)
		}
		s.seriesN[p.Tenant] = n + 1
	}
	if t, ok := s.touched[rk]; !ok || p.Time > t {
		s.touched[rk] = p.Time
	}
	if !known {
		first, seen := s.resources[rk]
		if !seen {
			s.resources[rk] = at
			s.fresh[rk] = true
		}
		s.remember(key, p)
		// the resource counts as followed only when the writer applied a
		// point of it from an earlier export. every series of one export
		// shares its time, so the series of the export in which the writer
		// first meets a process are all judged by the age rule together.
		followed := seen && first < at
		if !followed && time.Duration(p.Time-p.Start) > s.Threshold {
			// a process the writer meets for the first time and that has run
			// for a while: its counts so far are history from before the
			// writer followed it, and would all land in this one minute
			s.series[key].Baseline = true
			return Contribution{}, OutcomeBaseline, nil
		}
		return full, OutcomeNew, nil
	}
	if p.Time <= st.Last {
		return Contribution{}, OutcomeOld, nil
	}
	if p.Count < st.Count {
		s.remember(key, p)
		return full, OutcomeReset, nil
	}
	if st.Baseline && st.Last-st.Start <= 2*(p.Time-st.Last) {
		// the baseline was the process' first export: it was younger than
		// two export intervals, as the gap to this second export shows. a
		// process that exports every 5 minutes is 300 s old at its first
		// export, past the threshold, and without this its whole first
		// interval would be lost. a process that had run for a
		// long time is older than that and keeps its baseline. the count
		// since the start is the first two intervals; it lands in this
		// minute, one interval late for the first.
		s.remember(key, p)
		return full, OutcomeCatchUp, nil
	}
	var c Contribution
	c.Count = p.Count - st.Count
	c.Sum = p.Sum - st.Sum
	var negative bool
	var diff [NumBuckets]int64
	for i := range NumBuckets {
		diff[i] = int64(p.Counts[i]) - int64(st.Counts[i]) //nolint:gosec // counts of one request stream fit in int64
		if diff[i] < 0 {
			negative = true
		}
	}
	if negative && !p.Lossy {
		// an exact cumulative histogram never loses counts in a bucket while
		// its total grows unless the process started again
		s.remember(key, p)
		return full, OutcomeReset, nil
	}
	if negative {
		// a converted exponential histogram spreads each source bucket over
		// the canonical ones, and the split can move a request between
		// neighbours from one export to the next. the total is exact, so the
		// shortfall is taken from the nearest buckets that grew.
		rebalance(&diff)
	}
	for i := range NumBuckets {
		c.Counts[i] = uint64(diff[i]) //nolint:gosec // rebalance leaves no negative entry
	}
	s.remember(key, p)
	return c, OutcomeIncrement, nil
}

func (s *State) remember(k SeriesKey, p *Point) {
	s.series[k] = &SeriesState{Start: p.Start, Last: p.Time, Count: p.Count, Sum: p.Sum, Counts: p.Counts}
	s.dirty[k] = true
	s.owners[k] = p.Tenant
}

// Owner returns the tenant of a changed series.
func (s *State) Owner(k SeriesKey) (string, bool) {
	t, ok := s.owners[k]
	return t, ok
}

// rebalance removes negative entries from d without changing its sum, by
// taking each shortfall from the nearest positive entries (lower index first
// on a tie). the sum must not be negative.
func rebalance(d *[NumBuckets]int64) {
	for i := range NumBuckets {
		for d[i] < 0 {
			j := nearestPositive(d, i)
			if j < 0 {
				return
			}
			take := min(-d[i], d[j])
			d[i] += take
			d[j] -= take
		}
	}
}

func nearestPositive(d *[NumBuckets]int64, i int) int {
	for dist := 1; dist < NumBuckets; dist++ {
		if j := i - dist; j >= 0 && d[j] > 0 {
			return j
		}
		if j := i + dist; j < NumBuckets && d[j] > 0 {
			return j
		}
	}
	return -1
}
