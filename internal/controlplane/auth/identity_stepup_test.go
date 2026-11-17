package auth_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/stepup"
)

// stepUpStatus reads GET /api/operator/step-up.
func (b *browser) stepUpStatus() stepup.Status {
	b.t.Helper()
	res := b.send(http.MethodGet, "/api/operator/step-up", nil)
	require.Equal(b.t, http.StatusOK, res.StatusCode, string(res.body))
	return decode[stepup.Status](b.t, res)
}

// stepUp proves p for the operator step-up and returns the answer.
func (b *browser) stepUp(p *softPasskey) *resp {
	b.t.Helper()
	res := b.postJSON("/api/operator/step-up/challenge", map[string]any{})
	if res.StatusCode != http.StatusOK {
		return res
	}
	c := decode[stepup.Challenge](b.t, res)
	return b.postJSON("/api/operator/step-up", p.assert(b.t, c.Challenge, b.e.origin, c.RPID))
}

// addStepUpPasskey registers p in the operator area; the session needs a
// step-up or an open enrollment window.
func (b *browser) addStepUpPasskey(p *softPasskey) *resp {
	b.t.Helper()
	res := b.postJSON("/api/operator/passkeys/challenge", map[string]any{})
	if res.StatusCode != http.StatusOK {
		return res
	}
	c := decode[stepup.Challenge](b.t, res)
	body := p.assert(b.t, c.Challenge, b.e.origin, c.RPID)
	body["public_key"] = b64u.EncodeToString(p.pub)
	body["name"] = "key"
	return b.postJSON("/api/operator/passkeys", body)
}

func addRoster(t *testing.T, gh int64) {
	t.Helper()
	require.NoError(t, conn.WithSystem(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `insert into cp.operators (github_id, role) values ($1, 'operator')`, gh)
		return err
	}))
}

// a stolen console session of an operator, or a taken-over github account,
// must not reach the operator area: linking a passkey of the attacker's
// would otherwise give a key to step up with.
func TestOperatorSessionAloneCannotGainStepUpKey(t *testing.T) {
	gh := newGitHubID()
	e := newEnv(t, gh)
	b := e.browser(t)
	require.Equal(t, http.StatusSeeOther, b.signIn(gh, "op-"+strconv.FormatInt(gh, 10), "/").StatusCode)
	require.True(t, b.me().User.IsOperator)
	attacker := newPasskey(t)

	// the link challenge and the link itself need a step-up
	res := b.postJSON("/api/me/identities/passkey/challenge", map[string]any{})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "step_up_required", apiCode(t, res))
	res = b.postJSON("/api/me/identities/stellar/challenge", map[string]any{"address": newKP(t).Address()})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "step_up_required", apiCode(t, res))
	// the github link flow does not even start
	res = b.get(b.e.origin + "/api/auth/github/start?link=1")
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	loc, err := url.Parse(res.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/account", loc.Path)
	require.Equal(t, "step_up_required", loc.Query().Get("link_error"))
	// nor can the github identity be unlinked
	res = b.del("/api/me/identities/github")
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "step_up_required", apiCode(t, res))

	require.Nil(t, b.me().User.PasskeyAddress)
	st := b.stepUpStatus()
	require.Zero(t, st.Passkeys)
	res = b.stepUp(attacker)
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "passkey_required", apiCode(t, res))
}

// a passkey linked while the account was not an operator, or linked under
// a step-up, signs in but is no step-up key: only the roster can vouch for
// a sign-in passkey.
func TestLinkedPasskeyIsNoStepUpKey(t *testing.T) {
	e := newEnv(t)
	gh := newGitHubID()
	b := e.browser(t)
	require.Equal(t, http.StatusSeeOther, b.signIn(gh, "later-"+strconv.FormatInt(gh, 10), "/").StatusCode)
	planted := newPasskey(t)
	require.Equal(t, http.StatusNoContent, b.passkeyLink(planted, true).StatusCode)

	// the account becomes an operator afterwards
	addRoster(t, gh)
	require.True(t, b.me().User.IsOperator)
	st := b.stepUpStatus()
	require.Zero(t, st.Passkeys)
	require.False(t, st.SignInPasskey)
	res := b.stepUp(planted)
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "passkey_required", apiCode(t, res))
}

// the console's way: step up, then change the sign-in methods.
func TestOperatorChangesIdentitiesAfterStepUp(t *testing.T) {
	gh := newGitHubID()
	e := newEnv(t, gh)
	b := e.browser(t)
	require.Equal(t, http.StatusSeeOther, b.signIn(gh, "op-"+strconv.FormatInt(gh, 10), "/").StatusCode)
	// the bootstrap opened a first-passkey window and this sign-in is
	// inside it; the registration records a step-up
	key := newPasskey(t)
	res := b.addStepUpPasskey(key)
	require.Equal(t, http.StatusCreated, res.StatusCode, string(res.body))
	require.True(t, b.stepUpStatus().SteppedUp)

	linked := newPasskey(t)
	res = b.passkeyLink(linked, true)
	require.Equal(t, http.StatusNoContent, res.StatusCode, string(res.body))
	// it signs in, but the step-up still has the one registered key
	st := b.stepUpStatus()
	require.Equal(t, int64(1), st.Passkeys)
	require.False(t, st.SignInPasskey)
	res = b.stepUp(linked)
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.Equal(t, "step_up_failed", apiCode(t, res))
	require.Equal(t, http.StatusOK, b.stepUp(key).StatusCode)

	kp := newKP(t)
	c := b.challenge("/api/me/identities/stellar/challenge", kp)
	require.Equal(t, http.StatusNoContent, b.postJSON("/api/me/identities/stellar", map[string]string{
		"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, kp),
	}).StatusCode)
	require.Equal(t, http.StatusNoContent, b.del("/api/me/identities/stellar").StatusCode)
	require.Equal(t, http.StatusNoContent, b.del("/api/me/identities/passkey").StatusCode)

	// a new session without a step-up is refused again
	b2 := e.browser(t)
	require.Equal(t, http.StatusSeeOther, b2.signIn(gh, "op-"+strconv.FormatInt(gh, 10), "/").StatusCode)
	res = b2.postJSON("/api/me/identities/passkey/challenge", map[string]any{})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "step_up_required", apiCode(t, res))
}

// the sign-in passkey the roster names is a step-up key, and the identity
// route keeps the last one as /api/operator/passkeys does.
func TestUnlinkLastStepUpPasskey(t *testing.T) {
	p := newPasskey(t)
	e := newEnvWith(t, envOptions{stellarOperators: []string{p.address(t)}})
	b := e.browser(t)
	require.Equal(t, http.StatusOK, b.passkeySignIn(p, true).StatusCode)
	st := b.stepUpStatus()
	require.True(t, st.SignInPasskey)
	require.Equal(t, int64(1), st.Passkeys)

	kp := newKP(t)
	res := b.postJSON("/api/me/identities/stellar/challenge", map[string]any{"address": kp.Address()})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "step_up_required", apiCode(t, res))
	require.Equal(t, http.StatusOK, b.stepUp(p).StatusCode)
	c := b.challenge("/api/me/identities/stellar/challenge", kp)
	require.Equal(t, http.StatusNoContent, b.postJSON("/api/me/identities/stellar", map[string]string{
		"transaction": sign(t, c.Transaction, network.PublicNetworkPassphrase, kp),
	}).StatusCode)

	res = b.del("/api/me/identities/passkey")
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "last_passkey", apiCode(t, res))
	require.NotNil(t, b.me().User.PasskeyAddress)

	// with another key registered, the sign-in passkey can go
	require.Equal(t, http.StatusCreated, b.addStepUpPasskey(newPasskey(t)).StatusCode)
	require.Equal(t, http.StatusNoContent, b.del("/api/me/identities/passkey").StatusCode)
}
