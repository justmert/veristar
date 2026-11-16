// Package coverageview reads agg.web_coverage_daily into the estimate of
// unmeasured visits that projects, operators, the machine api, the mcp tools
// and the csv exports share. the transaction's role decides what it sees: a
// project reads its own rows under row-level security, an operator reads
// every tenant's.
package coverageview

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/coverage"
)

// Estimate is the coverage of a site or project over a range or a day:
// requests for the tracker file attributed to it, the part that carried
// global privacy control, and the share of the second in the first. the
// share is null below coverage.MinRequests requests.
type Estimate struct {
	ScriptRequests int64 `json:"script_requests"`
	GPCRequests    int64 `json:"gpc_requests"`
	// UnmeasuredShare estimates the share of visits that are not measured
	// because the browser sends global privacy control
	UnmeasuredShare *float64 `json:"unmeasured_share"`
}

// Of builds an estimate from two counts.
func Of(requests, gpc int64) Estimate {
	return Estimate{ScriptRequests: requests, GPCRequests: gpc, UnmeasuredShare: coverage.Share(requests, gpc)}
}

// Add returns the sum of two estimates, with the share of the sum.
func (e Estimate) Add(o Estimate) Estimate {
	return Of(e.ScriptRequests+o.ScriptRequests, e.GPCRequests+o.GPCRequests)
}

// SiteDay is a site and utc day of a tenant.
type SiteDay struct {
	Tenant uuid.UUID
	Site   uuid.UUID
	Day    string
}

// Counts are the coverage rows of the named tenants over [from, to], keyed
// by tenant, site and day.
type Counts map[SiteDay]Estimate

// Load reads the coverage rows of the named tenants over [from, to].
func Load(ctx context.Context, tx pgx.Tx, tenants []uuid.UUID, from, to time.Time) (Counts, error) {
	out := Counts{}
	if len(tenants) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `
		select tenant_id, site_id, day, script_requests, gpc_requests
		from agg.web_coverage_daily
		where tenant_id = any(@ids::uuid[]) and day between @from_day::date and @to_day::date`,
		pgx.NamedArgs{"ids": tenants, "from_day": from, "to_day": to})
	if err != nil {
		return nil, fmt.Errorf("coverage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k SiteDay
		var day time.Time
		var req, gpc int64
		if err := rows.Scan(&k.Tenant, &k.Site, &day, &req, &gpc); err != nil {
			return nil, fmt.Errorf("coverage: %w", err)
		}
		k.Day = day.Format(time.DateOnly)
		out[k] = Of(req, gpc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("coverage: %w", err)
	}
	return out, nil
}

// Tenant sums one tenant's rows.
func (c Counts) Tenant(id uuid.UUID) Estimate {
	var e Estimate
	for k, v := range c {
		if k.Tenant == id {
			e = e.Add(v)
		}
	}
	return Of(e.ScriptRequests, e.GPCRequests)
}

// Site sums one site's rows.
func (c Counts) Site(tenant, site uuid.UUID) Estimate {
	var e Estimate
	for k, v := range c {
		if k.Tenant == tenant && k.Site == site {
			e = e.Add(v)
		}
	}
	return Of(e.ScriptRequests, e.GPCRequests)
}

// TenantDay sums one tenant's sites on one day.
func (c Counts) TenantDay(tenant uuid.UUID, day string) Estimate {
	var e Estimate
	for k, v := range c {
		if k.Tenant == tenant && k.Day == day {
			e = e.Add(v)
		}
	}
	return Of(e.ScriptRequests, e.GPCRequests)
}

// Sites lists the sites of a tenant that have rows.
func (c Counts) Sites(tenant uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for k := range c {
		if k.Tenant == tenant && !seen[k.Site] {
			seen[k.Site] = true
			out = append(out, k.Site)
		}
	}
	return out
}

// TenantSQL is a common table expression body that sums the coverage of the
// tenants in the cte t over [@from_day, @to_day], for list queries that sort
// by the share. it yields tenant_id, script_requests and gpc_requests.
const TenantSQL = `
  select c.tenant_id,
         coalesce(sum(c.script_requests), 0)::bigint as script_requests,
         coalesce(sum(c.gpc_requests), 0)::bigint as gpc_requests
  from agg.web_coverage_daily c
  join t on t.id = c.tenant_id
  where c.day between @from_day::date and @to_day::date
  group by c.tenant_id`

// ShareSQL is the unmeasured share of a row with script_requests and
// gpc_requests columns, null below coverage.MinRequests, as coverage.Share.
// a constant, so list queries stay constant strings; coverageview_test
// checks that its threshold is coverage.MinRequests.
const ShareSQL = `case when script_requests >= 100 and gpc_requests <= script_requests
  then gpc_requests::double precision / script_requests end`
