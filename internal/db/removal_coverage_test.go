package db_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// removalRule is how a tenant removal (contract 5.5) treats one tenant
// table: deleted inside a purge function, deleted or updated by a named
// query file, or kept on purpose.
type removalRule struct {
	// function is a security definer purge function whose current body
	// must delete from the table
	function string
	// query is a file under db/queries whose text must contain stmt
	query, stmt string
	// kept says why the rows stay after removal
	kept string
}

// removalRules names, for every tenant table, what a removal does with its
// rows. a migration that adds a tenant table makes TestRemovalCoversEveryTenantTable
// fail until the table gets a rule here, so no new table is forgotten by
// the purge.
var removalRules = map[string]removalRule{
	"cp.tenants":           {kept: "status removed, the row stays (5.5)"},
	"cp.tenant_members":    {kept: "former members see the project under removed_tenants (5.10)"},
	"cp.invites":           {query: "system/lifecycle.sql", stmt: "delete from cp.invites where tenant_id"},
	"cp.sites":             {query: "system/lifecycle.sql", stmt: "update cp.sites set status = 'removed'"},
	"cp.otlp_tokens":       {query: "system/otlp_tokens.sql", stmt: "update cp.otlp_tokens"},
	"cp.erasure_requests":  {kept: "the record of erasures done holds ids only"},
	"cp.web_ingest_hourly": {query: "system/lifecycle.sql", stmt: "delete from cp.web_ingest_hourly where tenant_id"},
	"cp.web_evidence_daily": {
		query: "system/lifecycle.sql", stmt: "delete from cp.web_evidence_daily where tenant_id"},
	"cp.web_signals_daily": {query: "system/lifecycle.sql", stmt: "delete from cp.web_signals_daily where tenant_id"},
	"cp.web_networks_daily": {
		query: "system/lifecycle.sql", stmt: "delete from cp.web_networks_daily where tenant_id"},
	"cp.tenant_removals":  {kept: "the removal record itself"},
	"cp.mcp_credentials":  {function: "cp.mcp_revoke_matching(uuid,uuid,text,text)"},
	"cp.mcp_oauth_codes":  {kept: "single use, minutes long, and refused once the tenant is removed"},
	"cp.chain_ids":        {kept: "public ledger ids the project declared; nothing reads them once removed"},
	"cp.tenant_cohorts":   {kept: "the operator's program label of the project"},
	"cp.usage_reports":    {function: "cp.purge_usage_reports(uuid)"},
	"api.metrics_minute":  {function: "api.purge_tenant(uuid,boolean)"},
	"api.metrics_hourly":  {function: "api.purge_tenant(uuid,boolean)"},
	"api.metrics_daily":   {function: "api.purge_tenant(uuid,boolean)"},
	"api.ingest_warnings": {function: "api.purge_tenant(uuid,boolean)"},
	"api.series_state":    {function: "api.purge_tenant(uuid,boolean)"},
	"api.resource_seen":   {function: "api.purge_tenant(uuid,boolean)"},
	"api.route_registry":  {function: "api.purge_tenant(uuid,boolean)"},
	"api.rollup_pending":  {function: "api.purge_tenant(uuid,boolean)"},
	"api.ingest_dedup": {
		kept: "message ids that stop a queued message from applying again; api-retention ages them out"},
	"agg.web_daily":            {function: "agg.purge_tenant(uuid)"},
	"agg.web_daily_dim":        {function: "agg.purge_tenant(uuid)"},
	"agg.web_integrity_daily":  {function: "agg.purge_tenant(uuid)"},
	"agg.web_coverage_daily":   {function: "agg.purge_tenant(uuid)"},
	"agg.chain_daily":          {function: "agg.purge_chain(uuid,boolean)"},
	"agg.chain_reads":          {function: "agg.purge_chain(uuid,boolean)"},
	"agg.chain_weeks":          {function: "agg.purge_chain(uuid,boolean)"},
	"agg.chain_week_members":   {function: "agg.purge_chain(uuid,boolean)"},
	"agg.chain_function_daily": {function: "agg.purge_chain(uuid,boolean)"},
}

func TestRemovalCoversEveryTenantTable(t *testing.T) {
	ctx := context.Background()
	c := superuser(t)
	rows, err := c.Query(ctx, `
		select n.nspname || '.' || c.relname
		from pg_class c join pg_namespace n on n.oid = c.relnamespace
		where n.nspname in ('cp', 'api', 'agg') and c.relkind in ('r', 'p') and not c.relispartition
		  and (exists (select 1 from pg_attribute a where a.attrelid = c.oid and a.attname = 'tenant_id' and not a.attisdropped)
		       or (n.nspname = 'cp' and c.relname = 'tenants'))
		order by 1`)
	require.NoError(t, err)
	var tables []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		tables = append(tables, s)
	}
	require.NoError(t, rows.Err())

	seen := map[string]bool{}
	for _, table := range tables {
		seen[table] = true
		r, ok := removalRules[table]
		if !assertRule(t, ok, "%s has a tenant_id but no removal rule; add one to removalRules", table) {
			continue
		}
		switch {
		case r.function != "":
			var src string
			require.NoError(t, c.QueryRow(ctx, `select prosrc from pg_proc where oid = $1::regprocedure`, r.function).Scan(&src),
				r.function)
			verb := "delete from " + table + " "
			if table == "cp.mcp_credentials" {
				verb = "update " + table + " "
			}
			assertRule(t, strings.Contains(src, verb), "%s does not run %q", r.function, verb)
		case r.query != "":
			b, err := os.ReadFile(filepath.Join("..", "..", "db", "queries", r.query))
			require.NoError(t, err)
			assertRule(t, strings.Contains(string(b), r.stmt), "db/queries/%s lacks %q", r.query, r.stmt)
		default:
			assertRule(t, r.kept != "", "%s: a rule needs a function, a query or a reason to keep", table)
		}
	}
	for table := range removalRules {
		assertRule(t, seen[table], "removalRules names %s, which is not a tenant table", table)
	}
}

func assertRule(t *testing.T, ok bool, format string, args ...any) bool {
	t.Helper()
	if !ok {
		t.Errorf(format, args...)
	}
	return ok
}

// resetRules names, for every table with a site_id, what a site reset
// (contract 10, erasure kind site_reset) does with the site's rows: the
// erasure follow-up deletes them through the named query file, or they
// stay on purpose. a new table with a site_id fails
// TestSiteResetCoversEverySiteTable until it gets a rule.
var resetRules = map[string]removalRule{
	"cp.sites":              {kept: "the site itself stays, with no web data"},
	"cp.erasure_requests":   {kept: "the record of the reset"},
	"cp.web_ingest_hourly":  {query: "system/erasure.sql", stmt: "delete from cp.web_ingest_hourly x where x.site_id"},
	"cp.web_evidence_daily": {query: "system/erasure.sql", stmt: "delete from cp.web_evidence_daily x where x.site_id"},
	"cp.web_signals_daily":  {query: "system/erasure.sql", stmt: "delete from cp.web_signals_daily x where x.site_id"},
	"cp.web_networks_daily": {query: "system/erasure.sql", stmt: "delete from cp.web_networks_daily x where x.site_id"},
	"agg.web_daily":         {query: "jobs/aggweb.sql", stmt: "delete from agg.web_daily w where w.site_id"},
	"agg.web_daily_dim":     {query: "jobs/aggweb.sql", stmt: "delete from agg.web_daily_dim m where m.site_id"},
	"agg.web_integrity_daily": {
		query: "jobs/aggweb.sql", stmt: "delete from agg.web_integrity_daily g where g.site_id"},
	"agg.web_coverage_daily": {query: "jobs/aggweb.sql", stmt: "delete from agg.web_coverage_daily v where v.site_id"},
}

func TestSiteResetCoversEverySiteTable(t *testing.T) {
	ctx := context.Background()
	c := superuser(t)
	rows, err := c.Query(ctx, `
		select n.nspname || '.' || c.relname
		from pg_class c join pg_namespace n on n.oid = c.relnamespace
		where n.nspname in ('cp', 'api', 'agg') and c.relkind in ('r', 'p') and not c.relispartition
		  and (exists (select 1 from pg_attribute a where a.attrelid = c.oid and a.attname = 'site_id' and not a.attisdropped)
		       or (n.nspname = 'cp' and c.relname = 'sites'))
		order by 1`)
	require.NoError(t, err)
	var tables []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		tables = append(tables, s)
	}
	require.NoError(t, rows.Err())
	seen := map[string]bool{}
	for _, table := range tables {
		seen[table] = true
		r, ok := resetRules[table]
		if !assertRule(t, ok, "%s has a site_id but no site reset rule; add one to resetRules", table) {
			continue
		}
		if r.query == "" {
			assertRule(t, r.kept != "", "%s: a rule needs a query or a reason to keep", table)
			continue
		}
		b, err := os.ReadFile(filepath.Join("..", "..", "db", "queries", r.query))
		require.NoError(t, err)
		assertRule(t, strings.Contains(string(b), r.stmt), "db/queries/%s lacks %q", r.query, r.stmt)
	}
	for table := range resetRules {
		assertRule(t, seen[table], "resetRules names %s, which has no site_id", table)
	}
}
