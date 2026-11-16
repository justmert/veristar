package consoleapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/passkey"
)

// Passkey is a TEST-ONLY software webauthn authenticator with one es256
// credential, standing in for a browser's passkey: it answers a passkey
// challenge with user presence and user verification, as a platform
// authenticator does after face id or a pin.
type Passkey struct {
	key *ecdsa.PrivateKey
	// PublicKey is the uncompressed sec-1 key, as @soropass/core reads it
	PublicKey []byte
	// CredentialID is the base64url credential id
	CredentialID string
}

// NewPasskey makes a fresh credential.
func NewPasskey(t testing.TB) *Passkey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	pub, err := k.PublicKey.Bytes()
	require.NoError(t, err)
	id := make([]byte, 16)
	_, err = rand.Read(id)
	require.NoError(t, err)
	return &Passkey{key: k, PublicKey: pub, CredentialID: base64.RawURLEncoding.EncodeToString(id)}
}

// Address is the soropass account address of the passkey on the public
// network, the identity the console stores.
func (p *Passkey) Address(t testing.TB) string {
	t.Helper()
	a, err := passkey.AccountAddress(passkey.MainnetFactory, p.CredentialID, p.PublicKey, network.PublicNetworkPassphrase)
	require.NoError(t, err)
	return a
}

// Answer is the verify body for a challenge answer (contract 4.1.9) at the
// console origin; create adds the public key, as the console does for a
// passkey it created just now.
func (p *Passkey) Answer(t testing.TB, challenge, rpID, origin string, create bool) map[string]any {
	t.Helper()
	cdj, err := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": challenge, "origin": origin})
	require.NoError(t, err)
	h := sha256.Sum256([]byte(rpID))
	ad := append(h[:], 0x05, 0, 0, 0, 0)
	sig, err := ecdsa.SignASN1(rand.Reader, p.key, passkey.SignedDigest(ad, cdj))
	require.NoError(t, err)
	enc := base64.RawURLEncoding
	out := map[string]any{
		"credential_id":      p.CredentialID,
		"authenticator_data": enc.EncodeToString(ad),
		"client_data_json":   enc.EncodeToString(cdj),
		"signature":          enc.EncodeToString(sig),
	}
	if create {
		out["public_key"] = enc.EncodeToString(p.PublicKey)
	}
	return out
}

// SignInPasskey runs the real passkey sign-in (contract 4.1.9) for p in a
// new browser; create sends the public key, as for a passkey created just
// now. it returns the client and the verify answer's status.
func (e *Env) SignInPasskey(t testing.TB, p *Passkey, create bool) (*Client, Resp) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := *e.Srv.Client()
	hc.Jar = jar
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &Client{e: e, hc: &hc}
	r := c.Call(t, http.MethodPost, "/api/auth/passkey/challenge", map[string]any{})
	require.Equal(t, http.StatusOK, r.Status, "passkey challenge failed: %s", r.Body)
	ch := r.JSON(t)
	challenge, _ := ch["challenge"].(string)
	rpID, _ := ch["rp_id"].(string)
	return c, c.Call(t, http.MethodPost, "/api/auth/passkey/verify", p.Answer(t, challenge, rpID, e.Srv.URL, create))
}
