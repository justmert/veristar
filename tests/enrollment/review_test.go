//go:build enrollmentit

package enrollmentit

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/sites"
	"github.com/justmert/veristar/internal/controlplane/tenants"
)

func (c *client) site(t *testing.T, tenant, site uuid.UUID) map[string]any {
	t.Helper()
	r := c.call(t, http.MethodGet, "/api/tenants/"+tenant.String()+"/sites/"+site.String(), nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	return r.json(t)
}

func (c *client) createSite(t *testing.T, tenant uuid.UUID, domain string) map[string]any {
	t.Helper()
	r := c.call(t, http.MethodPost, "/api/tenants/"+tenant.String()+"/sites", map[string]any{
		"name": "site " + domain, "domain": domain, "origins": []string{"https://" + domain},
	})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	c.e.scope.Wait()
	return r.json(t)
}

// operatorSites reads the sites of a tenant the way an operator sees them.
func (c *client) operatorSites(t *testing.T, tenant uuid.UUID) map[string]map[string]any {
	t.Helper()
	r := c.call(t, http.MethodGet, "/api/operator/tenants/"+tenant.String(), nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	out := map[string]map[string]any{}
	for _, s := range r.json(t)["sites"].([]any) {
		m := s.(map[string]any)
		out[m["domain"].(string)] = m
	}
	return out
}

func (c *client) reviewQueue(t *testing.T) []string {
	t.Helper()
	r := c.call(t, http.MethodGet, "/api/operator/tenants?review=true&limit=200", nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	var ids []string
	for _, it := range r.json(t)["items"].([]any) {
		ids = append(ids, it.(map[string]any)["id"].(string))
	}
	return ids
}

// TestRemovedDomainReview is the re-enrollment rule of contract 5.6: a
// domain of a removed project, its subdomains and its parents always go to
// an operator, also in open mode and in an active tenant.
func TestRemovedDomainReview(t *testing.T) {
	e := start(t)
	ctx := t.Context()
	op := e.signIn(t, operatorGHID, "review-operator")
	e.stepUp(t, op)
	gone := e.newUser(t, "review-gone")
	again := e.newUser(t, "review-again")
	self := e.newUser(t, "review-self")

	// a project with two domains is removed by an operator
	goneTenant, status := gone.enroll(t, "Gone Project")
	require.Equal(t, "active", status)
	gone.addSite(t, goneTenant, "gone.example")
	gone.addSite(t, goneTenant, "app.parent.example")
	r := op.call(t, http.MethodPost, "/api/operator/tenants/"+goneTenant.String()+"/remove",
		map[string]any{"reason": "not a stellar project"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	e.scope.Wait()

	// the same people register those domains again in a new, active project
	tenant, status := again.enroll(t, "Back Again")
	require.Equal(t, "active", status)
	cases := []struct {
		domain, matched string
	}{
		{"gone.example", "gone.example"},         // equal
		{"shop.gone.example", "gone.example"},    // subdomain of a removed domain
		{"parent.example", "app.parent.example"}, // parent of a removed domain
	}
	ids := map[string]uuid.UUID{}
	for _, c := range cases {
		s := again.createSite(t, tenant, c.domain)
		require.Equal(t, "pending", s["status"], c.domain)
		require.Equal(t, true, s["in_review"], c.domain)
		ids[c.domain] = uuid.MustParse(s["id"].(string))
	}
	free := again.createSite(t, tenant, "unrelated.example")
	require.Equal(t, "active", free["status"])
	require.Equal(t, false, free["in_review"])

	// the audit says which removed domain and project matched
	rows := e.audit(t, sites.ActionReviewQueue)
	require.Len(t, rows, len(cases))
	for i, c := range cases {
		require.Equal(t, "site:"+ids[c.domain].String(), rows[i].target)
		require.Equal(t, c.matched, rows[i].detail["matched_domain"])
		require.Equal(t, goneTenant.String(), rows[i].detail["matched_tenant_id"])
		require.Equal(t, "user:"+again.userID.String(), rows[i].actor)
	}

	// the stored rule and the create-time check agree:
	// cp.operator_sites.domain_previously_removed marks the same sites
	opSites := op.operatorSites(t, tenant)
	for _, c := range cases {
		require.Equal(t, true, opSites[c.domain]["domain_previously_removed"], c.domain)
		require.Equal(t, true, opSites[c.domain]["in_review"], c.domain)
	}
	require.Equal(t, false, opSites["unrelated.example"]["domain_previously_removed"])
	e.superuser(t, func(c *pgx.Conn) {
		for _, c2 := range cases {
			var flagged bool
			require.NoError(t, c.QueryRow(ctx, `select domain_previously_removed from cp.operator_sites where id = $1`,
				ids[c2.domain]).Scan(&flagged))
			require.True(t, flagged, c2.domain)
		}
	})
	require.Contains(t, op.reviewQueue(t), tenant.String())

	// sites in review are neither published nor provisioned
	for _, id := range ids {
		_, err := e.sites.Get(ctx, id.String())
		require.ErrorIs(t, err, jetstream.ErrKeyNotFound)
		_, ok, err := e.umami.Admin.GetWebsite(ctx, id.String())
		require.NoError(t, err)
		require.False(t, ok)
	}
	_, err := e.sites.Get(ctx, free["id"].(string))
	require.NoError(t, err, "the unrelated site is live")

	// approving a site puts it live; rejecting removes it with a reason
	r = op.call(t, http.MethodPost, "/api/operator/sites/"+ids["gone.example"].String()+"/approve", map[string]any{})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	e.scope.Wait()
	require.Equal(t, "active", again.site(t, tenant, ids["gone.example"])["status"])
	_, err = e.sites.Get(ctx, ids["gone.example"].String())
	require.NoError(t, err)
	r = op.call(t, http.MethodPost, "/api/operator/sites/"+ids["parent.example"].String()+"/reject",
		map[string]any{"reason": "domain belonged to a removed project"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	e.scope.Wait()
	rejected := again.site(t, tenant, ids["parent.example"])
	require.Equal(t, "removed", rejected["status"])
	require.Equal(t, "domain belonged to a removed project", rejected["status_reason"])

	// a domain its own owner deleted (project not removed) never triggers review
	selfTenant, _ := self.enroll(t, "Self Project")
	first := self.createSite(t, selfTenant, "self.example")
	r = self.call(t, http.MethodDelete, "/api/tenants/"+selfTenant.String()+"/sites/"+first["id"].(string), nil)
	require.Equal(t, http.StatusAccepted, r.status, string(r.body))
	e.scope.Wait()
	second := self.createSite(t, selfTenant, "self.example")
	require.Equal(t, "active", second["status"])
	require.Len(t, e.audit(t, sites.ActionReviewQueue), len(cases))

	// non-operators cannot read the review queue
	r = again.call(t, http.MethodGet, "/api/operator/tenants?review=true", nil)
	require.Equal(t, http.StatusForbidden, r.status)
	require.Equal(t, "operator_required", r.code(t))

	// a former member learns why the project is gone
	r = gone.call(t, http.MethodGet, "/api/me", nil)
	require.Equal(t, http.StatusOK, r.status)
	removed := r.json(t)["removed_tenants"].([]any)
	require.Len(t, removed, 1)
	require.Equal(t, goneTenant.String(), removed[0].(map[string]any)["id"])
	require.Equal(t, "not a stellar project", removed[0].(map[string]any)["status_reason"])
}

// TestResubmitAfterRejection is contract 5.7: an owner edits the enrollment
// of a rejected project and it returns to pending for review.
func TestResubmitAfterRejection(t *testing.T) {
	e := start(t)
	op := e.signIn(t, operatorGHID, "resubmit-operator")
	e.stepUp(t, op)
	owner := e.newUser(t, "resubmit-owner")
	r := op.call(t, http.MethodPut, "/api/operator/settings/enrollment-mode", map[string]any{"mode": "approval"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))

	tenant, status := owner.enroll(t, "Rejected Project")
	require.Equal(t, "pending", status)
	path := "/api/tenants/" + tenant.String() + "/enrollment"
	body := map[string]any{
		"name": "Resubmitted Project",
		"enrollment": map[string]any{
			"website": "https://resubmitted.example", "stellar_usage": "an anchor on mainnet",
			"stellar_ids": []string{},
		},
	}

	// only a rejected project can be resubmitted
	r = owner.call(t, http.MethodPut, path, body)
	require.Equal(t, http.StatusConflict, r.status, string(r.body))
	require.Equal(t, "invalid_transition", r.code(t))

	r = op.call(t, http.MethodPost, "/api/operator/tenants/"+tenant.String()+"/reject",
		map[string]any{"reason": "tell us how you use stellar"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	e.scope.Wait()
	got := owner.tenant(t, tenant)
	require.Equal(t, "rejected", got["status"])
	require.Equal(t, "tell us how you use stellar", got["status_reason"])
	require.NotContains(t, op.reviewQueue(t), tenant.String())

	// invalid enrollment answers are refused like at enrollment
	r = owner.call(t, http.MethodPut, path, map[string]any{"enrollment": map[string]any{"website": "http://x"}})
	require.Equal(t, http.StatusUnprocessableEntity, r.status, string(r.body))
	fields := r.json(t)["error"].(map[string]any)["fields"].(map[string]any)
	require.Equal(t, "not_https_url", fields["enrollment.website"])
	require.Equal(t, "required", fields["enrollment.stellar_usage"])

	// csrf rules apply
	raw := &client{e: e, hc: owner.hc}
	req, err := http.NewRequest(http.MethodPut, e.srv.URL+path, nil)
	require.NoError(t, err)
	res, err := raw.hc.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)

	r = owner.call(t, http.MethodPut, path, body)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	m := r.json(t)
	require.Equal(t, "pending", m["status"])
	require.Nil(t, m["status_reason"])
	require.Equal(t, "Resubmitted Project", m["name"])
	require.Equal(t, "https://resubmitted.example", m["enrollment_answers"].(map[string]any)["website"])
	require.Equal(t, "an anchor on mainnet", m["enrollment_answers"].(map[string]any)["stellar_usage"])
	require.Contains(t, op.reviewQueue(t), tenant.String())

	rows := e.audit(t, tenants.ActionResubmit)
	require.Len(t, rows, 1)
	require.Equal(t, "user:"+owner.userID.String(), rows[0].actor)
	require.Equal(t, "tenant:"+tenant.String(), rows[0].target)
	require.Equal(t, map[string]any{"old": "rejected", "new": "pending"}, rows[0].detail["status"])
	require.Equal(t, map[string]any{"old": "Rejected Project", "new": "Resubmitted Project"}, rows[0].detail["name"])
	require.Contains(t, rows[0].detail, "enrollment")

	// a second resubmit while pending is a 409, and writes nothing
	r = owner.call(t, http.MethodPut, path, body)
	require.Equal(t, http.StatusConflict, r.status)
	require.Len(t, e.audit(t, tenants.ActionResubmit), 1)

	// nothing was provisioned while rejected or pending
	e.nothingProvisioned(t, tenant)

	// the operator approves the resubmitted project
	r = op.call(t, http.MethodPost, "/api/operator/tenants/"+tenant.String()+"/approve", map[string]any{})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	e.scope.Wait()
	require.Equal(t, "active", owner.tenant(t, tenant)["status"])

	// a suspended project keeps its reads, but site create and update are 409
	// (contract 5.11); deleting a site stays allowed
	live := owner.createSite(t, tenant, "resubmitted.example")
	r = op.call(t, http.MethodPost, "/api/operator/tenants/"+tenant.String()+"/suspend", map[string]any{"reason": "abuse report"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	e.scope.Wait()
	r = owner.call(t, http.MethodPost, "/api/tenants/"+tenant.String()+"/sites", map[string]any{
		"name": "x", "domain": "more.example", "origins": []string{"https://more.example"},
	})
	require.Equal(t, http.StatusConflict, r.status, string(r.body))
	require.Equal(t, "tenant_suspended", r.code(t))
	sitePath := "/api/tenants/" + tenant.String() + "/sites/" + live["id"].(string)
	r = owner.call(t, http.MethodPatch, sitePath, map[string]any{"name": "renamed"})
	require.Equal(t, http.StatusConflict, r.status, string(r.body))
	require.Equal(t, "tenant_suspended", r.code(t))
	require.Equal(t, http.StatusOK, owner.call(t, http.MethodGet, sitePath, nil).status)
	r = owner.call(t, http.MethodDelete, sitePath, nil)
	require.Equal(t, http.StatusAccepted, r.status, string(r.body))
	e.scope.Wait()

	// other users' resubmits answer 404, like every tenant route of another project
	stranger := e.newUser(t, "resubmit-stranger")
	r = stranger.call(t, http.MethodPut, path, body)
	require.Equal(t, http.StatusNotFound, r.status)
	require.True(t, slices.Contains([]string{"not_found"}, r.code(t)))
}
