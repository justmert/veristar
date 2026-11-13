// Package rolematrix is the expected rights matrix of the design for every
// relation in the cp, api and agg schemas of the platform database. it is a
// test fixture: internal/db's catalog test (TestGrantMatrix) compares the
// grants postgresql reports with it, and the behavioural gate in
// tests/isolation/dbroles runs real statements as each role against it.
// both read this one copy, so the two tests cannot drift apart.
package rolematrix

import (
	"fmt"
	"sort"
	"strings"

	"github.com/justmert/stellar-analytics/internal/db"
)

// Want is the whole rights matrix. letters follow postgresql acl notation:
// r select, a insert, w update, d delete, D truncate, x references, t
// trigger. column rights are written as letter(columns), columns sorted. a
// relation missing here, or a right not listed, fails both tests, so every
// new table must state its rights.
var Want = map[string]map[db.Role]string{
	"cp.users": {
		db.RoleCPSystem: "rawd",
		db.RoleCPTenant: "r(github_id,github_login,id)",
	},
	"cp.sessions": {db.RoleCPSystem: "rawd"},
	"cp.tenants": {
		db.RoleCPSystem:   "rawd",
		db.RoleCPTenant:   "r w(accepted_terms_version,description,name,updated_at)",
		db.RoleCPOperator: "r",
		// the writer refuses queued metrics of a removed tenant (00018)
		db.RoleMetricsWriter: "r(id,status)",
		// aggregate-web skips removed tenants (00019)
		db.RoleJobs: "r(id,status)",
	},
	// role only, and a trigger lets only an owner change members (00028, C-6)
	"cp.tenant_members": {db.RoleCPSystem: "rawd", db.RoleCPTenant: "rad w(role)"},
	"cp.invites":        {db.RoleCPSystem: "rawd", db.RoleCPTenant: "rawd"},
	// cp_tenant updates only what its handlers write (00024, review);
	// the verification columns are written under cp_system (00028)
	"cp.sites": {
		db.RoleCPSystem: "rawd",
		db.RoleCPTenant: "rad w(collect_utm,name,origins,status,updated_at)",
	},
	"cp.otlp_tokens": {
		db.RoleCPSystem:      "rawd",
		db.RoleCPTenant:      "rad w(revoked_at,status)",
		db.RoleMetricsWriter: "r(id,status,tenant_id) w(last_seen_at)",
	},
	"cp.operator_api_keys": {
		db.RoleCPSystem:   "rawd",
		db.RoleCPOperator: "r w(last_used_at)",
	},
	"cp.api_key_usage_hourly": {db.RoleCPSystem: "rawd", db.RoleCPOperator: "raw"},
	"cp.settings":             {db.RoleCPSystem: "rawd", db.RoleCPOperator: "r"},
	"cp.audit_log":            {db.RoleCPSystem: "ra", db.RoleCPTenant: "a"},
	"cp.erasure_requests":     {db.RoleCPSystem: "rawd", db.RoleCPTenant: "ra"},
	"cp.web_ingest_hourly": {
		db.RoleCPSystem:   "rawd",
		db.RoleCPTenant:   "r",
		db.RoleCPOperator: "r",
		db.RoleJobs:       "raw",
	},
	"cp.stats_dedup":     {db.RoleCPSystem: "rawd", db.RoleJobs: "rawd"},
	"cp.leader_fence":    {db.RoleCPSystem: "rawd", db.RoleMetricsWriter: "rw", db.RoleJobs: "rw"},
	"cp.operator_sites":  {db.RoleCPSystem: "r", db.RoleCPOperator: "r"},
	"cp.tenant_removals": {db.RoleCPSystem: "rawd", db.RoleCPOperator: "r"},

	// api (this stage). partitions carry no rights at all; internal/db checks
	// them on their own in TestPartitionRights.
	"api.metrics_minute": {db.RoleCPTenant: "r", db.RoleMetricsWriter: "raw", db.RoleJobs: "rawd"},
	"api.metrics_hourly": {db.RoleCPTenant: "r", db.RoleCPOperator: "r", db.RoleJobs: "rawd"},
	"api.metrics_daily":  {db.RoleCPTenant: "r", db.RoleCPOperator: "r", db.RoleJobs: "rawd"},
	"api.series_state":   {db.RoleMetricsWriter: "raw", db.RoleJobs: "rawd"},
	"api.resource_seen":  {db.RoleMetricsWriter: "raw", db.RoleJobs: "rawd"},
	"api.ingest_dedup":   {db.RoleMetricsWriter: "raw", db.RoleJobs: "rawd"},
	"api.route_registry": {db.RoleMetricsWriter: "raw", db.RoleJobs: "rawd"},
	"api.ingest_warnings": {
		db.RoleCPTenant: "r", db.RoleMetricsWriter: "raw", db.RoleJobs: "rawd",
	},
	// cp_tenant reads its own pending hours (00021, control-plane-api 7.2)
	"api.rollup_pending": {db.RoleCPTenant: "r", db.RoleMetricsWriter: "raw", db.RoleJobs: "rawd"},

	// agg (this stage): read by operators, written by aggregate-web
	"agg.web_daily":     {db.RoleCPOperator: "r", db.RoleJobs: "rawd"},
	"agg.web_daily_dim": {db.RoleCPOperator: "r", db.RoleJobs: "rawd"},
}

// Right is one privilege: its acl letter and its sql name.
type Right struct{ Letter, Name string }

// Rights lists the table privileges in acl order.
var Rights = []Right{
	{"r", "SELECT"}, {"a", "INSERT"}, {"w", "UPDATE"}, {"d", "DELETE"},
	{"D", "TRUNCATE"}, {"x", "REFERENCES"}, {"t", "TRIGGER"},
}

// Grant is one parsed entry of Want: per letter, either the whole relation
// (All) or the listed columns.
type Grant struct {
	All     bool
	Columns []string
}

// Parse reads one entry of Want, such as "r w(last_used_at)", into a grant
// per letter. a letter that is absent is not granted.
func Parse(s string) (map[string]Grant, error) {
	out := map[string]Grant{}
	for _, part := range strings.Fields(s) {
		letters, cols, hasCols := strings.Cut(part, "(")
		if hasCols {
			if len(letters) != 1 || !strings.HasSuffix(cols, ")") {
				return nil, fmt.Errorf("rolematrix: bad column grant %q", part)
			}
			names := strings.Split(strings.TrimSuffix(cols, ")"), ",")
			sort.Strings(names)
			out[letters] = Grant{Columns: names}
			continue
		}
		for _, l := range letters {
			out[string(l)] = Grant{All: true}
		}
	}
	return out, nil
}
