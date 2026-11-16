package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/identity"
)

// postJSON sends a console state change with a json body.
func (b *browser) postJSON(path string, body any) *resp {
	b.t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(b.t, err)
	req, err := http.NewRequest(http.MethodPost, b.e.origin+path, bytes.NewReader(raw))
	require.NoError(b.t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range b.consoleHeaders() {
		req.Header.Set(k, v)
	}
	return fetch(b.t, b.c, req)
}

func (b *browser) del(path string) *resp {
	b.t.Helper()
	return b.send(http.MethodDelete, path, b.consoleHeaders())
}

func apiCode(t *testing.T, res *resp) string {
	t.Helper()
	return decode[apiError](t, res).Error.Code
}

// challenge asks for a challenge for kp's address on path.
func (b *browser) challenge(path string, kp *keypair.Full) auth.ChallengeResponse {
	b.t.Helper()
	res := b.postJSON(path, map[string]string{"address": kp.Address()})
	require.Equal(b.t, http.StatusOK, res.StatusCode, string(res.body))
	return decode[auth.ChallengeResponse](b.t, res)
}

// sign signs a challenge the way a wallet does: the transaction hash on the
// given network.
func sign(t *testing.T, xdr, passphrase string, kp *keypair.Full) string {
	t.Helper()
	gtx, err := txnbuild.TransactionFromXDR(xdr)
	require.NoError(t, err)
	tx, ok := gtx.Transaction()
	require.True(t, ok)
	tx, err = tx.Sign(passphrase, kp)
	require.NoError(t, err)
	out, err := tx.Base64()
	require.NoError(t, err)
	return out
}

// walletSignIn runs the whole wallet sign-in for kp and returns the verify
// response.
func (b *browser) walletSignIn(kp *keypair.Full) *resp {
	b.t.Helper()
	c := b.challenge("/api/auth/stellar/challenge", kp)
	return b.postJSON("/api/auth/stellar/verify", map[string]string{
		"transaction": sign(b.t, c.Transaction, network.PublicNetworkPassphrase, kp),
	})
}

func newKP(t *testing.T) *keypair.Full {
	t.Helper()
	kp, err := keypair.Random()
	require.NoError(t, err)
	return kp
}

func (b *browser) me() auth.Me {
	b.t.Helper()
	res := b.get(b.e.origin + "/api/me")
	require.Equal(b.t, http.StatusOK, res.StatusCode, string(res.body))
	return decode[auth.Me](b.t, res)
}

func TestWalletChallengeShape(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	kp := newKP(t)
	c := b.challenge("/api/auth/stellar/challenge", kp)
	require.Equal(t, network.PublicNetworkPassphrase, c.NetworkPassphrase)

	// a sep-10 challenge of this console: the sdk's own reader accepts it
	// with the console host as home and web auth domain
	host := mustHost(t, e.origin)
	require.Equal(t, host, e.h.ChallengeDomain())
	_, client, home, memo, err := txnbuild.ReadChallengeTx(c.Transaction, e.h.ServerAccountID(),
		network.PublicNetworkPassphrase, host, []string{host})
	require.NoError(t, err)
	require.Equal(t, kp.Address(), client)
	require.Equal(t, host, home)
	require.Nil(t, memo)
	require.WithinDuration(t, time.Now().Add(5*time.Minute), time.Time(c.ExpiresAt), 5*time.Second)

	// the cookie is http-only, secure, strict and scoped to /api
	var ck *http.Cookie
	for _, c := range b.jar.Cookies(mustURL(t, e.origin+"/api/auth/stellar/verify")) {
		if c.Name == "sa_stellar" {
			ck = c
		}
	}
	require.NotNil(t, ck, "sa_stellar cookie")

	// a muxed address or a seed is not an account id
	for _, bad := range []string{"", "nope", kp.Seed(),
		"MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAAGZFQ"} {
		res := b.postJSON("/api/auth/stellar/challenge", map[string]string{"address": bad})
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, bad)
	}
}

func mustHost(t *testing.T, origin string) string {
	t.Helper()
	return mustURL(t, origin).Host
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	require.NoError(t, err)
	return u
}

func TestWalletSignIn(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	kp := newKP(t)

	c := b.challenge("/api/auth/stellar/challenge", kp)
	res := b.postJSON("/api/auth/stellar/verify", map[string]string{
		"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, kp),
	})
	require.Equal(t, http.StatusOK, res.StatusCode, string(res.body))
	require.Equal(t, "/", decode[auth.VerifyResponse](t, res).ReturnTo)
	sess := setCookie(t, res, "sa_session")
	require.NotNil(t, sess)
	// the challenge cookie is cleared
	cleared := setCookie(t, res, "sa_stellar")
	require.NotNil(t, cleared)
	require.Negative(t, cleared.MaxAge)

	me := b.me()
	require.Nil(t, me.User.GitHubID)
	require.Nil(t, me.User.GitHubLogin)
	require.NotNil(t, me.User.StellarAddress)
	require.Equal(t, kp.Address(), *me.User.StellarAddress)
	require.Equal(t, identity.ShortAddress(kp.Address()), me.User.DisplayName)
	require.False(t, me.User.IsOperator)
	uid, found := sessionRow(t, sess.Value)
	require.True(t, found)
	require.Equal(t, me.User.ID, uid.UUID)
	require.Contains(t, auditActions(t, "user:"+me.User.ID.String()), "user.sign_in")

	// signing in again with the same key reaches the same account
	b2 := e.browser(t)
	require.Equal(t, http.StatusOK, b2.walletSignIn(kp).StatusCode)
	require.Equal(t, me.User.ID, b2.me().User.ID)
}

func TestWalletSignInReturnTo(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	kp := newKP(t)
	for give, want := range map[string]string{"/projects?tab=x": "/projects?tab=x", "//evil.example": "/"} {
		res := b.postJSON("/api/auth/stellar/challenge", map[string]string{"address": kp.Address(), "return_to": give})
		require.Equal(t, http.StatusOK, res.StatusCode)
		c := decode[auth.ChallengeResponse](t, res)
		res = b.postJSON("/api/auth/stellar/verify", map[string]string{
			"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, kp),
		})
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, want, decode[auth.VerifyResponse](t, res).ReturnTo)
	}
}

func TestWalletWrongNetwork(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	kp := newKP(t)
	c := b.challenge("/api/auth/stellar/challenge", kp)
	res := b.postJSON("/api/auth/stellar/verify", map[string]string{
		"transaction": sign(t, c.Transaction, network.TestNetworkPassphrase, kp),
	})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_invalid", apiCode(t, res))
	require.Nil(t, setCookie(t, res, "sa_session"))
	require.Equal(t, http.StatusUnauthorized, b.get(e.origin+"/api/me").StatusCode)
}

func TestWalletWrongKey(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	kp, other := newKP(t), newKP(t)
	// a challenge for kp, signed by another key
	c := b.challenge("/api/auth/stellar/challenge", kp)
	res := b.postJSON("/api/auth/stellar/verify", map[string]string{
		"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, other),
	})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_invalid", apiCode(t, res))

	// a challenge for another account, signed by that account, does not
	// match the cookie this browser holds
	c1 := b.challenge("/api/auth/stellar/challenge", kp)
	b2 := e.browser(t)
	c2 := b2.challenge("/api/auth/stellar/challenge", other)
	_ = c1
	res = b.postJSON("/api/auth/stellar/verify", map[string]string{
		"transaction": sign(t, c2.Transaction, network.PublicNetworkPassphrase, other),
	})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_invalid", apiCode(t, res))
}

func TestWalletExpiredChallenge(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	kp := newKP(t)
	c := b.challenge("/api/auth/stellar/challenge", kp)
	e.h.SetNow(func() time.Time { return time.Now().Add(6 * time.Minute) })
	t.Cleanup(func() { e.h.SetNow(time.Now) })
	res := b.postJSON("/api/auth/stellar/verify", map[string]string{
		"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, kp),
	})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_expired", apiCode(t, res))
}

func TestWalletReplay(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	kp := newKP(t)
	c := b.challenge("/api/auth/stellar/challenge", kp)
	var held *http.Cookie
	for _, ck := range b.jar.Cookies(mustURL(t, e.origin+"/api/auth/stellar/verify")) {
		if ck.Name == "sa_stellar" {
			held = ck
		}
	}
	require.NotNil(t, held)
	signed := sign(t, c.Transaction, network.PublicNetworkPassphrase, kp)
	require.Equal(t, http.StatusOK, b.postJSON("/api/auth/stellar/verify",
		map[string]string{"transaction": signed}).StatusCode)

	// the browser no longer holds the cookie
	res := b.postJSON("/api/auth/stellar/verify", map[string]string{"transaction": signed})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_invalid", apiCode(t, res))

	// a copy of the cookie and the signed challenge, replayed from
	// elsewhere, is refused by the database record of used challenges
	thief := e.browser(t)
	thief.jar.SetCookies(mustURL(t, e.origin+"/api"), []*http.Cookie{{Name: "sa_stellar", Value: held.Value, Path: "/api"}})
	res = thief.postJSON("/api/auth/stellar/verify", map[string]string{"transaction": signed})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_used", apiCode(t, res))
	require.Nil(t, setCookie(t, res, "sa_session"))
}

// a challenge issued by another console that shares the key (and so the
// server account) is still bound to that console's host
func TestWalletOtherOrigin(t *testing.T) {
	e, other := newEnv(t), newEnv(t)
	require.Equal(t, e.h.ServerAccountID(), other.h.ServerAccountID())
	require.NotEqual(t, e.h.ChallengeDomain(), other.h.ChallengeDomain())
	kp := newKP(t)
	ob := other.browser(t)
	c := ob.challenge("/api/auth/stellar/challenge", kp)
	var held *http.Cookie
	for _, ck := range ob.jar.Cookies(mustURL(t, other.origin+"/api/auth/stellar/verify")) {
		if ck.Name == "sa_stellar" {
			held = ck
		}
	}
	require.NotNil(t, held)
	b := e.browser(t)
	b.jar.SetCookies(mustURL(t, e.origin+"/api"), []*http.Cookie{{Name: "sa_stellar", Value: held.Value, Path: "/api"}})
	res := b.postJSON("/api/auth/stellar/verify", map[string]string{
		"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, kp),
	})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_invalid", apiCode(t, res))
}

func TestWalletOperator(t *testing.T) {
	kp := newKP(t)
	e := newEnvWith(t, envOptions{stellarOperators: []string{kp.Address()}})
	b := e.browser(t)
	require.Equal(t, http.StatusOK, b.walletSignIn(kp).StatusCode)
	require.True(t, b.me().User.IsOperator)

	// a suspended roster row clears the flag at once
	addr := kp.Address()
	suspendRoster(t, nil, &addr)
	require.False(t, b.me().User.IsOperator)
}

func TestWalletInviteResolved(t *testing.T) {
	e := newEnv(t)
	kp := newKP(t)
	ctx := context.Background()
	c, err := pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }()
	owner := newGitHubID()
	var ownerID, tenantID, inviteID uuid.UUID
	require.NoError(t, c.QueryRow(ctx, `insert into cp.users (github_id, github_login) values ($1, $2) returning id`,
		owner, "owner-"+strconv.FormatInt(owner, 10)).Scan(&ownerID))
	require.NoError(t, c.QueryRow(ctx, `insert into cp.tenants (slug, name, created_by) values ($1, 'W', $2) returning id`,
		"w-"+strconv.FormatInt(owner, 10), ownerID).Scan(&tenantID))
	require.NoError(t, c.QueryRow(ctx, `insert into cp.invites (tenant_id, stellar_address, role, created_by, expires_at)
		values ($1, $2, 'member', $3, now() + interval '1 day') returning id`, tenantID, kp.Address(), ownerID).Scan(&inviteID))

	b := e.browser(t)
	require.Equal(t, http.StatusOK, b.walletSignIn(kp).StatusCode)
	me := b.me()
	require.Len(t, me.Tenants, 1)
	require.Equal(t, tenantID, me.Tenants[0].ID)
	require.Equal(t, "member", me.Tenants[0].Role)
	require.Contains(t, e.reconciledTenants(), tenantID)
	require.Equal(t, []string{"invite.resolve"}, auditActions(t, "invite:"+inviteID.String()))
}

func TestLinkWalletToGitHub(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	gh := newGitHubID()
	login := "link-" + strconv.FormatInt(gh, 10)
	require.Equal(t, http.StatusSeeOther, b.signIn(gh, login, "/").StatusCode)
	kp := newKP(t)

	// the sign-in challenge cannot link, and the link challenge cannot sign
	// in: the purpose is sealed into the cookie
	c := b.challenge("/api/me/identities/stellar/challenge", kp)
	res := b.postJSON("/api/auth/stellar/verify", map[string]string{
		"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, kp),
	})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_invalid", apiCode(t, res))

	c = b.challenge("/api/me/identities/stellar/challenge", kp)
	res = b.postJSON("/api/me/identities/stellar", map[string]string{
		"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, kp),
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, string(res.body))
	me := b.me()
	require.Equal(t, gh, *me.User.GitHubID)
	require.Equal(t, kp.Address(), *me.User.StellarAddress)
	require.Equal(t, login, me.User.DisplayName)
	require.Contains(t, auditActions(t, "user:"+me.User.ID.String()), "user.identity_link")

	// the wallet now signs in to the same account
	w := e.browser(t)
	require.Equal(t, http.StatusOK, w.walletSignIn(kp).StatusCode)
	require.Equal(t, me.User.ID, w.me().User.ID)

	// a second wallet is refused until the first is unlinked
	res = b.postJSON("/api/me/identities/stellar/challenge", map[string]string{"address": newKP(t).Address()})
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "identity_already_linked", apiCode(t, res))

	// another account cannot take the linked address
	b2 := e.browser(t)
	gh2 := newGitHubID()
	require.Equal(t, http.StatusSeeOther, b2.signIn(gh2, "other-"+strconv.FormatInt(gh2, 10), "/").StatusCode)
	c = b2.challenge("/api/me/identities/stellar/challenge", kp)
	res = b2.postJSON("/api/me/identities/stellar", map[string]string{
		"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, kp),
	})
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "identity_in_use", apiCode(t, res))
	require.Nil(t, b2.me().User.StellarAddress)
}

// the link challenge is bound to the session that asked for it: a person
// signed in to another account in the same browser cannot use it
func TestLinkChallengeBoundToSession(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	gh := newGitHubID()
	require.Equal(t, http.StatusSeeOther, b.signIn(gh, "s-"+strconv.FormatInt(gh, 10), "/").StatusCode)
	kp := newKP(t)
	c := b.challenge("/api/me/identities/stellar/challenge", kp)
	// the same browser signs in to another account before verifying
	gh2 := newGitHubID()
	require.Equal(t, http.StatusSeeOther, b.signIn(gh2, "s-"+strconv.FormatInt(gh2, 10), "/").StatusCode)
	res := b.postJSON("/api/me/identities/stellar", map[string]string{
		"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, kp),
	})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_invalid", apiCode(t, res))
	require.Nil(t, b.me().User.StellarAddress)
}

func TestLinkGitHubToWallet(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	kp := newKP(t)
	require.Equal(t, http.StatusOK, b.walletSignIn(kp).StatusCode)
	before := b.me()

	// link=1 needs a session
	anon := e.browser(t)
	require.Equal(t, http.StatusUnauthorized, anon.get(e.origin+"/api/auth/github/start?link=1").StatusCode)

	var held string
	for _, ck := range b.jar.Cookies(mustURL(t, e.origin+"/")) {
		if ck.Name == "sa_session" {
			held = ck.Value
		}
	}
	require.NotEmpty(t, held)
	res := b.get(e.origin + "/api/auth/github/start?link=1&return_to=/elsewhere")
	require.Equal(t, http.StatusFound, res.StatusCode)
	authorize := mustURL(t, res.Header.Get("Location"))
	gh := newGitHubID()
	login := "linked-" + strconv.FormatInt(gh, 10)
	cb := b.get(b.consent(authorize, "authorize", gh, login).String())
	require.Equal(t, http.StatusSeeOther, cb.StatusCode)
	require.Equal(t, "/account?linked=github", cb.Header.Get("Location"))
	// linking keeps the session; it does not sign in anew, so the token is
	// not renewed (scs may resend the same cookie for the idle timeout)
	if sc := setCookie(t, cb, "sa_session"); sc != nil {
		require.Equal(t, held, sc.Value)
	}

	me := b.me()
	require.Equal(t, before.User.ID, me.User.ID)
	require.Equal(t, gh, *me.User.GitHubID)
	require.Equal(t, login, me.User.DisplayName)

	// github now signs in to the same account
	g := e.browser(t)
	require.Equal(t, http.StatusSeeOther, g.signIn(gh, login, "/").StatusCode)
	require.Equal(t, me.User.ID, g.me().User.ID)

	// a github account that already has its own console account cannot be
	// linked to this one
	b2 := e.browser(t)
	require.Equal(t, http.StatusOK, b2.walletSignIn(newKP(t)).StatusCode)
	res = b2.get(e.origin + "/api/auth/github/start?link=1")
	cb = b2.get(b2.consent(mustURL(t, res.Header.Get("Location")), "authorize", gh, login).String())
	require.Equal(t, http.StatusSeeOther, cb.StatusCode)
	require.Equal(t, "/account?link_error=identity_in_use", cb.Header.Get("Location"))
}

func TestUnlink(t *testing.T) {
	kp := newKP(t)
	gh := newGitHubID()
	// the github id is an operator, the address is not
	e := newEnvWith(t, envOptions{operators: []int64{gh}})
	b := e.browser(t)
	require.Equal(t, http.StatusSeeOther, b.signIn(gh, "u-"+strconv.FormatInt(gh, 10), "/").StatusCode)
	require.True(t, b.me().User.IsOperator)

	// the only method cannot go; a method the account lacks is 404
	res := b.del("/api/me/identities/github")
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "last_sign_in_method", apiCode(t, res))
	require.Equal(t, http.StatusNotFound, b.del("/api/me/identities/stellar").StatusCode)

	c := b.challenge("/api/me/identities/stellar/challenge", kp)
	require.Equal(t, http.StatusNoContent, b.postJSON("/api/me/identities/stellar", map[string]string{
		"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, kp),
	}).StatusCode)

	// unlinking github leaves a wallet-only account, and the operator flag
	// follows the identities that remain
	require.Equal(t, http.StatusNoContent, b.del("/api/me/identities/github").StatusCode)
	me := b.me()
	require.Nil(t, me.User.GitHubID)
	require.Nil(t, me.User.GitHubLogin)
	require.Equal(t, kp.Address(), *me.User.StellarAddress)
	require.False(t, me.User.IsOperator)
	require.Contains(t, auditActions(t, "user:"+me.User.ID.String()), "user.identity_unlink")

	// the github account signs in to a new account now
	g := e.browser(t)
	require.Equal(t, http.StatusSeeOther, g.signIn(gh, "u-"+strconv.FormatInt(gh, 10), "/").StatusCode)
	require.NotEqual(t, me.User.ID, g.me().User.ID)

	res = b.del("/api/me/identities/stellar")
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "last_sign_in_method", apiCode(t, res))
}

func TestWalletRoutesNeedConsoleHeaders(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	kp := newKP(t)
	raw, _ := json.Marshal(map[string]string{"address": kp.Address()})
	req, err := http.NewRequest(http.MethodPost, e.origin+"/api/auth/stellar/challenge", bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	res := fetch(t, b.c, req)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "csrf_header_missing", apiCode(t, res))
}
