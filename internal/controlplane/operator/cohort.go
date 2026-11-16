package operator

import (
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/justmert/veristar/internal/controlplane/httpx"
)

// cohorts, growth and percentiles of the global views (contract 19.1 to
// 19.3). a project's cohort is the program, round, category and award
// date an operator confirmed in cp.tenant_cohorts (00035). the cohort
// filters narrow a list to the projects confirmed in it, and the same
// filters define the population every percentile is computed in: the
// listed (not internal) projects that are active or suspended and match the
// cohort filters, whatever the list's other filters are, so a search by
// name never changes a percentile.

// cohortFieldMax is the longest program, round or category.
const cohortFieldMax = 80

// cohortFilter is the cohort part of a list's filters.
type cohortFilter struct {
	Program   *string    `json:"program"`
	Round     *string    `json:"round"`
	Category  *string    `json:"category"`
	AwardFrom *time.Time `json:"award_from"`
	AwardTo   *time.Time `json:"award_to"`
}

// any reports whether a cohort filter is set.
func (c cohortFilter) any() bool {
	return c.Program != nil || c.Round != nil || c.Category != nil || c.AwardFrom != nil || c.AwardTo != nil
}

func parseCohortText(q url.Values, key string) (*string, error) {
	v, ok, err := oneValue(q, key)
	if err != nil || !ok {
		return nil, err
	}
	if n := utf8.RuneCountInString(v); n < 1 || n > cohortFieldMax {
		return nil, httpx.BadQuery(key + " must be 1 to 80 characters")
	}
	return &v, nil
}

// parseCohortFilter reads program, round, category, award_from and
// award_to.
func parseCohortFilter(q url.Values) (cohortFilter, error) {
	var c cohortFilter
	var err error
	if c.Program, err = parseCohortText(q, "program"); err != nil {
		return c, err
	}
	if c.Round, err = parseCohortText(q, "round"); err != nil {
		return c, err
	}
	if c.Category, err = parseCohortText(q, "category"); err != nil {
		return c, err
	}
	if d, ok, err := parseDay(q, "award_from"); err != nil {
		return c, err
	} else if ok {
		c.AwardFrom = &d
	}
	if d, ok, err := parseDay(q, "award_to"); err != nil {
		return c, err
	} else if ok {
		c.AwardTo = &d
	}
	if c.AwardFrom != nil && c.AwardTo != nil && c.AwardTo.Before(*c.AwardFrom) {
		return c, httpx.BadQuery("award_to must not be before award_from")
	}
	return c, nil
}

// cohortSQL is the condition of a confirmed cohort row co matching the
// cohort filters. a project without a confirmed value never matches a
// filter on it.
const cohortSQL = `(@program::text is null or co.program = @program::text)
    and (@round::text is null or co.round = @round::text)
    and (@category::text is null or co.category = @category::text)
    and (@award_from::date is null or co.award_date >= @award_from::date)
    and (@award_to::date is null or co.award_date <= @award_to::date)`

func (c cohortFilter) args(a map[string]any) {
	a["program"] = c.Program
	a["round"] = c.Round
	a["category"] = c.Category
	a["award_from"] = c.AwardFrom
	a["award_to"] = c.AwardTo
}

// previous is the range of equal length that ends the day before rg.
func (d dayRange) previous() dayRange {
	n := int(d.To.Sub(d.From).Hours()/24) + 1
	return dayRange{From: d.From.AddDate(0, 0, -n), To: d.From.AddDate(0, 0, -1)}
}

// kpiCTE computes the five key metrics of every project of the
// percentile population pop, over the range and over the previous range of
// equal length, then their change and their percentile in pop. it reads
// the operator views' own aggregates: agg.web_daily (primary band),
// api.operator_daily with the views' per-service source rule applied per
// period, and agg.chain_daily of the declared ids. the percentile of a
// value is the share of pop projects with a value at or below it
// (cume_dist), among the projects that have a value: a count above zero,
// or an error rate over at least one request.
const kpiCTE = `
pop as (
  select t.id
  from cp.tenants t
  left join cp.tenant_cohorts co on co.tenant_id = t.id
  where not t.is_internal and t.status in ('active', 'suspended') and ` + cohortSQL + `
),
kw as (
  select d.tenant_id,
         coalesce(sum(d.visitors) filter (where d.day >= @from_day::date), 0)::bigint as visitors,
         coalesce(sum(d.pageviews) filter (where d.day >= @from_day::date), 0)::bigint as pageviews,
         coalesce(sum(d.visitors) filter (where d.day < @from_day::date), 0)::bigint as prev_visitors,
         coalesce(sum(d.pageviews) filter (where d.day < @from_day::date), 0)::bigint as prev_pageviews
  from agg.web_daily d
  join pop on pop.id = d.tenant_id
  where d.band = 'primary' and d.day between @prev_from_day::date and @to_day::date
  group by d.tenant_id
),
kr as (
  select m.tenant_id, m.service, m.source, m.status_code, m.count, m.bucket_start >= @from_at::timestamptz as cur
  from api.operator_daily(@prev_from_at::timestamptz, @to_end::timestamptz, array(select id from pop)) m
),
ks as (
  select tenant_id, service, cur, case when bool_or(source = 'metric') then 'metric' else 'span' end as source
  from kr
  group by tenant_id, service, cur
),
ka as (
  select kr.tenant_id,
         coalesce(sum(kr.count) filter (where kr.cur), 0)::bigint as requests,
         coalesce(sum(kr.count) filter (where kr.cur and kr.status_code between 500 and 599), 0)::bigint as requests_5xx,
         coalesce(sum(kr.count) filter (where not kr.cur), 0)::bigint as prev_requests,
         coalesce(sum(kr.count) filter (where not kr.cur and kr.status_code between 500 and 599), 0)::bigint
           as prev_requests_5xx
  from kr
  join ks on ks.tenant_id = kr.tenant_id and ks.service = kr.service and ks.cur = kr.cur and ks.source = kr.source
  group by kr.tenant_id
),
ko as (
  select c.tenant_id,
         coalesce(sum(d.operations) filter (where d.day >= @from_day::date), 0)::bigint as ops,
         coalesce(sum(d.operations) filter (where d.day < @from_day::date), 0)::bigint as prev_ops
  from cp.chain_ids c
  join pop on pop.id = c.tenant_id
  join agg.chain_daily d on d.tenant_id = c.tenant_id and d.strkey = c.strkey
  where d.day between @prev_from_day::date and @to_day::date
  group by c.tenant_id
),
kv as (
  select pop.id as tenant_id,
         coalesce(kw.visitors, 0) as visitors, coalesce(kw.prev_visitors, 0) as prev_visitors,
         coalesce(kw.pageviews, 0) as pageviews, coalesce(kw.prev_pageviews, 0) as prev_pageviews,
         coalesce(ka.requests, 0) as requests, coalesce(ka.prev_requests, 0) as prev_requests,
         case when coalesce(ka.requests, 0) > 0 then ka.requests_5xx::double precision / ka.requests end as error_rate,
         case when coalesce(ka.prev_requests, 0) > 0
              then ka.prev_requests_5xx::double precision / ka.prev_requests end as prev_error_rate,
         coalesce(ko.ops, 0) as ops, coalesce(ko.prev_ops, 0) as prev_ops
  from pop
  left join kw on kw.tenant_id = pop.id
  left join ka on ka.tenant_id = pop.id
  left join ko on ko.tenant_id = pop.id
),
k as (
  select kv.*,
         case when visitors > 0 then cume_dist() over (partition by visitors > 0 order by visitors) end as pct_visitors,
         case when pageviews > 0 then cume_dist() over (partition by pageviews > 0 order by pageviews) end as pct_pageviews,
         case when requests > 0 then cume_dist() over (partition by requests > 0 order by requests) end as pct_requests,
         case when error_rate is not null
              then cume_dist() over (partition by error_rate is not null order by error_rate) end as pct_error_rate,
         case when ops > 0 then cume_dist() over (partition by ops > 0 order by ops) end as pct_ops,
         case when prev_visitors > 0
              then (visitors - prev_visitors)::double precision / prev_visitors end as growth_visitors,
         case when prev_pageviews > 0
              then (pageviews - prev_pageviews)::double precision / prev_pageviews end as growth_pageviews,
         case when prev_requests > 0
              then (requests - prev_requests)::double precision / prev_requests end as growth_requests,
         case when error_rate is not null and prev_error_rate is not null
              then error_rate - prev_error_rate end as change_error_rate,
         case when prev_ops > 0 then (ops - prev_ops)::double precision / prev_ops end as growth_ops
  from kv
)`

// kpiCols are the columns a list's x cte takes from k (left joined on
// the tenant) and from the cohort row co, in the order scanKPIs reads them.
// a project outside pop has null metrics and percentiles.
const kpiCols = `
         k.visitors as k_visitors, k.prev_visitors as k_prev_visitors, k.growth_visitors, k.pct_visitors,
         k.pageviews as k_pageviews, k.prev_pageviews as k_prev_pageviews, k.growth_pageviews, k.pct_pageviews,
         k.requests as k_requests, k.prev_requests as k_prev_requests, k.growth_requests, k.pct_requests,
         k.error_rate as k_error_rate, k.prev_error_rate as k_prev_error_rate, k.change_error_rate, k.pct_error_rate,
         k.ops as k_ops, k.prev_ops as k_prev_ops, k.growth_ops, k.pct_ops,
         (select count(*) from pop) as cohort_size,
         co.program as cohort_program, co.round as cohort_round, co.category as cohort_category,
         co.award_date as cohort_award_date`

// kpiSelect names the kpiCols of y in scan order.
const kpiSelect = `
         k_visitors, k_prev_visitors, growth_visitors, pct_visitors,
         k_pageviews, k_prev_pageviews, growth_pageviews, pct_pageviews,
         k_requests, k_prev_requests, growth_requests, pct_requests,
         k_error_rate, k_prev_error_rate, change_error_rate, pct_error_rate,
         k_ops, k_prev_ops, growth_ops, pct_ops,
         cohort_size, cohort_program, cohort_round, cohort_category, cohort_award_date`

// kpiJoins attaches k and the cohort row to the tenants t of a list.
const kpiJoins = `
  left join k on k.tenant_id = t.id
  left join cp.tenant_cohorts co on co.tenant_id = t.id`

// growthSorts are the sorts both lists add for the kpi columns.
var growthSorts = map[string]string{
	"visitors_growth": "growth_visitors", "pageviews_growth": "growth_pageviews",
	"requests_growth": "growth_requests", "error_rate_change": "change_error_rate",
	"onchain_operations_growth": "growth_ops",
	"visitors_percentile":       "pct_visitors", "pageviews_percentile": "pct_pageviews",
	"requests_percentile": "pct_requests", "error_rate_percentile": "pct_error_rate",
	"onchain_operations_percentile": "pct_ops",
}

func withGrowthSorts(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+len(growthSorts))
	for k, v := range m {
		out[k] = v
	}
	for k, v := range growthSorts {
		out[k] = v
	}
	return out
}

// KPI is one key metric of a project: its value over the range, over the
// previous range of equal length, the change between them and its
// percentile in the cohort. Change is relative ((value - previous) /
// previous) for counts and absolute (value - previous) for the error rate;
// it is null when the previous value is zero or missing. Percentile is in
// [0, 1] and null when the project has no value or is outside the
// population.
type KPI struct {
	Value      *float64 `json:"value"`
	Previous   *float64 `json:"previous"`
	Change     *float64 `json:"change"`
	Percentile *float64 `json:"percentile"`
}

// KPIs are the five key metrics of a project row (contract 19.2).
type KPIs struct {
	Visitors          KPI `json:"visitors"`
	Pageviews         KPI `json:"pageviews"`
	Requests          KPI `json:"requests"`
	ErrorRate         KPI `json:"error_rate"`
	OnchainOperations KPI `json:"onchain_operations"`
}

// Cohort is a project's confirmed cohort; every field is null when no
// operator confirmed one.
type Cohort struct {
	Program   *string `json:"program"`
	Round     *string `json:"round"`
	Category  *string `json:"category"`
	AwardDate *string `json:"award_date"`
}

// Comparison is what a list row adds for cohort comparison: the project's
// cohort, the size of the population its percentiles come from, and the
// key metrics.
type Comparison struct {
	Cohort     Cohort `json:"cohort"`
	CohortSize int64  `json:"cohort_size"`
	KPIs       KPIs   `json:"kpis"`
}

// kpiScan reads the kpiSelect columns.
type kpiScan struct {
	counts   [4][2]*int64
	rates    [2]*float64
	change   [5]*float64
	pct      [5]*float64
	size     int64
	program  *string
	round    *string
	category *string
	award    *time.Time
}

func (s *kpiScan) dest() []any {
	return []any{
		&s.counts[0][0], &s.counts[0][1], &s.change[0], &s.pct[0],
		&s.counts[1][0], &s.counts[1][1], &s.change[1], &s.pct[1],
		&s.counts[2][0], &s.counts[2][1], &s.change[2], &s.pct[2],
		&s.rates[0], &s.rates[1], &s.change[3], &s.pct[3],
		&s.counts[3][0], &s.counts[3][1], &s.change[4], &s.pct[4],
		&s.size, &s.program, &s.round, &s.category, &s.award,
	}
}

func floatOf(n *int64) *float64 {
	if n == nil {
		return nil
	}
	v := float64(*n)
	return &v
}

func (s *kpiScan) comparison() Comparison {
	count := func(i, c int) KPI {
		return KPI{Value: floatOf(s.counts[c][0]), Previous: floatOf(s.counts[c][1]), Change: s.change[i], Percentile: s.pct[i]}
	}
	out := Comparison{
		Cohort:     Cohort{Program: s.program, Round: s.round, Category: s.category, AwardDate: dateString(s.award)},
		CohortSize: s.size,
		KPIs: KPIs{
			Visitors: count(0, 0), Pageviews: count(1, 1), Requests: count(2, 2),
			ErrorRate:         KPI{Value: s.rates[0], Previous: s.rates[1], Change: s.change[3], Percentile: s.pct[3]},
			OnchainOperations: count(4, 3),
		},
	}
	return out
}
