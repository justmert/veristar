// Package webauthnsoft is a TEST-ONLY software passkey: an es256 key in
// memory that answers the control plane's passkey challenges the way a
// browser with a passkey does (user present and verified), in the answer
// form of contract 4.1.9. it lets go tests register a passkey and step up
// through the real routes. it is never built into a product binary.
package webauthnsoft

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// Authenticator holds one credential.
type Authenticator struct {
	key   *ecdsa.PrivateKey
	id    []byte
	count uint32
}

// New makes a passkey with a fresh key and credential id.
func New() (*Authenticator, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	return &Authenticator{key: key, id: id}, nil
}

var b64 = base64.RawURLEncoding

// CredentialID is the base64url credential id.
func (a *Authenticator) CredentialID() string { return b64.EncodeToString(a.id) }

// PublicKey is the uncompressed sec-1 key, base64url.
func (a *Authenticator) PublicKey() string {
	raw, err := a.key.PublicKey.Bytes()
	if err != nil {
		panic(err)
	}
	return b64.EncodeToString(raw)
}

// challenge is the part of a challenge answer an assertion needs
type challenge struct {
	Challenge string `json:"challenge"`
	RPID      string `json:"rp_id"`
}

// Answer signs the challenge of a challenge route (the json the route
// answered) for origin, and returns the answer fields of 4.1.9.
func (a *Authenticator) Answer(challengeJSON []byte, origin string) (map[string]any, error) {
	var c challenge
	if err := json.Unmarshal(challengeJSON, &c); err != nil {
		return nil, fmt.Errorf("webauthnsoft: challenge: %w", err)
	}
	h := sha256.Sum256([]byte(c.RPID))
	a.count++
	auth := append([]byte{}, h[:]...)
	auth = append(auth, 0x01|0x04) // user present, user verified
	auth = binary.BigEndian.AppendUint32(auth, a.count)
	cd, err := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": c.Challenge, "origin": origin, "crossOrigin": false})
	if err != nil {
		return nil, err
	}
	cdh := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, auth...), cdh[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"credential_id":      a.CredentialID(),
		"authenticator_data": b64.EncodeToString(auth),
		"client_data_json":   b64.EncodeToString(cd),
		"signature":          b64.EncodeToString(sig),
	}, nil
}
