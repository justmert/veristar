package auth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/passkey"
)

var b64u = base64.RawURLEncoding

// softPasskey is a software webauthn authenticator holding one es256
// credential, as a browser with a platform authenticator would.
type softPasskey struct {
	key    *ecdsa.PrivateKey
	pub    []byte
	credID string
	// flags of the assertions it makes: user present and user verified
	flags byte
}

func newPasskey(t *testing.T) *softPasskey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	pub, err := k.PublicKey.Bytes()
	require.NoError(t, err)
	id := make([]byte, 16)
	_, err = rand.Read(id)
	require.NoError(t, err)
	return &softPasskey{key: k, pub: pub, credID: b64u.EncodeToString(id), flags: 0x05}
}

// address is the soropass account of the passkey on the public network.
func (p *softPasskey) address(t *testing.T) string {
	t.Helper()
	a, err := passkey.AccountAddress(passkey.MainnetFactory, p.credID, p.pub, network.PublicNetworkPassphrase)
	require.NoError(t, err)
	return a
}

// assert answers a webauthn.get for challenge at origin and rpID.
func (p *softPasskey) assert(t *testing.T, challenge, origin, rpID string) map[string]any {
	t.Helper()
	cdj, err := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": challenge, "origin": origin, "crossOrigin": false})
	require.NoError(t, err)
	h := sha256.Sum256([]byte(rpID))
	ad := append(h[:], p.flags, 0, 0, 0, 0)
	sig, err := ecdsa.SignASN1(rand.Reader, p.key, passkey.SignedDigest(ad, cdj))
	require.NoError(t, err)
	return map[string]any{
		"credential_id":      p.credID,
		"authenticator_data": b64u.EncodeToString(ad),
		"client_data_json":   b64u.EncodeToString(cdj),
		"signature":          b64u.EncodeToString(sig),
	}
}

func (b *browser) passkeyChallenge(path string, body any) auth.PasskeyChallengeResponse {
	b.t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	res := b.postJSON(path, body)
	require.Equal(b.t, http.StatusOK, res.StatusCode, string(res.body))
	return decode[auth.PasskeyChallengeResponse](b.t, res)
}

// passkeyAnswer is the verify body for p; withKey sends the public key, as
// the console does for a passkey it created just now.
func (b *browser) passkeyAnswer(p *softPasskey, c auth.PasskeyChallengeResponse, withKey bool) map[string]any {
	body := p.assert(b.t, c.Challenge, b.e.origin, c.RPID)
	if withKey {
		body["public_key"] = b64u.EncodeToString(p.pub)
	}
	return body
}

// passkeySignIn runs the sign-in for p; create sends the key.
func (b *browser) passkeySignIn(p *softPasskey, create bool) *resp {
	b.t.Helper()
	c := b.passkeyChallenge("/api/auth/passkey/challenge", nil)
	return b.postJSON("/api/auth/passkey/verify", b.passkeyAnswer(p, c, create))
}

func (b *browser) passkeyLink(p *softPasskey, create bool) *resp {
	b.t.Helper()
	c := b.passkeyChallenge("/api/me/identities/passkey/challenge", nil)
	return b.postJSON("/api/me/identities/passkey", b.passkeyAnswer(p, c, create))
}

func TestPasskeyChallengeShape(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	c := b.passkeyChallenge("/api/auth/passkey/challenge", nil)
	// the rp id is the console host without the port
	require.Equal(t, mustURL(t, e.origin).Hostname(), c.RPID)
	require.Equal(t, c.RPID, e.h.PasskeyRPID())
	raw, err := b64u.DecodeString(c.Challenge)
	require.NoError(t, err)
	require.Len(t, raw, 32)
	require.Positive(t, c.TimeoutMS)
	require.WithinDuration(t, time.Now().Add(5*time.Minute), time.Time(c.ExpiresAt), 5*time.Second)

	var ck *http.Cookie
	for _, x := range b.jar.Cookies(mustURL(t, e.origin+"/api/auth/passkey/verify")) {
		if x.Name == "__Host-sa_passkey" {
			ck = x
		}
	}
	require.NotNil(t, ck, "__Host-sa_passkey cookie")
	// a fresh challenge each time
	require.NotEqual(t, c.Challenge, b.passkeyChallenge("/api/auth/passkey/challenge", nil).Challenge)
}

func TestPasskeyCreateAndSignIn(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	p := newPasskey(t)

	res := b.passkeySignIn(p, true)
	require.Equal(t, http.StatusOK, res.StatusCode, string(res.body))
	require.Equal(t, "/", decode[auth.VerifyResponse](t, res).ReturnTo)
	require.NotNil(t, setCookie(t, res, "__Host-sa_session"))
	cleared := setCookie(t, res, "__Host-sa_passkey")
	require.NotNil(t, cleared)
	require.Negative(t, cleared.MaxAge)

	me := b.me()
	require.Nil(t, me.User.GitHubID)
	require.Nil(t, me.User.StellarAddress)
	require.NotNil(t, me.User.PasskeyAddress)
	require.Equal(t, p.address(t), *me.User.PasskeyAddress)
	require.Equal(t, identity.ShortAddress(p.address(t)), me.User.DisplayName)
	require.False(t, me.User.IsOperator)
	require.Contains(t, auditActions(t, "user:"+me.User.ID.String()), "user.sign_in")

	// the stored row holds the credential and key the assertion verified
	ctx := context.Background()
	c, err := pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }()
	var cred string
	var key []byte
	require.NoError(t, c.QueryRow(ctx, `select passkey_credential_id, passkey_public_key from cp.users where id = $1`,
		me.User.ID).Scan(&cred, &key))
	require.Equal(t, p.credID, cred)
	require.Equal(t, p.pub, key)

	// a returning sign-in names the credential only; the stored key checks
	// it and it reaches the same account, from any browser
	b2 := e.browser(t)
	res = b2.passkeySignIn(p, false)
	require.Equal(t, http.StatusOK, res.StatusCode, string(res.body))
	require.Equal(t, me.User.ID, b2.me().User.ID)

	// sending the key again for a known passkey reaches the same account
	b3 := e.browser(t)
	require.Equal(t, http.StatusOK, b3.passkeySignIn(p, true).StatusCode)
	require.Equal(t, me.User.ID, b3.me().User.ID)
}

func TestPasskeyReturnTo(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	p := newPasskey(t)
	for give, want := range map[string]string{"/projects?tab=x": "/projects?tab=x", "//evil.example": "/"} {
		c := b.passkeyChallenge("/api/auth/passkey/challenge", map[string]string{"return_to": give})
		res := b.postJSON("/api/auth/passkey/verify", b.passkeyAnswer(p, c, true))
		require.Equal(t, http.StatusOK, res.StatusCode, string(res.body))
		require.Equal(t, want, decode[auth.VerifyResponse](t, res).ReturnTo)
	}
}

func TestPasskeyUnknown(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	res := b.passkeySignIn(newPasskey(t), false)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "passkey_unknown", apiCode(t, res))
	require.Nil(t, setCookie(t, res, "__Host-sa_session"))
}

func TestPasskeyRefusals(t *testing.T) {
	e := newEnv(t)
	p, other := newPasskey(t), newPasskey(t)
	// p has an account, so its stored key is what a returning sign-in uses
	require.Equal(t, http.StatusOK, e.browser(t).passkeySignIn(p, true).StatusCode)

	cases := map[string]func(b *browser, c auth.PasskeyChallengeResponse) map[string]any{
		// another key signs, claiming p's credential: the stored key refuses it
		"another key, known credential": func(b *browser, c auth.PasskeyChallengeResponse) map[string]any {
			body := other.assert(t, c.Challenge, e.origin, c.RPID)
			body["credential_id"] = p.credID
			return body
		},
		// a new passkey whose answer is signed by another key than the one sent
		"key not the signer": func(b *browser, c auth.PasskeyChallengeResponse) map[string]any {
			body := other.assert(t, c.Challenge, e.origin, c.RPID)
			body["public_key"] = b64u.EncodeToString(newPasskey(t).pub)
			return body
		},
		"another challenge": func(b *browser, c auth.PasskeyChallengeResponse) map[string]any {
			return p.assert(t, b64u.EncodeToString(make([]byte, 32)), e.origin, c.RPID)
		},
		"another origin": func(b *browser, c auth.PasskeyChallengeResponse) map[string]any {
			return p.assert(t, c.Challenge, "https://evil.example", c.RPID)
		},
		"another rp id": func(b *browser, c auth.PasskeyChallengeResponse) map[string]any {
			return p.assert(t, c.Challenge, e.origin, "evil.example")
		},
		"no user verification": func(b *browser, c auth.PasskeyChallengeResponse) map[string]any {
			weak := *p
			weak.flags = 0x01
			return weak.assert(t, c.Challenge, e.origin, c.RPID)
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			b := e.browser(t)
			c := b.passkeyChallenge("/api/auth/passkey/challenge", nil)
			res := b.postJSON("/api/auth/passkey/verify", mk(b, c))
			require.Equal(t, http.StatusForbidden, res.StatusCode, string(res.body))
			require.Equal(t, "challenge_invalid", apiCode(t, res))
			require.Nil(t, setCookie(t, res, "__Host-sa_session"))
		})
	}

	t.Run("no challenge cookie", func(t *testing.T) {
		b := e.browser(t)
		res := b.postJSON("/api/auth/passkey/verify", p.assert(t, "abc", e.origin, mustURL(t, e.origin).Hostname()))
		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Equal(t, "challenge_invalid", apiCode(t, res))
	})

	t.Run("malformed fields", func(t *testing.T) {
		b := e.browser(t)
		c := b.passkeyChallenge("/api/auth/passkey/challenge", nil)
		body := b.passkeyAnswer(p, c, false)
		body["credential_id"] = "not base64url!"
		body["signature"] = ""
		body["public_key"] = b64u.EncodeToString(make([]byte, 65))
		res := b.postJSON("/api/auth/passkey/verify", body)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, string(res.body))
		fields := decode[apiError](t, res).Error.Fields
		require.Equal(t, "invalid_format", fields["credential_id"])
		require.Equal(t, "required", fields["signature"])
		require.Equal(t, "invalid_value", fields["public_key"])
	})
}

func TestPasskeyExpiredAndReplay(t *testing.T) {
	e := newEnv(t)
	p := newPasskey(t)
	b := e.browser(t)
	c := b.passkeyChallenge("/api/auth/passkey/challenge", nil)
	e.h.SetNow(func() time.Time { return time.Now().Add(6 * time.Minute) })
	res := b.postJSON("/api/auth/passkey/verify", b.passkeyAnswer(p, c, true))
	e.h.SetNow(time.Now)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_expired", apiCode(t, res))

	c = b.passkeyChallenge("/api/auth/passkey/challenge", nil)
	var held *http.Cookie
	for _, ck := range b.jar.Cookies(mustURL(t, e.origin+"/api/auth/passkey/verify")) {
		if ck.Name == "__Host-sa_passkey" {
			held = ck
		}
	}
	require.NotNil(t, held)
	answer := b.passkeyAnswer(p, c, true)
	require.Equal(t, http.StatusOK, b.postJSON("/api/auth/passkey/verify", answer).StatusCode)

	// the browser no longer holds the cookie
	res = b.postJSON("/api/auth/passkey/verify", answer)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_invalid", apiCode(t, res))

	// a copy of the cookie and the answer, replayed from elsewhere, is
	// refused by the record of used challenges
	thief := e.browser(t)
	thief.jar.SetCookies(mustURL(t, e.origin+"/api"), []*http.Cookie{{Name: "__Host-sa_passkey", Value: held.Value, Path: "/"}})
	res = thief.postJSON("/api/auth/passkey/verify", answer)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_used", apiCode(t, res))
	require.Nil(t, setCookie(t, res, "__Host-sa_session"))
}

// a challenge cookie of another console sharing the state key is bound to
// that console's challenge, and its answer to that console's origin
func TestPasskeyOtherConsole(t *testing.T) {
	e, other := newEnv(t), newEnv(t)
	p := newPasskey(t)
	ob := other.browser(t)
	c := ob.passkeyChallenge("/api/auth/passkey/challenge", nil)
	var held *http.Cookie
	for _, ck := range ob.jar.Cookies(mustURL(t, other.origin+"/api/auth/passkey/verify")) {
		if ck.Name == "__Host-sa_passkey" {
			held = ck
		}
	}
	require.NotNil(t, held)
	b := e.browser(t)
	b.jar.SetCookies(mustURL(t, e.origin+"/api"), []*http.Cookie{{Name: "__Host-sa_passkey", Value: held.Value, Path: "/"}})
	body := p.assert(t, c.Challenge, other.origin, c.RPID)
	body["public_key"] = b64u.EncodeToString(p.pub)
	res := b.postJSON("/api/auth/passkey/verify", body)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_invalid", apiCode(t, res))
}

// a credential id is chosen by the authenticator and public once used; a
// second key claiming it derives another address and is refused
func TestPasskeyCredentialSquat(t *testing.T) {
	e := newEnv(t)
	p := newPasskey(t)
	require.Equal(t, http.StatusOK, e.browser(t).passkeySignIn(p, true).StatusCode)
	squat := newPasskey(t)
	squat.credID = p.credID
	res := e.browser(t).passkeySignIn(squat, true)
	require.Equal(t, http.StatusConflict, res.StatusCode, string(res.body))
	require.Equal(t, "identity_in_use", apiCode(t, res))
}

func TestPasskeyOperator(t *testing.T) {
	p := newPasskey(t)
	e := newEnvWith(t, envOptions{stellarOperators: []string{p.address(t)}})
	b := e.browser(t)
	require.Equal(t, http.StatusOK, b.passkeySignIn(p, true).StatusCode)
	require.True(t, b.me().User.IsOperator)

	// a suspended roster row clears the flag at once (5.1)
	addr := p.address(t)
	suspendRoster(t, nil, &addr)
	require.False(t, b.me().User.IsOperator)
	// and the next passkey sign-in does not bring it back
	b2 := e.browser(t)
	require.Equal(t, http.StatusOK, b2.passkeySignIn(p, false).StatusCode)
	require.False(t, b2.me().User.IsOperator)
}

func TestPasskeyInviteResolved(t *testing.T) {
	e := newEnv(t)
	p := newPasskey(t)
	ctx := context.Background()
	c, err := pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }()
	owner := newGitHubID()
	var ownerID, tenantID, inviteID uuid.UUID
	require.NoError(t, c.QueryRow(ctx, `insert into cp.users (github_id, github_login) values ($1, $2) returning id`,
		owner, "owner-"+strconv.FormatInt(owner, 10)).Scan(&ownerID))
	require.NoError(t, c.QueryRow(ctx, `insert into cp.tenants (slug, name, created_by) values ($1, 'P', $2) returning id`,
		"p-"+strconv.FormatInt(owner, 10), ownerID).Scan(&tenantID))
	require.NoError(t, c.QueryRow(ctx, `insert into cp.invites (tenant_id, stellar_address, role, created_by, expires_at)
		values ($1, $2, 'member', $3, now() + interval '1 day') returning id`, tenantID, p.address(t), ownerID).Scan(&inviteID))

	b := e.browser(t)
	require.Equal(t, http.StatusOK, b.passkeySignIn(p, true).StatusCode)
	me := b.me()
	require.Len(t, me.Tenants, 1)
	require.Equal(t, tenantID, me.Tenants[0].ID)
	require.Contains(t, e.reconciledTenants(), tenantID)
	require.Equal(t, []string{"invite.resolve"}, auditActions(t, "invite:"+inviteID.String()))
}

func TestLinkPasskey(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	gh := newGitHubID()
	login := "pk-" + strconv.FormatInt(gh, 10)
	require.Equal(t, http.StatusSeeOther, b.signIn(gh, login, "/").StatusCode)
	p := newPasskey(t)

	// the link challenge cannot sign in, the sign-in challenge cannot link
	c := b.passkeyChallenge("/api/me/identities/passkey/challenge", nil)
	res := b.postJSON("/api/auth/passkey/verify", b.passkeyAnswer(p, c, true))
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_invalid", apiCode(t, res))
	c = b.passkeyChallenge("/api/auth/passkey/challenge", nil)
	res = b.postJSON("/api/me/identities/passkey", b.passkeyAnswer(p, c, true))
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_invalid", apiCode(t, res))
	// return_to has no place in a link
	res = b.postJSON("/api/me/identities/passkey/challenge", map[string]string{"return_to": "/x"})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)

	res = b.passkeyLink(p, true)
	require.Equal(t, http.StatusNoContent, res.StatusCode, string(res.body))
	me := b.me()
	require.Equal(t, gh, *me.User.GitHubID)
	require.Equal(t, p.address(t), *me.User.PasskeyAddress)
	require.Equal(t, login, me.User.DisplayName)
	require.Contains(t, auditActions(t, "user:"+me.User.ID.String()), "user.identity_link")

	// the passkey signs in to the same account
	w := e.browser(t)
	require.Equal(t, http.StatusOK, w.passkeySignIn(p, false).StatusCode)
	require.Equal(t, me.User.ID, w.me().User.ID)

	// a second passkey waits for the first to be unlinked
	res = b.postJSON("/api/me/identities/passkey/challenge", map[string]any{})
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "identity_already_linked", apiCode(t, res))

	// another account cannot take the linked passkey
	b2 := e.browser(t)
	gh2 := newGitHubID()
	require.Equal(t, http.StatusSeeOther, b2.signIn(gh2, "o-"+strconv.FormatInt(gh2, 10), "/").StatusCode)
	res = b2.passkeyLink(p, false)
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "identity_in_use", apiCode(t, res))
	require.Nil(t, b2.me().User.PasskeyAddress)

	// the challenge is bound to the session that asked for it
	b3 := e.browser(t)
	gh3 := newGitHubID()
	require.Equal(t, http.StatusSeeOther, b3.signIn(gh3, "s-"+strconv.FormatInt(gh3, 10), "/").StatusCode)
	c = b3.passkeyChallenge("/api/me/identities/passkey/challenge", nil)
	gh4 := newGitHubID()
	require.Equal(t, http.StatusSeeOther, b3.signIn(gh4, "s-"+strconv.FormatInt(gh4, 10), "/").StatusCode)
	res = b3.postJSON("/api/me/identities/passkey", b3.passkeyAnswer(newPasskey(t), c, true))
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "challenge_invalid", apiCode(t, res))
}

func TestUnlinkPasskey(t *testing.T) {
	p := newPasskey(t)
	kp := newKP(t)
	e := newEnvWith(t, envOptions{stellarOperators: []string{p.address(t)}})
	b := e.browser(t)
	require.Equal(t, http.StatusOK, b.passkeySignIn(p, true).StatusCode)
	require.True(t, b.me().User.IsOperator)
	// an operator account changes its sign-in methods after a step-up; the
	// sign-in passkey the roster names is a step-up key
	require.Equal(t, http.StatusOK, b.stepUp(p).StatusCode)

	// the only method cannot go; methods the account lacks are 404
	res := b.del("/api/me/identities/passkey")
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "last_sign_in_method", apiCode(t, res))
	require.Equal(t, http.StatusNotFound, b.del("/api/me/identities/github").StatusCode)
	require.Equal(t, http.StatusNotFound, b.del("/api/me/identities/stellar").StatusCode)

	// a wallet joins the passkey account
	c := b.challenge("/api/me/identities/stellar/challenge", kp)
	require.Equal(t, http.StatusNoContent, b.postJSON("/api/me/identities/stellar", map[string]string{
		"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, kp),
	}).StatusCode)

	// the wallet can go while the passkey remains, and back
	require.Equal(t, http.StatusNoContent, b.del("/api/me/identities/stellar").StatusCode)
	c = b.challenge("/api/me/identities/stellar/challenge", kp)
	require.Equal(t, http.StatusNoContent, b.postJSON("/api/me/identities/stellar", map[string]string{
		"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, kp),
	}).StatusCode)

	// unlinking the passkey leaves a wallet account, and the operator flag
	// follows the identity that remains. the passkey is the account's only
	// step-up key, so another one is registered first
	require.Equal(t, http.StatusCreated, b.addStepUpPasskey(newPasskey(t)).StatusCode)
	require.Equal(t, http.StatusNoContent, b.del("/api/me/identities/passkey").StatusCode)
	me := b.me()
	require.Nil(t, me.User.PasskeyAddress)
	require.Equal(t, kp.Address(), *me.User.StellarAddress)
	require.False(t, me.User.IsOperator)

	// the passkey no longer signs in anywhere
	res = e.browser(t).passkeySignIn(p, false)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "passkey_unknown", apiCode(t, res))

	res = b.del("/api/me/identities/stellar")
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "last_sign_in_method", apiCode(t, res))
}
