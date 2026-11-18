package operator

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/exports"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/scfawards"
	"github.com/justmert/veristar/internal/db"
)

// the scf award directory (contract 19.4): every imported award with its
// match to an enrolled project, the project's cohort beside the award and
// whether Veristar measures the project, with counts by round and
// category. it answers which funded projects are measured. the routes only
// read; platformctl scf import writes the directory.

// SCFAwards serves GET /api/operator/scf/awards, its csv and the import
// history.
type SCFAwards struct {
	db  *db.DB
	now func() time.Time
}

// NewSCFAwards needs the database with the cp_operator and cp_system pools
// (the csv's audit row) and a clock.
func NewSCFAwards(d *db.DB, now func() time.Time) (*SCFAwards, error) {
	if d == nil || now == nil {
		return nil, errors.New("operator: the scf award routes need a database and a clock")
	}
	return &SCFAwards{db: d, now: now}, nil
}

// Routes registers the paths under the guarded /api/operator router.
func (h *SCFAwards) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/scf/awards", httpx.HandlerFunc(h.list))
	r.Method(http.MethodGet, "/scf/awards.csv", httpx.HandlerFunc(h.csv))
	r.Method(http.MethodGet, "/scf/imports", httpx.HandlerFunc(h.imports))
}

// SCFImport is one import run.
type SCFImport struct {
	ID         uuid.UUID       `json:"id"`
	FileSHA256 string          `json:"file_sha256"`
	FileBytes  int64           `json:"file_bytes"`
	HasHeader  bool            `json:"has_header"`
	Delimiter  string          `json:"delimiter"`
	Records    int64           `json:"records"`
	Inserted   int64           `json:"inserted"`
	Updated    int64           `json:"updated"`
	Unchanged  int64           `json:"unchanged"`
	Rejected   int64           `json:"rejected"`
	Actor      string          `json:"actor"`
	ImportedAt httpx.Timestamp `json:"imported_at"`
}

// SCFAwardMatch is the match of an award.
type SCFAwardMatch struct {
	// State is matched, ambiguous or not_enrolled
	State string `json:"state"`
	// Tenant is the matched project; null unless matched
	Tenant *TenantRef `json:"tenant"`
	// Evidence is what tied the award to the matched project
	Evidence []scfawards.Evidence `json:"evidence"`
	// Candidates are the projects of an ambiguous award, with their evidence
	Candidates []SCFCandidate `json:"candidates"`
	// SharedWith are the other awards that point to the same project
	SharedWith []uuid.UUID `json:"shared_with"`
}

// SCFCandidate is a project an ambiguous award points to.
type SCFCandidate struct {
	Tenant   TenantRef            `json:"tenant"`
	Evidence []scfawards.Evidence `json:"evidence"`
}

// SCFMeasured is what Veristar measures of a matched project over the
// range.
type SCFMeasured struct {
	// Measured is true when the project has web, api or on-chain data in
	// the range
	Measured           bool    `json:"measured"`
	HasWebData         bool    `json:"has_web_data"`
	HasAPIData         bool    `json:"has_api_data"`
	HasOnchainData     bool    `json:"has_onchain_data"`
	Sites              int64   `json:"sites"`
	SitesVerified      int64   `json:"sites_verified"`
	VerifiedOnchainIDs int64   `json:"verified_onchain_ids"`
	LastWebDay         *string `json:"last_web_day"`
	LastAPIDay         *string `json:"last_api_day"`
	LastOnchainDay     *string `json:"last_onchain_day"`
	// LastActivity is the latest of the three days
	LastActivity *string `json:"last_activity"`
}

// SCFCohort compares a matched project's cohort with the award.
type SCFCohort struct {
	// State is one of the cohort states of contract 19.4
	State          string            `json:"state"`
	Award          scfawards.Cohort  `json:"award"`
	Confirmed      *scfawards.Cohort `json:"confirmed"`
	Proposal       *scfawards.Cohort `json:"proposal"`
	ProposalSource *string           `json:"proposal_source"`
}

// SCFAward is one award of the directory.
type SCFAward struct {
	ID              uuid.UUID       `json:"id"`
	Project         string          `json:"project"`
	Round           string          `json:"round"`
	Category        string          `json:"category"`
	Program         string          `json:"program"`
	AwardDate       *string         `json:"award_date"`
	AwardAmountUSD  string          `json:"award_amount_usd"`
	TranchePercent  *int            `json:"tranche_percent"`
	TrancheStatus   *string         `json:"tranche_status"`
	WebsiteHost     *string         `json:"website_host"`
	StellarIDs      []string        `json:"stellar_ids"`
	FirstImportedAt httpx.Timestamp `json:"first_imported_at"`
	ImportedAt      httpx.Timestamp `json:"imported_at"`
	Match           SCFAwardMatch   `json:"match"`
	// Measured and Cohort are null unless the award is matched
	Measured *SCFMeasured `json:"measured"`
	Cohort   *SCFCohort   `json:"cohort"`
}

// SCFCount is one line of the summary.
type SCFCount struct {
	// Key is the round or the category; empty in the totals
	Key            string `json:"key,omitempty"`
	Awards         int64  `json:"awards"`
	AwardAmountUSD string `json:"award_amount_usd"`
	Matched        int64  `json:"matched"`
	Ambiguous      int64  `json:"ambiguous"`
	NotEnrolled    int64  `json:"not_enrolled"`
	// Measured counts the matched awards whose project has data in the range
	Measured int64 `json:"measured"`
}

// SCFSummary counts every award of the directory, whatever the filters.
type SCFSummary struct {
	Total      SCFCount   `json:"total"`
	ByRound    []SCFCount `json:"by_round"`
	ByCategory []SCFCount `json:"by_category"`
	// CohortStates counts the matched awards by cohort state
	CohortStates map[string]int64 `json:"cohort_states"`
}

// SCFAwardList is the answer of GET /api/operator/scf/awards.
type SCFAwardList struct {
	From       string     `json:"from"`
	To         string     `json:"to"`
	LastImport *SCFImport `json:"last_import"`
	Summary    SCFSummary `json:"summary"`
	Items      []SCFAward `json:"items"`
}

// scfFilter is the query of the list and the csv.
type scfFilter struct {
	rg       dayRange
	state    string
	round    string
	category string
	q        string
	measured *bool
}

var scfStates = []string{scfawards.StateMatched, scfawards.StateAmbiguous, scfawards.StateNotEnrolled}

func (h *SCFAwards) parse(q url.Values) (scfFilter, error) {
	var f scfFilter
	if err := checkParams(q, "from", "to", "state", "round", "category", "q", "measured"); err != nil {
		return f, err
	}
	var err error
	if f.rg, err = parseDayRange(q, h.now(), DefaultRangeDays, MaxRangeDays); err != nil {
		return f, err
	}
	if v, ok, err := oneValue(q, "state"); err != nil {
		return f, err
	} else if ok {
		if !containsString(scfStates, v) {
			return f, httpx.BadQuery("state must be matched, ambiguous or not_enrolled")
		}
		f.state = v
	}
	for _, p := range []struct {
		key string
		dst *string
		max int
	}{{"round", &f.round, cohortFieldMax}, {"category", &f.category, cohortFieldMax}, {"q", &f.q, 100}} {
		v, ok, err := oneValue(q, p.key)
		if err != nil {
			return f, err
		}
		if ok {
			if n := utf8.RuneCountInString(v); n < 1 || n > p.max {
				return f, httpx.BadQuery(fmt.Sprintf("%s must be 1 to %d characters", p.key, p.max))
			}
			*p.dst = v
		}
	}
	if f.measured, err = parseBool(q, "measured"); err != nil {
		return f, err
	}
	return f, nil
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (f scfFilter) keep(a SCFAward) bool {
	switch {
	case f.state != "" && a.Match.State != f.state:
		return false
	case f.round != "" && a.Round != f.round:
		return false
	case f.category != "" && a.Category != f.category:
		return false
	case f.q != "" && !strings.Contains(strings.ToLower(a.Project), strings.ToLower(f.q)):
		return false
	case f.measured != nil && (a.Measured != nil && a.Measured.Measured) != *f.measured:
		return false
	}
	return true
}

// load reads the directory, matches it and builds every row, in one
// snapshot.
func (h *SCFAwards) load(ctx context.Context, f scfFilter) (SCFAwardList, error) {
	out := SCFAwardList{From: f.rg.From.Format(time.DateOnly), To: f.rg.To.Format(time.DateOnly), Items: []SCFAward{}}
	var all []SCFAward
	err := h.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		imp, err := lastImport(ctx, tx)
		if err != nil {
			return err
		}
		out.LastImport = imp
		awards, err := scfawards.LoadAwards(ctx, tx)
		if err != nil {
			return err
		}
		tenants, err := scfawards.LoadTenants(ctx, tx)
		if err != nil {
			return err
		}
		matches := scfawards.MatchAll(awards, tenants)
		var matched []uuid.UUID
		for _, a := range awards {
			if id := matches[a.ID].Tenant(); id != uuid.Nil {
				matched = append(matched, id)
			}
		}
		cohorts, err := scfawards.LoadCohorts(ctx, tx, matched)
		if err != nil {
			return err
		}
		measured, err := loadMeasured(ctx, tx, matched, f.rg)
		if err != nil {
			return err
		}
		ref := func(t *scfawards.Tenant) TenantRef {
			return TenantRef{ID: t.ID, Slug: t.Slug, Name: t.Name, Status: t.Status, CreatedAt: httpx.Timestamp(t.CreatedAt)}
		}
		for _, a := range awards {
			m := matches[a.ID]
			row := SCFAward{
				ID: a.ID, Project: a.Project, Round: a.Round, Category: a.Category, Program: a.Program,
				AwardDate: dateString(a.AwardDate), AwardAmountUSD: scfawards.FormatCents(a.AmountCents),
				TranchePercent: a.TranchePercent, TrancheStatus: a.TrancheStatus, WebsiteHost: a.WebsiteHost,
				StellarIDs: a.Strkeys, FirstImportedAt: httpx.Timestamp(a.FirstImportedAt), ImportedAt: httpx.Timestamp(a.ImportedAt),
				Match: SCFAwardMatch{State: m.State, Evidence: []scfawards.Evidence{}, Candidates: []SCFCandidate{},
					SharedWith: m.SharedWith},
			}
			if row.StellarIDs == nil {
				row.StellarIDs = []string{}
			}
			if row.Match.SharedWith == nil {
				row.Match.SharedWith = []uuid.UUID{}
			}
			if m.State == scfawards.StateMatched {
				t := tenants[m.Tenant()]
				r := ref(t)
				row.Match.Tenant, row.Match.Evidence = &r, m.Candidates[0].Evidence
				ms := measured[t.ID]
				row.Measured = &ms
				c := cohorts[t.ID]
				row.Cohort = &SCFCohort{State: scfawards.CohortState(c, a), Award: scfawards.CohortOf(a),
					Confirmed: c.Confirmed, Proposal: c.Proposal, ProposalSource: c.ProposalSource}
			} else if m.State == scfawards.StateAmbiguous {
				for _, c := range m.Candidates {
					row.Match.Candidates = append(row.Match.Candidates, SCFCandidate{Tenant: ref(tenants[c.TenantID]), Evidence: c.Evidence})
				}
			}
			all = append(all, row)
		}
		return nil
	})
	if err != nil {
		return out, fmt.Errorf("operator: scf awards: %w", err)
	}
	out.Summary = summarize(all)
	for _, a := range all {
		if f.keep(a) {
			out.Items = append(out.Items, a)
		}
	}
	return out, nil
}

func lastImport(ctx context.Context, tx pgx.Tx) (*SCFImport, error) {
	list, err := readImports(ctx, tx, 1)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

func readImports(ctx context.Context, tx pgx.Tx, limit int) ([]SCFImport, error) {
	rows, err := tx.Query(ctx, `select id, file_sha256, file_bytes, has_header, delimiter, records, inserted, updated,
		       unchanged, rejected, actor, imported_at
		from cp.scf_award_imports order by imported_at desc, id desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SCFImport, error) {
		var x SCFImport
		var sum []byte
		var at time.Time
		err := r.Scan(&x.ID, &sum, &x.FileBytes, &x.HasHeader, &x.Delimiter, &x.Records, &x.Inserted, &x.Updated,
			&x.Unchanged, &x.Rejected, &x.Actor, &at)
		x.FileSHA256, x.ImportedAt = hex.EncodeToString(sum), httpx.Timestamp(at)
		return x, err
	})
}

// measuredSQL reads, for the matched projects, the last day of each kind
// of data in the range and the site and on-chain counts. web data are
// primary-band page views (agg.web_daily), api data application requests
// (api.operator_daily, as in the global views), on-chain data operations
// of verified ids (agg.chain_daily).
const measuredSQL = `
with t as (select unnest(@ids::uuid[]) as id),
w as (
  select d.tenant_id, max(d.day) as last_day
  from agg.web_daily d
  where d.tenant_id = any(@ids::uuid[]) and d.band = 'primary' and d.pageviews > 0
    and d.day between @from_day::date and @to_day::date
  group by d.tenant_id
),
a as (
  select m.tenant_id, max(m.bucket_start)::date as last_day
  from api.operator_daily(@from_at::timestamptz, @to_end::timestamptz, @ids::uuid[]) m
  where m.traffic_class = 'app' and m.count > 0
  group by m.tenant_id
),
c as (
  select d.tenant_id, max(d.day) as last_day
  from agg.chain_daily d
  join cp.chain_ids i on i.tenant_id = d.tenant_id and i.strkey = d.strkey and i.verified_at is not null
  where d.tenant_id = any(@ids::uuid[]) and d.operations > 0 and d.day between @from_day::date and @to_day::date
  group by d.tenant_id
),
v as (
  select tenant_id, count(*)::bigint as ids from cp.chain_ids
  where tenant_id = any(@ids::uuid[]) and verified_at is not null group by tenant_id
),
s as (
  select tenant_id, count(*)::bigint as sites,
         count(*) filter (where verified_at is not null and last_verify_error is null)::bigint as verified
  from cp.operator_sites where tenant_id = any(@ids::uuid[]) and status <> 'removed' group by tenant_id
)
select t.id, w.last_day, a.last_day, c.last_day, coalesce(v.ids, 0), coalesce(s.sites, 0), coalesce(s.verified, 0)
from t
left join w on w.tenant_id = t.id
left join a on a.tenant_id = t.id
left join c on c.tenant_id = t.id
left join v on v.tenant_id = t.id
left join s on s.tenant_id = t.id`

func loadMeasured(ctx context.Context, tx pgx.Tx, ids []uuid.UUID, rg dayRange) (map[uuid.UUID]SCFMeasured, error) {
	out := map[uuid.UUID]SCFMeasured{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, measuredSQL, pgx.NamedArgs{
		"ids": ids, "from_day": rg.From, "to_day": rg.To, "from_at": rg.From, "to_end": rg.To.AddDate(0, 0, 1),
	})
	if err != nil {
		return nil, fmt.Errorf("measured: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var web, api, chain *time.Time
		var m SCFMeasured
		if err := rows.Scan(&id, &web, &api, &chain, &m.VerifiedOnchainIDs, &m.Sites, &m.SitesVerified); err != nil {
			return nil, fmt.Errorf("measured: %w", err)
		}
		m.HasWebData, m.HasAPIData, m.HasOnchainData = web != nil, api != nil, chain != nil
		m.Measured = m.HasWebData || m.HasAPIData || m.HasOnchainData
		m.LastWebDay, m.LastAPIDay, m.LastOnchainDay = dateString(web), dateString(api), dateString(chain)
		var last *time.Time
		for _, d := range []*time.Time{web, api, chain} {
			if d != nil && (last == nil || d.After(*last)) {
				last = d
			}
		}
		m.LastActivity = dateString(last)
		out[id] = m
	}
	return out, rows.Err()
}

// summarize counts every award by round and category.
func summarize(all []SCFAward) SCFSummary {
	type acc struct {
		SCFCount
		cents int64
	}
	total := &acc{}
	rounds, cats := map[string]*acc{}, map[string]*acc{}
	states := map[string]int64{}
	for _, a := range all {
		cents, _ := centsOf(a.AwardAmountUSD)
		for _, x := range []*acc{total, get(rounds, a.Round), get(cats, a.Category)} {
			x.Awards++
			x.cents += cents
			switch a.Match.State {
			case scfawards.StateMatched:
				x.Matched++
				if a.Measured != nil && a.Measured.Measured {
					x.Measured++
				}
			case scfawards.StateAmbiguous:
				x.Ambiguous++
			default:
				x.NotEnrolled++
			}
		}
		if a.Cohort != nil {
			states[a.Cohort.State]++
		}
	}
	list := func(m map[string]*acc, less func(a, b string) bool) []SCFCount {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return less(keys[i], keys[j]) })
		out := make([]SCFCount, 0, len(keys))
		for _, k := range keys {
			c := m[k].SCFCount
			c.Key, c.AwardAmountUSD = k, scfawards.FormatCents(m[k].cents)
			out = append(out, c)
		}
		return out
	}
	t := total.SCFCount
	t.AwardAmountUSD = scfawards.FormatCents(total.cents)
	return SCFSummary{
		Total:        t,
		ByRound:      list(rounds, func(a, b string) bool { return roundNumber(a) > roundNumber(b) }),
		ByCategory:   list(cats, func(a, b string) bool { return a < b }),
		CohortStates: states,
	}
}

func get[T any](m map[string]*T, k string) *T {
	if v, ok := m[k]; ok {
		return v
	}
	v := new(T)
	m[k] = v
	return v
}

// roundNumber is n of "SCF #n"
func roundNumber(r string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(r, "SCF #"))
	return n
}

func centsOf(s string) (int64, error) {
	whole, frac, _ := strings.Cut(s, ".")
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, err
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	return w*100 + f, err
}

func (h *SCFAwards) list(w http.ResponseWriter, r *http.Request) error {
	f, err := h.parse(r.URL.Query())
	if err != nil {
		return err
	}
	out, err := h.load(r.Context(), f)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *SCFAwards) imports(w http.ResponseWriter, r *http.Request) error {
	if len(r.URL.Query()) > 0 {
		return httpx.BadQuery("this route takes no query parameters")
	}
	var items []SCFImport
	err := h.db.WithOperator(r.Context(), func(tx pgx.Tx) error {
		var err error
		items, err = readImports(r.Context(), tx, 100)
		return err
	})
	if err != nil {
		return fmt.Errorf("operator: scf imports: %w", err)
	}
	if items == nil {
		items = []SCFImport{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

var scfCSVHeader = []string{
	"award_id", "project", "round", "category", "program", "award_date", "award_amount_usd", "tranche_percent",
	"tranche_status", "website_host", "stellar_ids", "match_state", "tenant_id", "tenant_slug", "tenant_name",
	"tenant_status", "evidence", "candidate_tenant_ids", "measured", "has_web_data", "has_api_data", "has_onchain_data",
	"sites", "sites_verified", "verified_onchain_ids", "last_activity", "cohort_state", "confirmed_program",
	"confirmed_round", "confirmed_category", "confirmed_award_date", "proposal_source",
}

func (h *SCFAwards) csv(w http.ResponseWriter, r *http.Request) error {
	f, err := h.parse(r.URL.Query())
	if err != nil {
		return err
	}
	out, err := h.load(r.Context(), f)
	if err != nil {
		return err
	}
	if len(out.Items) > exports.MaxRows {
		return httpx.BadQuery(fmt.Sprintf("the export holds at most %d rows; narrow the filters", exports.MaxRows))
	}
	if err := auditExportIn(r.Context(), h.db, "scf_awards", r.URL.Query(), len(out.Items), nil); err != nil {
		return err
	}
	cw, err := exports.NewWriter(w, exports.FileName("veristar-scf-awards", out.From, out.To), scfCSVHeader)
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	for _, a := range out.Items {
		if err := cw.Row(scfCSVRow(a)...); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	if err := cw.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}

func scfCSVRow(a SCFAward) []string {
	var pct string
	if a.TranchePercent != nil {
		pct = strconv.Itoa(*a.TranchePercent)
	}
	cells := []string{
		a.ID.String(), exports.Text(a.Project), a.Round, exports.Text(a.Category), exports.Text(a.Program),
		exports.StrPtr(a.AwardDate), a.AwardAmountUSD, pct, exports.Text(deref(a.TrancheStatus)),
		exports.StrPtr(a.WebsiteHost), strings.Join(a.StellarIDs, " "), a.Match.State,
	}
	if t := a.Match.Tenant; t != nil {
		cells = append(cells, t.ID.String(), t.Slug, exports.Text(t.Name), t.Status)
	} else {
		cells = append(cells, "", "", "", "")
	}
	var ev []string
	for _, e := range a.Match.Evidence {
		ev = append(ev, e.Kind+":"+e.Value)
	}
	var cand []string
	for _, c := range a.Match.Candidates {
		cand = append(cand, c.Tenant.ID.String())
	}
	cells = append(cells, strings.Join(ev, " "), strings.Join(cand, " "))
	if m := a.Measured; m != nil {
		cells = append(cells, exports.Bool(m.Measured), exports.Bool(m.HasWebData), exports.Bool(m.HasAPIData),
			exports.Bool(m.HasOnchainData), exports.Int(m.Sites), exports.Int(m.SitesVerified),
			exports.Int(m.VerifiedOnchainIDs), exports.StrPtr(m.LastActivity))
	} else {
		cells = append(cells, "", "", "", "", "", "", "", "")
	}
	if c := a.Cohort; c != nil {
		var cf scfawards.Cohort
		if c.Confirmed != nil {
			cf = *c.Confirmed
		}
		cells = append(cells, c.State, exports.Text(deref(cf.Program)), exports.Text(deref(cf.Round)),
			exports.Text(deref(cf.Category)), exports.StrPtr(cf.AwardDate), deref(c.ProposalSource))
	} else {
		cells = append(cells, "", "", "", "", "", "")
	}
	return cells
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
