package chain

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// RPC calls a stellar rpc server over json-rpc 2.0. public mainnet servers
// keep events for a window of about seven days (ledgerRetentionWindow
// 120960 ledgers), so contract activity older than that is not readable
// from them.
type RPC struct {
	ep *Endpoint
	id atomic.Int64
}

// NewRPC wraps an endpoint.
func NewRPC(ep *Endpoint) *RPC { return &RPC{ep: ep} }

// Endpoint is the endpoint the reader uses.
func (c *RPC) Endpoint() *Endpoint { return c.ep }

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// call sends one request. a json-rpc error is a bad_response, except the
// range error of getEvents, which is before_source_history.
func (c *RPC) call(ctx context.Context, method string, params, into any) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": c.id.Add(1), "method": method, "params": params,
	})
	if err != nil {
		return err
	}
	r, err := c.ep.do(ctx, http.MethodPost, "", nil, body)
	if err != nil {
		return err
	}
	if r.status != http.StatusOK {
		return fail(CodeBadResponse, "%s: http %d", method, r.status)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := decode(r, &env, method); err != nil {
		return err
	}
	if env.Error != nil {
		if env.Error.Code == -32600 {
			return fail(CodeBeforeHistory, "%s: %s", method, cut(env.Error.Message, 200))
		}
		return fail(CodeBadResponse, "%s: rpc error %d", method, env.Error.Code)
	}
	if len(env.Result) == 0 {
		return fail(CodeBadResponse, "%s: no result", method)
	}
	return decode(response{status: r.status, body: env.Result}, into, method)
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Health is getHealth: the ledgers the server holds.
type Health struct {
	Status          string `json:"status"`
	LatestLedger    uint32 `json:"latestLedger"`
	OldestLedger    uint32 `json:"oldestLedger"`
	RetentionWindow uint32 `json:"ledgerRetentionWindow"`
}

// Health reads the server's state.
func (c *RPC) Health(ctx context.Context) (Health, error) {
	var h Health
	err := c.call(ctx, "getHealth", nil, &h)
	return h, err
}

// LedgerClose reads when a ledger closed, through getLedgers.
func (c *RPC) LedgerClose(ctx context.Context, seq uint32) (time.Time, error) {
	var res protocol.GetLedgersResponse
	err := c.call(ctx, "getLedgers", map[string]any{
		"startLedger": seq, "pagination": map[string]any{"limit": 1},
	}, &res)
	if err != nil {
		return time.Time{}, err
	}
	if len(res.Ledgers) == 0 || res.Ledgers[0].Sequence != seq {
		return time.Time{}, fail(CodeBadResponse, "getLedgers did not return ledger %d", seq)
	}
	return time.Unix(res.Ledgers[0].LedgerCloseTime, 0).UTC(), nil
}

// Event is one contract event of getEvents.
type Event struct {
	Ledger         uint32   `json:"ledger"`
	LedgerClosedAt string   `json:"ledgerClosedAt"`
	ContractID     string   `json:"contractId"`
	ID             string   `json:"id"`
	OpIndex        uint32   `json:"operationIndex"`
	TxHash         string   `json:"txHash"`
	Successful     *bool    `json:"inSuccessfulContractCall"`
	Topic          []string `json:"topic"`
}

// EventPage is one getEvents answer.
type EventPage struct {
	Events       []Event `json:"events"`
	Cursor       string  `json:"cursor"`
	LatestLedger uint32  `json:"latestLedger"`
	OldestLedger uint32  `json:"oldestLedger"`
	// unix seconds, sent as strings
	LatestLedgerCloseTime string `json:"latestLedgerCloseTime"`
}

// LatestClose is when the newest ledger the server holds closed.
func (p EventPage) LatestClose() (time.Time, bool) {
	n, err := strconv.ParseInt(p.LatestLedgerCloseTime, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(n, 0).UTC(), true
}

// EventLimit is the page size of getEvents; servers allow up to 10000.
const EventLimit = 1000

// ContractEvents reads one page of a contract's events, from startLedger
// on the first call and after cursor on later ones.
func (c *RPC) ContractEvents(ctx context.Context, contract string, startLedger uint32, cursor string) (EventPage, error) {
	pg := map[string]any{"limit": EventLimit}
	params := map[string]any{
		"filters":   []map[string]any{{"type": "contract", "contractIds": []string{contract}}},
		"xdrFormat": "base64",
	}
	if cursor != "" {
		pg["cursor"] = cursor
	} else {
		params["startLedger"] = startLedger
	}
	params["pagination"] = pg
	var p EventPage
	err := c.call(ctx, "getEvents", params, &p)
	return p, err
}

// Instance reads a contract's instance ledger entry, base64 xdr, or
// CodeNotFound when the contract does not exist (or its instance expired).
func (c *RPC) Instance(ctx context.Context, contract string) (string, error) {
	key, err := InstanceKey(contract)
	if err != nil {
		return "", err
	}
	var res protocol.GetLedgerEntriesResponse
	if err := c.call(ctx, "getLedgerEntries", map[string]any{"keys": []string{key}}, &res); err != nil {
		return "", err
	}
	if len(res.Entries) == 0 {
		return "", fail(CodeNotFound, "contract %s has no instance entry", contract)
	}
	return res.Entries[0].DataXDR, nil
}

// TopicAddresses returns the g and c addresses inside an event's topics.
func (e Event) TopicAddresses() []string {
	var out []string
	for _, t := range e.Topic {
		var v xdr.ScVal
		if err := xdr.SafeUnmarshalBase64(t, &v); err != nil {
			continue
		}
		a, ok := v.GetAddress()
		if !ok {
			continue
		}
		s, err := a.String()
		if err != nil || len(s) != 56 || (s[0] != 'G' && s[0] != 'C') {
			continue
		}
		out = append(out, s)
	}
	return out
}
