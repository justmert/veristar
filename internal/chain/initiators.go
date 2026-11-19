package chain

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Source returns the account that initiated a horizon operation. horizon
// fills source_account with the transaction's source when the operation
// names none, and with the underlying g account of a muxed source.
func (o Operation) Source() (string, bool) {
	v, ok := o.Fields["source_account"].(string)
	if !ok || len(v) != 56 || v[0] != 'G' {
		return "", false
	}
	return v, true
}

// OperationSource reads the account that initiated operation op of a
// transaction envelope (base64 xdr): the operation's own source, or the
// transaction's. a fee bump's inner transaction is the one that carries
// the operations; the fee payer initiates none of them. a muxed account
// counts as its underlying g account.
func OperationSource(envelopeXDR string, op uint32) (string, error) {
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(envelopeXDR, &env); err != nil {
		return "", fail(CodeBadResponse, "transaction envelope: %v", err)
	}
	switch env.Type {
	case xdr.EnvelopeTypeEnvelopeTypeTx, xdr.EnvelopeTypeEnvelopeTypeTxV0:
	case xdr.EnvelopeTypeEnvelopeTypeTxFeeBump:
		if env.FeeBump == nil || env.FeeBump.Tx.InnerTx.V1 == nil {
			return "", fail(CodeBadResponse, "fee bump envelope without an inner transaction")
		}
	default:
		return "", fail(CodeBadResponse, "envelope of type %d", env.Type)
	}
	ops := env.Operations()
	if int(op) >= len(ops) {
		return "", fail(CodeBadResponse, "operation %d of a transaction with %d", op, len(ops))
	}
	src := env.SourceAccount()
	if s := ops[op].SourceAccount; s != nil {
		src = *s
	}
	id := src.ToAccountId()
	addr, err := id.GetAddress()
	if err != nil {
		return "", fail(CodeBadResponse, "operation source: %v", err)
	}
	return addr, nil
}

// EnvelopeBatch is how many getTransaction calls one json-rpc batch
// carries. public stellar rpc servers refuse a batch of more than 10
// calls, and 10 answers with their metadata stay well under the client's
// body limit.
const EnvelopeBatch = 10

// TxRecord is the part of a getTransaction answer the reader uses: the
// envelope, for the initiator and the functions invoked at the top level,
// and the diagnostic events, for the calls between contracts. a server
// that does not record diagnostic events sends none.
type TxRecord struct {
	Status      string
	EnvelopeXDR string
	Diagnostics []string
}

// Transactions reads transactions by hash through getTransaction,
// EnvelopeBatch calls per json-rpc batch request. a hash the server no
// longer holds fails with before_source_history.
func (c *RPC) Transactions(ctx context.Context, hashes []string) (map[string]TxRecord, error) {
	out := make(map[string]TxRecord, len(hashes))
	for start := 0; start < len(hashes); start += EnvelopeBatch {
		part := hashes[start:min(start+EnvelopeBatch, len(hashes))]
		if err := c.transactionBatch(ctx, part, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c *RPC) transactionBatch(ctx context.Context, hashes []string, out map[string]TxRecord) error {
	reqs := make([]map[string]any, len(hashes))
	first := c.id.Add(int64(len(hashes))) - int64(len(hashes)) + 1
	for i, h := range hashes {
		reqs[i] = map[string]any{
			"jsonrpc": "2.0", "id": first + int64(i), "method": "getTransaction",
			"params": map[string]any{"hash": h, "xdrFormat": "base64"},
		}
	}
	body, err := json.Marshal(reqs)
	if err != nil {
		return err
	}
	r, err := c.ep.do(ctx, http.MethodPost, "", nil, body)
	if err != nil {
		return err
	}
	if r.status != http.StatusOK {
		return fail(CodeBadResponse, "getTransaction batch: http %d", r.status)
	}
	var answers []struct {
		ID     int64     `json:"id"`
		Error  *rpcError `json:"error"`
		Result *struct {
			Status      string   `json:"status"`
			TxHash      string   `json:"txHash"`
			EnvelopeXDR string   `json:"envelopeXdr"`
			Diagnostics []string `json:"diagnosticEventsXdr"`
		} `json:"result"`
	}
	if err := decode(r, &answers, "getTransaction batch"); err != nil {
		return err
	}
	if len(answers) != len(hashes) {
		return fail(CodeBadResponse, "getTransaction batch: %d answers to %d calls", len(answers), len(hashes))
	}
	seen := make([]bool, len(hashes))
	for _, a := range answers {
		i := a.ID - first
		if i < 0 || i >= int64(len(hashes)) || seen[i] {
			return fail(CodeBadResponse, "getTransaction batch: unexpected answer to call %d", a.ID)
		}
		seen[i] = true
		h := hashes[i]
		switch {
		case a.Error != nil:
			return fail(CodeBadResponse, "getTransaction: rpc error %d", a.Error.Code)
		case a.Result == nil:
			return fail(CodeBadResponse, "getTransaction: no result")
		case a.Result.Status == "NOT_FOUND":
			return fail(CodeBeforeHistory, "transaction %s is outside the server's window", h)
		case a.Result.EnvelopeXDR == "":
			return fail(CodeBadResponse, "getTransaction: %s answered without an envelope", h)
		}
		out[h] = TxRecord{Status: a.Result.Status, EnvelopeXDR: a.Result.EnvelopeXDR, Diagnostics: a.Result.Diagnostics}
	}
	return nil
}
