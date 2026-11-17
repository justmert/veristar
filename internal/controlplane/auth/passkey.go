package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stellar/go-stellar-sdk/network"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/passkey"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db/systemq"
)

// passkey sign-in (contract 4.1.9) uses soropass passkeys: a webauthn
// credential whose es256 key is the founding signer of a soropass smart
// account on the public network. the console creates and uses the passkey
// through @soropass/core in the browser; the control plane checks the
// webauthn assertion with package passkey and names the account by its
// C-address, derived from the credential id and the key.
//
// the console is the relying party: the rp id is the console host and the
// only accepted origin is CONSOLE_ORIGIN, so a passkey made for another site
// never verifies here. a challenge is bound, like a wallet challenge
// (stellar.go), to:
//   - the browser: the challenge, purpose and expiry travel in the sealed
//     sa_passkey cookie, and the assertion must sign that challenge;
//   - one use: the verification stores a hash of the challenge in
//     cp.wallet_challenges_used in the transaction that signs in or links;
//   - challengeTTL, after which the cookie is refused.
//
// a returning person's assertion names its credential id, and the key
// stored for that credential checks it. a new passkey has no stored key
// yet: the browser sends the key soropass read from the registration with
// the first assertion, and that assertion, signed by the key over the
// challenge, is the proof that the person holds it. registration answers
// are not signed (attestation "none"), so the console never trusts a key it
// has not seen sign.

const (
	passkeyCookie = "sa_passkey"
	// the bytes of a passkey challenge, sent base64url
	passkeyChallengeLen = 32
	// what the console asks the browser to wait for the authenticator
	passkeyTimeout = 2 * time.Minute
)

var passkeyAAD = []byte("sa_passkey/v1")

// passkeyNetwork and passkeyFactory name the soropass accounts the console
// accepts: those of the soropass AccountFactory on the public network, the
// network wallet challenges are bound to as well.
const (
	passkeyNetwork = network.PublicNetworkPassphrase
	passkeyFactory = passkey.MainnetFactory
)

// passkeyUsedPrefix keeps the hashes of passkey challenges apart from those
// of wallet nonces in cp.wallet_challenges_used.
const passkeyUsedPrefix = "passkey:"

type passkeyPayload struct {
	Challenge string `json:"challenge"`
	Purpose   string `json:"purpose"`
	UserID    string `json:"user_id,omitempty"`
	ReturnTo  string `json:"return_to,omitempty"`
	Exp       int64  `json:"exp"`
}

// passkeyAuth holds the relying party and the rate buckets of the passkey
// routes.
type passkeyAuth struct {
	rp         passkey.RelyingParty
	challenges *limiter
	verifies   *limiter
}

func newPasskeyAuth(consoleOrigin string, now time.Time) (*passkeyAuth, error) {
	u, err := url.Parse(consoleOrigin)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("auth: ConsoleOrigin is required for passkey sign-in")
	}
	return &passkeyAuth{
		rp: passkey.RelyingParty{ID: u.Hostname(), Origin: consoleOrigin},
		challenges: newLimiter(StartClientRatePerSecond, StartClientBurst, StartRatePerSecond, StartBurst,
			StartReserveRatePerSecond, StartReserveBurst, now),
		verifies: newLimiter(ExchangeClientRatePerSecond, ExchangeClientBurst, ExchangeRatePerSecond, ExchangeBurst,
			ExchangeReserveRatePerSecond, ExchangeReserveBurst, now),
	}, nil
}

// PasskeyRPID is the relying party id of passkey sign-in, for tests and docs.
func (h *Handler) PasskeyRPID() string { return h.passkey.rp.ID }

func passkeyCookieFor(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     passkeyCookie,
		Value:    value,
		Path:     stellarCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		// strict: the challenge and the verification are same-origin fetches
		SameSite: http.SameSiteStrictMode,
	}
}

// PasskeyChallengeResponse is the answer of the passkey challenge routes.
type PasskeyChallengeResponse struct {
	// Challenge is base64url, the value the assertion must sign
	Challenge string `json:"challenge"`
	// RPID is the rp id the browser asks the authenticator for
	RPID string `json:"rp_id"`
	// TimeoutMS is how long the browser waits for the authenticator
	TimeoutMS int64           `json:"timeout_ms"`
	ExpiresAt httpx.Timestamp `json:"expires_at"`
}

type passkeyChallengeRequest struct {
	ReturnTo *string `json:"return_to"`
}

// passkeyChallenge serves POST /api/auth/passkey/challenge (sign-in).
func (h *Handler) passkeyChallenge(w http.ResponseWriter, r *http.Request) error {
	var b passkeyChallengeRequest
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	returnTo := "/"
	if b.ReturnTo != nil {
		returnTo = cleanReturnTo(*b.ReturnTo)
	}
	return h.issuePasskeyChallenge(w, r, passkeyPayload{Purpose: purposeSignIn, ReturnTo: returnTo})
}

// passkeyLinkChallenge serves POST /api/me/identities/passkey/challenge.
func (h *Handler) passkeyLinkChallenge(w http.ResponseWriter, r *http.Request) error {
	u, err := session.MustUser(r.Context())
	if err != nil {
		return err
	}
	if err := h.checkIdentityChange(r.Context(), u.ID, u.IsOperator); err != nil {
		return err
	}
	if u.PasskeyAddress != nil {
		return httpx.Conflict(httpx.CodeIdentityLinked, "this account already has a passkey sign-in; unlink it first")
	}
	var b passkeyChallengeRequest
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	if b.ReturnTo != nil {
		return httpx.Validation("return_to is not used when linking", map[string]string{"return_to": httpx.FieldInvalidValue})
	}
	return h.issuePasskeyChallenge(w, r, passkeyPayload{Purpose: purposeLink, UserID: u.ID.String()})
}

func (h *Handler) issuePasskeyChallenge(w http.ResponseWriter, r *http.Request, p passkeyPayload) error {
	if ok, wait := h.passkey.challenges.take(h.clients.of(r), h.now()); !ok {
		return httpx.RateLimited(wait)
	}
	challenge, err := randomToken(passkeyChallengeLen)
	if err != nil {
		return err
	}
	exp := h.now().Add(challengeTTL)
	p.Challenge, p.Exp = challenge, exp.Unix()
	sealed, err := h.sealer.sealJSON(p, passkeyAAD)
	if err != nil {
		return err
	}
	http.SetCookie(w, passkeyCookieFor(sealed, int(challengeTTL/time.Second)))
	httpx.WriteJSON(w, http.StatusOK, PasskeyChallengeResponse{
		Challenge: challenge, RPID: h.passkey.rp.ID, TimeoutMS: passkeyTimeout.Milliseconds(),
		ExpiresAt: httpx.Timestamp(exp),
	})
	return nil
}

// passkeyVerifyRequest is a webauthn.get answer, every byte field base64url.
type passkeyVerifyRequest struct {
	CredentialID      *string `json:"credential_id"`
	AuthenticatorData *string `json:"authenticator_data"`
	ClientDataJSON    *string `json:"client_data_json"`
	Signature         *string `json:"signature"`
	// PublicKey is the sec-1 key of a passkey created just now, which no
	// account holds yet; omitted for a passkey the console knows
	PublicKey *string `json:"public_key"`
}

// fields of a verify body are bounded: authenticator data and client data
// are small, a der p-256 signature is at most 72 bytes
const (
	maxAuthDataLen   = 1024
	maxClientDataLen = 4096
	maxSignatureLen  = 80
)

func decodeField(fields map[string]string, name string, v *string, max int, required bool) []byte {
	if v == nil || *v == "" {
		if required {
			fields[name] = httpx.FieldRequired
		}
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(*v)
	if err != nil || len(b) == 0 {
		fields[name] = httpx.FieldInvalidFormat
		return nil
	}
	if len(b) > max {
		fields[name] = httpx.FieldTooLong
		return nil
	}
	return b
}

// passkeyVerified is an assertion that passed every check.
type passkeyVerified struct {
	payload      passkeyPayload
	address      string
	credentialID string
	publicKey    []byte
}

// verifyPasskey checks an assertion against the sa_passkey cookie and
// clears the cookie, whatever the result. it does not mark the challenge
// used; usePasskeyChallenge does, in the caller's transaction.
func (h *Handler) verifyPasskey(w http.ResponseWriter, r *http.Request, purpose string) (passkeyVerified, error) {
	http.SetCookie(w, passkeyCookieFor("", -1))
	var b passkeyVerifyRequest
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return passkeyVerified{}, err
	}
	fields := map[string]string{}
	if b.CredentialID == nil || *b.CredentialID == "" {
		fields["credential_id"] = httpx.FieldRequired
	} else if !passkey.ValidCredentialID(*b.CredentialID) {
		fields["credential_id"] = httpx.FieldInvalidFormat
	}
	a := passkey.Assertion{
		AuthenticatorData: decodeField(fields, "authenticator_data", b.AuthenticatorData, maxAuthDataLen, true),
		ClientDataJSON:    decodeField(fields, "client_data_json", b.ClientDataJSON, maxClientDataLen, true),
		Signature:         decodeField(fields, "signature", b.Signature, maxSignatureLen, true),
	}
	pub := decodeField(fields, "public_key", b.PublicKey, passkey.PublicKeyLen, false)
	if pub != nil {
		if _, err := passkey.ParsePublicKey(pub); err != nil {
			fields["public_key"] = httpx.FieldInvalidValue
		}
	}
	if len(fields) > 0 {
		return passkeyVerified{}, httpx.Validation("the passkey answer is incomplete or malformed", fields)
	}
	if ok, wait := h.passkey.verifies.take(h.clients.of(r), h.now()); !ok {
		return passkeyVerified{}, httpx.RateLimited(wait)
	}
	c, err := r.Cookie(passkeyCookie)
	if err != nil {
		return passkeyVerified{}, challengeInvalid("no challenge was issued to this browser, or it was already used")
	}
	plain, err := h.sealer.openRaw(c.Value, passkeyAAD)
	if err != nil {
		return passkeyVerified{}, challengeInvalid("the challenge cookie is not valid")
	}
	var p passkeyPayload
	if err := json.Unmarshal(plain, &p); err != nil {
		return passkeyVerified{}, fmt.Errorf("auth: sealed passkey challenge is not json: %w", err)
	}
	if p.Purpose != purpose {
		return passkeyVerified{}, challengeInvalid("the challenge was issued for another purpose")
	}
	if h.now().Unix() >= p.Exp {
		return passkeyVerified{}, httpx.Forbidden(httpx.CodeChallengeExpired, "the challenge expired; start again")
	}
	credID := *b.CredentialID
	var stored string
	if pub == nil {
		// a passkey the console knows: its stored key checks the assertion
		ctx := r.Context()
		err := h.db.WithSystem(ctx, func(tx pgx.Tx) error {
			row, err := systemq.New(tx).FindPasskey(ctx, &credID)
			if err != nil {
				return err
			}
			pub = row.PasskeyPublicKey
			if row.PasskeyAddress != nil {
				stored = *row.PasskeyAddress
			}
			return nil
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return passkeyVerified{}, httpx.Forbidden(httpx.CodePasskeyUnknown,
				"no account signs in with this passkey; create an account with a new passkey")
		}
		if err != nil {
			return passkeyVerified{}, fmt.Errorf("auth: find passkey: %w", err)
		}
	}
	if err := h.passkey.rp.Verify(a, p.Challenge, pub); err != nil {
		return passkeyVerified{}, challengeInvalid(passkeyReason(err))
	}
	address, err := passkey.AccountAddress(passkeyFactory, credID, pub, passkeyNetwork)
	if err != nil {
		return passkeyVerified{}, fmt.Errorf("auth: passkey address: %w", err)
	}
	if stored != "" && stored != address {
		// the row's address was derived from the same credential and key
		return passkeyVerified{}, fmt.Errorf("auth: stored passkey address %s differs from the derived %s", stored, address)
	}
	return passkeyVerified{payload: p, address: address, credentialID: credID, publicKey: pub}, nil
}

// passkeyReason is the refusal text of a failed assertion check.
func passkeyReason(err error) string {
	switch {
	case errors.Is(err, passkey.ErrChallenge):
		return "it signs another challenge than the one issued to this browser"
	case errors.Is(err, passkey.ErrOrigin), errors.Is(err, passkey.ErrRPID), errors.Is(err, passkey.ErrCrossOrigin):
		return "it was made for another site than this console"
	case errors.Is(err, passkey.ErrUserVerified), errors.Is(err, passkey.ErrUserPresent):
		return "the authenticator did not verify the person (pin or biometrics)"
	case errors.Is(err, passkey.ErrSignature), errors.Is(err, passkey.ErrPublicKey):
		return "the passkey's key did not sign it"
	default:
		return "it is not a passkey answer"
	}
}

// usePasskeyChallenge marks the challenge used inside the caller's
// transaction, as useChallenge does for wallet challenges.
func usePasskeyChallenge(ctx context.Context, q *systemq.Queries, v passkeyVerified) error {
	sum := sha256.Sum256([]byte(passkeyUsedPrefix + v.payload.Challenge))
	if err := q.PruneWalletChallenges(ctx); err != nil {
		return fmt.Errorf("prune challenges: %w", err)
	}
	n, err := q.UseWalletChallenge(ctx, systemq.UseWalletChallengeParams{
		NonceHash: sum[:], ExpiresAt: time.Unix(v.payload.Exp, 0),
	})
	if err != nil {
		return fmt.Errorf("use challenge: %w", err)
	}
	if n == 0 {
		return httpx.Forbidden(httpx.CodeChallengeUsed, "this challenge was already used; start again")
	}
	return nil
}

// passkeyVerify serves POST /api/auth/passkey/verify: signs in with the
// passkey account of a verified assertion, creating the account for a new
// passkey.
func (h *Handler) passkeyVerify(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	v, err := h.verifyPasskey(w, r, purposeSignIn)
	if err != nil {
		return err
	}
	var u systemq.CpUser
	var joined []uuid.UUID
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		if err := usePasskeyChallenge(ctx, q, v); err != nil {
			return err
		}
		var err error
		u, err = q.SignInPasskeyUser(ctx, systemq.SignInPasskeyUserParams{
			PasskeyAddress: &v.address, PasskeyCredentialID: &v.credentialID, PasskeyPublicKey: v.publicKey,
		})
		if isUnique(err, passkeyCredentialIdx) {
			return httpx.Conflict(httpx.CodeIdentityInUse,
				"this passkey's credential id signs in to another account")
		}
		if err != nil {
			return fmt.Errorf("upsert user: %w", err)
		}
		u, joined, err = h.afterSignIn(ctx, q, u, map[string]any{
			"method": "passkey", "passkey_address": v.address,
		})
		return err
	})
	if err != nil {
		return err
	}
	for _, t := range joined {
		h.reconcile(ctx, t)
	}
	if err := h.sessions.SignIn(ctx, u.ID); err != nil {
		return err
	}
	httpx.Logger(ctx).Info("signed in", "user_id", u.ID.String(), "method", "passkey")
	httpx.WriteJSON(w, http.StatusOK, VerifyResponse{ReturnTo: v.payload.ReturnTo})
	return nil
}

// passkeyLink serves POST /api/me/identities/passkey: links the passkey
// account of a verified assertion to the signed-in account.
func (h *Handler) passkeyLink(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	v, err := h.verifyPasskey(w, r, purposeLink)
	if err != nil {
		return err
	}
	if v.payload.UserID != u.ID.String() {
		return challengeInvalid("the challenge was issued to another session")
	}
	joined, err := h.link(ctx, u.ID, "passkey", func(q *systemq.Queries) (systemq.CpUser, error) {
		return q.LinkPasskey(ctx, systemq.LinkPasskeyParams{
			PasskeyAddress: &v.address, PasskeyCredentialID: &v.credentialID, PasskeyPublicKey: v.publicKey, ID: u.ID,
		})
	}, map[string]any{"passkey_address": v.address}, func(q *systemq.Queries) error {
		return usePasskeyChallenge(ctx, q, v)
	})
	if err != nil {
		return err
	}
	for _, t := range joined {
		h.reconcile(ctx, t)
	}
	httpx.Logger(ctx).Info("identity linked", "user_id", u.ID.String(), "kind", "passkey")
	httpx.NoContent(w)
	return nil
}
