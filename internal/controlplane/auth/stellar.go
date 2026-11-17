package auth

import (
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/txnbuild"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db/systemq"
)

// wallet sign-in (contract 4.1.7) is sep-10 stellar web authentication: the
// control plane builds a challenge transaction, the wallet signs it, and the
// control plane checks the signature. sep-10 is used rather than sep-53
// message signing because every wallet the console offers signs
// transactions, while some (rabet among them) cannot sign messages.
//
// the challenge is bound to:
//   - this console: its first operation is named "<console host> auth" and
//     its web_auth_domain operation holds the console host, both checked on
//     the way back, so a challenge made for another site never verifies;
//   - the public network: the server and the wallet sign the transaction
//     hash, which includes the network passphrase, and only
//     network.PublicNetworkPassphrase is accepted;
//   - the browser: the nonce, address, purpose and expiry travel in the
//     sealed sa_stellar cookie, and the signed transaction must carry that
//     nonce and address;
//   - one use: the verification stores the nonce's hash in
//     cp.wallet_challenges_used in the transaction that signs in or links,
//     so a second verification of the same challenge fails on every
//     control plane instance.
//
// the challenge expires after challengeTTL, both in the cookie and in the
// transaction's time bounds.

const (
	stellarCookie     = "sa_stellar"
	stellarCookiePath = "/api"
	challengeTTL      = 5 * time.Minute

	purposeSignIn = "sign_in"
	purposeLink   = "link"
)

var stellarAAD = []byte("sa_stellar/v1")

// stellarNetwork is the only network a challenge is accepted for.
const stellarNetwork = network.PublicNetworkPassphrase

type challengePayload struct {
	// Nonce is the 64-character base64 nonce of the first operation
	Nonce    string `json:"nonce"`
	Address  string `json:"address"`
	Purpose  string `json:"purpose"`
	UserID   string `json:"user_id,omitempty"`
	ReturnTo string `json:"return_to,omitempty"`
	Exp      int64  `json:"exp"`
}

// stellarAuth holds the server key and domain of the challenges.
type stellarAuth struct {
	server     *keypair.Full
	domain     string
	challenges *limiter
	verifies   *limiter
}

// serverKeyInfo separates the challenge signing key from every other use of
// OAUTH_STATE_KEY: hkdf with a label of its own gives an independent key.
const serverKeyInfo = "veristar sep-10 challenge signing key v1"

func newStellarAuth(stateKey []byte, consoleOrigin string, now time.Time) (*stellarAuth, error) {
	u, err := url.Parse(consoleOrigin)
	if err != nil || u.Host == "" {
		return nil, errors.New("auth: ConsoleOrigin is required for wallet sign-in")
	}
	if len(stateKey) != 32 {
		return nil, errors.New("auth: OAUTH_STATE_KEY must be 32 bytes")
	}
	seed, err := hkdf.Key(sha256.New, stateKey, nil, serverKeyInfo, 32)
	if err != nil {
		return nil, err
	}
	var raw [32]byte
	copy(raw[:], seed)
	kp, err := keypair.FromRawSeed(raw)
	if err != nil {
		return nil, err
	}
	return &stellarAuth{
		server: kp,
		domain: u.Host,
		challenges: newLimiter(StartClientRatePerSecond, StartClientBurst, StartRatePerSecond, StartBurst,
			StartReserveRatePerSecond, StartReserveBurst, now),
		verifies: newLimiter(ExchangeClientRatePerSecond, ExchangeClientBurst, ExchangeRatePerSecond, ExchangeBurst,
			ExchangeReserveRatePerSecond, ExchangeReserveBurst, now),
	}, nil
}

// ServerAccountID is the account that signs challenges, for tests and docs.
func (h *Handler) ServerAccountID() string { return h.stellar.server.Address() }

// ChallengeDomain is the console host the challenges are bound to.
func (h *Handler) ChallengeDomain() string { return h.stellar.domain }

func stellarCookieFor(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     stellarCookie,
		Value:    value,
		Path:     stellarCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		// strict: the challenge and the verification are same-origin fetches
		SameSite: http.SameSiteStrictMode,
	}
}

type challengeRequest struct {
	Address  *string `json:"address"`
	ReturnTo *string `json:"return_to"`
}

// ChallengeResponse is the answer of the challenge routes.
type ChallengeResponse struct {
	Transaction       string          `json:"transaction"`
	NetworkPassphrase string          `json:"network_passphrase"`
	ExpiresAt         httpx.Timestamp `json:"expires_at"`
}

type verifyRequest struct {
	Transaction *string `json:"transaction"`
}

// VerifyResponse is the answer of POST /api/auth/stellar/verify.
type VerifyResponse struct {
	ReturnTo string `json:"return_to"`
}

// stellarChallenge serves POST /api/auth/stellar/challenge (sign-in).
func (h *Handler) stellarChallenge(w http.ResponseWriter, r *http.Request) error {
	var b challengeRequest
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	returnTo := "/"
	if b.ReturnTo != nil {
		returnTo = cleanReturnTo(*b.ReturnTo)
	}
	return h.issueChallenge(w, r, b.Address, challengePayload{Purpose: purposeSignIn, ReturnTo: returnTo})
}

// stellarLinkChallenge serves POST /api/me/identities/stellar/challenge.
func (h *Handler) stellarLinkChallenge(w http.ResponseWriter, r *http.Request) error {
	u, err := session.MustUser(r.Context())
	if err != nil {
		return err
	}
	if err := h.checkIdentityChange(r.Context(), u.ID, u.IsOperator); err != nil {
		return err
	}
	if u.StellarAddress != nil {
		return httpx.Conflict(httpx.CodeIdentityLinked, "this account already has a stellar sign-in; unlink it first")
	}
	var b challengeRequest
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	if b.ReturnTo != nil {
		return httpx.Validation("return_to is not used when linking", map[string]string{"return_to": httpx.FieldInvalidValue})
	}
	return h.issueChallenge(w, r, b.Address, challengePayload{Purpose: purposeLink, UserID: u.ID.String()})
}

func (h *Handler) issueChallenge(w http.ResponseWriter, r *http.Request, address *string, p challengePayload) error {
	if address == nil {
		return httpx.Validation("address is required", map[string]string{"address": httpx.FieldRequired})
	}
	if !identity.ValidAddress(*address) {
		return httpx.Validation("address must be a stellar account id (G...)",
			map[string]string{"address": httpx.FieldInvalidStrkey})
	}
	if ok, wait := h.stellar.challenges.take(h.clients.of(r), h.now()); !ok {
		return httpx.RateLimited(wait)
	}
	tx, err := txnbuild.BuildChallengeTx(h.stellar.server.Seed(), *address, h.stellar.domain, h.stellar.domain,
		stellarNetwork, challengeTTL, nil)
	if err != nil {
		return fmt.Errorf("auth: build challenge: %w", err)
	}
	nonce, err := challengeNonce(tx)
	if err != nil {
		return err
	}
	xdr, err := tx.Base64()
	if err != nil {
		return fmt.Errorf("auth: encode challenge: %w", err)
	}
	exp := time.Unix(tx.Timebounds().MaxTime, 0)
	p.Nonce, p.Address, p.Exp = nonce, *address, exp.Unix()
	sealed, err := h.sealer.sealJSON(p, stellarAAD)
	if err != nil {
		return err
	}
	http.SetCookie(w, stellarCookieFor(sealed, int(challengeTTL/time.Second)))
	httpx.WriteJSON(w, http.StatusOK, ChallengeResponse{
		Transaction: xdr, NetworkPassphrase: stellarNetwork, ExpiresAt: httpx.Timestamp(exp),
	})
	return nil
}

// challengeNonce is the value of the challenge's first manage_data
// operation.
func challengeNonce(tx *txnbuild.Transaction) (string, error) {
	ops := tx.Operations()
	if len(ops) == 0 {
		return "", errors.New("auth: challenge has no operation")
	}
	md, ok := ops[0].(*txnbuild.ManageData)
	if !ok {
		return "", errors.New("auth: challenge's first operation is not manage_data")
	}
	return string(md.Value), nil
}

func challengeInvalid(why string) *httpx.Error {
	return httpx.Forbidden(httpx.CodeChallengeInvalid, "the signed challenge does not verify: "+why)
}

// verified is a challenge that passed every check.
type verified struct {
	payload challengePayload
	address string
	nonce   string
}

// verifyChallenge checks a signed challenge against the sa_stellar cookie
// and clears the cookie, whatever the result. it does not mark the
// challenge used; useChallenge does, in the caller's transaction.
func (h *Handler) verifyChallenge(w http.ResponseWriter, r *http.Request, purpose string) (verified, error) {
	http.SetCookie(w, stellarCookieFor("", -1))
	var b verifyRequest
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return verified{}, err
	}
	if b.Transaction == nil || *b.Transaction == "" {
		return verified{}, httpx.Validation("transaction is required",
			map[string]string{"transaction": httpx.FieldRequired})
	}
	if ok, wait := h.stellar.verifies.take(h.clients.of(r), h.now()); !ok {
		return verified{}, httpx.RateLimited(wait)
	}
	c, err := r.Cookie(stellarCookie)
	if err != nil {
		return verified{}, challengeInvalid("no challenge was issued to this browser, or it was already used")
	}
	plain, err := h.sealer.openRaw(c.Value, stellarAAD)
	if err != nil {
		return verified{}, challengeInvalid("the challenge cookie is not valid")
	}
	var p challengePayload
	if err := json.Unmarshal(plain, &p); err != nil {
		return verified{}, fmt.Errorf("auth: sealed challenge is not json: %w", err)
	}
	if p.Purpose != purpose {
		return verified{}, challengeInvalid("the challenge was issued for another purpose")
	}
	if h.now().Unix() >= p.Exp {
		return verified{}, httpx.Forbidden(httpx.CodeChallengeExpired, "the challenge expired; start again")
	}
	homeDomains := []string{h.stellar.domain}
	server := h.stellar.server.Address()
	tx, client, _, memo, err := txnbuild.ReadChallengeTx(*b.Transaction, server, stellarNetwork,
		h.stellar.domain, homeDomains)
	if err != nil {
		return verified{}, challengeInvalid("it is not a challenge of this console")
	}
	if memo != nil || !identity.ValidAddress(client) {
		return verified{}, challengeInvalid("the challenge names a muxed account or carries a memo")
	}
	if client != p.Address {
		return verified{}, challengeInvalid("the challenge was issued for another account")
	}
	nonce, err := challengeNonce(tx)
	if err != nil || subtle.ConstantTimeCompare([]byte(nonce), []byte(p.Nonce)) != 1 {
		return verified{}, challengeInvalid("the challenge is not the one issued to this browser")
	}
	// the account's own key must have signed: the identity is the key pair
	// behind the G-address, so another signer of the account (a multisig
	// co-signer) does not prove it
	if _, err := txnbuild.VerifyChallengeTxSigners(*b.Transaction, server, stellarNetwork,
		h.stellar.domain, homeDomains, client); err != nil {
		return verified{}, challengeInvalid("the account's key did not sign it on the public network")
	}
	return verified{payload: p, address: client, nonce: nonce}, nil
}

// useChallenge marks the challenge used inside the caller's transaction and
// prunes expired entries. a challenge seen before is refused.
func useChallenge(ctx context.Context, q *systemq.Queries, v verified) error {
	sum := sha256.Sum256([]byte(v.nonce))
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

// stellarVerify serves POST /api/auth/stellar/verify: signs in with the
// account of a verified challenge.
func (h *Handler) stellarVerify(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	v, err := h.verifyChallenge(w, r, purposeSignIn)
	if err != nil {
		return err
	}
	var u systemq.CpUser
	var joined []uuid.UUID
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		if err := useChallenge(ctx, q, v); err != nil {
			return err
		}
		var err error
		if u, err = q.SignInStellarUser(ctx, &v.address); err != nil {
			return fmt.Errorf("upsert user: %w", err)
		}
		u, joined, err = h.afterSignIn(ctx, q, u, map[string]any{
			"method": "stellar", "stellar_address": v.address,
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
	httpx.Logger(ctx).Info("signed in", "user_id", u.ID.String(), "method", "stellar")
	httpx.WriteJSON(w, http.StatusOK, VerifyResponse{ReturnTo: v.payload.ReturnTo})
	return nil
}

// stellarLink serves POST /api/me/identities/stellar: links the account of
// a verified challenge to the signed-in account.
func (h *Handler) stellarLink(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	v, err := h.verifyChallenge(w, r, purposeLink)
	if err != nil {
		return err
	}
	if v.payload.UserID != u.ID.String() {
		return challengeInvalid("the challenge was issued to another session")
	}
	joined, err := h.link(ctx, u.ID, "stellar", func(q *systemq.Queries) (systemq.CpUser, error) {
		return q.LinkStellar(ctx, systemq.LinkStellarParams{StellarAddress: &v.address, ID: u.ID})
	}, map[string]any{"stellar_address": v.address}, func(q *systemq.Queries) error {
		return useChallenge(ctx, q, v)
	})
	if err != nil {
		return err
	}
	for _, t := range joined {
		h.reconcile(ctx, t)
	}
	httpx.Logger(ctx).Info("identity linked", "user_id", u.ID.String(), "kind", "stellar")
	httpx.NoContent(w)
	return nil
}
