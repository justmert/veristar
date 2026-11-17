package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/exports"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/db"
)

// saved threshold rules (contract 19.4). an operator defines a rule once,
// for example "verified visitors >= N per day on at least M days in each of
// the last K months", and lists the projects that meet it with the
// evidence: the qualifying days of every month and the integrity grade over
// the window. the months are complete utc calendar months: the K months
// before the month of the evaluation day. rules are kept under cp_system
// with an audit row per change and read under cp_operator.

// rule metrics
const (
	MetricVisitors          = "visitors"
	MetricRequests          = "requests"
	MetricOnchainOperations = "onchain_operations"
)

// limits of a rule
const (
	MaxRuleMinDays = 28
	MaxRuleMonths  = 12
	MaxRulePerDay  = 1_000_000_000_000
	// MaxRules bounds how many rules operators keep
	MaxRules = 200
)

// audit actions of the rule routes (contract 3.3)
const (
	ActionRuleCreate = "rule.create"
	ActionRuleUpdate = "rule.update"
	ActionRuleDelete = "rule.delete"
)

// RuleCohort narrows a rule to the projects confirmed in a cohort.
type RuleCohort struct {
	Program  *string `json:"program"`
	Round    *string `json:"round"`
	Category *string `json:"category"`
}

// Rule is one saved threshold rule.
type Rule struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	Metric    string          `json:"metric"`
	MinPerDay int64           `json:"min_per_day"`
	MinDays   int             `json:"min_days"`
	Months    int             `json:"months"`
	MinGrade  *string         `json:"min_grade"`
	Cohort    RuleCohort      `json:"cohort"`
	CreatedAt httpx.Timestamp `json:"created_at"`
	UpdatedAt httpx.Timestamp `json:"updated_at"`
}

// Rules serves the rule routes of the console.
type Rules struct {
	db  *db.DB
	now func() time.Time
}

// NewRules needs a database with the cp_operator and cp_system pools.
func NewRules(d *db.DB, now func() time.Time) (*Rules, error) {
	if d == nil || now == nil {
		return nil, errors.New("operator: rules need a database and a clock")
	}
	return &Rules{db: d, now: now}, nil
}

// Routes registers the paths under the guarded /api/operator router.
func (h *Rules) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/rules", httpx.HandlerFunc(h.list))
	r.Method(http.MethodPost, "/rules", httpx.HandlerFunc(h.create))
	r.Method(http.MethodGet, "/rules/{rule_id}", httpx.HandlerFunc(h.get))
	r.Method(http.MethodPut, "/rules/{rule_id}", httpx.HandlerFunc(h.update))
	r.Method(http.MethodDelete, "/rules/{rule_id}", httpx.HandlerFunc(h.remove))
	r.Method(http.MethodGet, "/rules/{rule_id}/matches", httpx.HandlerFunc(h.matches))
	r.Method(http.MethodGet, "/rules/{rule_id}/matches.csv", httpx.HandlerFunc(h.matchesCSV))
}

const ruleCols = `id, name, metric, min_per_day, min_days, months, min_grade, program, round, category, created_at, updated_at`

func scanRule(row pgx.Row) (Rule, error) {
	var r Rule
	var created, updated time.Time
	err := row.Scan(&r.ID, &r.Name, &r.Metric, &r.MinPerDay, &r.MinDays, &r.Months, &r.MinGrade,
		&r.Cohort.Program, &r.Cohort.Round, &r.Cohort.Category, &created, &updated)
	r.CreatedAt, r.UpdatedAt = httpx.Timestamp(created), httpx.Timestamp(updated)
	return r, err
}

// ListRules reads every rule, oldest first, under whatever role tx has.
func ListRules(ctx context.Context, tx pgx.Tx) ([]Rule, error) {
	rows, err := tx.Query(ctx, `select `+ruleCols+` from cp.threshold_rules order by created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Rule, error) { return scanRule(row) })
	if err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	if out == nil {
		out = []Rule{}
	}
	return out, nil
}

func getRule(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock bool) (Rule, error) {
	q := `select ` + ruleCols + ` from cp.threshold_rules where id = $1`
	if lock {
		q += ` for update`
	}
	r, err := scanRule(tx.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, httpx.NotFound()
	}
	if err != nil {
		return r, fmt.Errorf("rules: %w", err)
	}
	return r, nil
}

func ruleID(r *http.Request) (uuid.UUID, error) { return httpx.ParseID(chi.URLParam(r, "rule_id")) }

// RuleList is the answer of GET /api/operator/rules.
type RuleList struct {
	Items []Rule `json:"items"`
}

func (h *Rules) list(w http.ResponseWriter, r *http.Request) error {
	if len(r.URL.Query()) > 0 {
		return httpx.BadQuery("this route takes no query parameters")
	}
	ctx := r.Context()
	var out RuleList
	err := h.db.WithOperator(ctx, func(tx pgx.Tx) error {
		var err error
		out.Items, err = ListRules(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *Rules) get(w http.ResponseWriter, r *http.Request) error {
	id, err := ruleID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out Rule
	err = h.db.WithOperator(ctx, func(tx pgx.Tx) error {
		out, err = getRule(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// ruleBody is the request body of create and update: every field
// required except min_grade and cohort.
type ruleBody struct {
	Name      *string     `json:"name"`
	Metric    *string     `json:"metric"`
	MinPerDay *int64      `json:"min_per_day"`
	MinDays   *int        `json:"min_days"`
	Months    *int        `json:"months"`
	MinGrade  *string     `json:"min_grade"`
	Cohort    *RuleCohort `json:"cohort"`
}

// check validates b and returns the rule it describes.
func (b ruleBody) check() (Rule, error) {
	var f tenants.Fields
	var out Rule
	if b.Name == nil {
		f.Add("name", httpx.FieldRequired)
	} else {
		out.Name = tenants.Text(&f, "name", *b.Name, 1, 100)
	}
	switch {
	case b.Metric == nil:
		f.Add("metric", httpx.FieldRequired)
	case *b.Metric != MetricVisitors && *b.Metric != MetricRequests && *b.Metric != MetricOnchainOperations:
		f.Add("metric", httpx.FieldInvalidValue)
	default:
		out.Metric = *b.Metric
	}
	switch {
	case b.MinPerDay == nil:
		f.Add("min_per_day", httpx.FieldRequired)
	case *b.MinPerDay < 1 || *b.MinPerDay > MaxRulePerDay:
		f.Add("min_per_day", httpx.FieldInvalidValue)
	default:
		out.MinPerDay = *b.MinPerDay
	}
	switch {
	case b.MinDays == nil:
		f.Add("min_days", httpx.FieldRequired)
	case *b.MinDays < 1 || *b.MinDays > MaxRuleMinDays:
		f.Add("min_days", httpx.FieldInvalidValue)
	default:
		out.MinDays = *b.MinDays
	}
	switch {
	case b.Months == nil:
		f.Add("months", httpx.FieldRequired)
	case *b.Months < 1 || *b.Months > MaxRuleMonths:
		f.Add("months", httpx.FieldInvalidValue)
	default:
		out.Months = *b.Months
	}
	if b.MinGrade != nil {
		if g := *b.MinGrade; g != "A" && g != "B" && g != "C" {
			f.Add("min_grade", httpx.FieldInvalidValue)
		}
		out.MinGrade = b.MinGrade
	}
	if b.Cohort != nil {
		c := tenants.CheckCohort(&f, "cohort", &tenants.CohortInput{
			Program: b.Cohort.Program, Round: b.Cohort.Round, Category: b.Cohort.Category,
		}, time.Now().UTC(), true)
		out.Cohort = RuleCohort{Program: c.Program, Round: c.Round, Category: c.Category}
	}
	return out, f.Err()
}

func ruleDetail(r Rule) map[string]any {
	return map[string]any{
		"name": r.Name, "metric": r.Metric, "min_per_day": r.MinPerDay, "min_days": r.MinDays, "months": r.Months,
		"min_grade": r.MinGrade, "cohort": map[string]any{"program": r.Cohort.Program, "round": r.Cohort.Round, "category": r.Cohort.Category},
	}
}

func (h *Rules) create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var b ruleBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	in, err := b.check()
	if err != nil {
		return err
	}
	var out Rule
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		// one creation at a time, so the bound holds under concurrent requests
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('cp.threshold_rules', 0))`); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from cp.threshold_rules`).Scan(&n); err != nil {
			return err
		}
		if n >= MaxRules {
			return httpx.Conflict(CodeRuleLimit, fmt.Sprintf("at most %d rules are kept; delete one first", MaxRules))
		}
		out, err = scanRule(tx.QueryRow(ctx, `insert into cp.threshold_rules
			(name, metric, min_per_day, min_days, months, min_grade, program, round, category, created_by, updated_by)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10) returning `+ruleCols,
			in.Name, in.Metric, in.MinPerDay, in.MinDays, in.Months, in.MinGrade,
			in.Cohort.Program, in.Cohort.Round, in.Cohort.Category, u.ID))
		if err != nil {
			return fmt.Errorf("operator: create rule: %w", err)
		}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: ActionRuleCreate, Target: RuleTarget(out.ID), Detail: ruleDetail(out),
		})
	})
	if err != nil {
		return err
	}
	httpx.Created(w, "/api/operator/rules/"+out.ID.String(), out)
	return nil
}

// CodeRuleLimit is the 409 of a creation beyond MaxRules.
const CodeRuleLimit = "rule_limit"

// RuleTarget names a rule in the audit log.
func RuleTarget(id uuid.UUID) string { return "rule:" + id.String() }

func (h *Rules) update(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	id, err := ruleID(r)
	if err != nil {
		return err
	}
	var b ruleBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	in, err := b.check()
	if err != nil {
		return err
	}
	var out Rule
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		before, err := getRule(ctx, tx, id, true)
		if err != nil {
			return err
		}
		out, err = scanRule(tx.QueryRow(ctx, `update cp.threshold_rules set name = $2, metric = $3, min_per_day = $4,
			min_days = $5, months = $6, min_grade = $7, program = $8, round = $9, category = $10,
			updated_by = $11, updated_at = now() where id = $1 returning `+ruleCols,
			id, in.Name, in.Metric, in.MinPerDay, in.MinDays, in.Months, in.MinGrade,
			in.Cohort.Program, in.Cohort.Round, in.Cohort.Category, u.ID))
		if err != nil {
			return fmt.Errorf("operator: update rule: %w", err)
		}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: ActionRuleUpdate, Target: RuleTarget(id),
			Detail: map[string]any{"rule": audit.Change(ruleDetail(before), ruleDetail(out))},
		})
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *Rules) remove(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	id, err := ruleID(r)
	if err != nil {
		return err
	}
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		before, err := getRule(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from cp.threshold_rules where id = $1`, id); err != nil {
			return fmt.Errorf("operator: delete rule: %w", err)
		}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: ActionRuleDelete, Target: RuleTarget(id), Detail: ruleDetail(before),
		})
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// RuleDay is one qualifying day and the project's value that day.
type RuleDay struct {
	Day   string `json:"day"`
	Value int64  `json:"value"`
}

// RuleMonth is one calendar month of the window with its qualifying days.
type RuleMonth struct {
	Month          string    `json:"month"`
	QualifyingDays int       `json:"qualifying_days"`
	Days           []RuleDay `json:"days"`
}

// RuleMatch is one evaluated project with its evidence.
type RuleMatch struct {
	Tenant    TenantRef      `json:"tenant"`
	Cohort    Cohort         `json:"cohort"`
	Meets     bool           `json:"meets"`
	Integrity IntegrityGrade `json:"integrity"`
	Months    []RuleMonth    `json:"months"`
}

// RuleMatches is the answer of GET /api/operator/rules/{rule_id}/matches:
// the projects that meet the rule over the window, with the evidence.
type RuleMatches struct {
	Rule      Rule        `json:"rule"`
	AsOf      string      `json:"as_of"`
	From      string      `json:"from"`
	To        string      `json:"to"`
	Months    []string    `json:"months"`
	Evaluated int         `json:"evaluated"`
	Items     []RuleMatch `json:"items"`
}

// RuleWindow is the evaluation window of a rule on day asOf: the rule's
// number of complete calendar months before asOf's month.
func RuleWindow(months int, asOf time.Time) (from, to time.Time, names []string) {
	first := time.Date(asOf.Year(), asOf.Month(), 1, 0, 0, 0, 0, time.UTC)
	from = first.AddDate(0, -months, 0)
	to = first.AddDate(0, 0, -1)
	for m := from; m.Before(first); m = m.AddDate(0, 1, 0) {
		names = append(names, m.Format("2006-01"))
	}
	return from, to, names
}

// ruleDailyCTE gives d (tenant_id, day, v): the rule metric of every
// evaluated project per utc day of the window, from the operator views'
// aggregates. api requests follow the views' per-service source rule over
// the window.
var ruleDailyCTE = map[string]string{
	MetricVisitors: `d as (
  select w.tenant_id, w.day, sum(w.visitors)::bigint as v
  from agg.web_daily w join t on t.id = w.tenant_id
  where w.band = 'primary' and w.day between @from_day::date and @to_day::date
  group by w.tenant_id, w.day
)`,
	MetricRequests: `rr as (
  select m.tenant_id, m.service, m.source, m.count, (m.bucket_start at time zone 'UTC')::date as day,
         m.traffic_class
  from api.operator_daily(@from_at::timestamptz, @to_end::timestamptz, array(select id from t)) m
),
rs as (
  select tenant_id, service, case when bool_or(source = 'metric') then 'metric' else 'span' end as source
  from rr group by tenant_id, service
),
d as (
  select rr.tenant_id, rr.day, coalesce(sum(rr.count) filter (where rr.traffic_class = 'app'), 0)::bigint as v
  from rr join rs on rs.tenant_id = rr.tenant_id and rs.service = rr.service and rs.source = rr.source
  group by rr.tenant_id, rr.day
)`,
	MetricOnchainOperations: `d as (
  select c.tenant_id, x.day, sum(x.operations)::bigint as v
  from cp.chain_ids c join t on t.id = c.tenant_id
  join agg.chain_daily x on x.tenant_id = c.tenant_id and x.strkey = c.strkey
  where c.verified_at is not null and x.day between @from_day::date and @to_day::date
  group by c.tenant_id, x.day
)`,
}

// EvaluateRule evaluates rule on asOf for every active or suspended
// project in the rule's cohort, or for only that one when only is set,
// under cp_operator. every evaluated project is returned with Meets set.
func EvaluateRule(ctx context.Context, tx pgx.Tx, rule Rule, asOf time.Time, only *uuid.UUID) (RuleMatches, error) {
	from, to, names := RuleWindow(rule.Months, asOf)
	out := RuleMatches{
		Rule: rule, AsOf: asOf.Format(time.DateOnly), From: from.Format(time.DateOnly), To: to.Format(time.DateOnly),
		Months: names, Items: []RuleMatch{},
	}
	daily, ok := ruleDailyCTE[rule.Metric]
	if !ok {
		return out, fmt.Errorf("rules: unknown metric %q", rule.Metric)
	}
	var onlyID *uuid.UUID
	if only != nil {
		onlyID = only
	}
	args := pgx.NamedArgs{
		"from_day": from, "to_day": to, "from_at": from, "to_end": to.AddDate(0, 0, 1), "min": rule.MinPerDay,
		"program": rule.Cohort.Program, "round": rule.Cohort.Round, "category": rule.Cohort.Category, "only": onlyID,
	}
	rows, err := tx.Query(ctx, `
		with t as (
		  select t.id
		  from cp.tenants t left join cp.tenant_cohorts co on co.tenant_id = t.id
		  where not t.is_internal and t.status in ('active', 'suspended')
		    and (@only::uuid is null or t.id = @only::uuid)
		    and (@program::text is null or co.program = @program::text)
		    and (@round::text is null or co.round = @round::text)
		    and (@category::text is null or co.category = @category::text)
		),
		`+daily+`,
		q as (
		  select tenant_id, to_char(day, 'YYYY-MM') as m,
		         jsonb_agg(jsonb_build_object('day', to_char(day, 'YYYY-MM-DD'), 'value', v) order by day)
		           filter (where v >= @min) as days
		  from d group by tenant_id, to_char(day, 'YYYY-MM')
		),
		g as (
		  select i.tenant_id,
		         count(*) filter (where i.grade = 'A')::bigint as days_a,
		         count(*) filter (where i.grade = 'B')::bigint as days_b,
		         count(*) filter (where i.grade = 'C')::bigint as days_c
		  from agg.web_integrity_daily i join t on t.id = i.tenant_id
		  where i.day between @from_day::date and @to_day::date
		  group by i.tenant_id
		)
		select x.id, x.slug, x.name, x.status, x.created_at, co.program, co.round, co.category, co.award_date,
		       coalesce(g.days_a, 0), coalesce(g.days_b, 0), coalesce(g.days_c, 0),
		       coalesce((select jsonb_object_agg(q.m, q.days) from q where q.tenant_id = x.id and q.days is not null), '{}')
		from t join cp.tenants x on x.id = t.id
		left join cp.tenant_cohorts co on co.tenant_id = x.id and co.confirmed_at is not null
		left join g on g.tenant_id = x.id
		order by lower(x.name), x.id`, args)
	if err != nil {
		return out, fmt.Errorf("rules: evaluate: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (RuleMatch, error) {
		var m RuleMatch
		var at time.Time
		var award *time.Time
		var byMonth map[string][]RuleDay
		err := row.Scan(&m.Tenant.ID, &m.Tenant.Slug, &m.Tenant.Name, &m.Tenant.Status, &at,
			&m.Cohort.Program, &m.Cohort.Round, &m.Cohort.Category, &award,
			&m.Integrity.DaysA, &m.Integrity.DaysB, &m.Integrity.DaysC, &byMonth)
		if err != nil {
			return m, err
		}
		m.Tenant.CreatedAt = httpx.Timestamp(at)
		m.Cohort.AwardDate = dateString(award)
		m.Integrity.derive()
		m.Meets = true
		for _, name := range names {
			days := byMonth[name]
			if days == nil {
				days = []RuleDay{}
			}
			m.Months = append(m.Months, RuleMonth{Month: name, QualifyingDays: len(days), Days: days})
			if len(days) < rule.MinDays {
				m.Meets = false
			}
		}
		if rule.MinGrade != nil && (m.Integrity.Grade == nil || *m.Integrity.Grade > *rule.MinGrade) {
			m.Meets = false
		}
		return m, nil
	})
	if err != nil {
		return out, fmt.Errorf("rules: evaluate: %w", err)
	}
	out.Evaluated = len(items)
	out.Items = items
	return out, nil
}

// parseAsOf reads as_of, a utc day not after today; today without it.
func parseAsOf(q url.Values, now time.Time) (time.Time, error) {
	for k := range q {
		if k != "as_of" {
			return time.Time{}, httpx.BadQuery("unknown parameter " + k)
		}
	}
	today := utcDay(now)
	d, ok, err := parseDay(q, "as_of")
	if err != nil {
		return time.Time{}, err
	}
	if !ok {
		return today, nil
	}
	if d.After(today) {
		return time.Time{}, httpx.BadQuery("as_of must not be after today (utc)")
	}
	return d, nil
}

// meeting keeps the projects that meet the rule.
func (m RuleMatches) meeting() RuleMatches {
	items := []RuleMatch{}
	for _, it := range m.Items {
		if it.Meets {
			items = append(items, it)
		}
	}
	m.Items = items
	return m
}

func (h *Rules) evaluate(r *http.Request) (RuleMatches, error) {
	id, err := ruleID(r)
	if err != nil {
		return RuleMatches{}, err
	}
	asOf, err := parseAsOf(r.URL.Query(), h.now())
	if err != nil {
		return RuleMatches{}, err
	}
	ctx := r.Context()
	var out RuleMatches
	err = h.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		rule, err := getRule(ctx, tx, id, false)
		if err != nil {
			return err
		}
		out, err = EvaluateRule(ctx, tx, rule, asOf, nil)
		return err
	})
	return out.meeting(), err
}

func (h *Rules) matches(w http.ResponseWriter, r *http.Request) error {
	out, err := h.evaluate(r)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// matchesCSV writes one row per meeting project and month, with the
// qualifying days of the month as a list. it is audited like every export.
func (h *Rules) matchesCSV(w http.ResponseWriter, r *http.Request) error {
	out, err := h.evaluate(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	rows := 0
	for _, it := range out.Items {
		rows += len(it.Months)
	}
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: exports.ActionExport, Target: "operator:rule_matches",
			Detail: map[string]any{"view": "rule_matches", "rule_id": out.Rule.ID.String(), "query": r.URL.Query().Encode(), "rows": rows},
		})
	})
	if err != nil {
		return err
	}
	cw, err := exports.NewWriter(w, exports.FileName("veristar-rule-matches", out.From, out.To), []string{
		"rule_id", "rule_name", "tenant_id", "slug", "name", "status", "cohort_program", "cohort_round",
		"cohort_category", "cohort_award_date", "integrity_grade", "integrity_days_a", "integrity_days_b",
		"integrity_days_c", "month", "qualifying_days", "days",
	})
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	for _, it := range out.Items {
		for _, m := range it.Months {
			days, err := json.Marshal(m.Days)
			if err != nil {
				return err
			}
			if err := cw.Row(cat([]string{
				out.Rule.ID.String(), exports.Text(out.Rule.Name), it.Tenant.ID.String(), exports.Text(it.Tenant.Slug),
				exports.Text(it.Tenant.Name), it.Tenant.Status, exports.StrPtr(it.Cohort.Program), exports.StrPtr(it.Cohort.Round),
				exports.StrPtr(it.Cohort.Category), exports.StrPtr(it.Cohort.AwardDate),
			}, integrityCells(it.Integrity), []string{m.Month, exports.Int(int64(m.QualifyingDays)), string(days)})...); err != nil {
				panic(http.ErrAbortHandler)
			}
		}
	}
	if err := cw.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}
