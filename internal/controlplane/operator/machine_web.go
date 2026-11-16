package operator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/coverageview"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/integrityview"
	"github.com/justmert/veristar/internal/umami"
)

// the tenant list and the web routes of the machine api (contract 9.4 to
// 9.7). web daily numbers are sums of agg.web_daily and agg.web_daily_dim,
// the rows the operator views read, so both always agree.

// MachineTenant is one item of GET /tenants.
type MachineTenant struct {
	ID        uuid.UUID       `json:"id"`
	Slug      string          `json:"slug"`
	Name      string          `json:"name"`
	Status    string          `json:"status"`
	CreatedAt httpx.Timestamp `json:"created_at"`
	SiteCounts
	Verified    bool `json:"verified"`
	StellarToml bool `json:"stellar_toml"`
}

// MachineList is the envelope of the machine lists that need no range.
type MachineList[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// MachineRangeList is the envelope of the machine lists over a range.
type MachineRangeList[T any] struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

var tenantKeys = keyset{cols: []string{"z.id"}, casts: []string{"uuid"}}

func (m *Machine) tenants(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := checkParams(q, "limit", "cursor"); err != nil {
		return err
	}
	key := keyFrom(r.Context())
	p, err := parseMachinePage(q, "machine-tenants", key)
	if err != nil {
		return err
	}
	if err := tenantKeys.validAfter(p); err != nil {
		return err
	}
	ctx := r.Context()
	out := MachineList[MachineTenant]{Items: []MachineTenant{}}
	err = m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		// a scoped key lists its own tenant, never another
		args := pgx.NamedArgs{"tenant": key.Tenant}
		inner := `with ` + tenantScopeCTE + `,
			s as (
			  select v.tenant_id,
			         count(*) filter (where v.status <> 'removed')::bigint as sites,
			         count(*) filter (where v.status <> 'removed' and v.verified_at is not null
			                            and v.last_verify_error is null)::bigint as sites_verified,
			         count(*) filter (where v.status <> 'removed' and v.stellar_toml ->> 'status' = 'found')::bigint
			           as sites_with_stellar_toml
			  from cp.operator_sites v join t on t.id = v.tenant_id
			  group by v.tenant_id
			)
			select t.id, t.slug, t.name, t.status, t.created_at,
			       coalesce(s.sites, 0) as sites, coalesce(s.sites_verified, 0) as sites_verified,
			       coalesce(s.sites_with_stellar_toml, 0) as sites_with_stellar_toml
			from t left join s on s.tenant_id = t.id`
		rows, err := tx.Query(ctx, tenantKeys.wrap(inner, p, args), args)
		if err != nil {
			return err
		}
		items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (MachineTenant, error) {
			var t MachineTenant
			var at time.Time
			err := row.Scan(&t.ID, &t.Slug, &t.Name, &t.Status, &at, &t.Sites, &t.SitesVerified, &t.SitesWithStellarToml)
			t.CreatedAt = httpx.Timestamp(at)
			t.Verified, t.StellarToml = t.SitesVerified > 0, t.SitesWithStellarToml > 0
			return t, err
		})
		if err != nil {
			return err
		}
		if len(items) > p.Limit {
			items = items[:p.Limit]
			out.NextCursor = p.next([]string{items[len(items)-1].ID.String()}, "", "")
		}
		out.Items = items
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: machine tenants: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// MachineWebTotals are umami's /stats fields summed over sites.
type MachineWebTotals struct {
	Sites     int64 `json:"sites"`
	Pageviews int64 `json:"pageviews"`
	Visitors  int64 `json:"visitors"`
	Visits    int64 `json:"visits"`
	Bounces   int64 `json:"bounces"`
	Totaltime int64 `json:"totaltime"`
}

// MachineWebDay is one item of GET /web/daily. Integrity is the grade of
// the tenant's sites on that day (contract 17.4), the same on both bands'
// items.
type MachineWebDay struct {
	TenantID uuid.UUID `json:"tenant_id"`
	Day      string    `json:"day"`
	Band     string    `json:"band"`
	MachineWebTotals
	Integrity integrityview.Summary `json:"integrity"`
	// Coverage is the tenant's estimate of visits not measured because of
	// privacy signals on that day (contract 17.5), the same on both bands'
	// items
	Coverage coverageview.Estimate `json:"coverage"`
}

var webDayKeys = keyset{cols: []string{"z.tenant_id", "z.day", "z.band"}, casts: []string{"uuid", "date", "text"}}

func (m *Machine) webDaily(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := checkParams(q, "tenant", "band", "from", "to", "limit", "cursor"); err != nil {
		return err
	}
	key := keyFrom(r.Context())
	p, err := parseMachinePage(q, "machine-web-daily", key)
	if err != nil {
		return err
	}
	if err := webDayKeys.validAfter(p); err != nil {
		return err
	}
	rg, err := p.days(q, m.now())
	if err != nil {
		return err
	}
	band, err := parseEnum(q, "band", "primary", "quarantine")
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := MachineRangeList[MachineWebDay]{From: rg.From.Format(time.DateOnly), To: rg.To.Format(time.DateOnly), Items: []MachineWebDay{}}
	err = m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		sc, err := resolveScope(ctx, tx, key, q)
		if err != nil {
			return err
		}
		args := pgx.NamedArgs{"tenant": sc.arg(), "from_day": rg.From, "to_day": rg.To, "band": band}
		inner := `with ` + tenantScopeCTE + `
			select d.tenant_id, d.day, d.band, count(*)::bigint as sites,
			       sum(d.pageviews)::bigint, sum(d.visitors)::bigint, sum(d.visits)::bigint,
			       sum(d.bounces)::bigint, sum(d.totaltime)::bigint
			from agg.web_daily d join t on t.id = d.tenant_id
			where d.day between @from_day::date and @to_day::date
			  and (@band::text is null or d.band = @band::text)
			group by d.tenant_id, d.day, d.band`
		rows, err := tx.Query(ctx, webDayKeys.wrap(inner, p, args), args)
		if err != nil {
			return err
		}
		items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (MachineWebDay, error) {
			var d MachineWebDay
			var day time.Time
			err := row.Scan(&d.TenantID, &day, &d.Band, &d.Sites, &d.Pageviews, &d.Visitors, &d.Visits, &d.Bounces, &d.Totaltime)
			d.Day = day.Format(time.DateOnly)
			return d, err
		})
		if err != nil {
			return err
		}
		if len(items) > p.Limit {
			items = items[:p.Limit]
			l := items[len(items)-1]
			out.NextCursor = p.next([]string{l.TenantID.String(), l.Day, l.Band}, out.From, out.To)
		}
		if err := attachDayIntegrity(ctx, tx, items, rg.From, rg.To); err != nil {
			return err
		}
		out.Items = items
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: machine web daily: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// MachineWebDimension is one item of GET /web/dimensions.
type MachineWebDimension struct {
	TenantID  uuid.UUID `json:"tenant_id"`
	Day       string    `json:"day"`
	Band      string    `json:"band"`
	Dimension string    `json:"dimension"`
	Key       string    `json:"key"`
	Value     int64     `json:"value"`
}

var webDimKeys = keyset{
	cols:  []string{"z.tenant_id", "z.day", "z.band", "z.dimension", "z.key"},
	casts: []string{"uuid", "date", "text", "text", "text"},
}

func (m *Machine) webDimensions(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := checkParams(q, "tenant", "band", "dimension", "from", "to", "limit", "cursor"); err != nil {
		return err
	}
	key := keyFrom(r.Context())
	p, err := parseMachinePage(q, "machine-web-dimensions", key)
	if err != nil {
		return err
	}
	if err := webDimKeys.validAfter(p); err != nil {
		return err
	}
	rg, err := p.days(q, m.now())
	if err != nil {
		return err
	}
	band, err := parseEnum(q, "band", "primary", "quarantine")
	if err != nil {
		return err
	}
	dim, err := parseEnum(q, "dimension", webDims...)
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := MachineRangeList[MachineWebDimension]{From: rg.From.Format(time.DateOnly), To: rg.To.Format(time.DateOnly), Items: []MachineWebDimension{}}
	err = m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		sc, err := resolveScope(ctx, tx, key, q)
		if err != nil {
			return err
		}
		args := pgx.NamedArgs{"tenant": sc.arg(), "from_day": rg.From, "to_day": rg.To, "band": band, "dimension": dim}
		inner := `with ` + tenantScopeCTE + `
			select d.tenant_id, d.day, d.band, d.dimension, d.key, sum(d.value)::bigint as value
			from agg.web_daily_dim d join t on t.id = d.tenant_id
			where d.day between @from_day::date and @to_day::date
			  and (@band::text is null or d.band = @band::text)
			  and (@dimension::text is null or d.dimension = @dimension::text)
			group by d.tenant_id, d.day, d.band, d.dimension, d.key`
		rows, err := tx.Query(ctx, webDimKeys.wrap(inner, p, args), args)
		if err != nil {
			return err
		}
		items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (MachineWebDimension, error) {
			var d MachineWebDimension
			var day time.Time
			err := row.Scan(&d.TenantID, &day, &d.Band, &d.Dimension, &d.Key, &d.Value)
			d.Day = day.Format(time.DateOnly)
			return d, err
		})
		if err != nil {
			return err
		}
		if len(items) > p.Limit {
			items = items[:p.Limit]
			l := items[len(items)-1]
			out.NextCursor = p.next([]string{l.TenantID.String(), l.Day, l.Band, l.Dimension, l.Key}, out.From, out.To)
		}
		out.Items = items
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: machine web dimensions: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// MachineWebHour is one item of GET /web/hourly.
type MachineWebHour struct {
	TenantID uuid.UUID       `json:"tenant_id"`
	Hour     httpx.Timestamp `json:"hour"`
	Band     string          `json:"band"`
	Complete bool            `json:"complete"`
	MachineWebTotals
}

// MachineWebHourly is the answer of GET /web/hourly.
type MachineWebHourly struct {
	TenantID uuid.UUID        `json:"tenant_id"`
	From     httpx.Timestamp  `json:"from"`
	To       httpx.Timestamp  `json:"to"`
	ReadAt   httpx.Timestamp  `json:"read_at"`
	Items    []MachineWebHour `json:"items"`
}

// webHourWorkers bounds the umami requests of one /web/hourly answer that
// run at once, so one consumer cannot occupy umami.
const webHourWorkers = 4

// hourSite is a site /web/hourly reads: its primary and quarantine websites.
type hourSite struct {
	ID         uuid.UUID
	Quarantine string
}

// webHourly reads umami live, through the control plane's service account,
// for the hours nothing has aggregated yet (the
// prober checks freshness with it). only /stats sums leave this handler.
func (m *Machine) webHourly(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := checkParams(q, "tenant", "from", "to"); err != nil {
		return err
	}
	key := keyFrom(r.Context())
	if key.Tenant == nil {
		if _, ok := q["tenant"]; !ok {
			return httpx.BadQuery("tenant is required with a key for all tenants: each request reads umami once per site, band and hour")
		}
	}
	readAt := m.now().UTC()
	rg, err := parseHourRange(q, readAt, maxWebHours)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var tenant uuid.UUID
	var sites []hourSite
	err = m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		sc, err := resolveScope(ctx, tx, key, q)
		if err != nil {
			return err
		}
		tenant = *sc.One
		rows, err := tx.Query(ctx, `select id, quarantine_website_id from cp.operator_sites
			where tenant_id = $1 and status = 'active' order by id`, tenant)
		if err != nil {
			return err
		}
		sites, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (hourSite, error) {
			var s hourSite
			var qid uuid.NullUUID
			if err := row.Scan(&s.ID, &qid); err != nil {
				return s, err
			}
			if !qid.Valid {
				// an active site always has both websites (provisioning
				// creates them before activation); reading only one band
				// would report the other as empty
				return s, fmt.Errorf("active site %s has no quarantine website", s.ID)
			}
			s.Quarantine = qid.UUID.String()
			return s, nil
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("operator: machine web hourly: %w", err)
	}
	hours := rg.hours()
	out := MachineWebHourly{
		TenantID: tenant, From: httpx.Timestamp(rg.From), To: httpx.Timestamp(rg.To), ReadAt: httpx.Timestamp(readAt),
		Items: make([]MachineWebHour, 0, 2*len(hours)),
	}
	for _, h := range hours {
		for _, band := range []string{"primary", "quarantine"} {
			out.Items = append(out.Items, MachineWebHour{
				TenantID: tenant, Hour: httpx.Timestamp(h), Band: band,
				Complete: !h.Add(time.Hour).After(readAt), MachineWebTotals: MachineWebTotals{Sites: int64(len(sites))},
			})
		}
	}
	if err := m.readHours(ctx, sites, hours, out.Items); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// readHours fills items (hour-major, primary then quarantine) with umami's
// /stats of every site. a site whose website umami does not know is an
// error: umami answers /stats of an unknown website with zeros for the
// admin, which would pass for an hour without traffic.
func (m *Machine) readHours(ctx context.Context, sites []hourSite, hours []time.Time, items []MachineWebHour) error {
	type job struct {
		website string
		item    int
		hour    time.Time
	}
	var jobs []job
	for _, s := range sites {
		for _, website := range []string{s.ID.String(), s.Quarantine} {
			ws, ok, err := m.umami.GetWebsite(ctx, website)
			if err != nil {
				return m.umamiFailed(ctx, err)
			}
			if !ok || ws.ID != website {
				return fmt.Errorf("operator: machine web hourly: umami has no website %s of site %s", website, s.ID)
			}
		}
		for i, h := range hours {
			jobs = append(jobs, job{website: s.ID.String(), item: 2 * i, hour: h}, job{website: s.Quarantine, item: 2*i + 1, hour: h})
		}
	}
	var mu sync.Mutex
	var firstErr error
	work := make(chan job)
	var wg sync.WaitGroup
	for range webHourWorkers {
		wg.Go(func() {
			for j := range work {
				// umami includes both ends of a range, so an hour ends one
				// millisecond before the next (docs/reference/web-metrics.md)
				st, err := m.umami.Stats(ctx, j.website, umami.Range{Start: j.hour, End: j.hour.Add(time.Hour - time.Millisecond)})
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
				} else {
					it := &items[j.item]
					it.Pageviews += st.Pageviews
					it.Visitors += st.Visitors
					it.Visits += st.Visits
					it.Bounces += st.Bounces
					it.Totaltime += st.Totaltime
				}
				mu.Unlock()
			}
		})
	}
	for _, j := range jobs {
		mu.Lock()
		stop := firstErr != nil
		mu.Unlock()
		if stop || ctx.Err() != nil {
			break
		}
		work <- j
	}
	close(work)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if firstErr != nil {
		return m.umamiFailed(ctx, firstErr)
	}
	return nil
}

// umamiFailed turns an umami failure into 503 unavailable; the detail goes
// to the log only.
func (m *Machine) umamiFailed(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	httpx.Logger(ctx).Error("machine web hourly: umami read failed", "error", err.Error())
	return httpx.Unavailable("umami could not be read")
}
