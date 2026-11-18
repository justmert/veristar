//go:build isolation

package consoleisolation

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/db/tenantq"
)

// one set of containers for every scenario; each scenario uses its own
// users and tenants, so they do not depend on each other's state
func TestConsole(t *testing.T) {
	e := start(t)
	t.Run("Unauthenticated", func(t *testing.T) { testUnauthenticated(t, e) })
	t.Run("Enrollment", func(t *testing.T) { testEnrollment(t, e) })
	t.Run("TenantListAndPatch", func(t *testing.T) { testTenantListAndPatch(t, e) })
	t.Run("MembersAndInvites", func(t *testing.T) { testMembersAndInvites(t, e) })
	t.Run("Sites", func(t *testing.T) { testSites(t, e) })
	t.Run("OriginValidation", func(t *testing.T) { testOriginValidation(t, e) })
	t.Run("Snippet", func(t *testing.T) { testSnippet(t, e) })
	t.Run("Ingest", func(t *testing.T) { testIngest(t, e) })
	t.Run("Terms", func(t *testing.T) { testTerms(t, e) })
	t.Run("Isolation", func(t *testing.T) { testIsolation(t, e) })
	t.Run("RemovedMemberLosesAccess", func(t *testing.T) { testRemovedMember(t, e) })
	t.Run("WrongTenantSeesNothing", func(t *testing.T) { testWrongTenant(t, e) })
}

func testUnauthenticated(t *testing.T, e *env) {
	c := e.anonymous(t)
	id := uuid.NewString()
	for _, p := range []string{"/api/tenants", "/api/tenants/" + id, "/api/tenants/" + id + "/members",
		"/api/tenants/" + id + "/sites", "/api/sites/" + id + "/snippet", "/api/sites/" + id + "/ingest"} {
		r := c.call(t, http.MethodGet, p, nil)
		require.Equal(t, http.StatusUnauthorized, r.status, p)
		require.Equal(t, "unauthenticated", r.code(t), p)
	}
	r := c.call(t, http.MethodDelete, "/api/me", nil)
	require.Equal(t, http.StatusUnauthorized, r.status)
	require.Equal(t, "unauthenticated", r.code(t))
}

// strkeyOf encodes 32 random bytes as a strkey of the given type.
func strkeyOf(t *testing.T, vb strkey.VersionByte) string {
	t.Helper()
	b := make([]byte, 32)
	_, err := rand.Read(b)
	require.NoError(t, err)
	s, err := strkey.Encode(vb, b)
	require.NoError(t, err)
	return s
}

func testEnrollment(t *testing.T, e *env) {
	c := e.signIn(t, "enroller")
	account := strkeyOf(t, strkey.VersionByteAccountID)
	contract := strkeyOf(t, strkey.VersionByteContract)
	seed := strkeyOf(t, strkey.VersionByteSeed)
	body := map[string]any{
		"name":        "Stellar DEX!",
		"description": "an amm",
		"enrollment": map[string]any{
			"website":       "https://dex.example.org",
			"stellar_usage": "Soroban AMM on mainnet",
			"stellar_ids":   []string{account, contract},
			"scf_round_url": "https://communityfund.stellar.org/round/30",
		},
		"accepted_terms_version": e.terms,
	}
	r := c.call(t, http.MethodPost, "/api/tenants", body)
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	tn := r.json(t)
	id := tn["id"].(string)
	require.Equal(t, "/api/tenants/"+id, r.header.Get("Location"))
	require.Equal(t, "stellar-dex", tn["slug"])
	require.Equal(t, "Stellar DEX!", tn["name"])
	require.Equal(t, "active", tn["status"], "enrollment_mode open gives active")
	require.Equal(t, "owner", tn["role"])
	require.Equal(t, e.terms, tn["accepted_terms_version"])
	require.Nil(t, tn["status_reason"])
	ans := tn["enrollment_answers"].(map[string]any)
	require.Equal(t, "https://dex.example.org", ans["website"])
	require.Len(t, ans["stellar_ids"], 2)
	for _, k := range []string{"is_internal", "created_by", "approved_by", "umami_team_id"} {
		require.NotContains(t, tn, k)
	}
	require.Equal(t, 1, e.auditCount(t, "tenant.create", "tenant:"+id))

	// the real reconciler provisions the umami team after the answer
	require.Eventually(t, func() bool {
		g := c.call(t, http.MethodGet, "/api/tenants/"+id, nil)
		return g.status == http.StatusOK && g.json(t)["provisioning"] == "ready"
	}, 90*time.Second, 500*time.Millisecond, "tenant never became provisioned")

	// same derived slug again
	r = c.call(t, http.MethodPost, "/api/tenants", body)
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "slug_taken", r.code(t))

	// terms version that is not current
	bad := map[string]any{"name": "Other", "enrollment": body["enrollment"], "accepted_terms_version": "2020-01-01-draft.1"}
	r = c.call(t, http.MethodPost, "/api/tenants", bad)
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "terms_version_mismatch", r.code(t))

	// field rules
	r = c.call(t, http.MethodPost, "/api/tenants", map[string]any{
		"name": " ", "slug": "Bad_Slug", "description": strings.Repeat("x", 1001),
		"enrollment": map[string]any{
			"website": "http://plain.example.org", "stellar_usage": "",
			"stellar_ids":   []string{account, seed, account},
			"scf_round_url": "nope",
		},
	})
	require.Equal(t, http.StatusUnprocessableEntity, r.status, string(r.body))
	require.Equal(t, "validation_failed", r.code(t))
	require.Equal(t, map[string]any{
		"name":                      "required",
		"slug":                      "invalid_format",
		"description":               "too_long",
		"enrollment.website":        "not_https_url",
		"enrollment.stellar_usage":  "required",
		"enrollment.stellar_ids[1]": "invalid_strkey",
		"enrollment.stellar_ids[2]": "duplicate",
		"enrollment.scf_round_url":  "not_https_url",
		"accepted_terms_version":    "required",
	}, r.fields(t))

	// unknown fields and wrong types are 400, not ignored
	r = c.call(t, http.MethodPost, "/api/tenants", map[string]any{"name": "x", "title": "typo"})
	require.Equal(t, http.StatusBadRequest, r.status)
	require.Equal(t, "invalid_body", r.code(t))

	// a slug derived from a name with no ascii letters must be given
	r = c.call(t, http.MethodPost, "/api/tenants", map[string]any{"name": "日本", "enrollment": body["enrollment"], "accepted_terms_version": e.terms})
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	require.Equal(t, "required", r.fields(t)["slug"])

	// at most 10 new tenants per user per day
	limited := e.signIn(t, "busy-enroller")
	for i := range 10 {
		limited.enroll(t, fmt.Sprintf("Busy %d", i))
	}
	r = limited.call(t, http.MethodPost, "/api/tenants", map[string]any{"name": "Busy 10", "enrollment": body["enrollment"], "accepted_terms_version": e.terms})
	require.Equal(t, http.StatusTooManyRequests, r.status)
	require.Equal(t, "rate_limited", r.code(t))
	require.NotEmpty(t, r.header.Get("Retry-After"))
}

func testTenantListAndPatch(t *testing.T, e *env) {
	owner := e.signIn(t, "lister")
	ids := []uuid.UUID{owner.enroll(t, "List One"), owner.enroll(t, "List Two"), owner.enroll(t, "List Three")}

	var got []string
	cursor := ""
	for {
		p := "/api/tenants?limit=2"
		if cursor != "" {
			p += "&cursor=" + cursor
		}
		r := owner.call(t, http.MethodGet, p, nil)
		require.Equal(t, http.StatusOK, r.status, string(r.body))
		m := r.json(t)
		for _, it := range m["items"].([]any) {
			got = append(got, it.(map[string]any)["id"].(string))
		}
		nc, _ := m["next_cursor"].(string)
		if nc == "" {
			require.Nil(t, m["next_cursor"])
			break
		}
		cursor = nc
	}
	require.Equal(t, []string{ids[0].String(), ids[1].String(), ids[2].String()}, got)

	r := owner.call(t, http.MethodGet, "/api/tenants?cursor=bm90LWEtY3Vyc29y", nil)
	require.Equal(t, http.StatusBadRequest, r.status)
	require.Equal(t, "invalid_cursor", r.code(t))

	tp := "/api/tenants/" + ids[0].String()
	r = owner.call(t, http.MethodPatch, tp, map[string]any{"name": "  Renamed  ", "description": "new"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	require.Equal(t, "Renamed", r.json(t)["name"])
	require.Equal(t, "new", r.json(t)["description"])
	require.Equal(t, 1, e.auditCount(t, "tenant.update", "tenant:"+ids[0].String()))

	r = owner.call(t, http.MethodPatch, tp, map[string]any{"slug": "nope"})
	require.Equal(t, http.StatusBadRequest, r.status, "slug is not patchable")

	// a plain member may read but not patch
	member := e.signIn(t, "list-member")
	r = owner.call(t, http.MethodPost, tp+"/members", map[string]any{"github_login": "list-member", "role": "member"})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	require.Equal(t, http.StatusOK, member.call(t, http.MethodGet, tp, nil).status)
	r = member.call(t, http.MethodPatch, tp, map[string]any{"name": "x"})
	require.Equal(t, http.StatusForbidden, r.status)
	require.Equal(t, "owner_required", r.code(t))
}

func testMembersAndInvites(t *testing.T, e *env) {
	owner := e.signIn(t, "team-owner")
	known := e.signIn(t, "Known-Person")
	tenant := owner.enroll(t, "Team")
	mp := "/api/tenants/" + tenant.String() + "/members"

	// a login that signed in before becomes a member at once, matched
	// case-insensitively
	r := owner.call(t, http.MethodPost, mp, map[string]any{"github_login": "known-person", "role": "member"})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	m := r.json(t)
	require.Equal(t, "member", m["kind"])
	require.Equal(t, known.userID.String(), m["member"].(map[string]any)["user_id"])
	require.Equal(t, 1, e.auditCount(t, "member.add", "user:"+known.userID.String()))

	r = owner.call(t, http.MethodPost, mp, map[string]any{"github_login": "Known-Person", "role": "owner"})
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "already_member", r.code(t))

	// a login that holds a github account but never signed in gets an
	// invite, bound to that account's id
	futureID := e.githubAccount(t, "Future-Dev")
	r = owner.call(t, http.MethodPost, mp, map[string]any{"github_login": "Future-Dev", "role": "owner"})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	inv := r.json(t)
	require.Equal(t, "invite", inv["kind"])
	inviteID := inv["invite"].(map[string]any)["id"].(string)
	require.Equal(t, 1, e.auditCount(t, "invite.create", "invite:"+inviteID))
	r = owner.call(t, http.MethodPost, mp, map[string]any{"github_login": "future-dev", "role": "member"})
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "invite_exists", r.code(t))

	// a login no github account holds can be neither added nor invited
	r = owner.call(t, http.MethodPost, mp, map[string]any{"github_login": "nobody-on-github", "role": "member"})
	require.Equal(t, http.StatusUnprocessableEntity, r.status, string(r.body))
	require.Contains(t, string(r.body), "unknown_github_login")

	for _, bad := range []map[string]any{
		{"github_login": "-bad", "role": "member"},
		{"github_login": "a--b", "role": "member"},
		{"github_login": strings.Repeat("a", 40), "role": "member"},
		{"github_login": "ok", "role": "admin"},
		{"role": "member"},
	} {
		r = owner.call(t, http.MethodPost, mp, bad)
		require.Equal(t, http.StatusUnprocessableEntity, r.status, "%v", bad)
	}

	r = owner.call(t, http.MethodGet, mp, nil)
	require.Equal(t, http.StatusOK, r.status)
	l := r.json(t)
	require.Len(t, l["members"], 2)
	require.Len(t, l["invites"], 1)

	// the invitee signs in for the first time: the invite becomes a
	// membership with the invited role, and disappears
	future := e.signInAs(t, futureID, "future-dev")
	r = future.call(t, http.MethodGet, "/api/tenants/"+tenant.String(), nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	require.Equal(t, "owner", r.json(t)["role"])
	l = owner.call(t, http.MethodGet, mp, nil).json(t)
	require.Len(t, l["members"], 3)
	require.Empty(t, l["invites"])

	// a revoked invite is gone and audited
	e.githubAccount(t, "never-comes")
	r = owner.call(t, http.MethodPost, mp, map[string]any{"github_login": "never-comes", "role": "member"})
	require.Equal(t, http.StatusCreated, r.status)
	id2 := r.json(t)["invite"].(map[string]any)["id"].(string)
	r = known.call(t, http.MethodDelete, "/api/tenants/"+tenant.String()+"/invites/"+id2, nil)
	require.Equal(t, http.StatusForbidden, r.status, "members cannot revoke invites")
	r = owner.call(t, http.MethodDelete, "/api/tenants/"+tenant.String()+"/invites/"+id2, nil)
	require.Equal(t, http.StatusNoContent, r.status)
	require.Equal(t, 1, e.auditCount(t, "invite.revoke", "invite:"+id2))
	r = owner.call(t, http.MethodDelete, "/api/tenants/"+tenant.String()+"/invites/"+id2, nil)
	require.Equal(t, http.StatusNotFound, r.status)

	// an expired invite neither lists nor resolves, and a new one can be made
	lateID := e.githubAccount(t, "late-person")
	r = owner.call(t, http.MethodPost, mp, map[string]any{"github_login": "late-person", "role": "member"})
	require.Equal(t, http.StatusCreated, r.status)
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `update cp.invites set expires_at = now() - interval '1 second' where lower(github_login) = 'late-person'`)
		require.NoError(t, err)
	})
	require.Len(t, owner.call(t, http.MethodGet, mp, nil).json(t)["invites"], 0)
	late := e.signInAs(t, lateID, "late-person")
	require.Equal(t, http.StatusNotFound, late.call(t, http.MethodGet, "/api/tenants/"+tenant.String(), nil).status)
	r = owner.call(t, http.MethodPost, mp, map[string]any{"github_login": "late-person", "role": "member"})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	require.Equal(t, "member", r.json(t)["kind"], "late-person has signed in by now")

	// role changes and the last-owner rule
	up := mp + "/" + known.userID.String()
	r = owner.call(t, http.MethodPatch, up, map[string]any{"role": "owner"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	require.Equal(t, "owner", r.json(t)["role"])
	require.Equal(t, 1, e.auditCount(t, "member.role_change", "user:"+known.userID.String()))
	for _, c := range []*client{future, known} {
		r = owner.call(t, http.MethodPatch, mp+"/"+c.userID.String(), map[string]any{"role": "member"})
		require.Equal(t, http.StatusOK, r.status, string(r.body))
	}
	r = owner.call(t, http.MethodPatch, mp+"/"+owner.userID.String(), map[string]any{"role": "member"})
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "last_owner", r.code(t))
	r = owner.call(t, http.MethodDelete, mp+"/"+owner.userID.String(), nil)
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "last_owner", r.code(t))
	r = known.call(t, http.MethodPatch, mp+"/"+future.userID.String(), map[string]any{"role": "owner"})
	require.Equal(t, http.StatusForbidden, r.status)
	require.Equal(t, "owner_required", r.code(t))
	r = owner.call(t, http.MethodPatch, mp+"/"+uuid.NewString(), map[string]any{"role": "owner"})
	require.Equal(t, http.StatusNotFound, r.status)

	// a member may leave; a member may not remove someone else
	r = known.call(t, http.MethodDelete, mp+"/"+future.userID.String(), nil)
	require.Equal(t, http.StatusForbidden, r.status)
	r = known.call(t, http.MethodDelete, mp+"/"+known.userID.String(), nil)
	require.Equal(t, http.StatusNoContent, r.status)
	require.Equal(t, 1, e.auditCount(t, "member.remove", "user:"+known.userID.String()))
	require.Equal(t, http.StatusNotFound, known.call(t, http.MethodGet, "/api/tenants/"+tenant.String(), nil).status)
}

func testSites(t *testing.T, e *env) {
	owner := e.signIn(t, "site-owner")
	member := e.signIn(t, "site-member")
	tenant := owner.enroll(t, "Sites")
	tp := "/api/tenants/" + tenant.String()
	require.Equal(t, http.StatusCreated, owner.call(t, http.MethodPost, tp+"/members", map[string]any{"github_login": "site-member", "role": "member"}).status)

	r := member.call(t, http.MethodPost, tp+"/sites", map[string]any{
		"name": "Marketing", "domain": "example.org",
		"origins": []string{"https://example.org", "https://www.example.org", "http://localhost:4321"},
	})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	s := r.json(t)
	siteID := s["id"].(string)
	require.Equal(t, tp+"/sites/"+siteID, r.header.Get("Location"))
	require.Equal(t, "active", s["status"])
	require.Equal(t, tenant.String(), s["tenant_id"])
	require.NotEmpty(t, s["quarantine_website_id"])
	require.NotEqual(t, siteID, s["quarantine_website_id"])
	require.Nil(t, s["verify_method"])
	require.Nil(t, s["verified_at"])
	require.NotContains(t, s, "verify_token")
	require.Equal(t, 1, e.auditCount(t, "site.create", "site:"+siteID))

	// the reconciler publishes the site to the registry the gateway reads
	require.Eventually(t, func() bool {
		_, err := e.sites.Get(context.Background(), siteID)
		return err == nil
	}, 90*time.Second, 500*time.Millisecond, "site never reached registry_sites")

	r = member.call(t, http.MethodGet, tp+"/sites/"+siteID, nil)
	require.Equal(t, http.StatusOK, r.status)
	require.Equal(t, "Marketing", r.json(t)["name"])

	r = member.call(t, http.MethodPatch, tp+"/sites/"+siteID, map[string]any{"origins": []string{"https://app.example.org"}, "name": "Main"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	require.Equal(t, []any{"https://app.example.org"}, r.json(t)["origins"])
	require.Equal(t, "Main", r.json(t)["name"])
	require.Equal(t, 1, e.auditCount(t, "site.update", "site:"+siteID))
	require.Eventually(t, func() bool {
		kv, err := e.sites.Get(context.Background(), siteID)
		return err == nil && strings.Contains(string(kv.Value()), "https://app.example.org")
	}, 90*time.Second, 500*time.Millisecond, "registry never got the new origins")

	r = member.call(t, http.MethodPatch, tp+"/sites/"+siteID, map[string]any{"domain": "other.org"})
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	require.Equal(t, "invalid_format", r.fields(t)["domain"])

	second := member.addSite(t, tenant, "second.example.net")
	r = member.call(t, http.MethodGet, tp+"/sites?limit=1", nil)
	require.Equal(t, http.StatusOK, r.status)
	page := r.json(t)
	require.Len(t, page["items"], 1)
	require.NotNil(t, page["next_cursor"])
	r = member.call(t, http.MethodGet, tp+"/sites?limit=1&cursor="+page["next_cursor"].(string), nil)
	require.Equal(t, second.String(), r.json(t)["items"].([]any)[0].(map[string]any)["id"])

	// deletion: owner only, 202, idempotent, then the site is read-only
	r = member.call(t, http.MethodDelete, tp+"/sites/"+siteID, nil)
	require.Equal(t, http.StatusForbidden, r.status)
	require.Equal(t, "owner_required", r.code(t))
	r = owner.call(t, http.MethodDelete, tp+"/sites/"+siteID, nil)
	require.Equal(t, http.StatusAccepted, r.status, string(r.body))
	require.Equal(t, "removed", r.json(t)["status"])
	r = owner.call(t, http.MethodDelete, tp+"/sites/"+siteID, nil)
	require.Equal(t, http.StatusAccepted, r.status)
	require.Equal(t, 1, e.auditCount(t, "site.delete", "site:"+siteID))
	r = owner.call(t, http.MethodPatch, tp+"/sites/"+siteID, map[string]any{"name": "back"})
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "site_removed", r.code(t))
	require.Eventually(t, func() bool {
		_, err := e.sites.Get(context.Background(), siteID)
		return err != nil
	}, 90*time.Second, 500*time.Millisecond, "registry key of the removed site stayed")

	r = owner.call(t, http.MethodGet, tp+"/sites", nil)
	require.Len(t, r.json(t)["items"], 1, "removed sites are hidden by default")
	r = owner.call(t, http.MethodGet, tp+"/sites?include_removed=true", nil)
	require.Len(t, r.json(t)["items"], 2)
	r = owner.call(t, http.MethodGet, tp+"/sites?include_removed=yes", nil)
	require.Equal(t, http.StatusBadRequest, r.status)
}

func testOriginValidation(t *testing.T, e *env) {
	owner := e.signIn(t, "origin-owner")
	tp := "/api/tenants/" + owner.enroll(t, "Origins").String() + "/sites"
	for _, tc := range []struct {
		domain  string
		origins []string
		fields  map[string]any
	}{
		{"example.org", []string{"https://example.org", "https://a.b.example.org", "http://127.0.0.1:8080", "https://example.org:8443"}, nil},
		{"example.org", []string{"http://example.org"}, map[string]any{"origins[0]": "not_bare_origin"}},
		{"example.org", []string{"https://example.org/"}, map[string]any{"origins[0]": "not_bare_origin"}},
		{"example.org", []string{"https://example.org/path"}, map[string]any{"origins[0]": "not_bare_origin"}},
		{"example.org", []string{"https://Example.org"}, map[string]any{"origins[0]": "not_bare_origin"}},
		{"example.org", []string{"https://example.org:443"}, map[string]any{"origins[0]": "not_bare_origin"}},
		{"example.org", []string{"https://*.example.org"}, map[string]any{"origins[0]": "not_bare_origin"}},
		{"example.org", []string{"https://example.org?q=1"}, map[string]any{"origins[0]": "not_bare_origin"}},
		{"example.org", []string{"https://evil-example.org"}, map[string]any{"origins[0]": "origin_outside_domain"}},
		{"example.org", []string{"https://example.org.evil.com"}, map[string]any{"origins[0]": "origin_outside_domain"}},
		{"example.org", []string{"https://example.org", "https://example.org"}, map[string]any{"origins[1]": "duplicate"}},
		{"example.org", []string{}, map[string]any{"origins": "required"}},
		{"Example.org", []string{"https://example.org"}, map[string]any{"domain": "invalid_domain"}},
		{"example.org.", []string{"https://example.org"}, map[string]any{"domain": "invalid_domain"}},
		{"localhost", []string{"http://localhost"}, map[string]any{"domain": "invalid_domain"}},
		{"10.0.0.1", []string{"https://10.0.0.1"}, map[string]any{"domain": "invalid_domain"}},
		{"example.org:8080", []string{"https://example.org"}, map[string]any{"domain": "invalid_domain"}},
		{"bücher.example", []string{"https://bücher.example"}, map[string]any{"domain": "invalid_domain"}},
	} {
		r := owner.call(t, http.MethodPost, tp, map[string]any{"name": "o", "domain": tc.domain, "origins": tc.origins})
		if tc.fields == nil {
			require.Equal(t, http.StatusCreated, r.status, "%s %v: %s", tc.domain, tc.origins, r.body)
			continue
		}
		require.Equal(t, http.StatusUnprocessableEntity, r.status, "%s %v: %s", tc.domain, tc.origins, r.body)
		for k, v := range tc.fields {
			require.Equal(t, v, r.fields(t)[k], "%s %v: %s", tc.domain, tc.origins, r.body)
		}
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = fmt.Sprintf("https://s%d.example.org", i)
	}
	r := owner.call(t, http.MethodPost, tp, map[string]any{"name": "o", "domain": "example.org", "origins": many})
	require.Equal(t, "too_many", r.fields(t)["origins"])
}

func testSnippet(t *testing.T, e *env) {
	owner := e.signIn(t, "snippet-owner")
	tenant := owner.enroll(t, "Snippets")
	site := owner.addSite(t, tenant, "snip.example.org")
	r := owner.call(t, http.MethodGet, "/api/sites/"+site.String()+"/snippet", nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	m := r.json(t)
	avail, err := e.tracker.Available()
	require.NoError(t, err)
	tag, rel, err := e.tracker.Snippet(site, avail[0].Version)
	require.NoError(t, err)
	require.Equal(t, avail[0].Version, m["version"])
	require.Equal(t, tag, m["tag"])
	require.Equal(t, rel.Integrity, m["integrity"])
	require.Equal(t, ingestOrigin+"/v/"+rel.Version+"/sdk.js", m["src"])
	require.Nil(t, m["npm"])
	require.Contains(t, m, "npm")
	v0 := m["versions"].([]any)[0].(map[string]any)
	require.Equal(t, rel.Version, v0["version"])
	require.EqualValues(t, rel.Bytes, v0["bytes"])
	require.EqualValues(t, rel.GzipBytes, v0["gzip_bytes"])
	require.Equal(t, rel.Released, v0["released"])
	// the json encoder must not escape the tag's angle brackets
	require.Contains(t, string(r.body), `"tag":"<script defer src=`)

	r = owner.call(t, http.MethodGet, "/api/sites/"+site.String()+"/snippet?version=0.0.9", nil)
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	require.Equal(t, "unknown_tracker_version", r.fields(t)["version"])

	r = owner.call(t, http.MethodDelete, "/api/tenants/"+tenant.String()+"/sites/"+site.String(), nil)
	require.Equal(t, http.StatusAccepted, r.status)
	r = owner.call(t, http.MethodGet, "/api/sites/"+site.String()+"/snippet", nil)
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "site_removed", r.code(t))
}

func testIngest(t *testing.T, e *env) {
	owner := e.signIn(t, "ingest-owner")
	tenant := owner.enroll(t, "Ingest")
	site := owner.addSite(t, tenant, "ingest.example.org")
	now := time.Now().UTC().Truncate(time.Hour)
	day := now.Truncate(24 * time.Hour)
	// rows as the jobs task edgestats writes them
	e.superuser(t, func(c *pgx.Conn) {
		for _, row := range []struct {
			at      time.Time
			outcome string
			n       int
		}{
			{now.Add(-2 * time.Hour), "accepted", 120},
			{now.Add(-2 * time.Hour), "rejected_origin", 2},
			{now.Add(-1 * time.Hour), "accepted", 30},
			{now.Add(-1 * time.Hour), "quarantined", 3},
			{now.Add(-1 * time.Hour), "bot", 0},
			{now.Add(-48 * time.Hour), "accepted", 7},
		} {
			_, err := c.Exec(context.Background(),
				`insert into cp.web_ingest_hourly (site_id, tenant_id, hour, outcome, count) values ($1, $2, $3, $4, $5)`,
				site, tenant, row.at, row.outcome, row.n)
			require.NoError(t, err)
		}
	})
	r := owner.call(t, http.MethodGet, "/api/sites/"+site.String()+"/ingest", nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	m := r.json(t)
	require.Equal(t, "hour", m["bucket"])
	require.Equal(t, now.Format(time.RFC3339), m["to"])
	require.Equal(t, now.Add(-24*time.Hour).Format(time.RFC3339), m["from"])
	series := m["series"].([]any)
	require.Len(t, series, 2, "empty buckets and the 48h-old row are not in the default range")
	require.Equal(t, map[string]any{"accepted": float64(120), "rejected_origin": float64(2)}, series[0].(map[string]any)["counts"])
	require.Equal(t, map[string]any{"accepted": float64(30), "quarantined": float64(3)}, series[1].(map[string]any)["counts"], "zero counts are left out")
	require.Equal(t, map[string]any{"accepted": float64(150), "quarantined": float64(3), "rejected_origin": float64(2)}, m["totals"])

	from := day.Add(-72 * time.Hour).Format(time.RFC3339)
	to := now.Add(time.Hour).Format(time.RFC3339)
	r = owner.call(t, http.MethodGet, "/api/sites/"+site.String()+"/ingest?bucket=day&from="+from+"&to="+to, nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	m = r.json(t)
	for _, p := range m["series"].([]any) {
		at, err := time.Parse(time.RFC3339, p.(map[string]any)["at"].(string))
		require.NoError(t, err)
		require.True(t, at.Equal(at.Truncate(24*time.Hour)), "day buckets start at utc midnight")
	}
	require.Equal(t, float64(157), m["totals"].(map[string]any)["accepted"])

	for _, q := range []string{
		"bucket=week",
		"from=" + now.Add(30*time.Minute).Format(time.RFC3339),
		"from=" + now.Format(time.RFC3339) + "&to=" + now.Format(time.RFC3339),
		"from=" + now.Add(-32*24*time.Hour).Format(time.RFC3339),
		"bucket=day&from=" + now.Add(-401*24*time.Hour).Format(time.RFC3339),
		"from=yesterday",
	} {
		r = owner.call(t, http.MethodGet, "/api/sites/"+site.String()+"/ingest?"+q, nil)
		require.Equal(t, http.StatusBadRequest, r.status, q)
		require.Equal(t, "invalid_query", r.code(t), q)
	}
}

func testTerms(t *testing.T, e *env) {
	owner := e.signIn(t, "terms-owner")
	member := e.signIn(t, "terms-member")
	tenant := owner.enroll(t, "Terms")
	tp := "/api/tenants/" + tenant.String()
	require.Equal(t, http.StatusCreated, owner.call(t, http.MethodPost, tp+"/members", map[string]any{"github_login": "terms-member", "role": "member"}).status)
	site := owner.addSite(t, tenant, "terms.example.org")

	// a newer terms version exists: the tenant's acceptance is now old
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `update cp.tenants set accepted_terms_version = '2026-01-01-draft.1' where id = $1`, tenant)
		require.NoError(t, err)
	})
	blocked := []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, tp, map[string]any{"name": "x"}},
		{http.MethodPost, tp + "/members", map[string]any{"github_login": "someone", "role": "member"}},
		{http.MethodPost, tp + "/sites", map[string]any{"name": "n", "domain": "t2.example.org", "origins": []string{"https://t2.example.org"}}},
		{http.MethodPatch, tp + "/sites/" + site.String(), map[string]any{"name": "x"}},
		{http.MethodDelete, tp + "/sites/" + site.String(), nil},
		{http.MethodPut, tp + "/cohort/proposal", map[string]any{"program": "Terms Award"}},
	}
	for _, b := range blocked {
		r := owner.call(t, b.method, b.path, b.body)
		require.Equal(t, http.StatusForbidden, r.status, "%s %s", b.method, b.path)
		require.Equal(t, "terms_not_accepted", r.code(t))
	}
	require.Equal(t, http.StatusOK, owner.call(t, http.MethodGet, tp+"/sites", nil).status, "reads keep working")

	r := member.call(t, http.MethodPost, tp+"/terms", map[string]any{"version": e.terms})
	require.Equal(t, http.StatusForbidden, r.status)
	require.Equal(t, "owner_required", r.code(t))
	r = owner.call(t, http.MethodPost, tp+"/terms", map[string]any{"version": "2026-01-01-draft.1"})
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "terms_version_mismatch", r.code(t))

	// leaving is always possible
	r = member.call(t, http.MethodDelete, tp+"/members/"+member.userID.String(), nil)
	require.Equal(t, http.StatusNoContent, r.status)

	r = owner.call(t, http.MethodPost, tp+"/terms", map[string]any{"version": e.terms})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	require.Equal(t, e.terms, r.json(t)["accepted_terms_version"])
	require.Equal(t, 1, e.auditCount(t, "tenant.terms_accept", "tenant:"+tenant.String()))
	r = owner.call(t, http.MethodPatch, tp, map[string]any{"name": "Terms OK"})
	require.Equal(t, http.StatusOK, r.status)
}

// tenantRoutes lists every tenant-scoped route with a body that would
// succeed for a member, so a 404 can only come from the tenant check.
func tenantRoutes(terms string, tenant, user, invite, site string) []struct {
	method, path string
	body         any
} {
	tp := "/api/tenants/" + tenant
	return []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, tp, nil},
		{http.MethodPatch, tp, map[string]any{"name": "hijacked"}},
		{http.MethodPost, tp + "/terms", map[string]any{"version": terms}},
		{http.MethodGet, tp + "/members", nil},
		{http.MethodPost, tp + "/members", map[string]any{"github_login": "intruder-friend", "role": "owner"}},
		{http.MethodPatch, tp + "/members/" + user, map[string]any{"role": "member"}},
		{http.MethodDelete, tp + "/members/" + user, nil},
		{http.MethodDelete, tp + "/invites/" + invite, nil},
		{http.MethodPost, tp + "/sites", map[string]any{"name": "x", "domain": "intruder.example.org", "origins": []string{"https://intruder.example.org"}}},
		{http.MethodGet, tp + "/sites", nil},
		{http.MethodGet, tp + "/sites/" + site, nil},
		{http.MethodPatch, tp + "/sites/" + site, map[string]any{"origins": []string{"https://intruder.example.org"}}},
		{http.MethodDelete, tp + "/sites/" + site, nil},
		{http.MethodGet, "/api/sites/" + site + "/snippet", nil},
		{http.MethodGet, "/api/sites/" + site + "/ingest", nil},
	}
}

func testIsolation(t *testing.T, e *env) {
	alice := e.signIn(t, "iso-alice")
	bob := e.signIn(t, "iso-bob")
	ta := alice.enroll(t, "Iso A")
	tb := bob.enroll(t, "Iso B")
	sa := alice.addSite(t, ta, "a.example.org")
	sb := bob.addSite(t, tb, "b.example.org")
	e.githubAccount(t, "iso-pending")
	r := alice.call(t, http.MethodPost, "/api/tenants/"+ta.String()+"/members", map[string]any{"github_login": "iso-pending", "role": "member"})
	require.Equal(t, http.StatusCreated, r.status)
	inviteA := r.json(t)["invite"].(map[string]any)["id"].(string)
	before := alice.call(t, http.MethodGet, "/api/tenants/"+ta.String()+"/members", nil).body
	// the control plane fetches the new site's stellar.toml after the create
	// response and stores the result on the site; the snapshot waits for it,
	// so the comparison below sees only what bob's requests could change
	sitePath := "/api/tenants/" + ta.String() + "/sites/" + sa.String()
	var beforeSite []byte
	require.Eventually(t, func() bool {
		r := alice.call(t, http.MethodGet, sitePath, nil)
		beforeSite = r.body
		return r.status == http.StatusOK && r.json(t)["stellar_toml_checked_at"] != nil
	}, time.Minute, 200*time.Millisecond, "stellar.toml check of site a")

	guess := uuid.NewString()
	cases := map[string][]string{
		// tenant a's real ids
		"real": {ta.String(), alice.userID.String(), inviteA, sa.String()},
		// guessed ids that exist nowhere
		"guessed": {guess, guess, guess, guess},
		// malformed ids
		"malformed": {"not-a-uuid", "1", "..", "%2e%2e"},
		// bob's own tenant with tenant a's site, invite and member
		"mixed": {tb.String(), alice.userID.String(), inviteA, sa.String()},
	}
	for name, ids := range cases {
		for _, rt := range tenantRoutes(e.terms, ids[0], ids[1], ids[2], ids[3]) {
			if name == "mixed" && !strings.Contains(rt.path, sa.String()) && !strings.Contains(rt.path, inviteA) &&
				!strings.Contains(rt.path, alice.userID.String()) {
				// routes on bob's own tenant without a foreign id are his to use
				continue
			}
			r := bob.call(t, rt.method, rt.path, rt.body)
			require.Equal(t, http.StatusNotFound, r.status, "%s: %s %s: %s", name, rt.method, rt.path, r.body)
			require.Equal(t, "not_found", r.code(t))
		}
	}

	// nothing of tenant a changed
	require.Equal(t, string(before), string(alice.call(t, http.MethodGet, "/api/tenants/"+ta.String()+"/members", nil).body))
	require.Equal(t, string(beforeSite), string(alice.call(t, http.MethodGet, sitePath, nil).body))
	list := bob.call(t, http.MethodGet, "/api/tenants", nil).json(t)["items"].([]any)
	require.Len(t, list, 1)
	require.Equal(t, tb.String(), list[0].(map[string]any)["id"])
	_ = sb

	// a removed tenant answers 404 to its own members too
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `update cp.tenants set status = 'removed' where id = $1`, tb)
		require.NoError(t, err)
	})
	for _, rt := range tenantRoutes(e.terms, tb.String(), bob.userID.String(), guess, sb.String()) {
		r := bob.call(t, rt.method, rt.path, rt.body)
		require.Equal(t, http.StatusNotFound, r.status, "removed: %s %s", rt.method, rt.path)
	}
	require.Empty(t, bob.call(t, http.MethodGet, "/api/tenants", nil).json(t)["items"])
}

func testRemovedMember(t *testing.T, e *env) {
	owner := e.signIn(t, "rm-owner")
	gone := e.signIn(t, "rm-member")
	tenant := owner.enroll(t, "Removal")
	site := owner.addSite(t, tenant, "rm.example.org")
	tp := "/api/tenants/" + tenant.String()
	require.Equal(t, http.StatusCreated, owner.call(t, http.MethodPost, tp+"/members", map[string]any{"github_login": "rm-member", "role": "member"}).status)
	paths := []string{tp, tp + "/members", tp + "/sites", tp + "/sites/" + site.String(),
		"/api/sites/" + site.String() + "/snippet", "/api/sites/" + site.String() + "/ingest"}
	for _, p := range paths {
		require.Equal(t, http.StatusOK, gone.call(t, http.MethodGet, p, nil).status, p)
	}
	require.Equal(t, http.StatusNoContent, owner.call(t, http.MethodDelete, tp+"/members/"+gone.userID.String(), nil).status)
	// the very next request of the same session is refused
	for _, p := range paths {
		r := gone.call(t, http.MethodGet, p, nil)
		require.Equal(t, http.StatusNotFound, r.status, p)
	}
}

// testWrongTenant runs the tenant queries of these routes under cp_tenant
// with another tenant set: row-level security must return zero rows and
// change zero rows, whatever ids the statement names.
func testWrongTenant(t *testing.T, e *env) {
	alice := e.signIn(t, "wt-alice")
	bob := e.signIn(t, "wt-bob")
	ta := alice.enroll(t, "WT A")
	tb := bob.enroll(t, "WT B")
	sa := alice.addSite(t, ta, "wt.example.org")
	ctx := context.Background()
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(ctx, `insert into cp.web_ingest_hourly (site_id, tenant_id, hour, outcome, count) values ($1, $2, date_trunc('hour', now()), 'accepted', 9)`, sa, ta)
		require.NoError(t, err)
	})
	now := time.Now().UTC().Truncate(time.Hour)
	err := e.conn.WithTenant(ctx, tb, func(tx pgx.Tx) error {
		q := tenantq.New(tx)
		_, err := q.GetSite(ctx, sa)
		require.ErrorIs(t, err, pgx.ErrNoRows)
		sites, err := q.ListSitesPage(ctx, tenantq.ListSitesPageParams{IncludeRemoved: true, RowLimit: 100})
		require.NoError(t, err)
		for _, s := range sites {
			require.Equal(t, tb, s.TenantID)
		}
		_, err = q.UpdateSite(ctx, tenantq.UpdateSiteParams{ID: sa, Name: ptr("stolen")})
		require.ErrorIs(t, err, pgx.ErrNoRows)
		_, err = q.RemoveSite(ctx, sa)
		require.ErrorIs(t, err, pgx.ErrNoRows)
		_, err = q.GetTenantMember(ctx, alice.userID)
		require.ErrorIs(t, err, pgx.ErrNoRows)
		n, err := q.DeleteTenantMember(ctx, alice.userID)
		require.NoError(t, err)
		require.Zero(t, n)
		rows, err := q.IngestCounts(ctx, tenantq.IngestCountsParams{Bucket: "hour", SiteID: sa, FromHour: now.Add(-time.Hour), ToHour: now.Add(time.Hour)})
		require.NoError(t, err)
		require.Empty(t, rows)
		ms, err := q.ListTenantMembers(ctx)
		require.NoError(t, err)
		require.Len(t, ms, 1)
		require.Equal(t, bob.userID, ms[0].UserID)
		tn, err := q.GetTenant(ctx)
		require.NoError(t, err)
		require.Equal(t, tb, tn.ID)
		return nil
	})
	require.NoError(t, err)
	// alice still sees her site unchanged
	r := alice.call(t, http.MethodGet, "/api/tenants/"+ta.String()+"/sites/"+sa.String(), nil)
	require.Equal(t, http.StatusOK, r.status)
	require.Equal(t, "active", r.json(t)["status"])
}

func ptr[T any](v T) *T { return &v }
