package onchain

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/chain"
	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/tenants"
)

type verifyBody struct {
	Method    *string `json:"method"`
	Signature *string `json:"signature"`
	TxHash    *string `json:"tx_hash"`
}

var txHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// outcome is one proof attempt: a method and detail when it held, an error
// code when it did not.
type outcome struct {
	method string
	via    *string
	detail map[string]any
	err    string
	// spends is the challenge nonce an account proof consumes
	spends *string
}

// verifyTimeout bounds the ledger reads of one proof, so a slow source
// answers the console instead of holding the request.
const verifyTimeout = 45 * time.Second

func (h *Routes) verify(w http.ResponseWriter, r *http.Request) error {
	a, row, err := h.loadOne(r)
	if err != nil {
		return err
	}
	var b verifyBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	if err := writable(a); err != nil {
		return err
	}
	method := ""
	if b.Method != nil {
		method = *b.Method
	}
	allowed := []string{MethodSEP53, MethodManageData}
	if row.Kind == "contract" {
		allowed = []string{MethodContractAdmin, MethodContractDeployer}
	}
	if !slices.Contains(allowed, method) {
		return httpx.Validation("method must be one of "+strings.Join(allowed, ", ")+" for this id",
			map[string]string{"method": httpx.FieldInvalidValue})
	}
	if method == MethodSEP53 && (b.Signature == nil || *b.Signature == "" || len(*b.Signature) > 200) {
		return httpx.Validation("signature is required for sep53", map[string]string{"signature": httpx.FieldRequired})
	}
	if method == MethodContractDeployer && (b.TxHash == nil || !txHash.MatchString(strings.ToLower(*b.TxHash))) {
		return httpx.Validation("tx_hash must be the 64 hex characters of the deploying transaction",
			map[string]string{"tx_hash": httpx.FieldInvalidFormat})
	}
	ctx := r.Context()
	if err := h.limit(ctx, row.ID); err != nil {
		return err
	}
	// the proof that needs the challenge reads it under cp_system, where
	// it is written
	var nonce *string
	var expires *time.Time
	err = h.scope.DB().WithSystem(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select challenge_nonce, challenge_expires_at from cp.chain_ids where id = $1 and tenant_id = $2`,
			row.ID, a.TenantID).Scan(&nonce, &expires)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NotFound()
	}
	if err != nil {
		return err
	}
	pctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	var o outcome
	switch method {
	case MethodSEP53, MethodManageData:
		o = h.proveAccount(pctx, a, row, method, b.Signature, nonce, expires)
		// the proof spends this challenge and no other: a second request
		// racing with the same signature finds it gone
		o.spends = nonce
	case MethodContractAdmin:
		o = h.proveAdmin(pctx, a, row)
	case MethodContractDeployer:
		o = h.proveDeployer(pctx, a, row, strings.ToLower(*b.TxHash))
	}
	out, err := h.store(ctx, a, row, method, o)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *Routes) proveAccount(ctx context.Context, a tenants.Access, row idRow, method string, sig *string, nonce *string, expires *time.Time) outcome {
	o := outcome{method: method}
	switch {
	case nonce == nil || expires == nil:
		o.err = ErrNoChallenge
		return o
	case !h.now().Before(*expires):
		o.err = ErrChallengeExpired
		return o
	}
	if method == MethodSEP53 {
		name, err := h.projectName(ctx, a)
		if err != nil {
			o.err = chain.CodeUnavailable
			return o
		}
		msg := ChallengeMessage(h.console, a.TenantID, name, row.Strkey, *nonce, *expires)
		if err := chain.VerifySEP53(row.Strkey, msg, *sig); err != nil {
			o.err = ErrBadSignature
			return o
		}
		// a valid signature shows who holds the account's own key, not that
		// the key still controls the account: an issuer that set its master
		// weight to zero, or below a multisig threshold, would otherwise be
		// claimable by whoever holds the old key
		acct, err := h.horizon.Account(ctx, row.Strkey)
		if err != nil {
			o.err = chain.Code(err)
			return o
		}
		if !acct.MasterControls() {
			o.err = ErrMasterNoWeight
			return o
		}
		o.detail = map[string]any{"standard": "sep-53"}
		return o
	}
	acct, err := h.horizon.Account(ctx, row.Strkey)
	if err != nil {
		o.err = chain.Code(err)
		return o
	}
	v, ok := acct.DataValue(ManageDataName)
	if !ok || v != *nonce {
		o.err = ErrDataMismatch
		return o
	}
	o.detail = map[string]any{"data_entry": ManageDataName}
	return o
}

// verifiedOwners lists the tenant's verified ids other than the one being
// proven: a contract is proven through one of them.
func (h *Routes) verifiedOwners(ctx context.Context, a tenants.Access, self string) ([]string, error) {
	var out []string
	err := h.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `select strkey from cp.chain_ids where verified_at is not null and strkey <> $1`, self)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return out, err
}

func (h *Routes) proveAdmin(ctx context.Context, a tenants.Access, row idRow) outcome {
	o := outcome{method: MethodContractAdmin}
	owners, err := h.verifiedOwners(ctx, a, row.Strkey)
	if err != nil {
		o.err = chain.CodeUnavailable
		return o
	}
	if len(owners) == 0 {
		o.err = ErrNoVerifiedOwner
		return o
	}
	entry, err := h.rpc.Instance(ctx, row.Strkey)
	if err != nil {
		o.err = chain.Code(err)
		return o
	}
	admins, err := chain.Admins(entry)
	if errors.Is(err, chain.ErrNoAdmin) {
		o.err = ErrNoAdmin
		return o
	}
	if err != nil {
		o.err = chain.CodeBadResponse
		return o
	}
	for _, ad := range admins {
		if slices.Contains(owners, ad.Address) {
			via := ad.Address
			o.via = &via
			o.detail = map[string]any{"storage_key": ad.Key}
			return o
		}
	}
	o.err = ErrAdminNotVerified
	return o
}

func (h *Routes) proveDeployer(ctx context.Context, a tenants.Access, row idRow, hash string) outcome {
	o := outcome{method: MethodContractDeployer}
	owners, err := h.verifiedOwners(ctx, a, row.Strkey)
	if err != nil {
		o.err = chain.CodeUnavailable
		return o
	}
	if len(owners) == 0 {
		o.err = ErrNoVerifiedOwner
		return o
	}
	tx, err := h.horizon.Transaction(ctx, hash)
	if err != nil {
		o.err = chain.Code(err)
		return o
	}
	if !tx.Successful {
		o.err = ErrTxFailed
		return o
	}
	deps, err := chain.Deployments(h.network, tx.EnvelopeXDR)
	if err != nil {
		o.err = chain.CodeBadResponse
		return o
	}
	for _, d := range deps {
		if d.Contract != row.Strkey {
			continue
		}
		if !slices.Contains(owners, d.Deployer) {
			o.err = ErrNotDeployer
			return o
		}
		via := d.Deployer
		o.via = &via
		o.detail = map[string]any{"tx_hash": hash}
		return o
	}
	o.err = ErrNotInTransaction
	return o
}

// store writes the outcome under cp_system with its audit row. a failed
// attempt keeps an earlier proof: the id stays verified, and the error
// says what the last attempt found.
func (h *Routes) store(ctx context.Context, a tenants.Access, row idRow, method string, o outcome) (IDJSON, error) {
	out := IDJSON{ID: row.ID, Strkey: row.Strkey, Kind: row.Kind, AddedBy: row.AddedBy, CreatedAt: httpx.Timestamp(row.CreatedAt)}
	now := h.now().UTC()
	err := h.scope.DB().WithSystem(ctx, func(tx pgx.Tx) error {
		args := []any{row.ID, a.TenantID, now}
		if o.err == "" {
			detail, err := json.Marshal(o.detail)
			if err != nil {
				return err
			}
			// an account proof holds only while its challenge is still the
			// open one, checked in the same statement that spends it
			tag, err := tx.Exec(ctx, `update cp.chain_ids set verify_method = $4, verified_at = $3, verified_via = $5, verify_detail = $6,
			       last_verify_at = $3, last_verify_error = null, challenge_nonce = null, challenge_expires_at = null
			     where id = $1 and tenant_id = $2 and ($7::text is null or challenge_nonce = $7)`,
				append(args, method, o.via, detail, o.spends)...)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				o.err = ErrNoChallenge
			}
		}
		if o.err != "" {
			tag, err := tx.Exec(ctx, `update cp.chain_ids set last_verify_at = $3, last_verify_error = $4 where id = $1 and tenant_id = $2`,
				append(args, o.err)...)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return httpx.NotFound()
			}
		}
		result := "ok"
		if o.err != "" {
			result = o.err
		}
		if err := audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: ActionVerify, Target: AuditTarget(row.ID),
			Detail: map[string]any{"tenant_id": a.TenantID.String(), "strkey": row.Strkey, "method": method, "result": result},
		}); err != nil {
			return err
		}
		var r idRow
		err := tx.QueryRow(ctx, `select verify_method, verified_at, verified_via, verify_detail, last_verify_at, last_verify_error
			from cp.chain_ids where id = $1`, row.ID).Scan(&r.Method, &r.VerifiedAt, &r.Via, &r.Detail, &r.LastVerifyAt, &r.LastVerifyErr)
		if err != nil {
			return err
		}
		out.Verification = Verification{
			Status: StatusUnverified, Method: r.Method, VerifiedAt: httpx.TimestampPtr(r.VerifiedAt), Via: r.Via,
			LastAttemptAt: httpx.TimestampPtr(r.LastVerifyAt), LastError: r.LastVerifyErr,
		}
		if r.VerifiedAt != nil {
			out.Verification.Status = StatusVerified
		}
		if len(r.Detail) > 0 {
			return json.Unmarshal(r.Detail, &out.Verification.Detail)
		}
		return nil
	})
	return out, err
}
