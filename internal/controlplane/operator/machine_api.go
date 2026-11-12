package operator

import (
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
)

// the api routes of the machine api (contract 9.8 to 9.10). they apply the
// source rule of the design the way the operator views of this stage do
// (apiChosenCTE): per tenant and service over the whole range, metric rows
// when the service has any, otherwise span rows, never both.

// machineChosenCTE is apiChosenCTE over source (operatorDaily or
// operatorHourly, which count the hours api-rollup has not reached yet,
// 00022) with an optional service filter. filtering a service
// before the choice cannot change that service's choice, which is made
// over its own rows only. it expects the cte t of tenantScopeCTE.
func machineChosenCTE(source string) string {
	return `
r as (
  select m.tenant_id, m.bucket_start, m.service, m.method, m.route, m.status_code, m.source, m.counts, m.count, m.lossy
  from ` + source + ` m
  join t on t.id = m.tenant_id
  where (@service::text is null or m.service = @service::text)
),
svc as (
  select tenant_id, service,
         case when bool_or(source = 'metric') then 'metric' else 'span' end as source
  from r
  group by tenant_id, service
),
c as (
  select r.*
  from r
  join svc on svc.tenant_id = r.tenant_id and svc.service = r.service and svc.source = r.source
)`
}

// the row sources of the machine api routes: hour and day grain rows of
// the range, with pending hours computed from minutes (00022)
const (
	operatorDaily  = "api.operator_daily(@from_at::timestamptz, @to_end::timestamptz)"
	operatorHourly = "api.operator_hourly(@from_at::timestamptz, @to_end::timestamptz)"
)

// MachineAPIBucket are the numbers of one tenant in one day or hour, over
// every service of the request.
type MachineAPIBucket struct {
	TenantID     uuid.UUID `json:"tenant_id"`
	Services     int64     `json:"services"`
	SpanServices int64     `json:"span_services"`
	Source       string    `json:"source"`
	APINumbers
	Lossy bool `json:"lossy"`
}

// MachineAPIDay is one item of GET /api/daily.
type MachineAPIDay struct {
	Day string `json:"day"`
	MachineAPIBucket
}

// MachineAPIHour is one item of GET /api/hourly.
type MachineAPIHour struct {
	Hour httpx.Timestamp `json:"hour"`
	MachineAPIBucket
}

// MachineAPIRoute is one item of GET /api/routes/daily.
type MachineAPIRoute struct {
	TenantID uuid.UUID `json:"tenant_id"`
	Day      string    `json:"day"`
	Service  string    `json:"service"`
	Method   string    `json:"method"`
	Route    string    `json:"route"`
	Source   string    `json:"source"`
	APINumbers
	Lossy bool `json:"lossy"`
}

var apiBucketKeys = keyset{cols: []string{"z.tenant_id", "z.bucket_start"}, casts: []string{"uuid", "timestamptz"}}

var apiRouteKeys = keyset{
	cols:  []string{"z.tenant_id", "z.bucket_start", "z.service", "z.method", "z.route"},
	casts: []string{"uuid", "timestamptz", "text", "text", "text"},
}

// bucketSelect groups the chosen rows per tenant and bucket.
const bucketSelect = `
select tenant_id, bucket_start, count(distinct service)::bigint,
       (count(distinct service) filter (where source = 'span'))::bigint,
       bool_or(lossy),` + numbersCols + `
from c group by tenant_id, bucket_start`

func scanBucket(row pgx.CollectableRow, at *time.Time) (MachineAPIBucket, error) {
	var b MachineAPIBucket
	if err := scanNumbers(&b.APINumbers, row.Scan, &b.TenantID, at, &b.Services, &b.SpanServices, &b.Lossy); err != nil {
		return b, err
	}
	src := source(b.Services, b.SpanServices)
	if src == nil {
		// a group exists only for rows, so it has at least one service
		return b, fmt.Errorf("api bucket of tenant %s without a service", b.TenantID)
	}
	b.Source = *src
	return b, nil
}

func (m *Machine) apiDaily(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := checkParams(q, "tenant", "service", "from", "to", "limit", "cursor"); err != nil {
		return err
	}
	key := keyFrom(r.Context())
	p, err := parseMachinePage(q, "machine-api-daily", key)
	if err != nil {
		return err
	}
	if err := apiBucketKeys.validAfter(p); err != nil {
		return err
	}
	rg, err := p.days(q, m.now())
	if err != nil {
		return err
	}
	service, err := parseService(q)
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := MachineRangeList[MachineAPIDay]{From: rg.From.Format(time.DateOnly), To: rg.To.Format(time.DateOnly), Items: []MachineAPIDay{}}
	err = m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		sc, err := resolveScope(ctx, tx, key, q)
		if err != nil {
			return err
		}
		args := pgx.NamedArgs{"tenant": sc.arg(), "from_at": rg.From, "to_end": rg.end(), "service": service}
		inner := `with ` + tenantScopeCTE + `,` + machineChosenCTE(operatorDaily) + bucketSelect
		rows, err := tx.Query(ctx, apiBucketKeys.wrap(inner, p, args), args)
		if err != nil {
			return err
		}
		var last time.Time
		items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (MachineAPIDay, error) {
			var d MachineAPIDay
			var err error
			d.MachineAPIBucket, err = scanBucket(row, &last)
			d.Day = utcDay(last).Format(time.DateOnly)
			return d, err
		})
		if err != nil {
			return err
		}
		if len(items) > p.Limit {
			items = items[:p.Limit]
			l := items[len(items)-1]
			day, _ := time.Parse(time.DateOnly, l.Day)
			out.NextCursor = p.next([]string{l.TenantID.String(), day.Format(time.RFC3339)}, out.From, out.To)
		}
		out.Items = items
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: machine api daily: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (m *Machine) apiHourly(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := checkParams(q, "tenant", "service", "from", "to", "limit", "cursor"); err != nil {
		return err
	}
	key := keyFrom(r.Context())
	p, err := parseMachinePage(q, "machine-api-hourly", key)
	if err != nil {
		return err
	}
	if err := apiBucketKeys.validAfter(p); err != nil {
		return err
	}
	rg, err := p.hourSpan(q, m.now(), maxAPIHours)
	if err != nil {
		return err
	}
	service, err := parseService(q)
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := MachineRangeList[MachineAPIHour]{From: httpx.Time(rg.From), To: httpx.Time(rg.To), Items: []MachineAPIHour{}}
	err = m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		sc, err := resolveScope(ctx, tx, key, q)
		if err != nil {
			return err
		}
		args := pgx.NamedArgs{"tenant": sc.arg(), "from_at": rg.From, "to_end": rg.end(), "service": service}
		inner := `with ` + tenantScopeCTE + `,` + machineChosenCTE(operatorHourly) + bucketSelect
		rows, err := tx.Query(ctx, apiBucketKeys.wrap(inner, p, args), args)
		if err != nil {
			return err
		}
		items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (MachineAPIHour, error) {
			var h MachineAPIHour
			var at time.Time
			var err error
			h.MachineAPIBucket, err = scanBucket(row, &at)
			h.Hour = httpx.Timestamp(at)
			return h, err
		})
		if err != nil {
			return err
		}
		if len(items) > p.Limit {
			items = items[:p.Limit]
			l := items[len(items)-1]
			out.NextCursor = p.next([]string{l.TenantID.String(), time.Time(l.Hour).UTC().Format(time.RFC3339)}, out.From, out.To)
		}
		out.Items = items
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: machine api hourly: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (m *Machine) apiRoutesDaily(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := checkParams(q, "tenant", "service", "from", "to", "limit", "cursor"); err != nil {
		return err
	}
	key := keyFrom(r.Context())
	p, err := parseMachinePage(q, "machine-api-routes-daily", key)
	if err != nil {
		return err
	}
	if err := apiRouteKeys.validAfter(p); err != nil {
		return err
	}
	rg, err := p.days(q, m.now())
	if err != nil {
		return err
	}
	service, err := parseService(q)
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := MachineRangeList[MachineAPIRoute]{From: rg.From.Format(time.DateOnly), To: rg.To.Format(time.DateOnly), Items: []MachineAPIRoute{}}
	err = m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		sc, err := resolveScope(ctx, tx, key, q)
		if err != nil {
			return err
		}
		args := pgx.NamedArgs{"tenant": sc.arg(), "from_at": rg.From, "to_end": rg.end(), "service": service}
		// a service has one chosen source, so min(source) is that source
		inner := `with ` + tenantScopeCTE + `,` + machineChosenCTE(operatorDaily) + `
			select tenant_id, bucket_start, service, method, route, min(source), bool_or(lossy),` + numbersCols + `
			from c group by tenant_id, bucket_start, service, method, route`
		rows, err := tx.Query(ctx, apiRouteKeys.wrap(inner, p, args), args)
		if err != nil {
			return err
		}
		items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (MachineAPIRoute, error) {
			var it MachineAPIRoute
			var at time.Time
			err := scanNumbers(&it.APINumbers, row.Scan, &it.TenantID, &at, &it.Service, &it.Method, &it.Route, &it.Source, &it.Lossy)
			it.Day = utcDay(at).Format(time.DateOnly)
			return it, err
		})
		if err != nil {
			return err
		}
		if len(items) > p.Limit {
			items = items[:p.Limit]
			l := items[len(items)-1]
			day, _ := time.Parse(time.DateOnly, l.Day)
			out.NextCursor = p.next([]string{l.TenantID.String(), day.Format(time.RFC3339), l.Service, l.Method, l.Route}, out.From, out.To)
		}
		out.Items = items
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: machine api routes daily: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
