// Package stepup is the second factor of the operator area (contract 5.15):
// passkeys of the console account, and a step-up recorded on the console
// session when the person proves one of them.
//
// it adds no webauthn code of its own. a passkey is what passkey sign-in
// (contract 4.1.9) uses: an es256 credential whose base64url id and 65-byte
// sec-1 key the control plane stores, and whose assertions
// internal/controlplane/passkey verifies (passkey.RelyingParty.Verify). the
// console runs every ceremony with the same @soropass/core helpers as sign-in
// (apps/console/src/lib/passkey). an account steps up with its sign-in
// passkey and with the passkeys it adds here (cp.operator_passkeys, 00041).
//
// the package has three parts other code builds on:
//
//   - challenges. IssueChallenge issues a 32-byte random challenge with a
//     purpose (step_up or register), keeps it in the console session under
//     keyChallenge, and answers it in the shape of 4.1.9's challenge.
//     takeChallenge removes it before it is checked, so each challenge is
//     checked once; the transaction that records the result also stores its
//     hash in cp.wallet_challenges_used, as 4.1.9 does, so two requests of
//     one session cannot both use it.
//   - the session step-up field. Record stores the moment of a verified
//     assertion and the account it was made for under KeyStepUpAt and
//     KeyStepUpUser, after renewing the session token. Fresh reports
//     whether that moment is younger than Config.MaxAge for the signed-in
//     account. Require is the middleware that answers 401
//     step_up_required otherwise. requests made with an mcp credential
//     (session.ViaCredential) pass: they carry no session to step up.
//   - the passkey store over cp.operator_passkeys and the account's sign-in
//     passkey (store.go).
//
// anything else that verifies a possession factor of the signed-in account
// can call Record in the request that verified it, and every operator route
// accepts the session from then on.
package stepup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/passkey"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db"
)

// session keys. KeyStepUpAt holds the unix second of the last verified
// assertion, KeyStepUpUser the account id it was made for.
const (
	KeyStepUpAt   = "operator_step_up_at"
	KeyStepUpUser = "operator_step_up_user"
	keyChallenge  = "operator_passkey_challenge"
)

// purposes of a challenge
const (
	PurposeStepUp   = "step_up"
	PurposeRegister = "register"
)

// bounds of the settings
const (
	MinMaxAge       = 5 * time.Minute
	MaxMaxAge       = 24 * time.Hour
	MinEnrollWindow = 10 * time.Minute
	MaxEnrollWindow = 7 * 24 * time.Hour
)

// a challenge lives 5 minutes and the browser waits 2 for the
// authenticator, as for passkey sign-in (4.1.9)
const (
	challengeTTL     = 5 * time.Minute
	challengeTimeout = 2 * time.Minute
	challengeLen     = 32
)

// usedPrefix keeps the hashes of step-up challenges apart from those of
// wallet nonces and sign-in passkey challenges in cp.wallet_challenges_used.
const usedPrefix = "operator-passkey:"

// Sessions is what the package needs of the console session;
// *session.Manager implements it.
type Sessions interface {
	Value(ctx context.Context, key string) string
	SetValue(ctx context.Context, key, value string)
	RemoveValue(ctx context.Context, key string)
	Renew(ctx context.Context) error
	SignedInAt(ctx context.Context) (time.Time, bool)
}

// Config sets the relying party and the two time limits.
type Config struct {
	// ConsoleOrigin is CONSOLE_ORIGIN; its host is the relying party id and
	// it is the only accepted origin, as for passkey sign-in
	ConsoleOrigin string
	// MaxAge is how long a step-up counts (OPERATOR_STEP_UP_MAX_AGE).
	MaxAge time.Duration
	// EnrollWindow is how long a person may register a first passkey
	// without a step-up after the window opened (OPERATOR_ENROLL_WINDOW).
	EnrollWindow time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Service issues challenges, verifies answers and keeps the step-up.
type Service struct {
	rp     passkey.RelyingParty
	db     *db.DB
	sess   Sessions
	maxAge time.Duration
	window time.Duration
	now    func() time.Time
}

// New checks cfg.
func New(cfg Config, d *db.DB, s Sessions) (*Service, error) {
	if d == nil || s == nil {
		return nil, errors.New("stepup: database and sessions are required")
	}
	u, err := url.Parse(cfg.ConsoleOrigin)
	if err != nil || u.Hostname() == "" || u.Path != "" {
		return nil, fmt.Errorf("stepup: console origin %q is not an origin", cfg.ConsoleOrigin)
	}
	if cfg.MaxAge < MinMaxAge || cfg.MaxAge > MaxMaxAge {
		return nil, fmt.Errorf("stepup: max age %s outside %s to %s", cfg.MaxAge, MinMaxAge, MaxMaxAge)
	}
	if cfg.EnrollWindow < MinEnrollWindow || cfg.EnrollWindow > MaxEnrollWindow {
		return nil, fmt.Errorf("stepup: enroll window %s outside %s to %s", cfg.EnrollWindow, MinEnrollWindow, MaxEnrollWindow)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		rp: passkey.RelyingParty{ID: u.Hostname(), Origin: cfg.ConsoleOrigin},
		db: d, sess: s, maxAge: cfg.MaxAge, window: cfg.EnrollWindow, now: now,
	}, nil
}

// MaxAge is how long a step-up counts.
func (s *Service) MaxAge() time.Duration { return s.maxAge }

// EnrollWindow is the length of a first-passkey window.
func (s *Service) EnrollWindow() time.Duration { return s.window }

// Challenge is the answer of a challenge route, the shape of 4.1.9's.
type Challenge struct {
	Challenge string          `json:"challenge"`
	RPID      string          `json:"rp_id"`
	TimeoutMS int64           `json:"timeout_ms"`
	ExpiresAt httpx.Timestamp `json:"expires_at"`
}

type challengeState struct {
	Challenge string `json:"challenge"`
	Purpose   string `json:"purpose"`
	User      string `json:"user"`
	Exp       int64  `json:"exp"`
}

// IssueChallenge stores a new challenge of purpose for user in the session,
// replacing any earlier one, and returns it.
func (s *Service) IssueChallenge(ctx context.Context, user uuid.UUID, purpose string) (Challenge, error) {
	b := make([]byte, challengeLen)
	if _, err := rand.Read(b); err != nil {
		return Challenge{}, fmt.Errorf("stepup: challenge: %w", err)
	}
	c := base64.RawURLEncoding.EncodeToString(b)
	exp := s.now().Add(challengeTTL)
	st, err := json.Marshal(challengeState{Challenge: c, Purpose: purpose, User: user.String(), Exp: exp.Unix()})
	if err != nil {
		return Challenge{}, err
	}
	s.sess.SetValue(ctx, keyChallenge, string(st))
	return Challenge{Challenge: c, RPID: s.rp.ID, TimeoutMS: challengeTimeout.Milliseconds(), ExpiresAt: httpx.Timestamp(exp)}, nil
}

// errNoCeremony is an answer without a matching challenge in this session,
// or after it expired or was used.
var errNoCeremony = httpx.E(http.StatusBadRequest, httpx.CodeCeremonyExpired,
	"start again: this passkey prompt is no longer valid")

// takeChallenge reads and removes the session's challenge, so an answer is
// checked against it once.
func (s *Service) takeChallenge(ctx context.Context, user uuid.UUID, purpose string) (string, error) {
	raw := s.sess.Value(ctx, keyChallenge)
	s.sess.RemoveValue(ctx, keyChallenge)
	var st challengeState
	if raw == "" || json.Unmarshal([]byte(raw), &st) != nil {
		return "", errNoCeremony
	}
	if st.Purpose != purpose || st.User != user.String() || s.now().Unix() >= st.Exp {
		return "", errNoCeremony
	}
	return st.Challenge, nil
}

// usedHash is what cp.wallet_challenges_used stores for a challenge.
func usedHash(challenge string) []byte {
	h := sha256.Sum256([]byte(usedPrefix + challenge))
	return h[:]
}

func failed(ctx context.Context, err error) error {
	httpx.Logger(ctx).Info("passkey answer refused", "error", err.Error())
	return httpx.E(http.StatusBadRequest, httpx.CodeStepUpFailed, "the passkey answer could not be verified")
}

// Record marks the session as stepped up for userID now. the session token
// is renewed first, like at sign-in, so a token copied before the step-up
// does not carry it.
func (s *Service) Record(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	if err := s.sess.Renew(ctx); err != nil {
		return time.Time{}, err
	}
	at := s.now().UTC().Truncate(time.Second)
	s.sess.SetValue(ctx, KeyStepUpAt, strconv.FormatInt(at.Unix(), 10))
	s.sess.SetValue(ctx, KeyStepUpUser, userID.String())
	return at, nil
}

// Clear removes the step-up from the session.
func (s *Service) Clear(ctx context.Context) {
	s.sess.RemoveValue(ctx, KeyStepUpAt)
	s.sess.RemoveValue(ctx, KeyStepUpUser)
}

// Fresh reports whether the session holds a step-up of userID younger than
// MaxAge, and when it was made.
func (s *Service) Fresh(ctx context.Context, userID uuid.UUID) (bool, time.Time) {
	if s.sess.Value(ctx, KeyStepUpUser) != userID.String() {
		return false, time.Time{}
	}
	n, err := strconv.ParseInt(s.sess.Value(ctx, KeyStepUpAt), 10, 64)
	if err != nil {
		return false, time.Time{}
	}
	at := time.Unix(n, 0).UTC()
	age := s.now().Sub(at)
	// a moment in the future is a clock step back; it does not extend the
	// step-up past its age limit
	return age >= -time.Minute && age < s.maxAge, at
}

// StepUpRequired is the 401 of a route that needs a fresh step-up.
func StepUpRequired() *httpx.Error {
	return httpx.E(http.StatusUnauthorized, httpx.CodeStepUpRequired,
		"confirm with your passkey to use the operator area")
}

// Require answers 401 step_up_required unless the session holds a fresh
// step-up of the signed-in user. it goes behind session.RequireUser and the
// operator guard. a request authenticated by an mcp credential passes.
func (s *Service) Require(next http.Handler) http.Handler {
	return httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		if session.ViaCredential(ctx) {
			next.ServeHTTP(w, r)
			return nil
		}
		u, err := session.MustUser(ctx)
		if err != nil {
			return err
		}
		if ok, _ := s.Fresh(ctx, u.ID); !ok {
			return StepUpRequired()
		}
		next.ServeHTTP(w, r)
		return nil
	})
}
