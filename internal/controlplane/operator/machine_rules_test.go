package operator

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestMachineRules reads the saved rules and their matches through the
// machine api with a key for all tenants and a key scoped to one,
// validating every answer against the openapi document.
func TestMachineRules(t *testing.T) {
	e := newMachineEnv(t)
	f := e.f
	aurora, borealis := f.tenants["aurora"], f.tenants["borealis"]
	// two days of 5 visitors in the previous calendar month for each
	first := time.Date(f.today.Year(), f.today.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	for _, id := range []uuid.UUID{aurora, borealis} {
		site := uuid.Must(uuid.NewV7())
		superExec(t, f.pg, `insert into cp.sites (id, tenant_id, name, domain, verify_token) values ($1, $2, $3, $3, 'x')`,
			site, id, site.String()+".example")
		for i := range 2 {
			superExec(t, f.pg, `insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
				values ($1, $2, $3, 'primary', 5, 5, 5, 0, 0)`, site, id, first.AddDate(0, 0, i))
		}
	}
	rule := uuid.Must(uuid.NewV7())
	superExec(t, f.pg, `insert into cp.threshold_rules (id, name, metric, min_per_day, min_days, months)
		values ($1, 'two good days', 'visitors', 5, 2, 1)`, rule)
	global := e.createKey(t, `{"name":"rules warehouse","scope":"metrics:read"}`)
	scoped := e.createKey(t, fmt.Sprintf(`{"name":"aurora rules","scope":"metrics:read","tenant_id":%q,"expires_in_days":30}`, aurora))

	a := e.get(t, global.Key, "/rules", http.StatusOK)
	rules := decode[RuleList](t, a.body)
	require.Len(t, rules.Items, 1)
	require.Equal(t, "two good days", rules.Items[0].Name)
	require.NotContains(t, string(a.body), "match_count", "the machine list keeps the saved rules alone")

	a = e.get(t, global.Key, "/rules/"+rule.String()+"/matches", http.StatusOK)
	m := decode[RuleMatches](t, a.body)
	var met []uuid.UUID
	for _, it := range m.Items {
		met = append(met, it.Tenant.ID)
		require.Equal(t, 2, it.Months[0].QualifyingDays)
	}
	require.ElementsMatch(t, []uuid.UUID{aurora, borealis}, met)

	a = e.get(t, global.Key, "/rules/"+rule.String()+"/matches?tenant="+borealis.String(), http.StatusOK)
	require.Len(t, decode[RuleMatches](t, a.body).Items, 1)

	// a scoped key evaluates its own tenant only
	a = e.get(t, scoped.Key, "/rules/"+rule.String()+"/matches", http.StatusOK)
	m = decode[RuleMatches](t, a.body)
	require.Len(t, m.Items, 1)
	require.Equal(t, aurora, m.Items[0].Tenant.ID)
	require.Equal(t, 1, m.Evaluated)
	e.get(t, scoped.Key, "/rules/"+rule.String()+"/matches?tenant="+borealis.String(), http.StatusForbidden)

	e.get(t, global.Key, "/rules/"+uuid.NewString()+"/matches", http.StatusNotFound)
	e.get(t, global.Key, "/rules/"+rule.String()+"/matches?as_of=2999-01-01", http.StatusBadRequest)
	e.get(t, global.Key, "/rules?limit=5", http.StatusBadRequest)
}
