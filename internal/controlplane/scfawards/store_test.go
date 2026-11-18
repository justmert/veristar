package scfawards

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/db"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
)

type fixture struct {
	pg *dbtest.Postgres
	d  *pdb.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
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
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPOperator, pdb.RoleCPTenant)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, pcfg, "scfawards-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	return &fixture{pg: pg, d: d}
}

func (f *fixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	c, err := f.pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	_, err = c.Exec(t.Context(), sql, args...)
	require.NoError(t, err)
}

func (f *fixture) query(t *testing.T, sql string, args ...any) string {
	t.Helper()
	c, err := f.pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	var s string
	require.NoError(t, c.QueryRow(t.Context(), sql, args...).Scan(&s))
	return s
}

// tenant enrolls a project with a site on domain (none when empty) and
// an enrollment website.
func (f *fixture) tenant(t *testing.T, slug, status, domain, website string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	answers, err := json.Marshal(map[string]any{"website": website, "stellar_usage": "payments", "stellar_ids": []string{}})
	require.NoError(t, err)
	f.exec(t, `insert into cp.tenants (id, slug, name, status, enrollment_answers) values ($1, $2, $2, $3, $4)`,
		id, slug, status, answers)
	if domain != "" {
		f.exec(t, `insert into cp.sites (id, tenant_id, name, domain, origins, verify_token, verified_at, verify_method)
			values ($1, $2, $3, $3, $4, 'x', now(), 'dns')`, uuid.Must(uuid.NewV7()), id, domain, []string{"https://" + domain})
	}
	return id
}

func (f *fixture) cohort(t *testing.T, id uuid.UUID) CohortRow {
	t.Helper()
	var out map[uuid.UUID]CohortRow
	require.NoError(t, f.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
		var err error
		out, err = LoadCohorts(t.Context(), tx, []uuid.UUID{id})
		return err
	}))
	return out[id]
}

func (f *fixture) audits(t *testing.T, action string) []map[string]any {
	t.Helper()
	raw := f.query(t, `select coalesce(json_agg(json_build_object('actor', actor, 'target', target, 'detail', detail) order by at, id), '[]')::text
		from cp.audit_log where action = $1`, action)
	var out []map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &out))
	return out
}

func TestImportMatchAndAutofill(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	chainID := account()

	// alpha: matched by its site, no cohort yet
	alpha := f.tenant(t, "alpha", "active", "alpha.example", "https://alpha.example")
	// beta: matched by its enrollment website, with a confirmed cohort that
	// differs from the award
	beta := f.tenant(t, "beta", "active", "", "https://www.Beta.example/")
	f.exec(t, `insert into cp.tenant_cohorts (tenant_id, program, round, category, award_date, confirmed_at)
		values ($1, 'Build Award', 'SCF #30', 'DeFi', '2025-01-01', now())`, beta)
	// gamma: matched by a verified on-chain id, with its member's own
	// proposal
	gamma := f.tenant(t, "gamma", "pending", "", "https://gamma-home.example")
	f.exec(t, `insert into cp.chain_ids (tenant_id, strkey, kind, added_by, verify_method, verified_at)
		values ($1, $2, 'account', 'console', 'sep53', now())`, gamma, chainID)
	f.exec(t, `insert into cp.tenant_cohorts (tenant_id, proposed_program, proposed_round, proposed_at, proposed_source)
		values ($1, 'Build Award', 'SCF #12', now(), 'member')`, gamma)
	// delta and epsilon both claim shared.example: ambiguous
	f.tenant(t, "delta", "active", "shared.example", "https://delta.example")
	f.tenant(t, "epsilon", "active", "", "https://shared.example")
	// a removed project never matches
	f.tenant(t, "gone", "removed", "gone.example", "https://gone.example")

	rA, rB, rC, rD, rE := good("Alpha"), good("Beta"), good("Gamma"), good("Shared"), good("Gone")
	rB.category, rB.round = "Financial Protocols", "SCF #41"
	rC.website, rC.traction = "https://gamma-product.example", "Treasury "+chainID+" holds the funds."
	rD.website, rE.website = "https://shared.example", "https://gone.example"
	bad := good("Broken")
	bad.amount = "a lot"
	data := encode(t, '\t', nil, rA, rB, rC, rD, rE, bad)

	rep, err := Import(ctx, f.d, data, ActorPlatformctl, now)
	require.NoError(t, err)
	require.Equal(t, 6, rep.Records)
	require.Equal(t, 5, rep.Inserted)
	require.Zero(t, rep.Updated+rep.Unchanged)
	require.Equal(t, []Rejection{{Record: 6, Line: rep.Rejected[0].Line, Field: "award_amount", Reason: ReasonInvalid}}, rep.Rejected)
	require.Equal(t, 3, rep.Autofill.Matched)
	require.Equal(t, 1, rep.Autofill.Ambiguous)
	require.Equal(t, 1, rep.Autofill.NotEnrolled)
	require.Equal(t, 1, rep.Autofill.Proposed)
	require.Equal(t, map[string]int{CohortProposed: 1, CohortConfirmedDiffers: 1, CohortMemberProposalDiffers: 1}, rep.Autofill.States)
	require.Equal(t, 3, rep.Autofill.Projects)

	// only organisation data reached the database
	stored := f.query(t, `select json_agg(x)::text from cp.scf_awards x`)
	for _, v := range []string{personName, "linkedin", "docsend", "borderless", "pilots", "Treasury", "Proposal of", "x.com"} {
		require.NotContains(t, stored, v)
	}
	require.Contains(t, stored, chainID)
	require.Equal(t, "gamma-product.example", f.query(t, `select website_host from cp.scf_awards where project = 'Gamma'`))

	// alpha got the award's proposal, marked as the import's
	c := f.cohort(t, alpha)
	require.Nil(t, c.Confirmed)
	require.Equal(t, SourceSCFImport, *c.ProposalSource)
	require.Equal(t, Cohort{Program: strp("Build Award"), Round: strp("SCF #45"), Category: strp("Developer Tooling"),
		AwardDate: strp("2026-09-01")}, *c.Proposal)
	// beta's confirmed cohort and gamma's own proposal are untouched
	require.Equal(t, "SCF #30", *f.cohort(t, beta).Confirmed.Round)
	require.Nil(t, f.cohort(t, beta).Proposal)
	require.Equal(t, "SCF #12", *f.cohort(t, gamma).Proposal.Round)
	require.Equal(t, SourceMember, *f.cohort(t, gamma).ProposalSource)

	props := f.audits(t, ActionCohortPropose)
	require.Len(t, props, 1)
	require.Equal(t, ActorPlatformctl, props[0]["actor"])
	require.Equal(t, "tenant:"+alpha.String(), props[0]["target"])
	detail := props[0]["detail"].(map[string]any)
	require.Equal(t, SourceSCFImport, detail["source"])
	require.Equal(t, "site_domain", detail["evidence"].([]any)[0].(map[string]any)["kind"])
	imports := f.audits(t, ActionImport)
	require.Len(t, imports, 1)
	require.EqualValues(t, 5, imports[0]["detail"].(map[string]any)["inserted"])

	// the same file again: nothing changes, no proposal, one more run
	rep, err = Import(ctx, f.d, data, ActorPlatformctl, now)
	require.NoError(t, err)
	require.Equal(t, 5, rep.Unchanged)
	require.Zero(t, rep.Inserted+rep.Updated+rep.Autofill.Proposed)
	require.Len(t, f.audits(t, ActionCohortPropose), 1)
	require.Len(t, f.audits(t, ActionImport), 2)
	require.Equal(t, "2", f.query(t, `select count(*)::text from cp.scf_award_imports`))
	require.Equal(t, "5", f.query(t, `select count(*)::text from cp.scf_awards`))

	// a later export changes alpha's award date: the pending proposal of
	// the import follows it
	rA.date = "9/15/2026"
	rep, err = Import(ctx, f.d, encode(t, '\t', nil, rA), ActorPlatformctl, now)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Updated)
	require.Equal(t, 1, rep.Autofill.Proposed)
	require.Equal(t, "2026-09-15", *f.cohort(t, alpha).Proposal.AwardDate)

	// the operator dismisses it: no import proposes the same award again
	f.exec(t, `update cp.tenant_cohorts set proposed_program = null, proposed_round = null, proposed_category = null,
		proposed_award_date = null, proposed_by = null, proposed_at = null, proposed_source = null where tenant_id = $1`, alpha)
	rep, err = Import(ctx, f.d, data, ActorPlatformctl, now)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Updated, "the date goes back")
	require.Zero(t, rep.Autofill.Proposed)
	require.Equal(t, 1, rep.Autofill.States[CohortDismissed])
	require.Nil(t, f.cohort(t, alpha).Proposal)

	// a new project enrolls with the host of the award nobody matched
	// (its first project was removed); its enrollment transaction writes
	// the proposal through the hook
	hooks, err := NewHooks(f.d, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	fresh := f.tenant(t, "fresh", "pending", "", "https://gone.example")
	require.NoError(t, f.d.WithSystem(ctx, func(tx pgx.Tx) error { return hooks.AfterEnroll(ctx, tx, fresh) }))
	require.Equal(t, "SCF #45", *f.cohort(t, fresh).Proposal.Round)
	last := f.audits(t, ActionCohortPropose)
	require.Equal(t, ActorMatch, last[len(last)-1]["actor"])

	// a site added later: the background hook writes the proposal
	late := f.tenant(t, "late", "active", "", "https://late-home.example")
	rL := good("Late")
	_, err = Import(ctx, f.d, encode(t, '\t', nil, rL), ActorPlatformctl, now)
	require.NoError(t, err)
	require.Nil(t, f.cohort(t, late).Proposal)
	site := uuid.Must(uuid.NewV7())
	f.exec(t, `insert into cp.sites (id, tenant_id, name, domain, origins, verify_token) values ($1, $2, 'late', 'late.example', '{https://late.example}', 'x')`, site, late)
	hooks.AfterSiteCreate(ctx, late, site, "late.example", "user:x")
	hooks.Wait()
	require.Equal(t, SourceSCFImport, *f.cohort(t, late).ProposalSource)
}

// a member cannot pass a proposal off as the import's, and the member's
// own proposal through tenants.ProposeCohort is marked member
func TestProposalSourceGuard(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	tenant := f.tenant(t, "own", "active", "", "https://own.example")
	user := uuid.Must(uuid.NewV7())
	f.exec(t, `insert into cp.users (id, github_id, github_login) values ($1, 5501, 'member-one')`, user)
	f.exec(t, `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'owner')`, tenant, user)
	asMember := func(fn func(tx pgx.Tx) error) error {
		return f.d.WithTenant(ctx, tenant, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `select set_config('app.user_id', $1, true)`, user.String()); err != nil {
				return err
			}
			return fn(tx)
		})
	}
	err := asMember(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into cp.tenant_cohorts (tenant_id, proposed_round, proposed_by, proposed_at, proposed_source, updated_at)
			values ($1, 'SCF #1', $2, now(), 'scf_import', now())`, tenant, user)
		return err
	})
	require.ErrorContains(t, err, "member's own")
	err = asMember(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into cp.tenant_cohorts (tenant_id, scf_award_id) values ($1, gen_random_uuid())`, tenant)
		return err
	})
	require.ErrorContains(t, err, "permission denied")
	round := "SCF #2"
	require.NoError(t, asMember(func(tx pgx.Tx) error {
		return tenants.ProposeCohort(ctx, tx, tenant, user, tenants.CohortValues{Round: &round})
	}))
	require.Equal(t, SourceMember, *f.cohort(t, tenant).ProposalSource)
}

// a project funded in two rounds under one website matches both awards
// and gets one proposal, from the most recent; a later export that adds a
// newer round moves the pending proposal to it
func TestMultiRoundProposal(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	id := f.tenant(t, "multi", "active", "multi.example", "https://multi.example")
	first, second := good("Multi"), good("Multi")
	first.round, first.date = "SCF #36", "3/1/2025"
	second.round, second.date = "SCF #41", "2/1/2026"
	rep, err := Import(ctx, f.d, encode(t, '\t', nil, first, second), ActorPlatformctl, now)
	require.NoError(t, err)
	require.Equal(t, 2, rep.Autofill.Matched)
	require.Equal(t, 1, rep.Autofill.Projects)
	require.Zero(t, rep.Autofill.Ambiguous)
	require.Equal(t, 1, rep.Autofill.Proposed)
	require.Equal(t, "SCF #41", *f.cohort(t, id).Proposal.Round)
	require.Len(t, f.audits(t, ActionCohortPropose), 1)

	third := good("Multi")
	third.round, third.date = "SCF #45", "9/1/2026"
	rep, err = Import(ctx, f.d, encode(t, '\t', nil, first, second, third), ActorPlatformctl, now)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Inserted)
	require.Equal(t, 1, rep.Autofill.Proposed)
	c := f.cohort(t, id)
	require.Equal(t, "SCF #45", *c.Proposal.Round)
	require.Equal(t, "2026-09-01", *c.Proposal.AwardDate)
	require.Equal(t, SourceSCFImport, *c.ProposalSource)
	props := f.audits(t, ActionCohortPropose)
	require.Len(t, props, 2)
	old := props[1]["detail"].(map[string]any)["proposal"].(map[string]any)["old"].(map[string]any)
	require.Equal(t, "SCF #41", old["round"])
}
