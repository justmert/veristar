//go:build isolation

// Package dbroles is the database part of the tenant isolation gate: the
// database role matrix. internal/db's TestGrantMatrix
// reads the grants from the catalog; this test does not trust the catalog:
// every application role runs real select, insert, update,
// delete and truncate statements against every relation in cp, api and agg,
// and what postgresql lets through must equal the shared matrix in
// tests/fixtures/rolematrix. it then checks, again by running statements,
// that cp_tenant reads and writes zero rows of another tenant.
package dbroles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/db"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
	"github.com/justmert/veristar/internal/db/systemq"
	"github.com/justmert/veristar/tests/fixtures/rolematrix"
)

// insufficient_privilege: a missing grant, and also a row that fails a
// row-level security check
const denied = "42501"

type env struct {
	pg   *dbtest.Postgres
	conn *pdb.DB
}

func start(t *testing.T) *env {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pg, err := dbtest.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	acfg, err := pdb.LoadAdminConfig(pg.Config("platform"))
	require.NoError(t, err)
	_, err = pdb.Migrate(ctx, acfg, db.Migrations(), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	rcfg, err := pdb.LoadRolesConfig(pg.Config("platform"))
	require.NoError(t, err)
	require.NoError(t, pdb.SetRolePasswords(ctx, rcfg))
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.AppRoles...)
	require.NoError(t, err)
	conn, err := pdb.Open(ctx, pcfg, "dbroles-gate")
	require.NoError(t, err)
	t.Cleanup(conn.Close)
	return &env{pg: pg, conn: conn}
}

func (e *env) superuser(t *testing.T) *pgx.Conn {
	t.Helper()
	c, err := e.pg.Superuser(context.Background(), "platform")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func sqlState(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

type column struct {
	name, typ string
	// generated or identity-always columns refuse every explicit value
	// before any privilege check, so they cannot be probed for insert or
	// update
	fixed bool
}

type relation struct {
	name string
	kind string
	cols []column
}

func relations(t *testing.T, c *pgx.Conn) []relation {
	t.Helper()
	ctx := context.Background()
	rows, err := c.Query(ctx, `
		select n.nspname || '.' || c.relname, c.relkind::text,
		       a.attname::text, format_type(a.atttypid, a.atttypmod),
		       a.attgenerated <> '' or a.attidentity = 'a'
		from pg_class c
		join pg_namespace n on n.oid = c.relnamespace
		join pg_attribute a on a.attrelid = c.oid and a.attnum > 0 and not a.attisdropped
		where n.nspname in ('cp', 'api', 'agg') and c.relkind in ('r', 'p', 'v', 'm', 'f')
		  and not c.relispartition
		order by 1, a.attnum`)
	require.NoError(t, err)
	defer rows.Close()
	var out []relation
	for rows.Next() {
		var name, kind string
		var col column
		require.NoError(t, rows.Scan(&name, &kind, &col.name, &col.typ, &col.fixed))
		if len(out) == 0 || out[len(out)-1].name != name {
			out = append(out, relation{name: name, kind: kind})
		}
		out[len(out)-1].cols = append(out[len(out)-1].cols, col)
	}
	require.NoError(t, rows.Err())
	return out
}

// probe runs one statement as role inside a savepoint of tx and reports
// whether postgresql allowed it. a statement that fails for any reason
// other than a privilege error fails the test, so a broken probe can never
// pass for a refusal; ok lists other codes that still mean "allowed" (a
// truncate refused by a foreign key got past the privilege check).
func probe(t *testing.T, tx pgx.Tx, sql string, ok ...string) bool {
	t.Helper()
	ctx := context.Background()
	_, err := tx.Exec(ctx, "savepoint p")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, sql)
	_, rerr := tx.Exec(ctx, "rollback to savepoint p")
	require.NoError(t, rerr)
	if err == nil {
		return true
	}
	code := sqlState(err)
	if code == denied {
		return false
	}
	for _, c := range ok {
		if code == c {
			return true
		}
	}
	t.Fatalf("probe %q: unexpected error (not a privilege refusal): %v", sql, err)
	return false
}

func ident(s string) string {
	schema, rel, _ := strings.Cut(s, ".")
	return pgx.Identifier{schema, rel}.Sanitize()
}

// observe returns what role can do on rel, in the notation of
// rolematrix.Want, from statements alone. column rights are probed one
// column at a time with statements that touch no row (where false), so row
// level security, constraints and triggers never answer instead of the
// grant.
func observe(t *testing.T, e *env, role pdb.Role, rel relation) string {
	t.Helper()
	ctx := context.Background()
	p, err := e.conn.Pool(role)
	require.NoError(t, err)
	tx, err := p.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	name := ident(rel.name)

	cols := map[string][]string{}
	for _, c := range rel.cols {
		col := pgx.Identifier{c.name}.Sanitize()
		if probe(t, tx, `select `+col+` from `+name+` where false`) {
			cols["r"] = append(cols["r"], c.name)
		}
		if rel.kind == "v" || c.fixed {
			continue
		}
		if probe(t, tx, `insert into `+name+` (`+col+`) select null::`+c.typ+` where false`) {
			cols["a"] = append(cols["a"], c.name)
		}
		if probe(t, tx, `update `+name+` set `+col+` = null::`+c.typ+` where false`) {
			cols["w"] = append(cols["w"], c.name)
		}
	}
	table := map[string]bool{}
	if rel.kind == "v" {
		// a view's writes would be judged by the rewriter before any grant;
		// the matrix gives no role a write on a view, and the probes below
		// require that every write is refused one way or another
		for _, sql := range []string{
			`insert into ` + name + ` default values`,
			`update ` + name + ` set ` + pgx.Identifier{rel.cols[0].name}.Sanitize() + ` = null where false`,
			`delete from ` + name + ` where false`,
		} {
			_, err := tx.Exec(ctx, "savepoint p")
			require.NoError(t, err)
			_, err = tx.Exec(ctx, sql)
			_, rerr := tx.Exec(ctx, "rollback to savepoint p")
			require.NoError(t, rerr)
			require.Error(t, err, "%s on view %s as %s must be refused", sql, rel.name, role)
		}
	} else {
		table["d"] = probe(t, tx, `delete from `+name+` where false`)
		// 0A000: a table referenced by a foreign key; the grant check
		// passed before that refusal
		table["D"] = probe(t, tx, `truncate `+name, "0A000")
	}
	return render(rel, cols, table)
}

// render writes rights in the notation of rolematrix.Want: a letter alone
// when every probed column has it, letter(columns) otherwise.
func render(rel relation, cols map[string][]string, table map[string]bool) string {
	probed := map[string]int{"r": len(rel.cols)}
	for _, c := range rel.cols {
		if rel.kind != "v" && !c.fixed {
			probed["a"]++
			probed["w"]++
		}
	}
	var whole strings.Builder
	var partial []string
	for _, r := range rolematrix.Rights {
		l := r.Letter
		switch l {
		case "r", "a", "w":
			got := cols[l]
			if len(got) == 0 {
				continue
			}
			if len(got) == probed[l] {
				whole.WriteString(l)
				continue
			}
			sort.Strings(got)
			partial = append(partial, l+"("+strings.Join(got, ",")+")")
		case "d", "D":
			if table[l] {
				whole.WriteString(l)
			}
		}
	}
	return strings.TrimSpace(whole.String() + " " + strings.Join(partial, " "))
}

// expected is the matrix entry rewritten to what the probes can see:
// references and trigger are not probed (the catalog test covers them), and
// a column grant that covers every probed column reads as the whole table.
func expected(t *testing.T, rel relation, entry string) string {
	t.Helper()
	g, err := rolematrix.Parse(entry)
	require.NoError(t, err, "%s: %q", rel.name, entry)
	cols := map[string][]string{}
	table := map[string]bool{}
	for l, gr := range g {
		switch l {
		case "r", "a", "w":
			for _, c := range rel.cols {
				if l != "r" && (rel.kind == "v" || c.fixed) {
					continue
				}
				if gr.All || contains(gr.Columns, c.name) {
					cols[l] = append(cols[l], c.name)
				}
			}
		case "d", "D":
			table[l] = true
		}
	}
	return render(rel, cols, table)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestRoleMatrix(t *testing.T) {
	e := start(t)
	rels := relations(t, e.superuser(t))

	// the matrix must name exactly the relations the database has, so a new
	// table without stated rights turns the gate red
	var names, want []string
	for _, r := range rels {
		names = append(names, r.name)
	}
	for n := range rolematrix.Want {
		want = append(want, n)
	}
	sort.Strings(want)
	require.Equal(t, want, names, "relations in cp, api and agg versus tests/fixtures/rolematrix")

	got := map[string]map[pdb.Role]string{}
	exp := map[string]map[pdb.Role]string{}
	probes := 0
	for _, rel := range rels {
		got[rel.name] = map[pdb.Role]string{}
		exp[rel.name] = map[pdb.Role]string{}
		for _, role := range pdb.AppRoles {
			if s := observe(t, e, role, rel); s != "" {
				got[rel.name][role] = s
			}
			if s := expected(t, rel, rolematrix.Want[rel.name][role]); s != "" {
				exp[rel.name][role] = s
			}
			probes += 3*len(rel.cols) + 2
		}
	}
	require.Equal(t, exp, got)
	t.Logf("%d relations x %d roles, about %d statements: behaviour equals the matrix", len(rels), len(pdb.AppRoles), probes)
}

// tenantTable is one relation that holds tenant rows: key is the column
// row-level security filters on.
type tenantTable struct {
	name, key string
}

func tenantTables(t *testing.T, c *pgx.Conn) []tenantTable {
	t.Helper()
	rows, err := c.Query(context.Background(), `
		select n.nspname || '.' || c.relname,
		       case when n.nspname = 'cp' and c.relname = 'tenants' then 'id' else 'tenant_id' end
		from pg_class c join pg_namespace n on n.oid = c.relnamespace
		where n.nspname in ('cp', 'api', 'agg') and c.relkind in ('r', 'p') and not c.relispartition
		  and (exists (select 1 from pg_attribute a where a.attrelid = c.oid and a.attname = 'tenant_id' and not a.attisdropped)
		       or (n.nspname = 'cp' and c.relname = 'tenants'))
		order by 1`)
	require.NoError(t, err)
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (tenantTable, error) {
		var x tenantTable
		return x, r.Scan(&x.name, &x.key)
	})
	require.NoError(t, err)
	return out
}

type side struct{ tenant, user, site uuid.UUID }

// seed writes, as the superuser, one row of each tenant in every tenant
// table. the test then checks that every tenant table really holds both, so
// a table added later without a seed row turns the gate red instead of
// passing with nothing to leak.
func seed(t *testing.T, c *pgx.Conn) (side, side) {
	t.Helper()
	ctx := context.Background()
	q := systemq.New(c)
	var sides [2]side
	for i := range sides {
		u, err := q.UpsertUser(ctx, systemq.UpsertUserParams{GithubID: int64(9100 + i), GithubLogin: fmt.Sprintf("gate-user-%d", i)})
		require.NoError(t, err)
		tn, err := q.CreateTenant(ctx, systemq.CreateTenantParams{
			Slug: fmt.Sprintf("gate-tenant-%d", i), Name: fmt.Sprintf("Gate %d", i), CreatedBy: uuid.NullUUID{UUID: u.ID, Valid: true},
		})
		require.NoError(t, err)
		require.NoError(t, q.AddTenantMember(ctx, systemq.AddTenantMemberParams{TenantID: tn.ID, UserID: u.ID, Role: "owner"}))
		s := side{tenant: tn.ID, user: u.ID, site: uuid.New()}
		sides[i] = s
		counts := []int64{0, 1, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		hash := []byte(fmt.Sprintf("%032d", i))
		for _, st := range []struct {
			sql  string
			args []any
		}{
			{`insert into cp.invites (tenant_id, github_id, github_login, role, created_by, expires_at)
			  values ($1, $4, $2, 'member', $3, now() + interval '7 days')`, []any{s.tenant, fmt.Sprintf("gate-invitee-%d", i), s.user, 5000 + i}},
			{`insert into cp.sites (id, tenant_id, name, domain, origins, verify_token)
			  values ($1, $2, $3, $4, $5, $6)`, []any{s.site, s.tenant, fmt.Sprintf("gate site %d", i),
				fmt.Sprintf("gate%d.example.org", i), []string{fmt.Sprintf("https://gate%d.example.org", i)}, fmt.Sprintf("gate-verify-%d", i)}},
			{`insert into cp.otlp_tokens (tenant_id, name, prefix, hash, created_by)
			  values ($1, 'gate', $2, sha256($3::bytea), $4)`, []any{s.tenant, fmt.Sprintf("sa_otlp_g%d", i), []byte(fmt.Sprintf("gate-token-%d", i)), s.user}},
			{`insert into cp.erasure_requests (tenant_id, site_id, kind, requested_by, session_id)
			  values ($1, $2, 'session', $3, gen_random_uuid())`, []any{s.tenant, s.site, s.user}},
			{`insert into cp.web_ingest_hourly (site_id, tenant_id, hour, outcome, count)
			  values ($1, $2, date_trunc('hour', now()), 'accepted', 5)`, []any{s.site, s.tenant}},
			{`insert into cp.mcp_credentials (kind, scope, tenant_id, user_id, name, prefix, hash, resource, expires_at)
			  values ('key', 'tenant', $1, $2, 'gate', $3, sha256($4::bytea), 'https://app.example.org/mcp', now() + interval '1 day')`,
				[]any{s.tenant, s.user, fmt.Sprintf("sa_mcp_g%d", i), []byte(fmt.Sprintf("gate-mcp-%d", i))}},
			{`insert into cp.mcp_oauth_clients (id, client_name, redirect_uris) values ($1, 'gate', '{https://client.example.org/cb}')`,
				[]any{fmt.Sprintf("mcpc_%032d", i)}},
			{`insert into cp.mcp_oauth_codes (hash, client_id, user_id, scope, tenant_id, redirect_uri, code_challenge, resource, expires_at)
			  values (sha256($1::bytea), $2, $3, 'tenant', $4, 'https://client.example.org/cb', $5, 'https://app.example.org/mcp', now() + interval '1 minute')`,
				[]any{[]byte(fmt.Sprintf("gate-code-%d", i)), fmt.Sprintf("mcpc_%032d", i), s.user, s.tenant, strings.Repeat("a", 43)}},
			{`insert into cp.tenant_removals (tenant_id, requested_by, reason, keep_aggregates)
			  values ($1, $2, 'gate seed', false)`, []any{s.tenant, s.user}},
			{`insert into api.metrics_minute (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
			  values ($1, date_trunc('minute', now(), 'UTC'), 'svc', 'GET', '/a', 200, 'metric', $2, 11.5, 4)`, []any{s.tenant, counts}},
			{`insert into api.metrics_hourly (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
			  values ($1, date_trunc('hour', now(), 'UTC'), 'svc', 'GET', '/a', 200, 'metric', $2, 11.5, 4)`, []any{s.tenant, counts}},
			{`insert into api.metrics_daily (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
			  values ($1, date_trunc('day', now(), 'UTC'), 'svc', 'GET', '/a', 200, 'metric', $2, 11.5, 4)`, []any{s.tenant, counts}},
			{`insert into api.ingest_warnings (tenant_id, day, kind, count) values ($1, current_date, 'lossy_histogram', 1)`, []any{s.tenant}},
			{`insert into api.series_state (tenant_id, series_hash, start_unix_nano, last_unix_nano, last_count, last_sum, last_counts, last_seen_at)
			  values ($1, $2, 1, 2, 4, 11.5, $3, now())`, []any{s.tenant, hash, counts}},
			{`insert into api.resource_seen (tenant_id, resource_hash, start_unix_nano, first_seen_at, last_seen_at)
			  values ($1, $2, 1, now(), now())`, []any{s.tenant, hash}},
			{`insert into api.ingest_dedup (msg_id, tenant_id) values ($1, $2)`, []any{fmt.Sprintf("gate-%d", i), s.tenant}},
			{`insert into api.route_registry (tenant_id, day, service, route) values ($1, current_date, 'svc', '/a')`, []any{s.tenant}},
			{`insert into api.rollup_pending (tenant_id, hour) values ($1, date_trunc('hour', now(), 'UTC'))`, []any{s.tenant}},
			{`insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
			  values ($1, $2, current_date, 'primary', 5, 2, 3, 1, 40)`, []any{s.site, s.tenant}},
			{`insert into agg.web_daily_dim (site_id, tenant_id, day, band, dimension, key, value)
			  values ($1, $2, current_date, 'primary', 'country', 'DE', 2)`, []any{s.site, s.tenant}},
			{`insert into cp.chain_ids (tenant_id, strkey, kind, added_by) values ($1, $2, 'account', 'console')`,
				[]any{s.tenant, "G" + strings.Repeat("A", 54) + string(rune('A'+i))}},
			{`insert into agg.chain_daily (tenant_id, strkey, day, source, operations, transactions, counterparties)
			  values ($1, $2, current_date, 'horizon', 3, 2, 1)`, []any{s.tenant, "G" + strings.Repeat("A", 54) + string(rune('A'+i))}},
			{`insert into agg.chain_reads (tenant_id, strkey, covered_from, covered_to, last_attempt_at)
			  values ($1, $2, current_date, current_date, now())`, []any{s.tenant, "G" + strings.Repeat("A", 54) + string(rune('A'+i))}},
			{`insert into agg.web_integrity_daily (site_id, tenant_id, day, rule_version, grade, closed, events, quarantined,
			    proof_failures, net_access, net_organisation, net_hosting, net_anonymiser, net_unknown, networks, visitors,
			    pageviews, baseline_visitors, baseline_days, reasons)
			  values ($1, $2, current_date, 1, 'A', false, 60, 0, 0, 60, 0, 0, 0, 0, 3, 2, 5, null, 0, '[]')`, []any{s.site, s.tenant}},
			{`insert into cp.web_evidence_daily (site_id, tenant_id, day, class, count) values ($1, $2, current_date, 'access', 5)`,
				[]any{s.site, s.tenant}},
			{`insert into cp.web_networks_daily (site_id, tenant_id, day, network) values ($1, $2, current_date, '00000000000000aa')`,
				[]any{s.site, s.tenant}},
			{`insert into cp.web_signals_daily (site_id, tenant_id, day, signal, count) values ($1, $2, current_date, 'signal_views', 5)`,
				[]any{s.site, s.tenant}},
			{`insert into agg.web_coverage_daily (site_id, tenant_id, day, script_requests, gpc_requests) values ($1, $2, current_date, 200, 20)`,
				[]any{s.site, s.tenant}},
		} {
			_, err := c.Exec(ctx, st.sql, st.args...)
			require.NoError(t, err, st.sql)
		}
	}
	return sides[0], sides[1]
}

// asTenant runs fn as cp_tenant with app.tenant_id set exactly as
// db.WithTenant sets it, and app.user_id set to the tenant's owner as
// tenants.Scope.InTenant sets the session user (migration 00028); tenant nil
// leaves both unset. it rolls back.
func (e *env) asTenant(t *testing.T, tenant *uuid.UUID, fn func(tx pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	p, err := e.conn.Pool(pdb.RoleCPTenant)
	require.NoError(t, err)
	tx, err := p.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	if tenant != nil {
		_, err := tx.Exec(ctx, `select set_config('app.tenant_id', $1, true)`, tenant.String())
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `select set_config('app.user_id',
			(select user_id::text from cp.tenant_members where role = 'owner' order by created_at limit 1), true)`)
		require.NoError(t, err)
	}
	fn(tx)
}

// stmt runs sql in a savepoint and returns the rows it touched and its
// error, leaving tx usable.
func stmt(t *testing.T, tx pgx.Tx, sql string, args ...any) (int64, error) {
	t.Helper()
	ctx := context.Background()
	_, err := tx.Exec(ctx, "savepoint s")
	require.NoError(t, err)
	tag, err := tx.Exec(ctx, sql, args...)
	_, rerr := tx.Exec(ctx, "rollback to savepoint s")
	require.NoError(t, rerr)
	return tag.RowsAffected(), err
}

func scalar(t *testing.T, tx pgx.Tx, sql string, args ...any) (int64, error) {
	t.Helper()
	ctx := context.Background()
	_, err := tx.Exec(ctx, "savepoint s")
	require.NoError(t, err)
	var n int64
	err = tx.QueryRow(ctx, sql, args...).Scan(&n)
	_, rerr := tx.Exec(ctx, "rollback to savepoint s")
	require.NoError(t, rerr)
	return n, err
}

// zeroOrDenied: the statement either touched no row or was refused by
// postgresql's privilege or row-level security check.
func zeroOrDenied(t *testing.T, n int64, err error, what string) {
	t.Helper()
	if err != nil {
		require.Equal(t, denied, sqlState(err), "%s: %v", what, err)
		return
	}
	require.Zero(t, n, what)
}

func TestTenantCrossRows(t *testing.T) {
	e := start(t)
	su := e.superuser(t)
	a, b := seed(t, su)
	ctx := context.Background()

	// the catalog view of the same rule; the statements below are the proof
	v, err := pdb.CheckPolicyCoverage(ctx, su)
	require.NoError(t, err)
	require.Empty(t, v, "row-level security coverage")

	tables := tenantTables(t, su)
	require.NotEmpty(t, tables)
	checked := 0
	for _, pair := range [][2]side{{a, b}, {b, a}} {
		self, other := pair[0], pair[1]
		for _, tt := range tables {
			name := ident(tt.name)
			key := pgx.Identifier{tt.key}.Sanitize()
			var own, foreign int64
			require.NoError(t, su.QueryRow(ctx, `select count(*) filter (where `+key+` = $1), count(*) filter (where `+key+` = $2) from `+name,
				self.tenant, other.tenant).Scan(&own, &foreign))
			require.Positive(t, own, "%s: the seed must hold a row of each tenant (add one in seed)", tt.name)
			require.Positive(t, foreign, "%s: the seed must hold a row of each tenant (add one in seed)", tt.name)

			// the other tenant's row, as json, to replay it as an insert
			var otherRow string
			require.NoError(t, su.QueryRow(ctx, `select row_to_json(x)::text from `+name+` x where `+key+` = $1 limit 1`, other.tenant).Scan(&otherRow))

			grant, err := rolematrix.Parse(rolematrix.Want[tt.name][pdb.RoleCPTenant])
			require.NoError(t, err)
			e.asTenant(t, &self.tenant, func(tx pgx.Tx) {
				seen, err := scalar(t, tx, `select count(*) from `+name+` where `+key+` = $1`, other.tenant)
				zeroOrDenied(t, seen, err, tt.name+": read of the other tenant's rows")
				all, err := scalar(t, tx, `select count(*) from `+name)
				if err == nil {
					require.Equal(t, own, all, "%s: cp_tenant sees exactly its own rows", tt.name)
				} else {
					require.Equal(t, denied, sqlState(err), "%s: %v", tt.name, err)
					require.False(t, grant["r"].All, "%s: the matrix gives cp_tenant select, but it was refused", tt.name)
				}

				n, err := stmt(t, tx, `update `+name+` set `+key+` = `+key+` where `+key+` = $1`, other.tenant)
				zeroOrDenied(t, n, err, tt.name+": update of the other tenant's rows")
				n, err = stmt(t, tx, `delete from `+name+` where `+key+` = $1`, other.tenant)
				zeroOrDenied(t, n, err, tt.name+": delete of the other tenant's rows")
				_, err = stmt(t, tx, `update `+name+` set `+key+` = $1 where `+key+` = $2`, other.tenant, self.tenant)
				require.Error(t, err, "%s: moving an own row to the other tenant", tt.name)
				require.Equal(t, denied, sqlState(err), "%s: moving an own row: %v", tt.name, err)
				_, err = stmt(t, tx, `insert into `+name+` select * from json_populate_record(null::`+name+`, $1::json)`, otherRow)
				require.Error(t, err, "%s: inserting a row of the other tenant", tt.name)
				require.Equal(t, denied, sqlState(err), "%s: inserting a row of the other tenant: %v", tt.name, err)
				_, err = stmt(t, tx, `truncate `+name)
				require.Equal(t, denied, sqlState(err), "%s: truncate: %v", tt.name, err)
			})

			// without a tenant set nothing is visible
			e.asTenant(t, nil, func(tx pgx.Tx) {
				n, err := scalar(t, tx, `select count(*) from `+name)
				zeroOrDenied(t, n, err, tt.name+": read without app.tenant_id")
			})
			checked++
		}
	}

	// the replayed foreign insert above must be refused by row-level
	// security, not by an accident of the data: the same replay of an own
	// row passes the privilege and policy checks wherever cp_tenant may
	// insert whole rows (it then hits a unique key, or succeeds)
	for _, tt := range tables {
		grant, err := rolematrix.Parse(rolematrix.Want[tt.name][pdb.RoleCPTenant])
		require.NoError(t, err)
		if !grant["a"].All || !grant["r"].All {
			continue
		}
		name := ident(tt.name)
		var ownRow string
		require.NoError(t, su.QueryRow(ctx, `select row_to_json(x)::text from `+name+` x where `+pgx.Identifier{tt.key}.Sanitize()+` = $1 limit 1`, a.tenant).Scan(&ownRow))
		e.asTenant(t, &a.tenant, func(tx pgx.Tx) {
			_, err := stmt(t, tx, `insert into `+name+` select * from json_populate_record(null::`+name+`, $1::json)`, ownRow)
			require.NotEqual(t, denied, sqlState(err), "%s: replay of an own row must pass the policy: %v", tt.name, err)
		})
	}
	var tenants []string
	for _, tt := range tables {
		tenants = append(tenants, tt.name)
	}
	b2, _ := json.Marshal(tenants)
	t.Logf("cp_tenant cross-tenant checks on %d tenant tables, both directions (%d cases): %s", len(tables), checked, b2)
}
