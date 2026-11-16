// Package webauthnsoft is a TEST-ONLY software authenticator: an es256 key
// in memory that answers the console's webauthn options the way a browser
// with a security key does (attestation "none", user present and verified).
// it lets go tests register a passkey and step up through the real routes.
// it is never built into a product binary.
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

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

// Authenticator holds one credential.
type Authenticator struct {
	key   *ecdsa.PrivateKey
	id    []byte
	count uint32
	rpID  string
}

// New makes an authenticator with a fresh key and credential id.
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

// options is the part of the console's options a response needs
type options struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		RPID string `json:"rpId"`
	} `json:"publicKey"`
}

func (a *Authenticator) authData(rpID string, attested []byte) []byte {
	h := sha256.Sum256([]byte(rpID))
	flags := byte(0x01 | 0x04) // user present, user verified
	if attested != nil {
		flags |= 0x40
	}
	a.count++
	out := append([]byte{}, h[:]...)
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, a.count)
	return append(out, attested...)
}

func clientData(typ, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}

// Register answers the options of POST /api/operator/passkeys/options
// with the PublicKeyCredential json of a new credential.
func (a *Authenticator) Register(optionsJSON []byte, origin string) (json.RawMessage, error) {
	var o options
	if err := json.Unmarshal(optionsJSON, &o); err != nil {
		return nil, fmt.Errorf("webauthnsoft: options: %w", err)
	}
	a.rpID = o.PublicKey.RP.ID
	// the uncompressed point is 0x04, then x and y of 32 bytes each
	raw, err := a.key.PublicKey.Bytes()
	if err != nil {
		return nil, err
	}
	x, y := raw[1:33], raw[33:65]
	cose, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(webauthncose.AlgES256)},
		Curve:         1, XCoord: x, YCoord: y,
	})
	if err != nil {
		return nil, err
	}
	attested := make([]byte, 16)                                          // aaguid of zeros
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(a.id))) //nolint:gosec // the id is 32 bytes
	attested = append(attested, a.id...)
	attested = append(attested, cose...)
	a.count = 0
	auth := a.authData(a.rpID, attested)
	att, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": auth})
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(a.id), "rawId": b64.EncodeToString(a.id), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientData("webauthn.create", o.PublicKey.Challenge, origin)),
			"attestationObject": b64.EncodeToString(att),
			"transports":        []string{"usb"},
		},
		"clientExtensionResults":  map[string]any{},
		"authenticatorAttachment": "cross-platform",
	})
}

// Assert answers the options of POST /api/operator/step-up/options.
func (a *Authenticator) Assert(optionsJSON []byte, origin string) (json.RawMessage, error) {
	var o options
	if err := json.Unmarshal(optionsJSON, &o); err != nil {
		return nil, fmt.Errorf("webauthnsoft: options: %w", err)
	}
	rpID := o.PublicKey.RPID
	if rpID == "" {
		rpID = a.rpID
	}
	auth := a.authData(rpID, nil)
	cd := clientData("webauthn.get", o.PublicKey.Challenge, origin)
	h := sha256.Sum256(cd)
	// es256 signs the sha-256 of authenticator data and client data hash
	digest := sha256.Sum256(append(append([]byte{}, auth...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(a.id), "rawId": b64.EncodeToString(a.id), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cd),
			"authenticatorData": b64.EncodeToString(auth),
			"signature":         b64.EncodeToString(sig),
		},
		"clientExtensionResults":  map[string]any{},
		"authenticatorAttachment": "cross-platform",
	})
}
