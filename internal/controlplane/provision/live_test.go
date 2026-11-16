package provision_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/provision"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/sso"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/jobs/umamirotate"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/umamictr"
)

// a desktop chrome user agent that umami's isbot check accepts
const ua = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

// clock is the time the reconciler, sso and the rotation see, so a test can
// move to the next utc day without waiting for it.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Set(t time.Time) { c.mu.Lock(); c.now = t; c.mu.Unlock() }

// env is one real postgres, nats and umami (with the web caddy in front),
// started once for every check below.
type env struct {
	d     *pdb.DB
	kv    jetstream.KeyValue
	um    *umamictr.Stack
	web   *umamictr.Web
	rec   *provision.Reconciler
	key   umami.PasswordKey
	clock *clock
	srv   *httptest.Server
	rot   *umamirotate.Rotator
	pg    *dbtest.Postgres
}

func start(t *testing.T) *env {
	t.Helper()
	ctx := t.Context()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, pcfg, "provision-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)

	srv := jobstest.NATS(t)
	_, js := jobstest.Connect(t, srv.URL)
	pub, err := registry.NewPublisher(ctx, js)
	require.NoError(t, err)
	kv, err := js.KeyValue(ctx, natsx.BucketRegistrySites)
	require.NoError(t, err)

	um := umamictr.Start(t)
	web := umamictr.StartWeb(t, um)

	raw := make([]byte, umami.PasswordKeyLen)
	_, err = rand.Read(raw)
	require.NoError(t, err)
	key, err := umami.NewPasswordKey(raw)
	require.NoError(t, err)

	c := &clock{now: time.Now().UTC()}
	log := slog.New(slog.DiscardHandler)
	e := &env{
		d: d, kv: kv, um: um, web: web, key: key, clock: c, pg: pg,
		rec: &provision.Reconciler{Admin: um.Admin, Registry: pub, Key: key, Tx: d.WithSystem, Now: c.Now, Log: log},
		rot: &umamirotate.Rotator{Admin: um.Admin, Key: key, Now: c.Now, Log: log},
	}

	// the sso route behind the real console session middleware. /test-sign-in
	// stands in for the github callback, which ends in the same
	// SignIn call.
	sm := session.New(d)
	h := &sso.Handler{
		Tx: d.WithSystem, Umami: um.Client, Key: key, WebOrigin: "https://web." + umamictr.WebDomain, Now: c.Now,
		UserID: func(r *http.Request) (uuid.UUID, bool) {
			u, ok := session.CurrentUser(r.Context())
			return u.ID, ok
		},
	}
	require.NoError(t, h.Check())
	mux := http.NewServeMux()
	mux.Handle(sso.Path, sm.RequireUser(h))
	mux.Handle("/test-sign-in", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		id, err := uuid.Parse(r.URL.Query().Get("user"))
		if err != nil {
			return httpx.NotFound()
		}
		if err := sm.SignIn(r.Context(), id); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))
	e.srv = httptest.NewServer(httpx.Base(log)(sm.Load(mux)))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) sys(t *testing.T, fn func(pgx.Tx) error) {
	t.Helper()
	require.NoError(t, e.d.WithSystem(t.Context(), fn))
}

var githubSeq int64 = 7_000_000

func (e *env) user(t *testing.T) uuid.UUID {
	t.Helper()
	githubSeq++
	var id uuid.UUID
	e.sys(t, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`insert into cp.users (github_id, github_login) values ($1, $2) returning id`,
			githubSeq, "user-"+uuid.NewString()[:8]).Scan(&id)
	})
	return id
}

func (e *env) tenant(t *testing.T, owner uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	e.sys(t, func(tx pgx.Tx) error {
		if err := tx.QueryRow(t.Context(),
			`insert into cp.tenants (slug, name, status, created_by) values ($1, 'Project', 'active', $2) returning id`,
			"p-"+uuid.NewString()[:8], owner).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'owner')`, id, owner)
		return err
	})
	return id
}

func (e *env) member(t *testing.T, tenant, user uuid.UUID) {
	t.Helper()
	e.sys(t, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'member')`, tenant, user)
		return err
	})
}

type site struct{ id, quarantine uuid.UUID }

func (e *env) site(t *testing.T, tenant uuid.UUID, domain string) site {
	t.Helper()
	s := site{id: uuid.Must(uuid.NewV7()), quarantine: uuid.Must(uuid.NewV7())}
	e.sys(t, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `
			insert into cp.sites (id, tenant_id, name, domain, origins, quarantine_website_id, status, verify_token)
			values ($1, $2, 'Marketing', $3, $4, $5, 'active', 'unused-in-this-test')`,
			s.id, tenant, domain, []string{"https://" + domain}, s.quarantine)
		return err
	})
	return s
}

func actions(c []provision.Change) []string {
	var out []string
	for _, x := range c {
		out = append(out, x.Action)
	}
	slices.Sort(out)
	return out
}

func (e *env) auditCount(t *testing.T) int {
	t.Helper()
	var n int
	e.sys(t, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `select count(*) from cp.audit_log`).Scan(&n)
	})
	return n
}

// signIn returns the session cookie of a console session for user.
func (e *env) signIn(t *testing.T, user uuid.UUID) string {
	t.Helper()
	resp, err := http.Get(e.srv.URL + "/test-sign-in?user=" + user.String())
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	for _, c := range resp.Cookies() {
		if c.Name == session.CookieName {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatal("no session cookie")
	return ""
}

var noRedirect = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// ssoClick is a click on "Open web dashboard" in the console. it returns
// the status and headers; the body of a redirect is not read.
func (e *env) ssoClick(t *testing.T, cookie string, query string, fetchSite string) (int, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, e.srv.URL+sso.Path+"?"+query, nil)
	require.NoError(t, err)
	req.Header.Set("Cookie", cookie)
	if fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	resp, err := noRedirect.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode, resp.Header
}

// ssoToken clicks through sso and returns the umami token of the redirect.
func (e *env) ssoToken(t *testing.T, cookie string, s site) umami.Token {
	t.Helper()
	status, hdr := e.ssoClick(t, cookie, "site="+s.id.String(), "same-origin")
	require.Equal(t, http.StatusFound, status, hdr.Get("Location"))
	require.Equal(t, "no-store", hdr.Get("Cache-Control"))
	require.Equal(t, "no-referrer", hdr.Get("Referrer-Policy"))
	loc, err := url.Parse(hdr.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "https", loc.Scheme)
	require.Equal(t, "web."+umamictr.WebDomain, loc.Host)
	require.Equal(t, "/sso", loc.Path)
	require.Equal(t, "/websites/"+s.id.String(), loc.Query().Get("url"))
	tok := loc.Query().Get("token")
	require.NotEmpty(t, tok)
	return umami.NewToken(tok)
}

func (e *env) ssoFailure(t *testing.T, cookie, query, fetchSite string) string {
	t.Helper()
	status, hdr := e.ssoClick(t, cookie, query, fetchSite)
	require.Equal(t, http.StatusSeeOther, status)
	loc, err := url.Parse(hdr.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/sso-error", loc.Path)
	return loc.Query().Get("code")
}

// stats reads a website's totals with a tenant user's token on the private network.
func (e *env) stats(t *testing.T, tok umami.Token, website uuid.UUID) (umami.Stats, error) {
	t.Helper()
	now := e.clock.Now()
	// the clock may run ahead of umami's; the range covers both
	return e.um.Client.Stats(t.Context(), tok, website.String(), umami.Range{
		Start: time.Now().Add(-2 * time.Hour), End: now.Add(72 * time.Hour),
	})
}

func TestProvisioningLive(t *testing.T) {
	e := start(t)
	ctx := t.Context()

	owner, other, outsider := e.user(t), e.user(t), e.user(t)
	tenant := e.tenant(t, owner)
	e.member(t, tenant, other)
	s := e.site(t, tenant, "example.org")
	var teamID string

	t.Run("enrollment state reconciles and a second run changes nothing", func(t *testing.T) {
		changes, err := e.rec.Changes(ctx, tenant)
		require.NoError(t, err)
		require.Equal(t, []string{
			provision.ActionMembershipAdd, provision.ActionMembershipAdd,
			provision.ActionRegistryPut, provision.ActionTeamCreate,
			provision.ActionUserCreate, provision.ActionUserCreate,
			provision.ActionWebsiteCreate, provision.ActionWebsiteCreate,
		}, actions(changes))

		// the stored team is the umami team named after the tenant
		var stored uuid.NullUUID
		e.sys(t, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `select umami_team_id from cp.tenants where id = $1`, tenant).Scan(&stored)
		})
		require.True(t, stored.Valid)
		teamID = stored.UUID.String()
		team, ok, err := e.um.Admin.GetTeam(ctx, teamID)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, provision.TeamName(tenant), team.Name)
		self, err := e.um.Admin.Self(ctx)
		require.NoError(t, err)
		roles := map[string]string{}
		for _, m := range team.Members {
			roles[m.UserID] = m.Role
		}
		require.Equal(t, map[string]string{
			self.ID:        umami.TeamRoleOwner,
			owner.String(): umami.TeamRoleViewOnly,
			other.String(): umami.TeamRoleViewOnly,
		}, roles)
		for _, u := range []uuid.UUID{owner, other} {
			got, ok, err := e.um.Admin.GetUser(ctx, u.String())
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, umami.RoleViewOnly, got.Role)
			// the reconciler named the new umami user after the account and
			// stored the name for sso (00034)
			require.Equal(t, provision.Username(u), got.Username)
			var stored *string
			e.sys(t, func(tx pgx.Tx) error {
				return tx.QueryRow(t.Context(), `select umami_username from cp.users where id = $1`, u).Scan(&stored)
			})
			require.NotNil(t, stored)
			require.Equal(t, provision.Username(u), *stored)
		}
		_, ok, err = e.um.Admin.GetUser(ctx, outsider.String())
		require.NoError(t, err)
		require.False(t, ok, "a user of no tenant gets no umami user")
		for _, id := range []uuid.UUID{s.id, s.quarantine} {
			w, ok, err := e.um.Admin.GetWebsite(ctx, id.String())
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, teamID, w.TeamID)
			require.Equal(t, "example.org", w.Domain)
		}
		entry, err := e.kv.Get(ctx, s.id.String())
		require.NoError(t, err)
		got, err := registry.DecodeSite(s.id.String(), entry.Value())
		require.NoError(t, err)
		require.Equal(t, registry.Site{
			ID: s.id.String(), TenantID: tenant.String(), Status: registry.StatusActive,
			Origins: []string{"https://example.org"}, QuarantineWebsiteID: s.quarantine.String(),
		}, got)

		audits := e.auditCount(t)
		require.Equal(t, len(changes), audits, "one audit row per change")
		again, err := e.rec.Changes(ctx, tenant)
		require.NoError(t, err)
		require.Empty(t, again)
		require.Equal(t, audits, e.auditCount(t))
		entry2, err := e.kv.Get(ctx, s.id.String())
		require.NoError(t, err)
		require.Equal(t, entry.Revision(), entry2.Revision(), "the registry entry was not rewritten")
		// all tenants: same result
		all, err := e.rec.ReconcileAll(ctx)
		require.NoError(t, err)
		require.Empty(t, all)
	})

	// the site setting of contract 4.3 reaches the gateway only through the
	// registry entry, so a change must rewrite it, and only it
	t.Run("utm collection reaches the registry entry", func(t *testing.T) {
		for _, on := range []bool{true, false} {
			e.sys(t, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `update cp.sites set collect_utm = $1 where id = $2`, on, s.id)
				return err
			})
			changes, err := e.rec.Changes(ctx, tenant)
			require.NoError(t, err)
			require.Equal(t, []string{provision.ActionRegistryPut}, actions(changes))
			entry, err := e.kv.Get(ctx, s.id.String())
			require.NoError(t, err)
			got, err := registry.DecodeSite(s.id.String(), entry.Value())
			require.NoError(t, err)
			require.Equal(t, on, got.CollectUTM)
			again, err := e.rec.Changes(ctx, tenant)
			require.NoError(t, err)
			require.Empty(t, again)
		}
	})

	// the integrity task's baseline reaches the gateway as the site's
	// scale, rounded to a power of two, so day-to-day noise rewrites nothing
	t.Run("the integrity baseline reaches the registry entry as the scale", func(t *testing.T) {
		c, err := e.pg.Superuser(ctx, "platform")
		require.NoError(t, err)
		defer func() { _ = c.Close(context.Background()) }()
		grade := func(day time.Time, baseline int64) {
			_, err := c.Exec(ctx, `insert into agg.web_integrity_daily (site_id, tenant_id, day, rule_version, grade, closed,
			    events, quarantined, proof_failures, net_access, net_organisation, net_hosting, net_anonymiser, net_unknown,
			    networks, visitors, pageviews, baseline_visitors, baseline_days, reasons)
			  values ($1, $2, $3, 1, 'A', true, 100, 0, 0, 100, 0, 0, 0, 0, 10, 50, 80, $4, 28, '[]')`,
				s.id, tenant, day, baseline)
			require.NoError(t, err)
		}
		today := time.Now().UTC().Truncate(24 * time.Hour)
		grade(today.AddDate(0, 0, -2), 100)
		grade(today.AddDate(0, 0, -1), 5000)
		changes, err := e.rec.Changes(ctx, tenant)
		require.NoError(t, err)
		require.Equal(t, []string{provision.ActionRegistryPut}, actions(changes))
		entry, err := e.kv.Get(ctx, s.id.String())
		require.NoError(t, err)
		got, err := registry.DecodeSite(s.id.String(), entry.Value())
		require.NoError(t, err)
		require.Equal(t, 4096, got.Scale, "the latest baseline, rounded down to a power of two")
		// a baseline within the same power of two changes nothing
		grade(today, 6000)
		again, err := e.rec.Changes(ctx, tenant)
		require.NoError(t, err)
		require.Empty(t, again)
	})

	t.Run("a website deleted directly in umami is repaired", func(t *testing.T) {
		require.NoError(t, e.um.Admin.DeleteWebsite(ctx, s.id.String()))
		_, ok, err := e.um.Admin.GetWebsite(ctx, s.id.String())
		require.NoError(t, err)
		require.False(t, ok)
		changes, err := e.rec.Changes(ctx, tenant)
		require.NoError(t, err)
		require.Equal(t, []provision.Change{{Action: provision.ActionWebsiteCreate, Target: "site:" + s.id.String()}}, changes)
		w, ok, err := e.um.Admin.GetWebsite(ctx, s.id.String())
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, teamID, w.TeamID)

		// a member removed from the team directly in umami comes back too
		require.NoError(t, e.um.Admin.RemoveTeamUser(ctx, teamID, other.String()))
		changes, err = e.rec.Changes(ctx, tenant)
		require.NoError(t, err)
		require.Equal(t, []string{provision.ActionMembershipAdd}, actions(changes))
	})

	// one pageview, so the dashboard has data
	_, err := e.um.Client.Send(ctx, umami.SendRequest{UserAgent: ua, Country: "DE", Event: umami.Event{
		Website: s.id.String(), Hostname: "example.org", URL: "/pricing", Title: "Pricing",
		Screen: "1920x1080", Language: "en-US", Timestamp: time.Now().Add(-time.Minute).Unix(), Salt: "visitor-1",
	}})
	require.NoError(t, err)

	ownerCookie := e.signIn(t, owner)
	var first umami.Token

	t.Run("sso lands on the site's dashboard with data", func(t *testing.T) {
		first = e.ssoToken(t, ownerCookie, s)

		// the browser follows the redirect to umami's /sso page through the
		// web caddy, as a navigation from the same site, then the dashboard
		// reads the website through the same caddy with the stored token
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.web.Base+"/sso?"+url.Values{
			"token": {first.Reveal()}, "url": {"/websites/" + s.id.String()},
		}.Encode(), nil)
		require.NoError(t, err)
		req.Host = e.web.Host
		req.Header.Set("Sec-Fetch-Site", "same-site")
		req.Header.Set("User-Agent", ua)
		resp, err := e.web.HTTP.Do(req)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, string(body), `<a href="https://db-ip.com"`, "the sso page carries the db-ip link")

		page, err := http.NewRequestWithContext(ctx, http.MethodGet, e.web.Base+"/websites/"+s.id.String(), nil)
		require.NoError(t, err)
		page.Host = e.web.Host
		page.Header.Set("User-Agent", ua)
		resp, err = e.web.HTTP.Do(page)
		require.NoError(t, err)
		body, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, 1, strings.Count(string(body), "IP Geolocation by DB-IP"))

		stats, err := http.NewRequestWithContext(ctx, http.MethodGet, e.web.Base+"/api/websites/"+s.id.String()+"/stats?"+url.Values{
			"startAt": {itoa(time.Now().Add(-time.Hour).UnixMilli())}, "endAt": {itoa(time.Now().UnixMilli())},
		}.Encode(), nil)
		require.NoError(t, err)
		stats.Host = e.web.Host
		stats.Header.Set("Authorization", "Bearer "+first.Reveal())
		resp, err = e.web.HTTP.Do(stats)
		require.NoError(t, err)
		body, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
		require.Contains(t, string(body), `"pageviews":1`)

		st, err := e.stats(t, first, s.id)
		require.NoError(t, err)
		require.Equal(t, int64(1), st.Pageviews)
		require.Equal(t, int64(1), st.Visitors)

		// the quarantine view goes to the quarantine website
		status, hdr := e.ssoClick(t, ownerCookie, "site="+s.id.String()+"&view=quarantine", "same-origin")
		require.Equal(t, http.StatusFound, status)
		loc, err := url.Parse(hdr.Get("Location"))
		require.NoError(t, err)
		require.Equal(t, "/websites/"+s.quarantine.String(), loc.Query().Get("url"))

		// refusals
		require.Equal(t, sso.CodeCrossSite, e.ssoFailure(t, ownerCookie, "site="+s.id.String(), "cross-site"))
		require.Equal(t, sso.CodeCrossSite, e.ssoFailure(t, ownerCookie, "site="+s.id.String(), "same-site"))
		require.Equal(t, sso.CodeNotFound, e.ssoFailure(t, ownerCookie, "site="+uuid.NewString(), "same-origin"))
		require.Equal(t, sso.CodeNotFound, e.ssoFailure(t, ownerCookie, "site=not-a-uuid", "same-origin"))
		outsiderCookie := e.signIn(t, outsider)
		require.Equal(t, sso.CodeNotFound, e.ssoFailure(t, outsiderCookie, "site="+s.id.String(), "none"))
		status, _ = e.ssoClick(t, "", "site="+s.id.String(), "same-origin")
		require.Equal(t, http.StatusUnauthorized, status)
	})

	// a site in review (contract 5.6) has no umami
	// website the user may open, so sso answers provisioning_pending
	// instead of sending the browser to an umami error page. the tenant is
	// active and its team and the membership exist, as for a reviewed site.
	t.Run("sso for a site in review answers provisioning_pending", func(t *testing.T) {
		ownerCookie := e.signIn(t, owner)
		setStatus := func(status string) {
			e.sys(t, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `update cp.sites set status = $2 where id = $1`, s.id, status)
				return err
			})
		}
		setStatus("pending")
		defer setStatus("active")
		for _, view := range []string{"", "&view=quarantine"} {
			require.Equal(t, sso.CodeProvisioningPending, e.ssoFailure(t, ownerCookie, "site="+s.id.String()+view, "same-origin"))
		}
		setStatus("active")
		status, _ := e.ssoClick(t, ownerCookie, "site="+s.id.String(), "same-origin")
		require.Equal(t, http.StatusFound, status)
	})

	t.Run("two tabs stay logged in after a second sso click", func(t *testing.T) {
		second := e.ssoToken(t, ownerCookie, s)
		for i, tok := range []umami.Token{first, second} {
			u, err := e.um.Client.Verify(ctx, tok)
			require.NoError(t, err, "tab %d", i+1)
			require.Equal(t, owner.String(), u.ID)
			_, err = e.stats(t, tok, s.id)
			require.NoError(t, err, "tab %d", i+1)
		}
	})

	t.Run("the rotation invalidates the previous day's token", func(t *testing.T) {
		day0 := e.clock.Now()
		res, err := e.rot.Rotate(ctx, e.d.WithSystem)
		require.NoError(t, err)
		require.False(t, res.Skipped)
		require.Equal(t, 2, res.Users, "both platform users, not the admin")
		// the users were made today with today's password, so the first
		// rotation of the day ended the tokens issued before it; sign in again
		_, err = e.um.Client.Verify(ctx, first)
		require.ErrorIs(t, err, umami.ErrUnauthorized)
		today := e.ssoToken(t, ownerCookie, s)
		_, err = e.um.Client.Verify(ctx, today)
		require.NoError(t, err)
		again, err := e.rot.Rotate(ctx, e.d.WithSystem)
		require.NoError(t, err)
		require.True(t, again.Skipped, "one rotation per day")
		_, err = e.um.Client.Verify(ctx, today)
		require.NoError(t, err, "a skipped rotation changes nothing")

		// next utc day, before the rotation ran: sso still works with the
		// previous day's password
		e.clock.Set(day0.Add(24 * time.Hour))
		beforeRotation := e.ssoToken(t, ownerCookie, s)
		_, err = e.um.Client.Verify(ctx, beforeRotation)
		require.NoError(t, err)

		res, err = e.rot.Rotate(ctx, e.d.WithSystem)
		require.NoError(t, err)
		require.False(t, res.Skipped)
		for name, tok := range map[string]umami.Token{"yesterday's": today, "issued before the rotation": beforeRotation} {
			_, err = e.um.Client.Verify(ctx, tok)
			require.ErrorIs(t, err, umami.ErrUnauthorized, name)
			_, err = e.stats(t, tok, s.id)
			require.ErrorIs(t, err, umami.ErrUnauthorized, name)
		}
		fresh := e.ssoToken(t, ownerCookie, s)
		st, err := e.stats(t, fresh, s.id)
		require.NoError(t, err)
		require.Equal(t, int64(1), st.Pageviews)

		var day int64
		var n int
		e.sys(t, func(tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `select (value #>> '{}')::bigint from cp.settings where key = 'umami_password_day'`).Scan(&day); err != nil {
				return err
			}
			return tx.QueryRow(ctx, `select count(*) from cp.audit_log where action = $1`, umamirotate.Action).Scan(&n)
		})
		require.Equal(t, umami.EpochDay(day0)+1, day)
		require.Equal(t, 2, n, "one audit row per rotation")

		// a rotation that stopped running is not papered over: two days
		// after the last rotation sso refuses
		e.clock.Set(day0.Add(3 * 24 * time.Hour))
		require.Equal(t, sso.CodeProvisioningPending, e.ssoFailure(t, ownerCookie, "site="+s.id.String(), "same-origin"))
		e.clock.Set(day0.Add(24 * time.Hour))
	})

	t.Run("a removed member's umami token stops working after reconcile", func(t *testing.T) {
		otherCookie := e.signIn(t, other)
		tok := e.ssoToken(t, otherCookie, s)
		_, err := e.stats(t, tok, s.id)
		require.NoError(t, err)

		e.sys(t, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `delete from cp.tenant_members where tenant_id = $1 and user_id = $2`, tenant, other)
			return err
		})
		changes, err := e.rec.Changes(ctx, tenant)
		require.NoError(t, err)
		require.Equal(t, []provision.Change{{Action: provision.ActionMembershipRemove, Target: "user:" + other.String()}}, changes)

		for _, id := range []uuid.UUID{s.id, s.quarantine} {
			_, err = e.stats(t, tok, id)
			require.ErrorIs(t, err, umami.ErrUnauthorized, "website %s", id)
		}
		require.Equal(t, sso.CodeNotFound, e.ssoFailure(t, otherCookie, "site="+s.id.String(), "same-origin"))
		// the owner keeps access
		_, err = e.stats(t, e.ssoToken(t, ownerCookie, s), s.id)
		require.NoError(t, err)
	})

	t.Run("site deletion removes both websites and the registry key", func(t *testing.T) {
		e.sys(t, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `update cp.sites set status = 'removed' where id = $1`, s.id)
			return err
		})
		changes, err := e.rec.Changes(ctx, tenant)
		require.NoError(t, err)
		require.Equal(t, []string{
			provision.ActionRegistryDelete, provision.ActionWebsiteDelete, provision.ActionWebsiteDelete,
		}, actions(changes))
		for _, id := range []uuid.UUID{s.id, s.quarantine} {
			_, ok, err := e.um.Admin.GetWebsite(ctx, id.String())
			require.NoError(t, err)
			require.False(t, ok)
		}
		_, err = e.kv.Get(ctx, s.id.String())
		require.True(t, errors.Is(err, jetstream.ErrKeyNotFound), "%v", err)
		again, err := e.rec.Changes(ctx, tenant)
		require.NoError(t, err)
		require.Empty(t, again, "deleting what is gone is not retried (umami answers 500 for unknown ids)")
		require.Equal(t, sso.CodeProvisioningPending, e.ssoFailure(t, ownerCookie, "site="+s.id.String(), "same-origin"))
	})
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
