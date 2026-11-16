// Package onchain serves the on-chain witness of control-plane-api section
// 15: the stellar accounts and soroban contracts a project declares, the
// proof that the project controls each one, their daily ledger activity as
// the chain-activity task stored it, and the two-way link between the
// project's verified domain and its stellar.toml. the same reads serve the
// tenant (under cp_tenant) and operators (under cp_operator); only the
// relation the sites come from differs.
package onchain

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/chain"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/jobs/chainactivity"
)

// the relation a read takes sites from: cp_tenant reads cp.sites under
// row-level security, cp_operator the view without the verification secret
const (
	TenantSites   = "cp.sites"
	OperatorSites = "cp.operator_sites"
)

// reasons a day has no numbers (contract 15.4). a read error is stored as
// its own code (chain.Code*), so the reason names the failure.
const (
	MissingNotComplete    = "day_not_complete"
	MissingBeforeBackfill = "before_backfill_window"
	MissingBeforeRPC      = "before_rpc_retention"
	MissingNotReadYet     = "not_read_yet"
)

// verification states
const (
	StatusVerified   = "verified"
	StatusUnverified = "unverified"
)

// Range is a closed range of utc days.
type Range struct {
	From time.Time
	To   time.Time
}

// Days lists the days of the range in order.
func (r Range) Days() []time.Time {
	var out []time.Time
	for d := r.From; !d.After(r.To); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

// Verification is the proof state of one id.
type Verification struct {
	Status        string           `json:"status"`
	Method        *string          `json:"method"`
	VerifiedAt    *httpx.Timestamp `json:"verified_at"`
	Via           *string          `json:"via"`
	Detail        map[string]any   `json:"detail"`
	LastAttemptAt *httpx.Timestamp `json:"last_attempt_at"`
	LastError     *string          `json:"last_error"`
}

// Read is the read state of one id (agg.chain_reads).
type Read struct {
	Source        string           `json:"source"`
	CoveredFrom   *string          `json:"covered_from"`
	CoveredTo     *string          `json:"covered_to"`
	LastSuccessAt *httpx.Timestamp `json:"last_success_at"`
	LastError     *string          `json:"last_error"`
	LastErrorAt   *httpx.Timestamp `json:"last_error_at"`
	HomeDomain    *string          `json:"home_domain"`
	AccountExists *bool            `json:"account_exists"`
}

// Point is one day of an id. a day without a stored row has null numbers
// and the reason in Missing; a stored zero is a real zero.
type Point struct {
	Day            string  `json:"day"`
	Operations     *int64  `json:"operations"`
	Transactions   *int64  `json:"transactions"`
	Counterparties *int64  `json:"counterparties"`
	Missing        *string `json:"missing"`
}

// Totals are an id's numbers over the read days of the range.
// counterparties are distinct per day, so the range carries the peak day
// and never a sum that would count one counterparty several times.
type Totals struct {
	Operations         int64 `json:"operations"`
	Transactions       int64 `json:"transactions"`
	PeakCounterparties int64 `json:"peak_counterparties"`
	DaysRead           int   `json:"days_read"`
	DaysMissing        int   `json:"days_missing"`
}

// ID is one declared account or contract with its proof, read state,
// totals and daily series.
type ID struct {
	ID           uuid.UUID       `json:"id"`
	Strkey       string          `json:"strkey"`
	Kind         string          `json:"kind"`
	AddedBy      string          `json:"added_by"`
	CreatedAt    httpx.Timestamp `json:"created_at"`
	Verification Verification    `json:"verification"`
	Read         Read            `json:"read"`
	Totals       Totals          `json:"totals"`
	Series       []Point         `json:"series"`
}

// ProjectTotals add up the ids of a project. operations and transactions
// are summed over ids; an operation between two ids of the same project
// counts once for each.
type ProjectTotals struct {
	IDs          int   `json:"ids"`
	IDsVerified  int   `json:"ids_verified"`
	Accounts     int   `json:"accounts"`
	Contracts    int   `json:"contracts"`
	Operations   int64 `json:"operations"`
	Transactions int64 `json:"transactions"`
	DaysMissing  int   `json:"days_missing"`
}

// Sources names the ledger apis behind the numbers (contract 15.2).
type Sources struct {
	Accounts            string `json:"accounts"`
	Contracts           string `json:"contracts"`
	AccountBackfillDays int    `json:"account_backfill_days"`
}

// View is the answer of GET /api/tenants/{tenant_id}/onchain and of the
// operator's on-chain detail (contract 15.3).
type View struct {
	From    string        `json:"from"`
	To      string        `json:"to"`
	Days    []string      `json:"days"`
	Sources Sources       `json:"sources"`
	Totals  ProjectTotals `json:"totals"`
	IDs     []ID          `json:"ids"`
	Link    Link          `json:"link"`
}

// DefaultSources are the sources of the stored rows.
var DefaultSources = Sources{
	Accounts: chain.SourceHorizon, Contracts: chain.SourceRPCEvents,
	AccountBackfillDays: chainactivity.AccountBackfillDays,
}

type idRow struct {
	ID            uuid.UUID
	Strkey        string
	Kind          string
	AddedBy       string
	CreatedAt     time.Time
	Method        *string
	VerifiedAt    *time.Time
	Via           *string
	Detail        []byte
	LastVerifyAt  *time.Time
	LastVerifyErr *string
	CoveredFrom   *time.Time
	CoveredTo     *time.Time
	LastSuccessAt *time.Time
	LastError     *string
	LastErrorAt   *time.Time
	HomeDomain    *string
	AccountExists *bool
}

// loadIDs reads a tenant's declared ids with their read state, accounts
// first, then by when they were added.
func loadIDs(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]idRow, error) {
	rows, err := tx.Query(ctx, `
		select c.id, c.strkey, c.kind, c.added_by, c.created_at, c.verify_method, c.verified_at, c.verified_via,
		       c.verify_detail, c.last_verify_at, c.last_verify_error,
		       r.covered_from, r.covered_to, r.last_success_at, r.last_error, r.last_error_at, r.home_domain, r.account_exists
		from cp.chain_ids c
		left join agg.chain_reads r on r.tenant_id = c.tenant_id and r.strkey = c.strkey
		where c.tenant_id = $1
		order by c.kind, c.created_at, c.strkey`, tenant)
	if err != nil {
		return nil, fmt.Errorf("onchain: ids: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[idRow])
}

type dayRow struct {
	Strkey         string
	Day            time.Time
	Operations     int64
	Transactions   int64
	Counterparties int64
}

func date(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

// missing says why day has no row for an id.
func missing(r idRow, day, today time.Time) string {
	switch {
	case !day.Before(today):
		return MissingNotComplete
	case r.CoveredFrom != nil && day.Before(*r.CoveredFrom):
		if r.Kind == "account" {
			return MissingBeforeBackfill
		}
		return MissingBeforeRPC
	case r.LastError != nil:
		return *r.LastError
	}
	return MissingNotReadYet
}

// Load reads a tenant's on-chain view over rg. sites is TenantSites or
// OperatorSites, the relation the caller's role may read. today is the
// current utc day; it is never complete.
func Load(ctx context.Context, tx pgx.Tx, sites string, tenant uuid.UUID, rg Range, today time.Time) (View, error) {
	days := rg.Days()
	v := View{From: rg.From.Format(time.DateOnly), To: rg.To.Format(time.DateOnly), Sources: DefaultSources, IDs: []ID{}}
	for _, d := range days {
		v.Days = append(v.Days, d.Format(time.DateOnly))
	}
	ids, err := loadIDs(ctx, tx, tenant)
	if err != nil {
		return v, err
	}
	rows, err := tx.Query(ctx, `
		select d.strkey, d.day, d.operations, d.transactions, d.counterparties
		from agg.chain_daily d
		join cp.chain_ids c on c.tenant_id = d.tenant_id and c.strkey = d.strkey
		where d.tenant_id = $1 and d.day between $2::date and $3::date`, tenant, rg.From, rg.To)
	if err != nil {
		return v, fmt.Errorf("onchain: days: %w", err)
	}
	dayRows, err := pgx.CollectRows(rows, pgx.RowToStructByPos[dayRow])
	if err != nil {
		return v, fmt.Errorf("onchain: days: %w", err)
	}
	byKey := map[string]dayRow{}
	for _, d := range dayRows {
		byKey[d.Strkey+"/"+d.Day.Format(time.DateOnly)] = d
	}
	for _, r := range ids {
		x := ID{
			ID: r.ID, Strkey: r.Strkey, Kind: r.Kind, AddedBy: r.AddedBy, CreatedAt: httpx.Timestamp(r.CreatedAt),
			Verification: Verification{
				Status: StatusUnverified, Method: r.Method, VerifiedAt: httpx.TimestampPtr(r.VerifiedAt), Via: r.Via,
				LastAttemptAt: httpx.TimestampPtr(r.LastVerifyAt), LastError: r.LastVerifyErr,
			},
			Read: Read{
				Source: DefaultSources.Accounts, CoveredFrom: date(r.CoveredFrom), CoveredTo: date(r.CoveredTo),
				LastSuccessAt: httpx.TimestampPtr(r.LastSuccessAt), LastError: r.LastError,
				LastErrorAt: httpx.TimestampPtr(r.LastErrorAt), HomeDomain: r.HomeDomain, AccountExists: r.AccountExists,
			},
			Series: make([]Point, 0, len(days)),
		}
		if r.Kind == "contract" {
			x.Read.Source = DefaultSources.Contracts
		}
		if r.VerifiedAt != nil {
			x.Verification.Status = StatusVerified
		}
		if len(r.Detail) > 0 {
			if err := json.Unmarshal(r.Detail, &x.Verification.Detail); err != nil {
				return v, fmt.Errorf("onchain: verify detail of %s: %w", r.Strkey, err)
			}
		}
		for _, d := range days {
			p := Point{Day: d.Format(time.DateOnly)}
			if row, ok := byKey[r.Strkey+"/"+p.Day]; ok {
				ops, txs, peers := row.Operations, row.Transactions, row.Counterparties
				p.Operations, p.Transactions, p.Counterparties = &ops, &txs, &peers
				x.Totals.Operations += ops
				x.Totals.Transactions += txs
				x.Totals.PeakCounterparties = max(x.Totals.PeakCounterparties, peers)
				x.Totals.DaysRead++
			} else {
				m := missing(r, d, today)
				p.Missing = &m
				x.Totals.DaysMissing++
			}
			x.Series = append(x.Series, p)
		}
		v.Totals.IDs++
		if x.Verification.Status == StatusVerified {
			v.Totals.IDsVerified++
		}
		if r.Kind == "account" {
			v.Totals.Accounts++
		} else {
			v.Totals.Contracts++
		}
		v.Totals.Operations += x.Totals.Operations
		v.Totals.Transactions += x.Totals.Transactions
		v.Totals.DaysMissing += x.Totals.DaysMissing
		v.IDs = append(v.IDs, x)
	}
	link, err := loadLink(ctx, tx, sites, tenant, ids)
	if err != nil {
		return v, err
	}
	v.Link = link
	return v, nil
}

// Summary is a project's on-chain column in the operator lists and the
// exports (contract 15.7): ids, proofs and numbers over the range.
type Summary struct {
	IDs          int64 `json:"ids"`
	IDsVerified  int64 `json:"ids_verified"`
	Operations   int64 `json:"operations"`
	Transactions int64 `json:"transactions"`
	// day-id pairs of the range without a stored row, today excluded
	DaysMissing int64 `json:"days_missing"`
	// ids whose last read failed
	IDsFailing int64 `json:"ids_failing"`
}

// Summaries reads the on-chain summary of every tenant in tenants over rg.
// a tenant without ids gets a zero summary.
func Summaries(ctx context.Context, tx pgx.Tx, tenants []uuid.UUID, rg Range, today time.Time) (map[uuid.UUID]Summary, error) {
	out := make(map[uuid.UUID]Summary, len(tenants))
	for _, id := range tenants {
		out[id] = Summary{}
	}
	if len(tenants) == 0 {
		return out, nil
	}
	// complete days of the range: today and later are never complete
	last := rg.To
	if !last.Before(today) {
		last = today.AddDate(0, 0, -1)
	}
	complete := 0
	if !last.Before(rg.From) {
		complete = int(last.Sub(rg.From).Hours()/24) + 1
	}
	rows, err := tx.Query(ctx, `
		with c as (
		  select c.tenant_id, c.strkey, c.verified_at is not null as verified,
		         r.last_error is not null as failing
		  from cp.chain_ids c
		  left join agg.chain_reads r on r.tenant_id = c.tenant_id and r.strkey = c.strkey
		  where c.tenant_id = any($1::uuid[])
		),
		d as (
		  select d.tenant_id, d.strkey,
		         sum(d.operations)::bigint as operations, sum(d.transactions)::bigint as transactions,
		         count(*) filter (where d.day <= $4::date)::bigint as days
		  from agg.chain_daily d
		  join c on c.tenant_id = d.tenant_id and c.strkey = d.strkey
		  where d.day between $2::date and $3::date
		  group by d.tenant_id, d.strkey
		)
		select c.tenant_id, count(*)::bigint, (count(*) filter (where c.verified))::bigint,
		       coalesce(sum(d.operations), 0)::bigint, coalesce(sum(d.transactions), 0)::bigint,
		       (count(*) * $5::bigint - coalesce(sum(d.days), 0))::bigint,
		       (count(*) filter (where c.failing))::bigint
		from c left join d on d.tenant_id = c.tenant_id and d.strkey = c.strkey
		group by c.tenant_id`, tenants, rg.From, rg.To, last, complete)
	if err != nil {
		return nil, fmt.Errorf("onchain: summaries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var s Summary
		if err := rows.Scan(&id, &s.IDs, &s.IDsVerified, &s.Operations, &s.Transactions, &s.DaysMissing, &s.IDsFailing); err != nil {
			return nil, fmt.Errorf("onchain: summaries: %w", err)
		}
		out[id] = s
	}
	return out, rows.Err()
}

// DayTotal is one day of a project's summed activity, for compare series
// and exports. Read is how many of the project's ids have a row for the
// day; Operations and Transactions are null when none has.
type DayTotal struct {
	Day          string `json:"day"`
	IDsRead      int64  `json:"ids_read"`
	Operations   *int64 `json:"operations"`
	Transactions *int64 `json:"transactions"`
}

// DailyTotals reads the summed daily activity of every tenant over rg,
// one point per day of the range.
func DailyTotals(ctx context.Context, tx pgx.Tx, tenants []uuid.UUID, rg Range) (map[uuid.UUID][]DayTotal, error) {
	days := rg.Days()
	out := make(map[uuid.UUID][]DayTotal, len(tenants))
	for _, id := range tenants {
		pts := make([]DayTotal, len(days))
		for i, d := range days {
			pts[i] = DayTotal{Day: d.Format(time.DateOnly)}
		}
		out[id] = pts
	}
	if len(tenants) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `
		select d.tenant_id, d.day, count(*)::bigint, sum(d.operations)::bigint, sum(d.transactions)::bigint
		from agg.chain_daily d
		join cp.chain_ids c on c.tenant_id = d.tenant_id and c.strkey = d.strkey
		where d.tenant_id = any($1::uuid[]) and d.day between $2::date and $3::date
		group by d.tenant_id, d.day`, tenants, rg.From, rg.To)
	if err != nil {
		return nil, fmt.Errorf("onchain: daily totals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var day time.Time
		var n, ops, txs int64
		if err := rows.Scan(&id, &day, &n, &ops, &txs); err != nil {
			return nil, fmt.Errorf("onchain: daily totals: %w", err)
		}
		i := int(day.Sub(rg.From).Hours() / 24)
		pts := out[id]
		if i < 0 || i >= len(pts) {
			return nil, fmt.Errorf("onchain: day %s outside the range", day.Format(time.DateOnly))
		}
		pts[i].IDsRead, pts[i].Operations, pts[i].Transactions = n, &ops, &txs
	}
	return out, rows.Err()
}

// kinds lists the strkeys of rows of one kind.
func kinds(rows []idRow, kind string) []idRow {
	return slices.DeleteFunc(slices.Clone(rows), func(r idRow) bool { return r.Kind != kind })
}
