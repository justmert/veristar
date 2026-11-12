package metricswriter

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nats-io/nats.go/jetstream"

	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/writerq"
	"github.com/justmert/stellar-analytics/internal/natsx"
)

// Other replaces a route or service beyond the tenant's daily cap.
const Other = "{other}"

// futureSkew is how far a point's time may lie after the moment the stream
// received its message. a point beyond it comes from a wrong clock; storing
// it would put requests into a minute that has not happened yet, and a time
// past the partitions would block the queue.
const futureSkew = 10 * time.Minute

// warning kinds of api.ingest_warnings
const (
	warnOverflow   = "cardinality_overflow"
	warnLossy      = "lossy_histogram"
	warnSecret     = "secret_in_path"
	warnSpanDerive = "span_derived"
)

// item is one fetched message on its way through a batch.
type item struct {
	msg     jetstream.Msg
	meta    *jetstream.MsgMetadata
	id      string
	tenant  uuid.UUID
	decoded Message
	// dlq holds the reason when the message can never be applied
	dlq string
	// duplicate is set when ingest_dedup shows it was applied before
	duplicate bool
	// refused holds the reason when the tenant is removed or the token
	// revoked: the message is acked and nothing of it is stored
	refused string
}

type rowKey struct {
	tenant  uuid.UUID
	minute  int64 // unix seconds
	service string
	method  string
	route   string
	status  int16
	source  string
}

type row struct {
	count  uint64
	sum    float64
	counts Buckets
	lossy  bool
	points int
}

type warnKey struct {
	tenant uuid.UUID
	day    int64 // unix seconds of the utc day
	kind   string
}

// result is what one committed batch did, recorded into metrics after the
// acks.
type result struct {
	applied, duplicates, dlq int
	refused                  int
	outcomes                 map[Outcome]int
	rejected                 int
	requests                 map[string]uint64
	rows                     int
	noPartition              int
	overflow                 int
}

func (r *result) record(m *metrics) {
	m.messages.WithLabelValues(msgApplied).Add(float64(r.applied))
	m.messages.WithLabelValues(msgDuplicate).Add(float64(r.duplicates))
	m.messages.WithLabelValues(msgDLQ).Add(float64(r.dlq))
	m.messages.WithLabelValues(msgRefused).Add(float64(r.refused))
	for o, n := range r.outcomes {
		m.points.WithLabelValues(o.String()).Add(float64(n))
	}
	m.points.WithLabelValues(pointRejected).Add(float64(r.rejected))
	for s, n := range r.requests {
		m.requests.WithLabelValues(s).Add(float64(n))
	}
	m.rows.Add(float64(r.rows))
	m.noPartition.Add(float64(r.noPartition))
	m.overflow.Add(float64(r.overflow))
}

// prepare reads metadata, tenant, message id and payload of every message.
// a message that can never be applied gets a dlq reason; it is moved to
// API_DLQ only after the batch committed, so a failed batch moves nothing.
func prepare(msgs []jetstream.Msg) ([]*item, error) {
	items := make([]*item, 0, len(msgs))
	for _, m := range msgs {
		meta, err := m.Metadata()
		if err != nil {
			return nil, fmt.Errorf("metricswriter: message without jetstream metadata: %w", err)
		}
		it := &item{msg: m, meta: meta, id: m.Headers().Get(jetstream.MsgIDHeader)}
		items = append(items, it)
		if it.tenant, err = tenantOf(m.Subject()); err != nil {
			it.dlq = err.Error()
			continue
		}
		if it.id == "" || len(it.id) > 256 {
			// the collector always sets the payload hash; without an id the
			// message could be applied twice
			it.dlq = fmt.Sprintf("message id of %d bytes, want 1 to 256", len(it.id))
			continue
		}
		if it.decoded, err = Decode(it.tenant, m.Data()); err != nil {
			it.dlq = err.Error()
		}
	}
	return items, nil
}

// apply runs one batch in one fenced transaction and then moves undecodable
// messages to API_DLQ. it does not ack.
func (w *Writer) apply(ctx context.Context, fence natsx.Fence, msgs []jetstream.Msg) (*result, error) {
	items, err := prepare(msgs)
	if err != nil {
		return nil, err
	}
	var res *result
	err = w.db.WithRole(ctx, pdb.RoleMetricsWriter, func(tx pgx.Tx) error {
		if err := natsx.CheckFence(ctx, tx, natsx.LockMetricsWriter, fence); err != nil {
			return err
		}
		var err error
		res, err = w.applyTx(ctx, tx, items)
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.refused != "" {
			w.log.Info("message refused, nothing stored", "tenant", it.tenant.String(),
				"stream_seq", it.meta.Sequence.Stream, "reason", it.refused)
		}
		if it.dlq == "" {
			continue
		}
		tok := strings.TrimPrefix(it.msg.Subject(), natsx.SubjectAPIMetrics+".")
		if strings.ContainsAny(tok, ".*> ") || tok == "" {
			tok = "invalid"
		}
		if err := natsx.DeadLetter(ctx, w.js, natsx.StreamAPIDLQ, natsx.Subject(natsx.SubjectAPIDLQ, tok), it.msg, it.dlq); err != nil {
			// nothing of it was stored, so a redelivery is harmless
			return nil, fmt.Errorf("metricswriter: dead letter stream seq %d: %w", it.meta.Sequence.Stream, err)
		}
		w.log.Error("message moved to the dead-letter stream", "stream_seq", it.meta.Sequence.Stream, "reason", it.dlq)
	}
	return res, nil
}

func (w *Writer) applyTx(ctx context.Context, tx pgx.Tx, items []*item) (*result, error) {
	q := writerq.New(tx)
	now := w.now().UTC()
	res := &result{outcomes: map[Outcome]int{}, requests: map[string]uint64{}}

	// 1. refusal: nothing of a removed tenant or a revoked token is stored,
	// so a message still queued when a tenant is purged writes no row
	if err := refuse(ctx, q, items); err != nil {
		return nil, err
	}
	// 2. dedup: a message applied before is skipped entirely
	for _, it := range items {
		if it.dlq != "" {
			res.dlq++
			continue
		}
		if it.refused != "" {
			res.refused++
			continue
		}
		n, err := q.RecordIngestMessage(ctx, writerq.RecordIngestMessageParams{MsgID: it.id, TenantID: it.tenant, AppliedAt: now})
		if err != nil {
			return nil, fmt.Errorf("record message %s: %w", it.id, err)
		}
		if n == 0 {
			it.duplicate = true
			res.duplicates++
			continue
		}
		res.applied++
	}

	// 3. load the stored state of every cumulative stream and resource
	state := NewState(w.set.FirstPointThreshold)
	if err := loadState(ctx, q, items, state); err != nil {
		return nil, err
	}

	// 4. the engine, in queue order
	rows := map[rowKey]*row{}
	warns := map[warnKey]int64{}
	tokens := map[uuid.UUID]time.Time{}
	for _, it := range items {
		if it.dlq != "" || it.refused != "" || it.duplicate {
			continue
		}
		recv := it.meta.Timestamp
		day := utcDay(recv.UnixNano())
		if it.decoded.SecretSeeds > 0 {
			warns[warnKey{it.tenant, day, warnSecret}] += int64(it.decoded.SecretSeeds)
		}
		for i := range it.decoded.Points {
			d := &it.decoded.Points[i]
			if t := recv; t.After(tokens[d.TokenID]) {
				tokens[d.TokenID] = t
			}
			at := time.Unix(0, d.Time)
			if at.After(recv.Add(futureSkew)) || at.Before(recv.Add(-w.maxAge)) {
				res.rejected++
				w.log.Warn("point outside the accepted time range, not stored",
					"tenant", it.tenant.String(), "point_time", at.UTC().Format(time.RFC3339Nano),
					"received", recv.UTC().Format(time.RFC3339Nano))
				continue
			}
			c, out, err := state.Apply(&d.Point)
			if err != nil {
				res.rejected++
				w.log.Warn("point refused", "tenant", it.tenant.String(), "error", err.Error())
				continue
			}
			res.outcomes[out]++
			if c.Count == 0 {
				continue
			}
			res.requests[d.Source] += c.Count
			k := rowKey{
				tenant: it.tenant, minute: at.UTC().Truncate(time.Minute).Unix(),
				service: d.Service, method: d.Method, route: d.Route, status: d.Status, source: d.Source,
			}
			r := rows[k]
			if r == nil {
				r = &row{}
				rows[k] = r
			}
			r.count += c.Count
			r.sum += c.Sum
			for b := range NumBuckets {
				r.counts[b] += c.Counts[b]
			}
			r.lossy = r.lossy || d.Lossy
			r.points++
			pday := utcDay(d.Time)
			if d.Lossy {
				warns[warnKey{it.tenant, pday, warnLossy}]++
			}
			if d.Source == SourceSpan {
				warns[warnKey{it.tenant, pday, warnSpanDerive}]++
			}
		}
	}

	// 5. caps: relabel rows of routes and services beyond the daily caps
	capped, overflow, err := w.applyCaps(ctx, q, rows)
	if err != nil {
		return nil, err
	}
	for k, n := range overflow {
		warns[k] += n
		res.overflow += int(n)
	}

	// 6. writes, in a fixed order so concurrent writers (a former leader
	// before its fence check fails) cannot deadlock on row locks
	if err := writeRows(ctx, tx, q, capped, res, w.log); err != nil {
		return nil, err
	}
	if err := writeState(ctx, q, state, now); err != nil {
		return nil, err
	}
	if err := writeWarnings(ctx, q, warns); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(tokens))
	for id := range tokens {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return compareBytes(a[:], b[:]) })
	for _, id := range ids {
		// zero rows means the token was deleted meanwhile; its data was
		// accepted while it was valid, so it is still stored
		if _, err := q.TouchOTLPToken(ctx, writerq.TouchOTLPTokenParams{SeenAt: tokens[id].UTC(), ID: id}); err != nil {
			return nil, fmt.Errorf("touch token %s: %w", id, err)
		}
	}
	return res, nil
}

func utcDay(ns int64) int64 {
	return time.Unix(0, ns).UTC().Truncate(24 * time.Hour).Unix()
}

// loadState reads series_state and resource_seen for every cumulative point
// of the applied messages, per tenant.
func loadState(ctx context.Context, q *writerq.Queries, items []*item, state *State) error {
	series := map[uuid.UUID]map[[32]byte]bool{}
	resources := map[uuid.UUID]map[string]bool{}
	for _, it := range items {
		if it.dlq != "" || it.refused != "" || it.duplicate {
			continue
		}
		for i := range it.decoded.Points {
			p := &it.decoded.Points[i].Point
			if p.Temporality != Cumulative || p.NoRecordedValue {
				continue
			}
			if series[it.tenant] == nil {
				series[it.tenant] = map[[32]byte]bool{}
				resources[it.tenant] = map[string]bool{}
			}
			series[it.tenant][p.Key().Hash] = true
			resources[it.tenant][p.ResourceHash] = true
		}
	}
	for tenant, keys := range series {
		hashes := make([][]byte, 0, len(keys))
		for k := range keys {
			hashes = append(hashes, k[:])
		}
		rows, err := q.GetSeriesStates(ctx, writerq.GetSeriesStatesParams{TenantID: tenant, SeriesHashes: hashes})
		if err != nil {
			return fmt.Errorf("load series state: %w", err)
		}
		for _, r := range rows {
			var k SeriesKey
			if len(r.SeriesHash) != len(k.Hash) || len(r.LastCounts) != NumBuckets || r.LastCount < 0 {
				return fmt.Errorf("series state row of tenant %s has an invalid shape", tenant)
			}
			copy(k.Hash[:], r.SeriesHash)
			k.Start = r.StartUnixNano
			st := SeriesState{Start: r.StartUnixNano, Last: r.LastUnixNano, Count: uint64(r.LastCount), Sum: r.LastSum}
			for i, c := range r.LastCounts {
				st.Counts[i] = uint64(c) //nolint:gosec // the table's check keeps counts non-negative
			}
			state.LoadSeries(k, st)
		}
		rh := make([][]byte, 0, len(resources[tenant]))
		for h := range resources[tenant] {
			b, err := hex.DecodeString(h)
			if err != nil {
				return fmt.Errorf("resource hash %q: %w", h, err)
			}
			rh = append(rh, b)
		}
		seen, err := q.GetResourcesSeen(ctx, writerq.GetResourcesSeenParams{TenantID: tenant, ResourceHashes: rh})
		if err != nil {
			return fmt.Errorf("load resources: %w", err)
		}
		for _, r := range seen {
			state.LoadResource(ResourceKey{Tenant: tenant.String(), ResourceHash: hex.EncodeToString(r.ResourceHash), Start: r.StartUnixNano},
				r.FirstSeenAt.UnixNano())
		}
	}
	return nil
}

// capKey is one tenant and utc day of the route registry.
type capKey struct {
	tenant uuid.UUID
	day    int64
}

type capState struct {
	services map[string]bool
	routes   map[[2]string]bool
	// counted routes: admitted pairs whose route is not Other
	nroutes int
}

// applyCaps returns the rows with every service and route beyond the
// tenant's daily caps replaced by Other, and the overflow warnings: one per
// data point that was relabelled. a pair admitted earlier the same day
// keeps its labels. services are checked first: a refused service takes its
// route with it, because the route under an unknown service would still
// grow the registry without bound.
func (w *Writer) applyCaps(ctx context.Context, q *writerq.Queries, rows map[rowKey]*row) (map[rowKey]*row, map[warnKey]int64, error) {
	keys := make([]rowKey, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sortRowKeys(keys)
	caps := map[capKey]*capState{}
	out := make(map[rowKey]*row, len(rows))
	overflow := map[warnKey]int64{}
	for _, k := range keys {
		r := rows[k]
		day := time.Unix(k.minute, 0).UTC().Truncate(24 * time.Hour)
		ck := capKey{k.tenant, day.Unix()}
		cs := caps[ck]
		if cs == nil {
			list, err := q.ListRoutes(ctx, writerq.ListRoutesParams{TenantID: k.tenant, Day: day})
			if err != nil {
				return nil, nil, fmt.Errorf("load route registry: %w", err)
			}
			cs = &capState{services: map[string]bool{}, routes: map[[2]string]bool{}}
			for _, l := range list {
				cs.services[l.Service] = true
				cs.routes[[2]string{l.Service, l.Route}] = true
				if l.Route != Other {
					cs.nroutes++
				}
			}
			caps[ck] = cs
		}
		service, route := k.service, k.route
		if !cs.services[service] && service != Other {
			if len(cs.services) >= w.set.ServiceCap {
				service, route = Other, Other
			}
		}
		pair := [2]string{service, route}
		if !cs.routes[pair] && route != Other && cs.nroutes >= w.set.RouteCap {
			route = Other
			pair = [2]string{service, route}
		}
		if !cs.routes[pair] {
			if _, err := q.RegisterRoute(ctx, writerq.RegisterRouteParams{TenantID: k.tenant, Day: day, Service: service, Route: route}); err != nil {
				return nil, nil, fmt.Errorf("register route: %w", err)
			}
			cs.routes[pair] = true
			cs.services[service] = true
			if route != Other {
				cs.nroutes++
			}
		}
		if service != k.service || route != k.route {
			overflow[warnKey{k.tenant, ck.day, warnOverflow}] += int64(r.points)
		}
		nk := k
		nk.service, nk.route = service, route
		if prev := out[nk]; prev != nil {
			prev.count += r.count
			prev.sum += r.sum
			for b := range NumBuckets {
				prev.counts[b] += r.counts[b]
			}
			prev.lossy = prev.lossy || r.lossy
			prev.points += r.points
			continue
		}
		c := *r
		out[nk] = &c
	}
	return out, overflow, nil
}

func sortRowKeys(keys []rowKey) {
	slices.SortFunc(keys, func(a, b rowKey) int {
		if c := compareBytes(a.tenant[:], b.tenant[:]); c != 0 {
			return c
		}
		switch {
		case a.minute != b.minute:
			if a.minute < b.minute {
				return -1
			}
			return 1
		case a.service != b.service:
			return compareStrings(a.service, b.service)
		case a.method != b.method:
			return compareStrings(a.method, b.method)
		case a.route != b.route:
			return compareStrings(a.route, b.route)
		case a.status != b.status:
			return int(a.status) - int(b.status)
		}
		return compareStrings(a.source, b.source)
	})
}

// batchErr collects the first error of a pgx batch.
func batchErr(what string) (func(int, error), func() error) {
	var first error
	return func(_ int, err error) {
			if err != nil && first == nil {
				first = err
			}
		}, func() error {
			if first != nil {
				return fmt.Errorf("%s: %w", what, first)
			}
			return nil
		}
}

func toInt64(b Buckets) []int64 {
	out := make([]int64, NumBuckets)
	for i, c := range b {
		out[i] = int64(c) //nolint:gosec // request counts of one minute fit in int64
	}
	return out
}

func writeRows(ctx context.Context, tx pgx.Tx, q *writerq.Queries, rows map[rowKey]*row, res *result, log *slog.Logger) error {
	if len(rows) == 0 {
		return nil
	}
	keys := make([]rowKey, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sortRowKeys(keys)
	params := make([]writerq.AddMinuteParams, 0, len(keys))
	type hourKey struct {
		tenant uuid.UUID
		hour   int64
	}
	for _, k := range keys {
		r := rows[k]
		start := time.Unix(k.minute, 0).UTC()
		params = append(params, writerq.AddMinuteParams{
			TenantID: k.tenant, BucketStart: start, Service: k.service, Method: k.method, Route: k.route,
			StatusCode: k.status, Source: k.source, Counts: toInt64(r.counts), Sum: r.sum,
			Count: int64(r.count), Lossy: r.lossy, //nolint:gosec // as in toInt64
		})
	}
	stored, err := addMinutes(ctx, tx, params, log)
	if err != nil {
		return err
	}
	res.rows = len(stored)
	res.noPartition = len(params) - len(stored)
	var marks []writerq.MarkRollupPendingParams
	hours := map[hourKey]bool{}
	for _, p := range stored {
		hk := hourKey{p.TenantID, p.BucketStart.Truncate(time.Hour).Unix()}
		if !hours[hk] {
			hours[hk] = true
			marks = append(marks, writerq.MarkRollupPendingParams{TenantID: p.TenantID, BucketStart: p.BucketStart})
		}
	}
	if len(marks) == 0 {
		return nil
	}
	f, done := batchErr("mark rollup hours")
	q.MarkRollupPending(ctx, marks).Exec(f)
	return done()
}

func writeState(ctx context.Context, q *writerq.Queries, state *State, now time.Time) error {
	changed := state.Changed()
	if len(changed) > 0 {
		params := make([]writerq.UpsertSeriesStateParams, 0, len(changed))
		for _, k := range changed {
			st, _ := state.Series(k)
			owner, ok := state.Owner(k)
			if !ok {
				return errUnknownSeries
			}
			tenant, err := uuid.Parse(owner)
			if err != nil {
				return fmt.Errorf("series tenant: %w", err)
			}
			params = append(params, writerq.UpsertSeriesStateParams{
				TenantID: tenant, SeriesHash: append([]byte(nil), k.Hash[:]...), StartUnixNano: st.Start,
				LastUnixNano: st.Last, LastCount: int64(st.Count), LastSum: st.Sum, //nolint:gosec // as in toInt64
				LastCounts: toInt64(st.Counts), SeenAt: now,
			})
		}
		f, done := batchErr("store series state")
		q.UpsertSeriesState(ctx, params).Exec(f)
		if err := done(); err != nil {
			return err
		}
	}
	touched := state.Touched()
	if len(touched) == 0 {
		return nil
	}
	params := make([]writerq.TouchResourceParams, 0, len(touched)+1)
	for _, t := range touched {
		tenant, err := uuid.Parse(t.Key.Tenant)
		if err != nil {
			return fmt.Errorf("resource tenant: %w", err)
		}
		hash, err := hex.DecodeString(t.Key.ResourceHash)
		if err != nil {
			return fmt.Errorf("resource hash: %w", err)
		}
		if t.New {
			// first_seen_at is written only by the insert, so the first
			// point's time goes first and the latest one updates last_seen_at
			params = append(params, writerq.TouchResourceParams{TenantID: tenant, ResourceHash: hash, StartUnixNano: t.Key.Start, SeenAt: time.Unix(0, t.FirstSeen).UTC()})
		}
		params = append(params, writerq.TouchResourceParams{TenantID: tenant, ResourceHash: hash, StartUnixNano: t.Key.Start, SeenAt: time.Unix(0, t.LastSeen).UTC()})
	}
	f, done := batchErr("touch resources")
	q.TouchResource(ctx, params).Exec(f)
	return done()
}

func writeWarnings(ctx context.Context, q *writerq.Queries, warns map[warnKey]int64) error {
	keys := make([]warnKey, 0, len(warns))
	for k, n := range warns {
		if n > 0 {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b warnKey) int {
		if c := compareBytes(a.tenant[:], b.tenant[:]); c != 0 {
			return c
		}
		if a.day != b.day {
			if a.day < b.day {
				return -1
			}
			return 1
		}
		return compareStrings(a.kind, b.kind)
	})
	for _, k := range keys {
		err := q.AddIngestWarning(ctx, writerq.AddIngestWarningParams{
			TenantID: k.tenant, Day: time.Unix(k.day, 0).UTC(), Kind: k.kind, Count: warns[k],
		})
		if err != nil {
			return fmt.Errorf("add warning %s: %w", k.kind, err)
		}
	}
	return nil
}

var errUnknownSeries = errors.New("metricswriter: changed series without a tenant")

// sqlstate of a row that no partition accepts
const noPartition = "23514"

// addMinutes adds the minute rows. normally one batch does it. a minute
// outside every partition fails the whole batch with 23514; it can never be
// stored, and retrying it would block the queue, so the rows are then added
// one at a time, each behind its own savepoint, and the ones without a
// partition are logged and left out. it returns the rows that were stored.
func addMinutes(ctx context.Context, tx pgx.Tx, params []writerq.AddMinuteParams, log *slog.Logger) ([]writerq.AddMinuteParams, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("savepoint: %w", err)
	}
	f, done := batchErr("add minute rows")
	writerq.New(sp).AddMinute(ctx, params).Exec(f)
	err = done()
	if err == nil {
		if err := sp.Commit(ctx); err != nil {
			return nil, fmt.Errorf("release savepoint: %w", err)
		}
		return params, nil
	}
	if !isNoPartition(err) {
		_ = sp.Rollback(ctx)
		return nil, err
	}
	if err := sp.Rollback(ctx); err != nil {
		return nil, fmt.Errorf("rollback to savepoint: %w", err)
	}
	stored := make([]writerq.AddMinuteParams, 0, len(params))
	for _, p := range params {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("savepoint: %w", err)
		}
		f, done := batchErr("add minute row")
		writerq.New(sp).AddMinute(ctx, []writerq.AddMinuteParams{p}).Exec(f)
		if err := done(); err != nil {
			_ = sp.Rollback(ctx)
			if !isNoPartition(err) {
				return nil, err
			}
			log.Error("minute has no partition, its requests are not stored",
				"tenant", p.TenantID.String(), "minute", p.BucketStart.Format(time.RFC3339), "requests", p.Count)
			continue
		}
		if err := sp.Commit(ctx); err != nil {
			return nil, fmt.Errorf("release savepoint: %w", err)
		}
		stored = append(stored, p)
	}
	return stored, nil
}

func isNoPartition(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == noPartition && strings.Contains(pe.Message, "no partition")
}

type tokenKey struct{ tenant, token uuid.UUID }

// refuse marks every message whose tenant is removed or whose token is
// revoked or unknown to the tenant. the status is read with a share lock on
// the token row, so a revocation that commits after this check waits for
// this transaction, and the purge that follows it deletes what it wrote.
func refuse(ctx context.Context, q *writerq.Queries, items []*item) error {
	reasons := map[tokenKey]string{}
	for _, it := range items {
		if it.dlq != "" {
			continue
		}
		for i := range it.decoded.Points {
			k := tokenKey{it.tenant, it.decoded.Points[i].TokenID}
			reason, ok := reasons[k]
			if !ok {
				st, err := q.IngestStatus(ctx, writerq.IngestStatusParams{TokenID: k.token, TenantID: k.tenant})
				switch {
				case errors.Is(err, pgx.ErrNoRows):
					reason = "token " + k.token.String() + " does not belong to the tenant"
				case err != nil:
					return fmt.Errorf("ingest status of token %s: %w", k.token, err)
				case st.TenantStatus == "removed":
					reason = "tenant is removed"
				case st.TokenStatus != "active":
					reason = "token " + k.token.String() + " is " + st.TokenStatus
				}
				reasons[k] = reason
			}
			if reason != "" {
				it.refused = reason
				break
			}
		}
	}
	return nil
}
