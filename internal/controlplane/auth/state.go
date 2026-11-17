package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// the __Host-sa_oauth cookie of contract 1.1: it carries state, the pkce
// verifier and the return path through the github round trip, sealed so the
// browser can neither read the verifier nor change any field. no database
// row is written for a visitor who has not signed in. the __Host- prefix
// makes the browser refuse a cookie of that name set by another host of
// the domain or with a Domain attribute, so a sibling host cannot plant its
// own sealed state (a login csrf); the prefix needs Secure and Path=/.
const (
	stateCookie    = "__Host-sa_oauth"
	stateCookieTTL = 10 * time.Minute // github's code lifetime
	callbackPath   = "/api/auth/github/callback"
)

// additional data binds a sealed value to this cookie and format, so a
// value sealed with the same key for another purpose never opens here
var stateAAD = []byte("sa_oauth/v1")

type statePayload struct {
	State    string `json:"state"`
	Verifier string `json:"verifier"`
	ReturnTo string `json:"return_to"`
	Exp      int64  `json:"exp"`
	// LinkUser is set when the flow links github to the signed-in account
	// (contract 4.1.8) instead of signing in: the account's id, which the
	// callback requires the session to still hold
	LinkUser string `json:"link_user,omitempty"`
}

type sealer struct{ aead cipher.AEAD }

func newSealer(key []byte) (*sealer, error) {
	if len(key) != 32 {
		return nil, errors.New("auth: OAUTH_STATE_KEY must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sealer{aead: aead}, nil
}

func (s *sealer) seal(p statePayload) (string, error) {
	return s.sealJSON(p, stateAAD)
}

// sealJSON seals v as json under aad, which binds the value to one cookie
// and format.
func (s *sealer) sealJSON(v any, aad []byte) (string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := s.aead.Seal(nonce, nonce, plain, aad)
	return base64.RawURLEncoding.EncodeToString(out), nil
}

var errBadState = errors.New("auth: oauth state cookie missing, tampered or expired")

// openRaw opens a value sealed under aad; errBadState when it was not.
func (s *sealer) openRaw(v string, aad []byte) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return nil, errBadState
	}
	n := s.aead.NonceSize()
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], aad)
	if err != nil {
		return nil, errBadState
	}
	return plain, nil
}

func (s *sealer) open(v string, now time.Time) (statePayload, error) {
	plain, err := s.openRaw(v, stateAAD)
	if err != nil {
		return statePayload{}, err
	}
	var p statePayload
	if err := json.Unmarshal(plain, &p); err != nil {
		return statePayload{}, fmt.Errorf("auth: sealed state is not json: %w", err)
	}
	if p.State == "" || p.Verifier == "" || now.Unix() >= p.Exp {
		return statePayload{}, errBadState
	}
	return p, nil
}

// stateCookieFor builds the __Host-sa_oauth cookie. an empty value with maxAge -1
// deletes it.
func stateCookieFor(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     stateCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

// randomToken returns n random bytes in base64url.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
