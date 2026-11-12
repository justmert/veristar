package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Violation is one table that breaks the row-level security rules.
type Violation struct {
	Table   string
	Problem string
}

func (v Violation) Error() string { return v.Table + ": " + v.Problem }

// Beginner starts a transaction; *pgx.Conn and *pgxpool.Pool both do.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// coverageTables lists every table in cp, api and agg that holds tenant
// rows: those with a tenant_id column, and cp.tenants itself, keyed on id.
// partitions are listed on their own, because a query that names a
// partition directly does not see the parent's policies.
const coverageTables = `
select n.nspname, c.relname, c.oid, c.relrowsecurity, c.relforcerowsecurity,
       case when n.nspname = 'cp' and c.relname = 'tenants' then 'id' else 'tenant_id' end
from pg_class c
join pg_namespace n on n.oid = c.relnamespace
where n.nspname in ('cp', 'api', 'agg')
  and c.relkind in ('r', 'p')
  and (exists (select 1 from pg_attribute a
               where a.attrelid = c.oid and a.attname = 'tenant_id' and a.attnum > 0 and not a.attisdropped)
       or (n.nspname = 'cp' and c.relname = 'tenants'))
order by 1, 2`

// coveragePolicies lists the policies of one table. role oid 0 is public.
const coveragePolicies = `
select p.polname, p.polcmd::text, p.polpermissive,
       0 = any(p.polroles),
       coalesce((select r.oid from pg_roles r where r.rolname = 'cp_tenant') = any(p.polroles), false),
       coalesce(pg_get_expr(p.polqual, p.polrelid), ''),
       coalesce(pg_get_expr(p.polwithcheck, p.polrelid), '')
from pg_policy p
where p.polrelid = $1
order by p.polname`

type coverageTable struct {
	schema, name    string
	oid             uint32
	enabled, forced bool
	keyColumn       string
}

// CheckPolicyCoverage returns every tenant table that lacks forced row-level
// security or the single cp_tenant policy keyed on cp.current_tenant_id(),
// and every such table with a policy granted to public. an empty result
// means the database follows the rules in db/migrations/00003.
func CheckPolicyCoverage(ctx context.Context, b Beginner) ([]Violation, error) {
	tx, err := b.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: coverage check: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// pg_get_expr qualifies names by the search path; a fixed path makes the
	// expected policy text exact
	if _, err := tx.Exec(ctx, `set local search_path = pg_catalog`); err != nil {
		return nil, fmt.Errorf("db: coverage check: %w", err)
	}

	rows, err := tx.Query(ctx, coverageTables)
	if err != nil {
		return nil, fmt.Errorf("db: coverage check: %w", err)
	}
	tables, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (coverageTable, error) {
		var t coverageTable
		err := r.Scan(&t.schema, &t.name, &t.oid, &t.enabled, &t.forced, &t.keyColumn)
		return t, err
	})
	if err != nil {
		return nil, fmt.Errorf("db: coverage check: %w", err)
	}

	var out []Violation
	for _, t := range tables {
		vs, err := checkTable(ctx, tx, t)
		if err != nil {
			return nil, err
		}
		out = append(out, vs...)
	}
	return out, nil
}

func checkTable(ctx context.Context, tx pgx.Tx, t coverageTable) ([]Violation, error) {
	table := t.schema + "." + t.name
	var out []Violation
	add := func(format string, args ...any) {
		out = append(out, Violation{Table: table, Problem: fmt.Sprintf(format, args...)})
	}
	if !t.enabled {
		add("row-level security is not enabled")
	}
	if !t.forced {
		add("row-level security is not forced")
	}

	rows, err := tx.Query(ctx, coveragePolicies, t.oid)
	if err != nil {
		return nil, fmt.Errorf("db: coverage check %s: %w", table, err)
	}
	want := "(" + t.keyColumn + " = cp.current_tenant_id())"
	tenantPolicies := 0
	for rows.Next() {
		var name, cmd, qual, check string
		var permissive, public, forTenant bool
		if err := rows.Scan(&name, &cmd, &permissive, &public, &forTenant, &qual, &check); err != nil {
			rows.Close()
			return nil, fmt.Errorf("db: coverage check %s: %w", table, err)
		}
		if public {
			add("policy %s applies to public", name)
		}
		if !forTenant {
			continue
		}
		tenantPolicies++
		// one policy for all commands, so no command is left unfiltered
		if cmd != "*" || !permissive {
			add("policy %s for cp_tenant must be a permissive policy for all commands", name)
		}
		if qual != want || check != want {
			add("policy %s for cp_tenant must use and check %s, has using %q and check %q", name, want, qual, check)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: coverage check %s: %w", table, err)
	}
	if tenantPolicies != 1 {
		add("has %d policies for cp_tenant, want exactly 1", tenantPolicies)
	}
	return out, nil
}
