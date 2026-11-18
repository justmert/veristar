package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/db"
	pdb "github.com/justmert/veristar/internal/db"
)

// the down step of 00050 drops the award directory and the two cohort
// columns with rows in them, keeps the proposal itself, and the database
// migrates up again with the proposal marked as the member's
func TestSCFAwardsDownUp(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pg.CreateDatabase(ctx, "scfdown"))
	cfg, err := pdb.LoadAdminConfig(pg.Config("scfdown"))
	require.NoError(t, err)
	_, err = pdb.Migrate(ctx, cfg, db.Migrations(), quietLog())
	require.NoError(t, err)
	c, err := pg.Superuser(ctx, "scfdown")
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := c.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	scalar := func(sql string) string {
		t.Helper()
		var s string
		require.NoError(t, c.QueryRow(ctx, sql).Scan(&s))
		return s
	}
	exec(`insert into cp.tenants (id, slug, name) values ('00000000-0000-7000-8000-000000000001', 'alpha', 'Alpha')`)
	exec(`insert into cp.scf_award_imports (id, file_sha256, file_bytes, has_header, delimiter, records, inserted, updated,
	        unchanged, rejected, actor)
	      values ('00000000-0000-7000-8000-0000000000a1', sha256('x'), 1, false, 'tab', 1, 1, 0, 0, 0, 'system:test')`)
	exec(`insert into cp.scf_awards (id, project, round, category, program, award_amount, website_host, strkeys, import_id)
	      values ('00000000-0000-7000-8000-0000000000b1', 'Alpha', 'SCF #45', 'Applications', 'Build Award', 1000,
	              'alpha.example', '{}', '00000000-0000-7000-8000-0000000000a1')`)
	exec(`insert into cp.tenant_cohorts (tenant_id, proposed_round, proposed_at, proposed_source, scf_award_id)
	      values ('00000000-0000-7000-8000-000000000001', 'SCF #45', now(), 'scf_import', '00000000-0000-7000-8000-0000000000b1')`)

	// the checks of the new columns and tables hold
	_, err = c.Exec(ctx, `insert into cp.scf_awards (project, round, category, program, award_amount, import_id)
	      values ('Bad', 'Round 45', 'Applications', 'Build Award', 1, '00000000-0000-7000-8000-0000000000a1')`)
	require.ErrorContains(t, err, "scf_awards_round_check")
	_, err = c.Exec(ctx, `insert into cp.scf_awards (project, round, category, program, award_amount, strkeys, import_id)
	      values ('Bad', 'SCF #1', 'Applications', 'Build Award', 1, '{GSHORT}', '00000000-0000-7000-8000-0000000000a1')`)
	require.ErrorContains(t, err, "scf_awards_strkeys_check")
	_, err = c.Exec(ctx, `update cp.tenant_cohorts set proposed_source = null`)
	require.ErrorContains(t, err, "tenant_cohorts_proposal_source_check")

	require.NoError(t, pdb.DownTo(ctx, cfg, db.Migrations(), 49))
	require.Equal(t, "false", scalar(`select (to_regclass('cp.scf_awards') is not null or to_regclass('cp.scf_award_imports') is not null)::text`))
	require.Equal(t, "0", scalar(`select count(*)::text from information_schema.columns
		where table_schema = 'cp' and table_name = 'tenant_cohorts' and column_name in ('proposed_source', 'scf_award_id')`))
	require.Equal(t, "SCF #45", scalar(`select proposed_round from cp.tenant_cohorts`))

	_, err = pdb.Migrate(ctx, cfg, db.Migrations(), quietLog())
	require.NoError(t, err)
	require.Equal(t, "member", scalar(`select proposed_source from cp.tenant_cohorts`))
	require.Equal(t, "0", scalar(`select count(*)::text from cp.scf_awards`))
}
