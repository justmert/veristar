package operator

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/integrityview"
	"github.com/justmert/veristar/internal/integrity"
)

// attachDayIntegrity sets the integrity of every /web/daily item from the
// grades of the tenant's sites on that day.
func attachDayIntegrity(ctx context.Context, tx pgx.Tx, items []MachineWebDay, from, to time.Time) error {
	var tenants []uuid.UUID
	for _, it := range items {
		if !slices.Contains(tenants, it.TenantID) {
			tenants = append(tenants, it.TenantID)
		}
	}
	byDay, err := integrityview.ByTenantDay(ctx, tx, tenants, from, to)
	if err != nil {
		return err
	}
	empty := integrityview.SummaryOf(integrity.Combine(nil))
	for i := range items {
		s, ok := byDay[integrityview.DayKey{Tenant: items[i].TenantID, Day: items[i].Day}]
		if !ok {
			s = empty
		}
		items[i].Integrity = s
	}
	return nil
}

// MachineWebIntegrity is one item of GET /web/integrity: one site and day
// with its grade, reasons and the evidence they rest on.
type MachineWebIntegrity struct {
	TenantID uuid.UUID `json:"tenant_id"`
	SiteID   uuid.UUID `json:"site_id"`
	integrityview.Day
}

var webIntegrityKeys = keyset{cols: []string{"z.tenant_id", "z.site_id", "z.day"}, casts: []string{"uuid", "uuid", "date"}}

func (m *Machine) webIntegrity(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := checkParams(q, "tenant", "from", "to", "limit", "cursor"); err != nil {
		return err
	}
	key := keyFrom(r.Context())
	p, err := parseMachinePage(q, "machine-web-integrity", key)
	if err != nil {
		return err
	}
	if err := webIntegrityKeys.validAfter(p); err != nil {
		return err
	}
	rg, err := p.days(q, m.now())
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := MachineRangeList[MachineWebIntegrity]{From: rg.From.Format(time.DateOnly), To: rg.To.Format(time.DateOnly), Items: []MachineWebIntegrity{}}
	err = m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		// the page's keys and its rows are read in two statements, so both
		// must see one snapshot
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		sc, err := resolveScope(ctx, tx, key, q)
		if err != nil {
			return err
		}
		args := pgx.NamedArgs{"tenant": sc.arg(), "from_day": rg.From, "to_day": rg.To}
		inner := `with ` + tenantScopeCTE + `
			select i.tenant_id, i.site_id, i.day
			from agg.web_integrity_daily i join t on t.id = i.tenant_id
			where i.day between @from_day::date and @to_day::date`
		rows, err := tx.Query(ctx, webIntegrityKeys.wrap(inner, p, args), args)
		if err != nil {
			return err
		}
		type ref struct {
			tenant, site uuid.UUID
			day          time.Time
		}
		refs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ref, error) {
			var x ref
			return x, row.Scan(&x.tenant, &x.site, &x.day)
		})
		if err != nil {
			return err
		}
		if len(refs) > p.Limit {
			refs = refs[:p.Limit]
			l := refs[len(refs)-1]
			out.NextCursor = p.next([]string{l.tenant.String(), l.site.String(), l.day.Format(time.DateOnly)}, out.From, out.To)
		}
		if len(refs) == 0 {
			return nil
		}
		// the page's rows in full, through the same reader as the console
		var tenants []uuid.UUID
		for _, x := range refs {
			if !slices.Contains(tenants, x.tenant) {
				tenants = append(tenants, x.tenant)
			}
		}
		reps, err := integrityview.Load(ctx, tx, tenants, rg.From, rg.To)
		if err != nil {
			return err
		}
		days := map[string]integrityview.Day{}
		for t, rep := range reps {
			for _, s := range rep.Sites {
				for _, d := range s.Days {
					days[t.String()+"/"+s.SiteID.String()+"/"+d.Day] = d
				}
			}
		}
		for _, x := range refs {
			d, ok := days[x.tenant.String()+"/"+x.site.String()+"/"+x.day.Format(time.DateOnly)]
			if !ok {
				return fmt.Errorf("integrity row of site %s day %s missing from the second read", x.site, x.day.Format(time.DateOnly))
			}
			out.Items = append(out.Items, MachineWebIntegrity{TenantID: x.tenant, SiteID: x.site, Day: d})
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: machine web integrity: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
