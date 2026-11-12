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

// the sa_oauth cookie of contract 1.1: it carries state, the pkce verifier
// and the return path through the github round trip, sealed so the browser
// can neither read the verifier nor change any field. no database row is
// written for a visitor who has not signed in.
const (
	stateCookie    = "sa_oauth"
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
	plain, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := s.aead.Seal(nonce, nonce, plain, stateAAD)
	return base64.RawURLEncoding.EncodeToString(out), nil
}

var errBadState = errors.New("auth: oauth state cookie missing, tampered or expired")

func (s *sealer) open(v string, now time.Time) (statePayload, error) {
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return statePayload{}, errBadState
	}
	n := s.aead.NonceSize()
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], stateAAD)
	if err != nil {
		return statePayload{}, errBadState
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

// stateCookieFor builds the sa_oauth cookie. an empty value with maxAge -1
// deletes it.
func stateCookieFor(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     stateCookie,
		Value:    value,
		Path:     callbackPath,
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
