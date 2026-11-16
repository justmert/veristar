//go:build isolation

package consoleisolation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/provision"
	"github.com/justmert/veristar/tests/fixtures/consoleapi"
)

// signInWallet runs the real wallet sign-in (contract 4.1.7) for kp.
func (e *env) signInWallet(t *testing.T, kp *keypair.Full) *client {
	t.Helper()
	c := e.anonymous(t)
	r := c.call(t, http.MethodPost, "/api/auth/stellar/challenge", map[string]any{"address": kp.Address()})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	r = c.call(t, http.MethodPost, "/api/auth/stellar/verify", map[string]any{
		"transaction": consoleapi.SignChallenge(t, r.json(t)["transaction"].(string), kp),
	})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	me := c.call(t, http.MethodGet, "/api/me", nil)
	require.Equal(t, http.StatusOK, me.status, string(me.body))
	c.userID = uuid.MustParse(me.json(t)["user"].(map[string]any)["id"].(string))
	return c
}

func (c *client) cookie(name, path string) *http.Cookie {
	u, _ := url.Parse(c.e.srv.URL + path)
	for _, ck := range c.hc.Jar.Cookies(u) {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

// a wallet account is held to the same tenant boundary as a github one,
// gets its own umami user through the real reconciler and umami, opens a
// dashboard of a project it belongs to, and cannot use another session's
// link challenge or touch another account's sign-in methods
func TestWalletIsolation(t *testing.T) {
	e := start(t)
	ctx := context.Background()
	alice := e.signIn(t, "wi-alice")
	ta := alice.enroll(t, "WI A")
	sa := alice.addSite(t, ta, "wi-a.example.org")

	kp, err := keypair.Random()
	require.NoError(t, err)
	w := e.signInWallet(t, kp)
	tb := w.enroll(t, "WI B")

	// another tenant's routes answer 404, as for a github account
	for _, p := range []string{"/api/tenants/" + ta.String(), "/api/tenants/" + ta.String() + "/members",
		"/api/tenants/" + ta.String() + "/sites/" + sa.String()} {
		r := w.call(t, http.MethodGet, p, nil)
		require.Equal(t, http.StatusNotFound, r.status, p)
	}
	require.Equal(t, http.StatusNotFound, alice.call(t, http.MethodGet, "/api/tenants/"+tb.String(), nil).status)

	// alice adds the wallet by its address; the real reconciler makes its
	// umami user under the stored u-<account id> name
	r := alice.call(t, http.MethodPost, "/api/tenants/"+ta.String()+"/members",
		map[string]any{"stellar_address": kp.Address(), "role": "member"})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	require.Equal(t, "member", r.json(t)["kind"])
	require.Eventually(t, func() bool {
		u, ok, err := e.umami.Admin.GetUser(ctx, w.userID.String())
		return err == nil && ok && u.Username == provision.Username(w.userID)
	}, time.Minute, 200*time.Millisecond, "the reconciler never made the wallet account's umami user")
	require.Equal(t, http.StatusOK, w.call(t, http.MethodGet, "/api/tenants/"+ta.String()+"/sites/"+sa.String(), nil).status)

	// the shared project's web dashboard reads umami as the stored name
	// (the site starts pending until the reconciler has made its website)
	require.Eventually(t, func() bool {
		res := w.call(t, http.MethodGet, "/api/tenants/"+ta.String()+"/web/realtime?site="+sa.String(), nil)
		var rt struct {
			Sites []any `json:"sites"`
		}
		return res.status == http.StatusOK && json.Unmarshal(res.body, &rt) == nil && len(rt.Sites) == 1
	}, time.Minute, 500*time.Millisecond, "the wallet member's umami reads never succeeded")

	// alice's link challenge, copied with its cookie into the wallet's
	// session, is refused: it was issued to her session
	kp2, err := keypair.Random()
	require.NoError(t, err)
	r = alice.call(t, http.MethodPost, "/api/me/identities/stellar/challenge", map[string]any{"address": kp2.Address()})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	ck := alice.cookie("sa_stellar", "/api/me")
	require.NotNil(t, ck)
	u, _ := url.Parse(e.srv.URL + "/api")
	w.hc.Jar.SetCookies(u, []*http.Cookie{{Name: "sa_stellar", Value: ck.Value, Path: "/api"}})
	// the signature and the account check out, but the challenge names
	// alice's session
	r = w.call(t, http.MethodPost, "/api/me/identities/stellar", map[string]any{
		"transaction": consoleapi.SignChallenge(t, r.json(t)["transaction"].(string), kp2),
	})
	require.Equal(t, http.StatusForbidden, r.status, string(r.body))
	require.Equal(t, "challenge_invalid", r.code(t))
	require.Nil(t, alice.call(t, http.MethodGet, "/api/me", nil).json(t)["user"].(map[string]any)["stellar_address"])

	// unlinking acts on the caller's account only: the wallet account has
	// no github identity (404) and cannot drop its only one (409), and
	// alice's account is unchanged
	require.Equal(t, http.StatusNotFound, w.call(t, http.MethodDelete, "/api/me/identities/github", nil).status)
	r = w.call(t, http.MethodDelete, "/api/me/identities/stellar", nil)
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "last_sign_in_method", r.code(t))
	me := alice.call(t, http.MethodGet, "/api/me", nil).json(t)["user"].(map[string]any)
	require.Equal(t, "wi-alice", me["github_login"])

	// the member list shows the wallet member by its short address, to
	// members of that project only
	m := alice.call(t, http.MethodGet, "/api/tenants/"+ta.String()+"/members", nil)
	require.Contains(t, string(m.body), kp.Address())
	require.NotContains(t, string(w.call(t, http.MethodGet, "/api/tenants/"+tb.String()+"/members", nil).body), "wi-alice")
}
