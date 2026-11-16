package chain

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Horizon reads accounts, operations, transactions and ledgers from a
// horizon instance. sdf's public instance keeps about one year of history
// and answers 410 before_history for older ledgers.
type Horizon struct{ ep *Endpoint }

// NewHorizon wraps an endpoint.
func NewHorizon(ep *Endpoint) *Horizon { return &Horizon{ep: ep} }

// Endpoint is the endpoint the reader uses.
func (h *Horizon) Endpoint() *Endpoint { return h.ep }

func (h *Horizon) get(ctx context.Context, path string, q url.Values, into any, what string) error {
	r, err := h.ep.do(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return err
	}
	switch {
	case r.status == http.StatusNotFound:
		return fail(CodeNotFound, "%s: not found", what)
	case r.status == http.StatusGone:
		return fail(CodeBeforeHistory, "%s: before the history the source keeps", what)
	case r.status != http.StatusOK:
		return fail(CodeBadResponse, "%s: http %d", what, r.status)
	}
	return decode(r, into, what)
}

// Root is the part of horizon's root document the reader needs.
type Root struct {
	LatestLedger int64 `json:"history_latest_ledger"`
	ElderLedger  int64 `json:"history_elder_ledger"`
}

// Root reads the ledger range horizon holds.
func (h *Horizon) Root(ctx context.Context) (Root, error) {
	var r Root
	err := h.get(ctx, "/", nil, &r, "root")
	return r, err
}

// Account is the part of an account record the witness uses.
type Account struct {
	ID         string            `json:"account_id"`
	HomeDomain string            `json:"home_domain"`
	Data       map[string]string `json:"data"`
}

// DataValue returns a decoded manage-data entry.
func (a Account) DataValue(name string) (string, bool) {
	v, ok := a.Data[name]
	if !ok {
		return "", false
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// Account reads an account. an account that does not exist on the ledger
// fails with CodeAccountMissing.
func (h *Horizon) Account(ctx context.Context, id string) (Account, error) {
	var a Account
	err := h.get(ctx, "/accounts/"+url.PathEscape(id), nil, &a, "account")
	if Code(err) == CodeNotFound {
		return a, fail(CodeAccountMissing, "account %s does not exist on the ledger", id)
	}
	return a, err
}

// Ledger is one ledger header.
type Ledger struct {
	Sequence int64     `json:"sequence"`
	ClosedAt time.Time `json:"closed_at"`
}

// Ledger reads one ledger header.
func (h *Horizon) Ledger(ctx context.Context, seq int64) (Ledger, error) {
	var l Ledger
	err := h.get(ctx, "/ledgers/"+strconv.FormatInt(seq, 10), nil, &l, "ledger")
	return l, err
}

// Operation is one operation record. Fields keeps the type-specific
// fields, from which the counterparties are read.
type Operation struct {
	PagingToken string
	TxHash      string
	CreatedAt   time.Time
	Fields      map[string]any
}

type opPage struct {
	Embedded struct {
		Records []map[string]any `json:"records"`
	} `json:"_embedded"`
}

// MaxPage is the largest page horizon serves.
const MaxPage = 200

// AccountOperations reads one page of an account's successful operations
// in ledger order after cursor (a paging token).
func (h *Horizon) AccountOperations(ctx context.Context, account, cursor string) ([]Operation, error) {
	q := url.Values{"order": {"asc"}, "limit": {strconv.Itoa(MaxPage)}, "include_failed": {"false"}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var p opPage
	if err := h.get(ctx, "/accounts/"+url.PathEscape(account)+"/operations", q, &p, "operations"); err != nil {
		if Code(err) == CodeNotFound {
			return nil, fail(CodeAccountMissing, "account %s does not exist on the ledger", account)
		}
		return nil, err
	}
	out := make([]Operation, 0, len(p.Embedded.Records))
	for _, r := range p.Embedded.Records {
		tok, _ := r["paging_token"].(string)
		hash, _ := r["transaction_hash"].(string)
		at, _ := r["created_at"].(string)
		t, err := time.Parse(time.RFC3339, at)
		if tok == "" || hash == "" || err != nil {
			return nil, fail(CodeBadResponse, "operation record without paging_token, transaction_hash or created_at")
		}
		out = append(out, Operation{PagingToken: tok, TxHash: hash, CreatedAt: t.UTC(), Fields: r})
	}
	return out, nil
}

// Transaction is the part of a transaction record the deployer proof uses.
type Transaction struct {
	Hash        string `json:"hash"`
	Successful  bool   `json:"successful"`
	EnvelopeXDR string `json:"envelope_xdr"`
}

// Transaction reads one transaction by hash.
func (h *Horizon) Transaction(ctx context.Context, hash string) (Transaction, error) {
	var t Transaction
	err := h.get(ctx, "/transactions/"+url.PathEscape(hash), nil, &t, "transaction")
	return t, err
}

// counterpartyFields are the operation fields that name another account:
// payments and path payments (from, to), account creation (funder,
// account), merges (account, into), trust lines (trustor, trustee),
// claimable balances and sponsorship (claimant, sponsor), offers and
// trades (seller, buyer), and the operation's source.
var counterpartyFields = []string{
	"source_account", "from", "to", "funder", "account", "into", "trustor", "trustee",
	"claimant", "sponsor", "seller", "buyer", "sponsored_id", "begin_sponsor",
}

// Counterparties returns the g accounts an operation names other than
// self.
func (o Operation) Counterparties(self string) []string {
	var out []string
	for _, f := range counterpartyFields {
		v, ok := o.Fields[f].(string)
		if !ok || v == self || len(v) != 56 || v[0] != 'G' {
			continue
		}
		out = append(out, v)
	}
	return out
}
