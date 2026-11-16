//go:build isolation

package consoleisolation

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/provision"
	"github.com/justmert/veristar/tests/fixtures/consoleapi"
)

// passkeyAnswer asks path for a passkey challenge and answers it with p.
func (c *client) passkeyAnswer(t *testing.T, path string, p *consoleapi.Passkey, create bool) map[string]any {
	t.Helper()
	r := c.call(t, http.MethodPost, path, map[string]any{})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	ch := r.json(t)
	return p.Answer(t, ch["challenge"].(string), ch["rp_id"].(string), c.e.srv.URL, create)
}

// signInPasskey runs the real passkey sign-in (contract 4.1.9) for p.
func (e *env) signInPasskey(t *testing.T, p *consoleapi.Passkey, create bool) *client {
	t.Helper()
	c := e.anonymous(t)
	r := c.call(t, http.MethodPost, "/api/auth/passkey/verify", c.passkeyAnswer(t, "/api/auth/passkey/challenge", p, create))
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	me := c.call(t, http.MethodGet, "/api/me", nil)
	require.Equal(t, http.StatusOK, me.status, string(me.body))
	c.userID = uuid.MustParse(me.json(t)["user"].(map[string]any)["id"].(string))
	return c
}

// a passkey account is held to the same tenant boundary as the others, gets
// its own umami user through the real reconciler, joins a project by its
// C-address, and cannot use another session's link challenge or touch
// another account's sign-in methods
func TestPasskeyIsolation(t *testing.T) {
	e := start(t)
	ctx := context.Background()
	alice := e.signIn(t, "pi-alice")
	ta := alice.enroll(t, "PI A")
	sa := alice.addSite(t, ta, "pi-a.example.org")

	pk := consoleapi.NewPasskey(t)
	p := e.signInPasskey(t, pk, true)
	tb := p.enroll(t, "PI B")

	for _, path := range []string{"/api/tenants/" + ta.String(), "/api/tenants/" + ta.String() + "/members",
		"/api/tenants/" + ta.String() + "/sites/" + sa.String()} {
		require.Equal(t, http.StatusNotFound, p.call(t, http.MethodGet, path, nil).status, path)
	}
	require.Equal(t, http.StatusNotFound, alice.call(t, http.MethodGet, "/api/tenants/"+tb.String(), nil).status)

	// alice adds the passkey account by its C-address
	r := alice.call(t, http.MethodPost, "/api/tenants/"+ta.String()+"/members",
		map[string]any{"stellar_address": pk.Address(t), "role": "member"})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	require.Equal(t, "member", r.json(t)["kind"])
	require.Eventually(t, func() bool {
		u, ok, err := e.umami.Admin.GetUser(ctx, p.userID.String())
		return err == nil && ok && u.Username == provision.Username(p.userID)
	}, time.Minute, 200*time.Millisecond, "the reconciler never made the passkey account's umami user")
	require.Equal(t, http.StatusOK, p.call(t, http.MethodGet, "/api/tenants/"+ta.String()+"/sites/"+sa.String(), nil).status)

	// alice's link challenge, copied with its cookie into the passkey
	// account's session, is refused: it was issued to her session
	r = alice.call(t, http.MethodPost, "/api/me/identities/passkey/challenge", map[string]any{})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	ch := r.json(t)
	ck := alice.cookie("sa_passkey", "/api/me")
	require.NotNil(t, ck)
	u, _ := url.Parse(e.srv.URL + "/api")
	other := consoleapi.NewPasskey(t)
	b := e.signInPasskey(t, other, true)
	b.hc.Jar.SetCookies(u, []*http.Cookie{{Name: "sa_passkey", Value: ck.Value, Path: "/api"}})
	r = b.call(t, http.MethodPost, "/api/me/identities/passkey",
		consoleapi.NewPasskey(t).Answer(t, ch["challenge"].(string), ch["rp_id"].(string), e.srv.URL, true))
	require.Equal(t, http.StatusConflict, r.status, string(r.body))
	require.Equal(t, "identity_already_linked", r.code(t))
	// a github account holding the copied cookie: the challenge names alice
	carol := e.signIn(t, "pi-carol")
	carol.hc.Jar.SetCookies(u, []*http.Cookie{{Name: "sa_passkey", Value: ck.Value, Path: "/api"}})
	r = carol.call(t, http.MethodPost, "/api/me/identities/passkey",
		consoleapi.NewPasskey(t).Answer(t, ch["challenge"].(string), ch["rp_id"].(string), e.srv.URL, true))
	require.Equal(t, http.StatusForbidden, r.status, string(r.body))
	require.Equal(t, "challenge_invalid", r.code(t))
	require.Nil(t, alice.call(t, http.MethodGet, "/api/me", nil).json(t)["user"].(map[string]any)["passkey_address"])

	// alice cannot link the passkey another account holds
	r = alice.call(t, http.MethodPost, "/api/me/identities/passkey",
		alice.passkeyAnswer(t, "/api/me/identities/passkey/challenge", pk, false))
	require.Equal(t, http.StatusConflict, r.status, string(r.body))
	require.Equal(t, "identity_in_use", r.code(t))

	// unlinking acts on the caller's account only
	require.Equal(t, http.StatusNotFound, p.call(t, http.MethodDelete, "/api/me/identities/github", nil).status)
	r = p.call(t, http.MethodDelete, "/api/me/identities/passkey", nil)
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "last_sign_in_method", r.code(t))
	require.Equal(t, http.StatusNotFound, alice.call(t, http.MethodDelete, "/api/me/identities/passkey", nil).status)

	// members of the project see the C-address; others do not
	m := alice.call(t, http.MethodGet, "/api/tenants/"+ta.String()+"/members", nil)
	require.Contains(t, string(m.body), pk.Address(t))
	require.NotContains(t, string(m.body), pk.CredentialID)
	require.NotContains(t, string(p.call(t, http.MethodGet, "/api/tenants/"+tb.String()+"/members", nil).body), "pi-alice")
}
