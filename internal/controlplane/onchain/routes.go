package onchain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/justmert/veristar/internal/chain"
	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/tenants"
)

// limits of contract 15.3 to 15.6
const (
	// MaxIDs is how many ids a project may declare; enrollment allows 20
	MaxIDs = 20
	// DefaultRangeDays and MaxRangeDays bound GET .../onchain
	DefaultRangeDays = 30
	MaxRangeDays     = 366
	// ChallengeTTL is how long a challenge can be signed or published
	ChallengeTTL = 30 * time.Minute
	// verifyLimit console checks per id and verifyWindow
	verifyLimit  = 10
	verifyWindow = time.Hour
	// ManageDataName is the account data entry of the manage-data proof
	ManageDataName = "veristar"
)

// audit actions of contract 15
const (
	ActionAdd       = "onchain.add"
	ActionRemove    = "onchain.remove"
	ActionChallenge = "onchain.challenge"
	ActionVerify    = "onchain.verify"
)

// verify methods (cp.chain_ids.verify_method)
const (
	MethodSEP53            = "sep53"
	MethodManageData       = "manage_data"
	MethodContractAdmin    = "contract_admin"
	MethodContractDeployer = "contract_deployer"
)

// verify error codes (cp.chain_ids.last_verify_error), next to the read
// codes of package chain
const (
	ErrNoChallenge      = "no_challenge"
	ErrChallengeExpired = "challenge_expired"
	ErrBadSignature     = "bad_signature"
	ErrDataMismatch     = "data_entry_mismatch"
	ErrNoVerifiedOwner  = "no_verified_owner"
	ErrNotDeployer      = "deployer_not_verified"
	ErrNotInTransaction = "contract_not_created_in_transaction"
	ErrTxFailed         = "transaction_failed"
	ErrNoAdmin          = "no_admin_entry"
	ErrAdminNotVerified = "admin_not_verified"
	// ErrMasterNoWeight: the sep-53 signature holds, but the account's own
	// key cannot authorise a medium threshold operation, so it does not
	// prove control; the manage-data method proves it through the signers
	ErrMasterNoWeight = "master_key_no_weight"
	// ErrViaRemoved: the id was proven through a verified id the project
	// removed since; the proof no longer holds and the id is unverified
	ErrViaRemoved = "verified_through_removed_id"
	// ErrFactoryCreated: the transaction creates no contract at its top
	// level but calls one, so a factory contract may have created the id;
	// such a creation is not read, and the admin method proves the id
	ErrFactoryCreated = "contract_created_by_contract"
)

// AuditTarget names a declared id in the audit log.
func AuditTarget(id uuid.UUID) string { return "chain_id:" + id.String() }

// Routes serves the tenant routes of contract 15.
type Routes struct {
	scope   *tenants.Scope
	horizon *chain.Horizon
	rpc     *chain.RPC
	network string
	console string
	now     func() time.Time
}

// NewRoutes needs the scope, both ledger apis, which the proofs read, and
// the console origin, whose host the challenge message names.
func NewRoutes(s *tenants.Scope, h *chain.Horizon, r *chain.RPC, consoleOrigin string, now func() time.Time) (*Routes, error) {
	if s == nil || h == nil || r == nil || now == nil {
		return nil, errors.New("onchain: scope, horizon, rpc and clock are required")
	}
	u, err := url.Parse(consoleOrigin)
	if err != nil || u.Host == "" {
		return nil, errors.New("onchain: the console origin must be an absolute url")
	}
	return &Routes{scope: s, horizon: h, rpc: r, network: chain.PublicNetwork, console: u.Host, now: now}, nil
}

// Routes registers the paths under a router mounted at /api.
func (h *Routes) Routes(r chi.Router) {
	p := "/tenants/{tenant_id}/onchain"
	r.Method(http.MethodGet, p, httpx.HandlerFunc(h.view))
	r.Method(http.MethodGet, p+"/ids/{chain_id}/functions", httpx.HandlerFunc(h.functions))
	r.Method(http.MethodPost, p+"/ids", httpx.HandlerFunc(h.add))
	r.Method(http.MethodDelete, p+"/ids/{chain_id}", httpx.HandlerFunc(h.remove))
	r.Method(http.MethodPost, p+"/ids/{chain_id}/challenge", httpx.HandlerFunc(h.challenge))
	r.Method(http.MethodPost, p+"/ids/{chain_id}/verify", httpx.HandlerFunc(h.verify))
}

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ParseRange reads from and to as utc days, both included: without to the
// range ends today, without from it holds def days; to after today, to
// before from and more than maxDays days are 400 invalid_query.
func ParseRange(q url.Values, now time.Time, def, maxDays int) (Range, error) {
	today := utcDay(now)
	read := func(key string) (time.Time, bool, error) {
		vs, ok := q[key]
		if !ok {
			return time.Time{}, false, nil
		}
		if len(vs) != 1 {
			return time.Time{}, false, httpx.BadQuery(key + " may be given once")
		}
		d, err := time.Parse(time.DateOnly, vs[0])
		if err != nil {
			return time.Time{}, false, httpx.BadQuery(key + " must be a date yyyy-mm-dd")
		}
		return d, true, nil
	}
	from, hasFrom, err := read("from")
	if err != nil {
		return Range{}, err
	}
	to, hasTo, err := read("to")
	if err != nil {
		return Range{}, err
	}
	if !hasTo {
		to = today
	}
	if !hasFrom {
		from = to.AddDate(0, 0, -(def - 1))
	}
	switch {
	case to.After(today):
		return Range{}, httpx.BadQuery("to must not be after today (utc)")
	case to.Before(from):
		return Range{}, httpx.BadQuery("to must not be before from")
	case int(to.Sub(from).Hours()/24)+1 > maxDays:
		return Range{}, httpx.BadQuery(fmt.Sprintf("the range may hold at most %d days", maxDays))
	}
	return Range{From: from, To: to}, nil
}

func (h *Routes) view(w http.ResponseWriter, r *http.Request) error {
	a, err := h.scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	now := h.now()
	rg, err := ParseRange(r.URL.Query(), now, DefaultRangeDays, MaxRangeDays)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var v View
	err = h.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		var lerr error
		v, lerr = Load(ctx, tx, TenantSites, a.TenantID, rg, utcDay(now))
		return lerr
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, v)
	return nil
}

// functions serves GET .../onchain/ids/{chain_id}/functions: one declared
// id's calls per function, caller and day (contract 15.8). an id of
// another tenant is 404.
func (h *Routes) functions(w http.ResponseWriter, r *http.Request) error {
	a, err := h.scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	id, err := httpx.ParseID(chi.URLParam(r, "chain_id"))
	if err != nil {
		return err
	}
	now := h.now()
	rg, err := ParseRange(r.URL.Query(), now, DefaultRangeDays, MaxRangeDays)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out ContractFunctions
	err = h.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		v, ok, lerr := LoadFunctions(ctx, tx, a.TenantID, id, rg, utcDay(now))
		if lerr != nil {
			return lerr
		}
		if !ok {
			return httpx.NotFound()
		}
		out = v
		return nil
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// writable refuses changes while the terms are not accepted or the
// project is suspended, as the site routes do.
func writable(a tenants.Access) error {
	if err := a.RequireTerms(); err != nil {
		return err
	}
	if a.TenantStatus == "suspended" {
		return httpx.Conflict(httpx.CodeTenantSuspended, "the project is suspended")
	}
	return nil
}

type addBody struct {
	Strkey *string `json:"strkey"`
}

// Kind returns account or contract for a g or c strkey, or "".
func Kind(s string) string {
	switch {
	case strkey.IsValidEd25519PublicKey(s):
		return "account"
	case strkey.IsValidContractAddress(s):
		return "contract"
	}
	return ""
}

// IDJSON is the answer of the add, challenge and verify routes: the id
// with its proof state (contract 15.3).
type IDJSON struct {
	ID           uuid.UUID       `json:"id"`
	Strkey       string          `json:"strkey"`
	Kind         string          `json:"kind"`
	AddedBy      string          `json:"added_by"`
	CreatedAt    httpx.Timestamp `json:"created_at"`
	Verification Verification    `json:"verification"`
}

func (h *Routes) add(w http.ResponseWriter, r *http.Request) error {
	a, err := h.scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	var b addBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	if err := writable(a); err != nil {
		return err
	}
	if b.Strkey == nil {
		return httpx.Validation("strkey is required", map[string]string{"strkey": httpx.FieldRequired})
	}
	kind := Kind(*b.Strkey)
	if kind == "" {
		// m (muxed) ids are refused: their activity is the g account's
		return httpx.Validation("strkey must be a stellar account (G...) or contract (C...)",
			map[string]string{"strkey": httpx.FieldInvalidStrkey})
	}
	ctx := r.Context()
	var row IDJSON
	err = h.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		// one add at a time per tenant, so the count below holds
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('onchain.ids:' || $1::text, 0))`, a.TenantID.String()); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from cp.chain_ids`).Scan(&n); err != nil {
			return err
		}
		if n >= MaxIDs {
			return httpx.Validation(fmt.Sprintf("a project may declare at most %d accounts and contracts", MaxIDs),
				map[string]string{"strkey": httpx.FieldTooMany})
		}
		var at time.Time
		err := tx.QueryRow(ctx, `insert into cp.chain_ids (tenant_id, strkey, kind, added_by) values ($1, $2, $3, 'console')
			returning id, created_at`, a.TenantID, *b.Strkey, kind).Scan(&row.ID, &at)
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return httpx.Validation("the project already declares this id", map[string]string{"strkey": httpx.FieldDuplicate})
		}
		if err != nil {
			return err
		}
		row.Strkey, row.Kind, row.AddedBy, row.CreatedAt = *b.Strkey, kind, "console", httpx.Timestamp(at)
		row.Verification = Verification{Status: StatusUnverified}
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: ActionAdd, Target: AuditTarget(row.ID),
			Detail: map[string]any{"tenant_id": a.TenantID.String(), "strkey": row.Strkey, "kind": kind},
		})
	})
	if err != nil {
		return err
	}
	httpx.Created(w, "/api/tenants/"+a.TenantID.String()+"/onchain", row)
	return nil
}

// loadOne reads one id of the caller's tenant, or 404.
func (h *Routes) loadOne(r *http.Request) (tenants.Access, idRow, error) {
	a, err := h.scope.Tenant(r, "tenant_id")
	if err != nil {
		return a, idRow{}, err
	}
	id, err := httpx.ParseID(chi.URLParam(r, "chain_id"))
	if err != nil {
		return a, idRow{}, err
	}
	ctx := r.Context()
	var row idRow
	err = h.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		rows, err := loadIDs(ctx, tx, a.TenantID)
		if err != nil {
			return err
		}
		for _, x := range rows {
			if x.ID == id {
				row = x
				return nil
			}
		}
		return httpx.NotFound()
	})
	return a, row, err
}

func (h *Routes) remove(w http.ResponseWriter, r *http.Request) error {
	a, row, err := h.loadOne(r)
	if err != nil {
		return err
	}
	if err := a.RequireOwner(); err != nil {
		return err
	}
	if err := writable(a); err != nil {
		return err
	}
	ctx := r.Context()
	// a contract proven through this id, and anything proven through that
	// contract, loses its proof first: the proof rested on this id being
	// the project's. done before the removal, so a failure in between
	// leaves fewer verified ids, never more
	if err := h.unverifyDerived(ctx, a, row); err != nil {
		return err
	}
	err = h.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `delete from cp.chain_ids where id = $1`, row.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound()
		}
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: ActionRemove, Target: AuditTarget(row.ID),
			Detail: map[string]any{"tenant_id": a.TenantID.String(), "strkey": row.Strkey, "kind": row.Kind},
		})
	})
	if err != nil {
		return err
	}
	// a contract proof that read the ledger while this id was removed
	// either saw the id gone or held its row until it committed (store);
	// in the second case its proof rests on the removed id, so it is
	// cleared now
	if err := h.unverifyDerived(ctx, a, row); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

// unverifyDerived clears the proof of every id verified through row,
// directly or through another such id, under cp_system where proofs are
// written, with an audit row each.
func (h *Routes) unverifyDerived(ctx context.Context, a tenants.Access, row idRow) error {
	return h.scope.DB().WithSystem(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			with recursive via(strkey) as (
			  select $2::text
			  union
			  select c.strkey from cp.chain_ids c join via on c.verified_via = via.strkey
			  where c.tenant_id = $1 and c.verified_at is not null
			)
			update cp.chain_ids set verify_method = null, verified_at = null, verified_via = null, verify_detail = null,
			       last_verify_at = now(), last_verify_error = $3
			where tenant_id = $1 and verified_at is not null and strkey in (select strkey from via) and strkey <> $2
			returning id, strkey`, a.TenantID, row.Strkey, ErrViaRemoved)
		if err != nil {
			return err
		}
		type cleared struct {
			ID     uuid.UUID
			Strkey string
		}
		list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[cleared])
		if err != nil {
			return err
		}
		for _, c := range list {
			if err := audit.InSystem(ctx, tx, audit.Entry{
				Actor: audit.User(a.User.ID), Action: ActionVerify, Target: AuditTarget(c.ID),
				Detail: map[string]any{"tenant_id": a.TenantID.String(), "strkey": c.Strkey, "result": ErrViaRemoved,
					"removed": row.Strkey},
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Challenge is the answer of POST .../challenge (contract 15.4.1): the
// message a wallet signs with sep-53, and the data entry that proves the
// same through a manage-data operation instead.
type Challenge struct {
	ID         uuid.UUID       `json:"id"`
	Account    string          `json:"account"`
	Message    string          `json:"message"`
	ManageData ManageData      `json:"manage_data"`
	ExpiresAt  httpx.Timestamp `json:"expires_at"`
}

// ManageData is the account data entry of the manage-data proof.
type ManageData struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ChallengeMessage is the exact text signed for a challenge. it names the
// platform, the console host, the project and the account, so a signature
// cannot be reused for another project, account or challenge, and a wallet
// shows the signer which project on which console asks. the name is quoted,
// so a name cannot add a line of its own.
func ChallengeMessage(console string, tenant uuid.UUID, name, account, nonce string, expires time.Time) string {
	return "Veristar ownership proof\n" +
		"Console: " + console + "\n" +
		"Project: " + strconv.Quote(name) + " (" + tenant.String() + ")\n" +
		"Account: " + account + "\n" +
		"Nonce: " + nonce + "\n" +
		"Expires: " + expires.UTC().Format(time.RFC3339)
}

func (h *Routes) challenge(w http.ResponseWriter, r *http.Request) error {
	a, row, err := h.loadOne(r)
	if err != nil {
		return err
	}
	if err := writable(a); err != nil {
		return err
	}
	if row.Kind != "account" {
		return httpx.Validation("a contract is proven through its admin or its deployer, not a challenge",
			map[string]string{"chain_id": httpx.FieldInvalidValue})
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	nonce := hex.EncodeToString(raw[:])
	expires := h.now().UTC().Add(ChallengeTTL).Truncate(time.Second)
	ctx := r.Context()
	name, err := h.projectName(ctx, a)
	if err != nil {
		return err
	}
	// the challenge columns are cp_system's, like every proof column; the
	// id was read under the member's tenant scope
	err = h.scope.DB().WithSystem(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `update cp.chain_ids set challenge_nonce = $1, challenge_expires_at = $2
			where id = $3 and tenant_id = $4`, nonce, expires, row.ID, a.TenantID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound()
		}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: ActionChallenge, Target: AuditTarget(row.ID),
			Detail: map[string]any{"tenant_id": a.TenantID.String(), "strkey": row.Strkey},
		})
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, Challenge{
		ID: row.ID, Account: row.Strkey, Message: ChallengeMessage(h.console, a.TenantID, name, row.Strkey, nonce, expires),
		ManageData: ManageData{Name: ManageDataName, Value: nonce}, ExpiresAt: httpx.Timestamp(expires),
	})
	return nil
}

// projectName reads the project's name for the challenge message.
func (h *Routes) projectName(ctx context.Context, a tenants.Access) (string, error) {
	var name string
	err := h.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select name from cp.tenants where id = $1`, a.TenantID).Scan(&name)
	})
	return name, err
}

// limit answers 429 after verifyLimit console checks of an id within the
// window, counted from the audit rows the route writes.
func (h *Routes) limit(ctx context.Context, id uuid.UUID) error {
	now := h.now()
	var n int
	var oldest time.Time
	err := h.scope.DB().WithSystem(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select count(*), coalesce(min(at), now()) from cp.audit_log
			where target = $1 and action = $2 and at > $3`, AuditTarget(id), ActionVerify, now.Add(-verifyWindow)).Scan(&n, &oldest)
	})
	if err != nil {
		return fmt.Errorf("onchain: rate count: %w", err)
	}
	if n < verifyLimit {
		return nil
	}
	wait := max(oldest.Add(verifyWindow).Sub(now), time.Second)
	return httpx.RateLimited(wait)
}
