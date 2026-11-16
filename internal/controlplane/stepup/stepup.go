// Package stepup is the hardware-backed second factor of the operator area
// (contract 5.15): webauthn passkeys and security keys registered to a console
// account, and a step-up recorded on the console session when the person
// proves one of them.
//
// the package has three parts other code builds on:
//
//   - ceremony helpers. BeginRegistration, FinishRegistration,
//     BeginAssertion and FinishAssertion run the two webauthn ceremonies
//     with github.com/go-webauthn/webauthn. Begin* returns the options the
//     browser passes to navigator.credentials.create or .get (binary fields
//     as base64url strings) and keeps the ceremony state in the session
//     under keyRegister or keyAssert; Finish* takes the browser's
//     PublicKeyCredential json, verifies it against that state, and
//     removes the state, so each ceremony is single use.
//   - the session step-up field. Record stores the moment of a verified
//     assertion and the account it was made for under KeyStepUpAt and
//     KeyStepUpUser, after renewing the session token. Fresh reports
//     whether that moment is younger than Config.MaxAge for the signed-in
//     account. Require is the middleware that answers 401
//     step_up_required otherwise. requests made with an mcp credential
//     (session.ViaCredential) pass: they carry no session to step up.
//   - the passkey store over cp.operator_passkeys (00039).
//
// a different authenticator can feed the same step-up: anything that
// verifies a possession factor of the signed-in account calls Record in
// the request that verified it, and every operator route accepts the
// session from then on.
package stepup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db"
)

// session keys. KeyStepUpAt holds the unix second of the last verified
// assertion, KeyStepUpUser the account id it was made for.
const (
	KeyStepUpAt   = "operator_step_up_at"
	KeyStepUpUser = "operator_step_up_user"
	keyRegister   = "webauthn_register"
	keyAssert     = "webauthn_assert"
)

// bounds of the settings
const (
	MinMaxAge       = 5 * time.Minute
	MaxMaxAge       = 24 * time.Hour
	MinEnrollWindow = 10 * time.Minute
	MaxEnrollWindow = 7 * 24 * time.Hour
)

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
	// ConsoleOrigin is the console's https origin (http on localhost for a
	// development stack); its host is the relying party id, so a credential
	// works on that host only.
	ConsoleOrigin string
	// RPName is shown by the authenticator.
	RPName string
	// MaxAge is how long a step-up counts (OPERATOR_STEP_UP_MAX_AGE).
	MaxAge time.Duration
	// EnrollWindow is how long a person may register a first passkey
	// without a step-up after the window opened (OPERATOR_ENROLL_WINDOW).
	EnrollWindow time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Service runs the ceremonies and keeps the step-up.
type Service struct {
	wa     *webauthn.WebAuthn
	db     *db.DB
	sess   Sessions
	maxAge time.Duration
	window time.Duration
	now    func() time.Time
}

// New checks cfg and builds the relying party.
func New(cfg Config, d *db.DB, s Sessions) (*Service, error) {
	if d == nil || s == nil {
		return nil, errors.New("stepup: database and sessions are required")
	}
	u, err := url.Parse(cfg.ConsoleOrigin)
	// browsers run webauthn in secure contexts only: https, or plain http
	// on localhost for a development stack
	secure := u != nil && (u.Scheme == "https" || (u.Scheme == "http" && u.Hostname() == "localhost"))
	if err != nil || !secure || u.Hostname() == "" || u.Path != "" {
		return nil, fmt.Errorf("stepup: console origin %q is not an https origin", cfg.ConsoleOrigin)
	}
	if cfg.MaxAge < MinMaxAge || cfg.MaxAge > MaxMaxAge {
		return nil, fmt.Errorf("stepup: max age %s outside %s to %s", cfg.MaxAge, MinMaxAge, MaxMaxAge)
	}
	if cfg.EnrollWindow < MinEnrollWindow || cfg.EnrollWindow > MaxEnrollWindow {
		return nil, fmt.Errorf("stepup: enroll window %s outside %s to %s", cfg.EnrollWindow, MinEnrollWindow, MaxEnrollWindow)
	}
	name := cfg.RPName
	if name == "" {
		name = "Veristar"
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: name,
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
		// the browser shows its prompt for at most five minutes; the
		// state in the session expires with it
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: 5 * time.Minute, TimeoutUVD: 5 * time.Minute},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: 5 * time.Minute, TimeoutUVD: 5 * time.Minute},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("stepup: relying party: %w", err)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{wa: wa, db: d, sess: s, maxAge: cfg.MaxAge, window: cfg.EnrollWindow, now: now}, nil
}

// MaxAge is how long a step-up counts.
func (s *Service) MaxAge() time.Duration { return s.maxAge }

// EnrollWindow is the length of a first-passkey window.
func (s *Service) EnrollWindow() time.Duration { return s.window }

// Account is a console account as the ceremonies see it. it implements
// webauthn.User: the user handle is the account id, so a credential is
// bound to the account and not to a github or stellar identity.
type Account struct {
	User        session.User
	Credentials []webauthn.Credential
}

// WebAuthnID is the 16 bytes of the account id.
func (a Account) WebAuthnID() []byte { b := a.User.ID; return b[:] }

// WebAuthnName is the name the authenticator stores with the credential.
func (a Account) WebAuthnName() string {
	if n := a.User.DisplayName(); n != "" {
		return n
	}
	return a.User.ID.String()
}

// WebAuthnDisplayName is the same name.
func (a Account) WebAuthnDisplayName() string { return a.WebAuthnName() }

// WebAuthnCredentials are the account's registered credentials.
func (a Account) WebAuthnCredentials() []webauthn.Credential { return a.Credentials }

func (a Account) descriptors() []protocol.CredentialDescriptor {
	out := make([]protocol.CredentialDescriptor, len(a.Credentials))
	for i := range a.Credentials {
		out[i] = a.Credentials[i].Descriptor()
	}
	return out
}

// errNoCeremony is a finish without a matching begin in this session, or
// after the state expired.
var errNoCeremony = httpx.E(http.StatusBadRequest, httpx.CodeCeremonyExpired,
	"start again: this passkey prompt is no longer valid")

func (s *Service) saveState(ctx context.Context, key string, sd *webauthn.SessionData) error {
	b, err := json.Marshal(sd)
	if err != nil {
		return fmt.Errorf("stepup: encode ceremony state: %w", err)
	}
	s.sess.SetValue(ctx, key, string(b))
	return nil
}

// takeState reads and removes the state of a ceremony, so a response can be
// verified against it once.
func (s *Service) takeState(ctx context.Context, key string, user uuid.UUID) (webauthn.SessionData, error) {
	raw := s.sess.Value(ctx, key)
	s.sess.RemoveValue(ctx, key)
	if raw == "" {
		return webauthn.SessionData{}, errNoCeremony
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal([]byte(raw), &sd); err != nil {
		return webauthn.SessionData{}, errNoCeremony
	}
	if string(sd.UserID) != string(user[:]) || (!sd.Expires.IsZero() && s.now().After(sd.Expires)) {
		return webauthn.SessionData{}, errNoCeremony
	}
	return sd, nil
}

// BeginRegistration starts a registration for acc and keeps its state in
// the session. the account's credentials are excluded, so one
// authenticator is registered once.
func (s *Service) BeginRegistration(ctx context.Context, acc Account) (*protocol.CredentialCreation, error) {
	opts, sd, err := s.wa.BeginRegistration(acc,
		webauthn.WithExclusions(acc.descriptors()),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
	)
	if err != nil {
		return nil, fmt.Errorf("stepup: begin registration: %w", err)
	}
	if err := s.saveState(ctx, keyRegister, sd); err != nil {
		return nil, err
	}
	return opts, nil
}

// FinishRegistration verifies the browser's response to the latest
// BeginRegistration of this session. a response that does not verify is
// 400 step_up_failed.
func (s *Service) FinishRegistration(ctx context.Context, acc Account, response []byte) (*webauthn.Credential, error) {
	sd, err := s.takeState(ctx, keyRegister, acc.User.ID)
	if err != nil {
		return nil, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return nil, failed(ctx, "parse registration", err)
	}
	cred, err := s.wa.CreateCredential(acc, sd, parsed)
	if err != nil {
		return nil, failed(ctx, "verify registration", err)
	}
	return cred, nil
}

// BeginAssertion starts an assertion against the account's credentials and
// keeps its state in the session. an account without a credential is 409
// passkey_required.
func (s *Service) BeginAssertion(ctx context.Context, acc Account) (*protocol.CredentialAssertion, error) {
	if len(acc.Credentials) == 0 {
		return nil, httpx.Conflict(httpx.CodePasskeyRequired, "register a passkey or security key first")
	}
	opts, sd, err := s.wa.BeginLogin(acc, webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		return nil, fmt.Errorf("stepup: begin assertion: %w", err)
	}
	if err := s.saveState(ctx, keyAssert, sd); err != nil {
		return nil, err
	}
	return opts, nil
}

// FinishAssertion verifies the browser's response to the latest
// BeginAssertion of this session and returns the credential with its new
// sign count. an authenticator whose counter went backwards may be a
// clone and is refused.
func (s *Service) FinishAssertion(ctx context.Context, acc Account, response []byte) (*webauthn.Credential, error) {
	sd, err := s.takeState(ctx, keyAssert, acc.User.ID)
	if err != nil {
		return nil, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return nil, failed(ctx, "parse assertion", err)
	}
	cred, err := s.wa.ValidateLogin(acc, sd, parsed)
	if err != nil {
		return nil, failed(ctx, "verify assertion", err)
	}
	if cred.Authenticator.CloneWarning {
		return nil, failed(ctx, "verify assertion", errors.New("the signature counter did not increase"))
	}
	return cred, nil
}

func failed(ctx context.Context, what string, err error) error {
	httpx.Logger(ctx).Info("webauthn ceremony refused", "step", what, "error", err.Error())
	return httpx.E(http.StatusBadRequest, httpx.CodeStepUpFailed, "the passkey response could not be verified")
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
		"confirm with your passkey or security key to use the operator area")
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
