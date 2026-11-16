package chain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/require"
)

// the test vectors of sep-53 1.0.0
const (
	sepAccount = "GBXFXNDLV4LSWA4VB7YIL5GBD7BVNR22SGBTDKMO2SBZZHDXSKZYCP7L"
	sepSeed    = "SAKICEVQLYWGSOJS4WW7HZJWAHZVEEBS527LHK5V4MLJALYKICQCJXMW"
)

func TestSEP53Vectors(t *testing.T) {
	require.NoError(t, VerifySEP53(sepAccount, "Hello, World!",
		"fO5dbYhXUhBMhe6kId/cuVq/AfEnHRHEvsP8vXh03M1uLpi5e46yO2Q8rEBzu3feXQewcQE5GArp88u6ePK6BA=="))
	require.NoError(t, VerifySEP53(sepAccount, "こんにちは、世界！",
		"CDU265Xs8y3OWbB/56H9jPgUss5G9A0qFuTqH2zs2YDgTm+++dIfmAEceFqB7bhfN3am59lCtDXrCtwH2k1GBA=="))
	// the same signature in hex
	raw, err := base64.StdEncoding.DecodeString("fO5dbYhXUhBMhe6kId/cuVq/AfEnHRHEvsP8vXh03M1uLpi5e46yO2Q8rEBzu3feXQewcQE5GArp88u6ePK6BA==")
	require.NoError(t, err)
	require.NoError(t, VerifySEP53(sepAccount, "Hello, World!", fmt.Sprintf("%x", raw)))

	// another message, another account, garbage and a transaction-style
	// signature over the raw message all fail
	require.ErrorIs(t, VerifySEP53(sepAccount, "Hello, World?",
		"fO5dbYhXUhBMhe6kId/cuVq/AfEnHRHEvsP8vXh03M1uLpi5e46yO2Q8rEBzu3feXQewcQE5GArp88u6ePK6BA=="), ErrBadSignature)
	other := keypair.MustRandom()
	require.ErrorIs(t, VerifySEP53(other.Address(), "Hello, World!",
		"fO5dbYhXUhBMhe6kId/cuVq/AfEnHRHEvsP8vXh03M1uLpi5e46yO2Q8rEBzu3feXQewcQE5GArp88u6ePK6BA=="), ErrBadSignature)
	require.ErrorIs(t, VerifySEP53(sepAccount, "x", "not a signature"), ErrBadSignature)
	kp := keypair.MustParseFull(sepSeed)
	direct, err := kp.Sign([]byte("Hello, World!"))
	require.NoError(t, err)
	require.ErrorIs(t, VerifySEP53(sepAccount, "Hello, World!", base64.StdEncoding.EncodeToString(direct)), ErrBadSignature)
	require.Error(t, VerifySEP53("CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAHK3M", "x", "y"))
}

func accountAddress(t *testing.T, g string) xdr.ScAddress {
	t.Helper()
	var id xdr.AccountId
	require.NoError(t, id.SetAddress(g))
	return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &id}
}

func TestDeployments(t *testing.T) {
	deployer := keypair.MustRandom().Address()
	var salt xdr.Uint256
	copy(salt[:], []byte("veristar-test-salt-0000000000000"))
	want, err := ContractIDFromAddress(PublicNetwork, accountAddress(t, deployer), salt)
	require.NoError(t, err)
	require.True(t, strkey.IsValidContractAddress(want))
	// a different network or salt gives a different id
	other, err := ContractIDFromAddress("Test SDF Network ; September 2015", accountAddress(t, deployer), salt)
	require.NoError(t, err)
	require.NotEqual(t, want, other)

	var src xdr.MuxedAccount
	require.NoError(t, src.SetAddress(deployer))
	wasm := xdr.Hash{1}
	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{Tx: xdr.Transaction{
			SourceAccount: src, Fee: 100, SeqNum: 1,
			Cond: xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
			Operations: []xdr.Operation{{Body: xdr.OperationBody{
				Type: xdr.OperationTypeInvokeHostFunction,
				InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{HostFunction: xdr.HostFunction{
					Type: xdr.HostFunctionTypeHostFunctionTypeCreateContractV2,
					CreateContractV2: &xdr.CreateContractArgsV2{
						ContractIdPreimage: xdr.ContractIdPreimage{
							Type:        xdr.ContractIdPreimageTypeContractIdPreimageFromAddress,
							FromAddress: &xdr.ContractIdPreimageFromAddress{Address: accountAddress(t, deployer), Salt: salt},
						},
						Executable: xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &wasm},
					},
				}},
			}}},
		}},
	}
	b64, err := xdr.MarshalBase64(env)
	require.NoError(t, err)
	got, err := Deployments(PublicNetwork, b64)
	require.NoError(t, err)
	require.Equal(t, []Deployment{{Contract: want, Deployer: deployer}}, got)

	_, err = Deployments(PublicNetwork, "AAAA")
	require.Error(t, err)
}

func instanceEntry(t *testing.T, contract string, storage xdr.ScMap) string {
	t.Helper()
	raw := strkey.MustDecode(strkey.VersionByteContract, contract)
	var cid xdr.ContractId
	copy(cid[:], raw)
	wasm := xdr.Hash{2}
	data := xdr.LedgerEntryData{
		Type: xdr.LedgerEntryTypeContractData,
		ContractData: &xdr.ContractDataEntry{
			Contract:   xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &cid},
			Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
			Durability: xdr.ContractDataDurabilityPersistent,
			Val: xdr.ScVal{Type: xdr.ScValTypeScvContractInstance, Instance: &xdr.ScContractInstance{
				Executable: xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &wasm},
				Storage:    &storage,
			}},
		},
	}
	b64, err := xdr.MarshalBase64(data)
	require.NoError(t, err)
	return b64
}

func sym(s string) xdr.ScVal {
	v := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &v}
}

func addrVal(t *testing.T, g string) xdr.ScVal {
	a := accountAddress(t, g)
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &a}
}

func TestAdmins(t *testing.T) {
	contract, err := strkey.Encode(strkey.VersionByteContract, make([]byte, 32))
	require.NoError(t, err)
	admin := keypair.MustRandom().Address()
	owner := keypair.MustRandom().Address()
	vec := xdr.ScVec{sym("Owner")}
	pvec := &vec
	n := xdr.Uint32(7)
	got, err := Admins(instanceEntry(t, contract, xdr.ScMap{
		{Key: sym("Admin"), Val: addrVal(t, admin)},
		{Key: xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pvec}, Val: addrVal(t, owner)},
		{Key: sym("Counter"), Val: xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &n}},
		// an admin key that holds no address is not an admin
		{Key: sym("admin"), Val: xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &n}},
	}))
	require.NoError(t, err)
	require.Equal(t, []Admin{{Key: "Admin", Address: admin}, {Key: "Owner", Address: owner}}, got)

	_, err = Admins(instanceEntry(t, contract, xdr.ScMap{{Key: sym("Counter"), Val: xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &n}}}))
	require.ErrorIs(t, err, ErrNoAdmin)
	key, err := InstanceKey(contract)
	require.NoError(t, err)
	require.NotEmpty(t, key)
	_, err = InstanceKey(admin)
	require.Error(t, err)
}

func instant(context.Context, time.Duration) error { return nil }

func testEndpoint(t *testing.T, h http.Handler) *Endpoint {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ep, err := NewEndpoint(srv.URL, 1000, "veristar-test")
	require.NoError(t, err)
	return ep.WithClient(srv.Client()).WithSleep(instant)
}

func TestRetryAndBackoff(t *testing.T) {
	var calls atomic.Int32
	ep := testEndpoint(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"history_latest_ledger": 10, "history_elder_ledger": 2}`))
	}))
	root, err := NewHorizon(ep).Root(t.Context())
	require.NoError(t, err)
	require.Equal(t, Root{LatestLedger: 10, ElderLedger: 2}, root)
	require.Equal(t, int32(3), calls.Load())

	always := testEndpoint(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	_, err = NewHorizon(always).Root(t.Context())
	require.Equal(t, CodeRateLimited, Code(err))

	down := testEndpoint(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	_, err = NewHorizon(down).Root(t.Context())
	require.Equal(t, CodeUnavailable, Code(err))

	gone := testEndpoint(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	_, err = NewHorizon(gone).Ledger(t.Context(), 1)
	require.Equal(t, CodeBeforeHistory, Code(err))

	missing := testEndpoint(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	_, err = NewHorizon(missing).Account(t.Context(), sepAccount)
	require.Equal(t, CodeAccountMissing, Code(err))

	require.GreaterOrEqual(t, backoff(1, &retryAfter{status: 429, wait: 90 * time.Second}), 90*time.Second)
	require.LessOrEqual(t, backoff(30, nil), maxBackoff*3/2)
	require.Equal(t, 5*time.Second, parseRetryAfter("5"))
}

// fakeHorizon serves ledgers that close every 6 seconds from base and an
// operation list for one account, paged by paging token.
type fakeHorizon struct {
	base    time.Time
	latest  int64
	ops     []map[string]any
	pages   atomic.Int32
	failOps bool
}

func (f *fakeHorizon) closeOf(seq int64) time.Time {
	return f.base.Add(time.Duration(seq) * 6 * time.Second)
}

func (f *fakeHorizon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/":
		_ = json.NewEncoder(w).Encode(map[string]any{"history_latest_ledger": f.latest, "history_elder_ledger": 1})
	case len(r.URL.Path) > 9 && r.URL.Path[:9] == "/ledgers/":
		seq, _ := strconv.ParseInt(r.URL.Path[9:], 10, 64)
		_ = json.NewEncoder(w).Encode(map[string]any{"sequence": seq, "closed_at": f.closeOf(seq).Format(time.RFC3339)})
	default:
		f.pages.Add(1)
		if f.failOps {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		cur, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
		var page []map[string]any
		for _, op := range f.ops {
			tok, _ := strconv.ParseInt(op["paging_token"].(string), 10, 64)
			if tok > cur && len(page) < MaxPage {
				page = append(page, op)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"_embedded": map[string]any{"records": page}})
	}
}

func (f *fakeHorizon) op(seq int64, idx int, tx string, fields map[string]any) {
	m := map[string]any{
		"paging_token":     strconv.FormatInt(seq<<32+int64(idx), 10),
		"transaction_hash": tx,
		"created_at":       f.closeOf(seq).Format(time.RFC3339),
	}
	for k, v := range fields {
		m[k] = v
	}
	f.ops = append(f.ops, m)
}

func TestAccountDays(t *testing.T) {
	self := keypair.MustRandom().Address()
	a, b := keypair.MustRandom().Address(), keypair.MustRandom().Address()
	day1 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	f := &fakeHorizon{base: day1.AddDate(0, 0, -2), latest: 6 * 14400}
	perDay := int64(14400) // 86400 / 6
	// day -1: outside the range, ignored
	f.op(perDay+10, 1, "t0", map[string]any{"source_account": a})
	// day 1: two operations of one transaction and one more transaction
	f.op(2*perDay+5, 1, "t1", map[string]any{"source_account": self, "to": a, "from": self})
	f.op(2*perDay+5, 2, "t1", map[string]any{"source_account": self, "to": b})
	f.op(2*perDay+900, 1, "t2", map[string]any{"source_account": a, "funder": a, "account": self})
	// day 2: nothing; day 3: one
	f.op(4*perDay+1, 1, "t3", map[string]any{"source_account": self, "trustor": self})
	// day 4: after the range
	f.op(5*perDay+1, 1, "t4", map[string]any{"source_account": b})

	h := NewHorizon(testEndpoint(t, f))
	r, err := NewReader(h, NewRPC(testEndpoint(t, http.NotFoundHandler())))
	require.NoError(t, err)
	root, err := h.Root(t.Context())
	require.NoError(t, err)
	got, err := r.AccountDays(t.Context(), self, day1, day1.AddDate(0, 0, 2), root)
	require.NoError(t, err)
	require.Equal(t, []Day{
		{Day: day1, Operations: 3, Transactions: 2, Counterparties: 2},
		{Day: day1.AddDate(0, 0, 1)},
		{Day: day1.AddDate(0, 0, 2), Operations: 1, Transactions: 1},
	}, got)

	// a source that fails returns no day at all, never zeros
	f.failOps = true
	got, err = r.AccountDays(t.Context(), self, day1, day1.AddDate(0, 0, 2), root)
	require.Equal(t, CodeUnavailable, Code(err))
	require.Empty(t, got)
}

func TestAccountDaysCap(t *testing.T) {
	self := keypair.MustRandom().Address()
	day1 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	f := &fakeHorizon{base: day1, latest: 4 * 14400}
	// one operation on day 1, then more than the cap on day 2
	f.op(10, 1, "a", map[string]any{"source_account": self})
	for i := range MaxOperationPages*MaxPage + 1 {
		f.op(14400+int64(i/2), i%2+1, "b"+strconv.Itoa(i), map[string]any{"source_account": self})
	}
	h := NewHorizon(testEndpoint(t, f))
	r, err := NewReader(h, NewRPC(testEndpoint(t, http.NotFoundHandler())))
	require.NoError(t, err)
	root, err := h.Root(t.Context())
	require.NoError(t, err)
	got, err := r.AccountDays(t.Context(), self, day1, day1.AddDate(0, 0, 2), root)
	require.Equal(t, CodeDayTooLarge, Code(err))
	// day 1 is whole and kept; day 2 was not finished and is missing
	require.Equal(t, []Day{{Day: day1, Operations: 1, Transactions: 1}}, got)
}

func TestCounterparties(t *testing.T) {
	self := keypair.MustRandom().Address()
	other := keypair.MustRandom().Address()
	op := Operation{Fields: map[string]any{
		"source_account": self, "to": other, "from_muxed": "MA...", "asset_issuer": other, "amount": "1.0",
	}}
	require.Equal(t, []string{other}, op.Counterparties(self))
}

func TestCursorLedger(t *testing.T) {
	l, ok := cursorLedger("0281367602528256000-0000000001")
	require.True(t, ok)
	require.Equal(t, uint32(281367602528256000>>32), l)
	_, ok = cursorLedger("x")
	require.False(t, ok)
}

// fakeRPC serves getHealth, getLedgers (a ledger every 6 seconds from
// base) and getEvents for one contract, in pages of two events.
type fakeRPC struct {
	base   time.Time
	oldest uint32
	latest uint32
	events []map[string]any
}

func (f *fakeRPC) closeOf(seq uint32) time.Time {
	return f.base.Add(time.Duration(seq) * 6 * time.Second)
}

func (f *fakeRPC) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     int64          `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	var result any
	switch req.Method {
	case "getHealth":
		result = map[string]any{"status": "healthy", "oldestLedger": f.oldest, "latestLedger": f.latest, "ledgerRetentionWindow": 120960}
	case "getLedgers":
		seq := uint32(req.Params["startLedger"].(float64))
		result = map[string]any{"ledgers": []map[string]any{{"sequence": seq, "ledgerCloseTime": strconv.FormatInt(f.closeOf(seq).Unix(), 10)}}}
	case "getEvents":
		pg := req.Params["pagination"].(map[string]any)
		after, start := int64(-1), uint32(0)
		if c, ok := pg["cursor"].(string); ok {
			after, _ = strconv.ParseInt(c, 10, 64)
		} else {
			start = uint32(req.Params["startLedger"].(float64))
		}
		var page []map[string]any
		next := ""
		for i, e := range f.events {
			if int64(i) > after && len(page) < 2 && uint32(e["ledger"].(int)) >= start {
				page = append(page, e)
				next = strconv.Itoa(i)
			}
		}
		if len(page) < 2 {
			// the end of the window
			next = strconv.FormatInt(int64(f.latest)<<32, 10)
		}
		result = map[string]any{"events": page, "cursor": next, "latestLedger": f.latest, "oldestLedger": f.oldest,
			"latestLedgerCloseTime": strconv.FormatInt(f.closeOf(f.latest).Unix(), 10)}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

func topicAddr(t *testing.T, g string) string {
	v := addrVal(t, g)
	s, err := xdr.MarshalBase64(v)
	require.NoError(t, err)
	return s
}

func TestContractDays(t *testing.T) {
	contract, err := strkey.Encode(strkey.VersionByteContract, make([]byte, 32))
	require.NoError(t, err)
	a, b := keypair.MustRandom().Address(), keypair.MustRandom().Address()
	day1 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	f := &fakeRPC{base: day1.AddDate(0, 0, -1), oldest: 100, latest: 4 * 14400}
	no := false
	ev := func(ledger int, tx string, op int, successful *bool, topics ...string) {
		e := map[string]any{
			"ledger": ledger, "ledgerClosedAt": f.closeOf(uint32(ledger)).Format(time.RFC3339), "contractId": contract,
			"txHash": tx, "operationIndex": op, "topic": topics,
		}
		if successful != nil {
			e["inSuccessfulContractCall"] = *successful
		}
		f.events = append(f.events, e)
	}
	// day 1: two events of one operation, one of another transaction, one
	// failed call that does not count
	ev(14400+10, "t1", 0, nil, topicAddr(t, a), topicAddr(t, b))
	ev(14400+10, "t1", 0, nil, topicAddr(t, a))
	ev(14400+50, "t2", 0, nil, topicAddr(t, b))
	ev(14400+60, "t3", 0, &no, topicAddr(t, a))
	// day 2: nothing, day 3: after the range
	ev(3*14400+5, "t4", 0, nil)

	r, err := NewReader(NewHorizon(testEndpoint(t, http.NotFoundHandler())), NewRPC(testEndpoint(t, f)))
	require.NoError(t, err)
	first, h, err := r.RPCWindow(t.Context())
	require.NoError(t, err)
	require.Equal(t, day1, first)
	got, err := r.ContractDays(t.Context(), contract, day1, day1.AddDate(0, 0, 1), h)
	require.NoError(t, err)
	require.Equal(t, []Day{
		{Day: day1, Operations: 2, Transactions: 2, Counterparties: 2},
		{Day: day1.AddDate(0, 0, 1)},
	}, got)
}
