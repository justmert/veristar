// Package rolematrix is the expected database rights matrix for every
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

	"github.com/justmert/veristar/internal/db"
)

// Want is the whole rights matrix. letters follow postgresql acl notation:
// r select, a insert, w update, d delete, D truncate, x references, t
// trigger. column rights are written as letter(columns), columns sorted. a
// relation missing here, or a right not listed, fails both tests, so every
// new table must state its rights.
var Want = map[string]map[db.Role]string{
	"cp.users": {
		db.RoleCPSystem: "rawd",
		db.RoleCPTenant: "r(github_id,github_login,id,stellar_address)",
	},
	"cp.sessions": {db.RoleCPSystem: "rawd"},
	// used wallet sign-in challenges (00034): written and pruned by sign-in
	"cp.wallet_challenges_used": {db.RoleCPSystem: "rad"},
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
	// cp_tenant updates only what its handlers write (migration 00024);
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
	// mcp credentials (00030): the console's key routes create and revoke
	// under cp_tenant, everything else runs under cp_system
	"cp.mcp_oauth_clients": {db.RoleCPSystem: "rawd"},
	"cp.mcp_credentials": {
		db.RoleCPSystem: "rawd",
		db.RoleCPTenant: "ra w(revoked_at,revoked_reason)",
	},
	"cp.mcp_oauth_codes":      {db.RoleCPSystem: "rawd"},
	"cp.mcp_oauth_tokens":     {db.RoleCPSystem: "rawd"},
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

	// api. partitions carry no rights at all; internal/db checks
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

	// the on-chain footprint (00032): a tenant adds and removes its own
	// ids, cp_system writes challenges and verification, chain-activity
	// reads the list
	"cp.chain_ids": {
		db.RoleCPSystem: "rawd", db.RoleCPTenant: "rd a(added_by,kind,strkey,tenant_id)",
		db.RoleCPOperator: "r", db.RoleJobs: "r",
	},

	// agg: read by operators, written by aggregate-web; a tenant reads its
	// own web rows for the overview export (00032)
	"agg.web_daily":     {db.RoleCPTenant: "r", db.RoleCPOperator: "r", db.RoleJobs: "rawd"},
	"agg.web_daily_dim": {db.RoleCPOperator: "r", db.RoleJobs: "rawd"},
	// ledger activity (00032): written by chain-activity, read by the
	// tenant (its own rows) and operators
	"agg.chain_daily": {db.RoleCPTenant: "r", db.RoleCPOperator: "r", db.RoleJobs: "rawd"},
	"agg.chain_reads": {db.RoleCPTenant: "r", db.RoleCPOperator: "r", db.RoleJobs: "rawd"},
	// integrity (00033): the grades are read by the tenant (its own rows)
	// and operators; edgestats and integrity write the evidence and network
	// rows behind them, and cp_system deletes those in a purge
	"agg.web_integrity_daily": {db.RoleCPTenant: "r", db.RoleCPOperator: "r", db.RoleJobs: "rawd"},
	"cp.web_evidence_daily":   {db.RoleCPSystem: "rawd", db.RoleJobs: "rawd"},
	"cp.web_networks_daily":   {db.RoleCPSystem: "rawd", db.RoleJobs: "rawd"},

	// cohorts and threshold rules (00035): a member writes its own
	// project's proposal, operators confirm and keep the rules under
	// cp_system and read both under cp_operator
	"cp.tenant_cohorts": {
		db.RoleCPSystem: "rawd",
		db.RoleCPTenant: "r a(proposed_at,proposed_award_date,proposed_by,proposed_category,proposed_program," +
			"proposed_round,tenant_id,updated_at) w(proposed_at,proposed_award_date,proposed_by,proposed_category," +
			"proposed_program,proposed_round,updated_at)",
		db.RoleCPOperator: "r",
	},
	"cp.threshold_rules": {db.RoleCPSystem: "rawd", db.RoleCPOperator: "r"},
	// signed usage reports (00036): a member stores and revokes its
	// project's reports; the public route reads one and counts its views
	"cp.usage_reports": {
		db.RoleCPSystem:   "r w(last_viewed_at,view_count)",
		db.RoleCPTenant:   "r a(body,created_by,id,period_from,period_to,signature,signer,tenant_id) w(revoked_at,revoked_by)",
		db.RoleCPOperator: "r",
	},
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
