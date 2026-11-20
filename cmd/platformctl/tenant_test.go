package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/tests/fixtures/consoleapi"
	"github.com/justmert/veristar/tests/fixtures/githuboauth"
)

const trackerReleases = "../../packages/tracker/releases"

// startTenantEnv serves the console api, with the snippet route, over a
// real postgresql, so the tests see what the commands wrote as the owner
// sees it after signing in
func startTenantEnv(t *testing.T) (*consoleapi.Env, *controlplane.Tracker) {
	t.Helper()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleCPOperator)
	require.NoError(t, err)
	d, err := pdb.Open(context.Background(), pcfg, "platformctl-tenant-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	tr, err := controlplane.LoadTracker(trackerReleases, "https://in.example.org")
	require.NoError(t, err)
	e := consoleapi.Start(t, consoleapi.Options{
		PG: pg, DB: d,
		Reconciler: tenants.ReconcileFunc(func(context.Context, uuid.UUID) error { return nil }),
		Extra: func(s *tenants.Scope) []controlplane.Routes {
			r, err := controlplane.NewSnippetRoutes(s, tr, "")
			require.NoError(t, err)
			return []controlplane.Routes{r}
		},
	})
	return e, tr
}

func mustCreateOpts(t *testing.T, args ...string) createOpts {
	t.Helper()
	o, err := parseCreate(args, config.FromMap(map[string]string{
		"TRACKER_RELEASE_DIR": trackerReleases, "INGEST_ORIGIN": "https://in.example.org",
	}))
	require.NoError(t, err)
	return o
}

func auditCount(t *testing.T, e *consoleapi.Env, action, target string) int {
	t.Helper()
	return e.Count(t, `select count(*) from cp.audit_log where actor = $1 and action = $2 and target = $3`,
		tenantActor, action, target)
}

func TestTenantCreate(t *testing.T) {
	e, tr := startTenantEnv(t)
	ownerGH := e.NewGitHubID()
	owner := e.SignIn(t, ownerGH, "scf-owner")

	t.Run("flags are checked as the console checks them", func(t *testing.T) {
		_, err := parseCreate([]string{"-owner-github-id", "5", "-name", "x", "-website", "http://x.org", "-stellar-usage", "y"}, config.FromMap(nil))
		require.ErrorContains(t, err, "-website is not https url")
		_, err = parseCreate([]string{"-owner-github-id", "5", "-name", "x", "-website", "https://x.org"}, config.FromMap(nil))
		require.ErrorContains(t, err, "-stellar-usage is required")
		_, err = parseCreate([]string{"-owner-github-id", "5", "-owner-github-login", "a", "-name", "x"}, config.FromMap(nil))
		require.ErrorContains(t, err, "exactly one of")
		_, err = parseCreate([]string{"-owner-github-id", "5", "-name", "x", "-website", "https://x.org", "-stellar-usage", "y",
			"-site-domain", "x.org", "-origin", "https://evil.org", "-award-date", "2017-01-01"}, config.FromMap(nil))
		require.ErrorContains(t, err, "-origin is origin outside domain")
		require.ErrorContains(t, err, "-award-date is invalid value")
		require.ErrorContains(t, err, "TRACKER_RELEASE_DIR")
	})

	t.Run("unknown owner is refused", func(t *testing.T) {
		ctx := context.Background()
		o := mustCreateOpts(t, "-owner-github-login", "nobody-here", "-name", "Nobody", "-website", "https://nobody.org", "-stellar-usage", "payments")
		_, err := createTenant(ctx, e.DB, o, nil)
		require.ErrorContains(t, err, "sign in to the console once")
		require.Zero(t, e.Count(t, `select count(*) from cp.tenants where slug = 'nobody'`))
	})

	t.Run("dry run writes nothing", func(t *testing.T) {
		ctx := context.Background()
		o := mustCreateOpts(t, "-owner-github-login", "SCF-Owner", "-name", "Dry Project", "-website", "https://dry.org",
			"-stellar-usage", "payments", "-site-domain", "dry.org", "-origin", "https://dry.org", "-otlp-token-name", "prod", "-dry-run")
		res, err := createTenant(ctx, e.DB, o, tr)
		require.NoError(t, err)
		var out bytes.Buffer
		require.NoError(t, res.print(&out))
		require.Contains(t, out.String(), "dry_run=true\n")
		require.Contains(t, out.String(), "slug=dry-project\n")
		require.NotContains(t, out.String(), "otlp_token=")
		require.NotContains(t, out.String(), "script_tag=")
		require.Zero(t, e.Count(t, `select count(*) from cp.tenants where slug = 'dry-project'`))
		require.Zero(t, e.Count(t, `select count(*) from cp.audit_log where actor = $1`, tenantActor))
	})

	var withSite createResult
	t.Run("with site, token and cohort", func(t *testing.T) {
		ctx := context.Background()
		o := mustCreateOpts(t, "-owner-github-id", itoa(ownerGH), "-name", "Anchor Pay", "-slug", "anchor-pay",
			"-website", "https://anchorpay.org", "-stellar-usage", "soroban payments",
			"-program", "Build Award", "-round", "SCF #30", "-category", "Applications", "-award-date", "2025-03-01",
			"-site-domain", "anchorpay.org", "-origin", "https://anchorpay.org", "-origin", "https://app.anchorpay.org",
			"-otlp-token-name", "production")
		res, err := createTenant(ctx, e.DB, o, tr)
		require.NoError(t, err)
		withSite = res
		require.Equal(t, "active", res.Status)
		require.Equal(t, "active", res.SiteStatus)

		// the tag is the one the console's snippet route answers
		r := owner.Call(t, http.MethodGet, "/api/sites/"+res.SiteID.String()+"/snippet", nil)
		require.Equal(t, http.StatusOK, r.Status, string(r.Body))
		require.Equal(t, r.JSON(t)["tag"], res.ScriptTag)
		avail, err := tr.Available()
		require.NoError(t, err)
		require.Equal(t, avail[0].Version, res.TrackerVer)

		var out bytes.Buffer
		require.NoError(t, res.print(&out))
		for _, want := range []string{"tenant_id=" + res.TenantID.String(), "site_id=" + res.SiteID.String(),
			"script_tag=" + res.ScriptTag, "otlp_token=" + res.OTLPToken, "cohort_proposal=true"} {
			require.Contains(t, out.String(), want+"\n")
		}

		hash := sha256.Sum256([]byte(res.OTLPToken))
		require.Equal(t, 1, e.Count(t, `select count(*) from cp.otlp_tokens where tenant_id = $1 and hash = $2 and status = 'active'`, res.TenantID, hash[:]))
		require.Equal(t, 1, e.Count(t, `select count(*) from cp.tenant_members m join cp.users u on u.id = m.user_id
			where m.tenant_id = $1 and m.role = 'owner' and u.github_id = $2`, res.TenantID, ownerGH))
		require.Equal(t, 1, e.Count(t, `select count(*) from cp.tenant_cohorts where tenant_id = $1 and proposed_program = 'Build Award'
			and proposed_round = 'SCF #30' and proposed_category = 'Applications' and proposed_award_date = '2025-03-01'
			and proposed_source = 'member' and proposed_by = $2`, res.TenantID, res.Owner.ID))
		require.Equal(t, 1, e.Count(t, `select count(*) from cp.tenants where id = $1 and accepted_terms_version is null`, res.TenantID),
			"the owner accepts the terms, not the operator")
		require.Equal(t, 1, auditCount(t, e, "tenant.create", "tenant:"+res.TenantID.String()))
		require.Equal(t, 1, auditCount(t, e, "site.create", "site:"+res.SiteID.String()))
		require.Equal(t, 1, e.Count(t, `select count(*) from cp.audit_log where actor = $1 and action = 'otlp_token.create'
			and detail ->> 'tenant_id' = $2`, tenantActor, res.TenantID.String()))
	})

	t.Run("owner reads but cannot change before accepting the terms", func(t *testing.T) {
		base := "/api/tenants/" + withSite.TenantID.String()
		r := owner.Call(t, http.MethodGet, base, nil)
		require.Equal(t, http.StatusOK, r.Status, string(r.Body))
		require.Nil(t, r.JSON(t)["accepted_terms_version"])
		require.Equal(t, "owner", r.JSON(t)["role"])
		r = owner.Call(t, http.MethodGet, base+"/sites", nil)
		require.Equal(t, http.StatusOK, r.Status, string(r.Body))

		r = owner.Call(t, http.MethodPatch, base, map[string]any{"description": "changed"})
		require.Equal(t, http.StatusForbidden, r.Status, string(r.Body))
		require.Equal(t, "terms_not_accepted", r.Code(t))
		r = owner.Call(t, http.MethodPost, base+"/sites", map[string]any{"name": "b", "domain": "b.anchorpay.org", "origins": []string{"https://b.anchorpay.org"}})
		require.Equal(t, http.StatusForbidden, r.Status, string(r.Body))
		require.Equal(t, "terms_not_accepted", r.Code(t))

		r = owner.Call(t, http.MethodPost, base+"/terms", map[string]any{"version": e.Terms})
		require.Equal(t, http.StatusOK, r.Status, string(r.Body))
		r = owner.Call(t, http.MethodPatch, base, map[string]any{"description": "changed"})
		require.Equal(t, http.StatusOK, r.Status, string(r.Body))
	})

	t.Run("without site or token", func(t *testing.T) {
		ctx := context.Background()
		o := mustCreateOpts(t, "-owner-github-login", "scf-owner", "-name", "Bare Project", "-website", "https://bare.org", "-stellar-usage", "wallets")
		res, err := createTenant(ctx, e.DB, o, nil)
		require.NoError(t, err)
		var out bytes.Buffer
		require.NoError(t, res.print(&out))
		require.NotContains(t, out.String(), "site_id=")
		require.NotContains(t, out.String(), "otlp_token")
		require.Contains(t, out.String(), "cohort_proposal=false\n")
		require.Zero(t, e.Count(t, `select count(*) from cp.sites where tenant_id = $1`, res.TenantID))
		require.Zero(t, e.Count(t, `select count(*) from cp.tenant_cohorts where tenant_id = $1`, res.TenantID))
	})

	t.Run("slug taken", func(t *testing.T) {
		ctx := context.Background()
		o := mustCreateOpts(t, "-owner-github-login", "scf-owner", "-name", "Anchor Pay", "-website", "https://anchorpay.org", "-stellar-usage", "x")
		_, err := createTenant(ctx, e.DB, o, nil)
		require.ErrorContains(t, err, "slug anchor-pay is taken")
	})

	t.Run("approval mode", func(t *testing.T) {
		ctx := context.Background()
		e.Superuser(t, func(c *pgx.Conn) {
			_, err := c.Exec(context.Background(), `update cp.settings set value = '"approval"' where key = 'enrollment_mode'`)
			require.NoError(t, err)
		})
		o := mustCreateOpts(t, "-owner-github-login", "scf-owner", "-name", "Waiting", "-website", "https://waiting.org", "-stellar-usage", "x",
			"-site-domain", "waiting.org", "-origin", "https://waiting.org")
		res, err := createTenant(ctx, e.DB, o, tr)
		require.NoError(t, err)
		require.Equal(t, "pending", res.Status)
		require.Equal(t, "pending", res.SiteStatus)

		o = mustCreateOpts(t, "-owner-github-login", "scf-owner", "-name", "Token While Pending", "-website", "https://twp.org", "-stellar-usage", "x",
			"-otlp-token-name", "p")
		_, err = createTenant(ctx, e.DB, o, nil)
		require.ErrorContains(t, err, "tokens can be created once it is active")

		o = mustCreateOpts(t, "-owner-github-login", "scf-owner", "-name", "Approved", "-website", "https://approved.org", "-stellar-usage", "x",
			"-otlp-token-name", "p", "-approve")
		res, err = createTenant(ctx, e.DB, o, nil)
		require.NoError(t, err)
		require.Equal(t, "active", res.Status)
		require.Equal(t, 1, auditCount(t, e, "tenant.approve", "tenant:"+res.TenantID.String()))
	})
}

func TestTenantInvite(t *testing.T) {
	e, _ := startTenantEnv(t)
	ctx := context.Background()
	owner := e.SignIn(t, e.NewGitHubID(), "invite-owner")
	tenant, _ := owner.Enroll(t, "Invite Project")
	known := e.NewGitHubID()
	e.SignIn(t, known, "known-person")
	stranger := e.NewGitHubID()
	e.OAuth.Register(githuboauth.User{ID: stranger, Login: "new-person"})

	_, err := parseInvite([]string{"-tenant", tenant.String(), "-github-login", "x", "-role", "admin"})
	require.ErrorContains(t, err, "-role must be member or owner")

	o, err := parseInvite([]string{"-slug", "invite-project", "-github-login", "new-person", "-dry-run"})
	require.NoError(t, err)
	res, err := inviteToTenant(ctx, e.DB, e.Lookup, o)
	require.NoError(t, err)
	require.Equal(t, "invite", res.Kind)
	require.Zero(t, e.Count(t, `select count(*) from cp.invites where tenant_id = $1`, tenant))

	o.dryRun = false
	res, err = inviteToTenant(ctx, e.DB, e.Lookup, o)
	require.NoError(t, err)
	require.Equal(t, "invite", res.Kind)
	require.Equal(t, 1, e.Count(t, `select count(*) from cp.invites where tenant_id = $1 and github_id = $2 and role = 'member'
		and expires_at > now() + interval '13 days'`, tenant, stranger))
	require.Equal(t, 1, auditCount(t, e, "invite.create", "invite:"+res.InviteID.String()))
	_, err = inviteToTenant(ctx, e.DB, e.Lookup, o)
	require.ErrorContains(t, err, "an open invite for new-person exists")

	// the invite appears in the owner's member list, as one sent from the console
	r := owner.Call(t, http.MethodGet, "/api/tenants/"+tenant.String()+"/members", nil)
	require.Equal(t, http.StatusOK, r.Status, string(r.Body))
	require.Contains(t, string(r.Body), `"github_login":"new-person"`)

	o, err = parseInvite([]string{"-tenant", tenant.String(), "-github-login", "known-person", "-role", "owner"})
	require.NoError(t, err)
	res, err = inviteToTenant(ctx, e.DB, e.Lookup, o)
	require.NoError(t, err)
	require.Equal(t, "member", res.Kind)
	require.Equal(t, 1, e.Count(t, `select count(*) from cp.tenant_members m join cp.users u on u.id = m.user_id
		where m.tenant_id = $1 and u.github_id = $2 and m.role = 'owner'`, tenant, known))
	_, err = inviteToTenant(ctx, e.DB, e.Lookup, o)
	require.ErrorContains(t, err, "already a member")

	o, err = parseInvite([]string{"-tenant", tenant.String(), "-github-login", "ghost-login"})
	require.NoError(t, err)
	_, err = inviteToTenant(ctx, e.DB, e.Lookup, o)
	require.ErrorContains(t, err, "no github user has the login ghost-login")
}

func TestTenantRemove(t *testing.T) {
	e, _ := startTenantEnv(t)
	ctx := context.Background()
	owner := e.SignIn(t, e.NewGitHubID(), "remove-owner")
	tenant, _ := owner.Enroll(t, "Test")
	site := owner.AddSite(t, tenant, "leftover.example.org")

	_, err := parseRemove([]string{"-slug", "test"})
	require.ErrorContains(t, err, "-reason is required")
	_, err = parseRemove([]string{"-slug", "test", "-tenant", tenant.String(), "-reason", "x"})
	require.ErrorContains(t, err, "exactly one of -tenant and -slug")

	o, err := parseRemove([]string{"-slug", "test", "-reason", "leftover test project", "-dry-run"})
	require.NoError(t, err)
	sum, err := removeTenant(ctx, e.DB, o)
	require.NoError(t, err)
	require.Equal(t, "removed", sum.Status)
	require.Equal(t, 1, e.Count(t, `select count(*) from cp.tenants where id = $1 and status = 'active'`, tenant))
	require.Zero(t, e.Count(t, `select count(*) from cp.tenant_removals where tenant_id = $1`, tenant))

	o.dryRun = false
	sum, err = removeTenant(ctx, e.DB, o)
	require.NoError(t, err)
	require.Equal(t, tenant, sum.ID)
	var out bytes.Buffer
	require.NoError(t, printRemoval(&out, sum, false))
	require.True(t, strings.HasPrefix(out.String(), "tenant_id="+tenant.String()+"\n"))
	require.Equal(t, 1, e.Count(t, `select count(*) from cp.tenants where id = $1 and status = 'removed' and status_reason = 'leftover test project'`, tenant))
	require.Equal(t, 1, e.Count(t, `select count(*) from cp.sites where id = $1 and status = 'removed'`, site))
	require.Equal(t, 1, e.Count(t, `select count(*) from cp.tenant_removals where tenant_id = $1 and requested_by is null
		and reason = 'leftover test project' and completed_at is null`, tenant))
	require.Equal(t, 1, auditCount(t, e, "tenant.remove", "tenant:"+tenant.String()))

	// the removal is the operator's: the owner now gets 404
	r := owner.Call(t, http.MethodGet, "/api/tenants/"+tenant.String(), nil)
	require.Equal(t, http.StatusNotFound, r.Status)

	_, err = removeTenant(ctx, e.DB, o)
	require.ErrorContains(t, err, "not allowed while the status is removed")

	internal, _ := owner.Enroll(t, "Probe Like")
	e.Superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `update cp.tenants set is_internal = true where id = $1`, internal)
		require.NoError(t, err)
	})
	_, err = removeTenant(ctx, e.DB, removeOpts{ref: tenantRef{id: internal}, reason: "x"})
	require.ErrorContains(t, err, "internal projects are refused")
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestTenantDemo(t *testing.T) {
	e, _ := startTenantEnv(t)
	ctx := context.Background()
	owner := e.SignIn(t, e.NewGitHubID(), "demo-owner")
	tenant, _ := owner.Enroll(t, "Sample Data")

	_, err := parseDemo([]string{"-slug", "sample-data"})
	require.ErrorContains(t, err, "exactly one of -on and -off")
	_, err = parseDemo([]string{"-slug", "sample-data", "-on", "-off"})
	require.ErrorContains(t, err, "exactly one of -on and -off")
	_, err = parseDemo([]string{"-on"})
	require.ErrorContains(t, err, "exactly one of -tenant and -slug")

	demoOf := func() int {
		return e.Count(t, `select count(*) from cp.tenants where id = $1 and demo`, tenant)
	}
	o, err := parseDemo([]string{"-slug", "sample-data", "-on", "-dry-run"})
	require.NoError(t, err)
	row, err := setDemo(ctx, e.DB, o)
	require.NoError(t, err)
	require.False(t, row.WasDemo)
	require.Zero(t, demoOf(), "a dry run changes nothing")
	require.Zero(t, auditCount(t, e, ActionTenantDemo, "tenant:"+tenant.String()))

	o.dryRun = false
	row, err = setDemo(ctx, e.DB, o)
	require.NoError(t, err)
	require.Equal(t, tenant, row.ID)
	require.Equal(t, 1, demoOf())
	require.Equal(t, 1, auditCount(t, e, ActionTenantDemo, "tenant:"+tenant.String()))
	var out bytes.Buffer
	require.NoError(t, printDemo(&out, row, o))
	require.Equal(t, "tenant_id="+tenant.String()+"\nslug=sample-data\nname=Sample Data\nstatus=active\ndemo=true\nwas_demo=false\n", out.String())

	off, err := parseDemo([]string{"-tenant", tenant.String(), "-off"})
	require.NoError(t, err)
	row, err = setDemo(ctx, e.DB, off)
	require.NoError(t, err)
	require.True(t, row.WasDemo)
	require.Zero(t, demoOf())
	require.Equal(t, 2, auditCount(t, e, ActionTenantDemo, "tenant:"+tenant.String()))

	// internal and removed projects never join the demo
	internal, _ := owner.Enroll(t, "Probe Like")
	e.Superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `update cp.tenants set is_internal = true where id = $1`, internal)
		require.NoError(t, err)
	})
	_, err = setDemo(ctx, e.DB, demoOpts{ref: tenantRef{id: internal}, on: true})
	require.ErrorContains(t, err, "internal projects are refused")
	e.Superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `update cp.tenants set status = 'removed' where id = $1`, tenant)
		require.NoError(t, err)
	})
	_, err = setDemo(ctx, e.DB, o)
	require.ErrorContains(t, err, "cannot be in the demo")
	require.Zero(t, demoOf())
}
