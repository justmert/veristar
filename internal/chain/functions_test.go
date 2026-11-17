package chain

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/require"
)

// the fixtures under testdata are getTransaction answers of stellar
// mainnet, read from mainnet.sorobanrpc.com on 2026-11-17 with
// xdrFormat base64; only resultMetaXdr and the window fields are left out.
//
//   - a3adca3c...f690 (ledger 65,526,000): a fee bump whose inner
//     transaction calls harvest on CBGS...KKY3, which calls harvest on
//     CDL7...IGWA 14 times, each of which calls mint on CB23...OUOV
//   - 9ab4a86a...361d (ledger 65,520,023): deposit on CBQD...6QUK, which
//     calls deposit on CCY2...IZFWV; that pool calls balance on
//     CARC...BRU3W twice, and both calls fail with contract error 13
//     while the pool handles the error and the transaction succeeds
//   - 71993751...6965 (ledger 65,526,000): a classic transaction whose
//     asset contract emitted events, with no contract invocation
const (
	fixFeeBump = "a3adca3c0dbb8d7b006afd59bea15bc967f76f1a0b9d99a4a895ccd9ea70f690"
	fixFailed  = "9ab4a86a900cb2d5ec61fc77b1a86962acef14e74679b7c9d88bb868122a361d"
	fixClassic = "71993751270817b44ee32a1c25a0eaf2016c68d92851739c5a1a28e9ebce6965"

	harvester = "CBGSBKYMYO6OMGHQXXNOBRGVUDFUDVC2XLC3SXON5R2SNXILR7XCKKY3"
	farm      = "CDL74RF5BLYR2YBLCCI7F5FB6TPSCLKEJUBSD2RSVWZ4YHF3VMFAIGWA"
	minter    = "CB23WRDQWGSP6YPMY4UV5C4OW5CBTXKYN3XEATG7KJEZCXMJBYEHOUOV"
	router    = "CBQDHNBFBZYE4MKPWBSJOPIYLW4SFSXAXUTSXJN76GNKYVYPCKWC6QUK"
	pool      = "CCY2PXGMKNQHO7WNYXEWX76L2C5BH3JUW3RCATGUYKY7QQTRILBZIFWV"
	failing   = "CARCKZ66U4AI2545NS4RAF47QVEXG3PRRCDA52H4Q3FDRAGSMP4BRU3W"
	native    = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
)

type fixture struct {
	Status      string   `json:"status"`
	TxHash      string   `json:"txHash"`
	FeeBump     bool     `json:"feeBump"`
	EnvelopeXDR string   `json:"envelopeXdr"`
	Diagnostics []string `json:"diagnosticEventsXdr"`
}

func loadFixture(t *testing.T, hash string) fixture {
	t.Helper()
	b, err := os.ReadFile("testdata/tx-" + hash + ".json")
	require.NoError(t, err)
	var f fixture
	require.NoError(t, json.Unmarshal(b, &f))
	require.Equal(t, hash, f.TxHash)
	return f
}

func (f fixture) record() TxRecord {
	return TxRecord{Status: f.Status, EnvelopeXDR: f.EnvelopeXDR, Diagnostics: f.Diagnostics}
}

func calls(fn, caller string, failed bool, n int) []Call {
	out := make([]Call, n)
	for i := range out {
		out[i] = Call{Function: fn, Caller: caller, Failed: failed}
	}
	return out
}

func TestFeeBumpInvocation(t *testing.T) {
	f := loadFixture(t, fixFeeBump)
	require.True(t, f.FeeBump)
	// the inner transaction's source initiates the invocation; the fee
	// payer GA2J...HVAW initiates nothing
	op, src, ok, err := InvokeSource(f.EnvelopeXDR)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint32(0), op)
	require.Equal(t, "GAY4AVB5UF2WBI3UTWZWH6WCARU4TL2MR4CU674QQVBNUCPFCE4SMTHL", src)

	top, err := TopLevelCalls(f.EnvelopeXDR, harvester)
	require.NoError(t, err)
	require.Equal(t, calls("harvest", CallerAccount, false, 1), top)
	// the contracts it calls are never called at the top level
	top, err = TopLevelCalls(f.EnvelopeXDR, farm)
	require.NoError(t, err)
	require.Empty(t, top)

	sub, err := ContractCalls(f.Diagnostics, farm)
	require.NoError(t, err)
	require.Equal(t, calls("harvest", CallerContract, false, 14), sub)
	sub, err = ContractCalls(f.Diagnostics, minter)
	require.NoError(t, err)
	require.Equal(t, calls("mint", CallerContract, false, 14), sub)
	// the host's call of the operation's function is a call of the
	// account, which the envelope gives, never a call of a contract
	sub, err = ContractCalls(f.Diagnostics, harvester)
	require.NoError(t, err)
	require.Empty(t, sub)
}

func TestFailedContractCalls(t *testing.T) {
	f := loadFixture(t, fixFailed)
	require.Equal(t, "SUCCESS", f.Status)
	_, src, ok, err := InvokeSource(f.EnvelopeXDR)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "GACBDYR3MNVUYY7NXP47ZL6JAWZNFXXXHTHLKWENADUMAWRO762L32WW", src)
	top, err := TopLevelCalls(f.EnvelopeXDR, router)
	require.NoError(t, err)
	require.Equal(t, calls("deposit", CallerAccount, false, 1), top)

	sub, err := ContractCalls(f.Diagnostics, failing)
	require.NoError(t, err)
	require.Equal(t, calls("balance", CallerContract, true, 2), sub, "both calls end in an error the pool handles")
	sub, err = ContractCalls(f.Diagnostics, pool)
	require.NoError(t, err)
	require.Equal(t, calls("deposit", CallerContract, false, 1), sub)
	// the calls after a failed one close normally
	sub, err = ContractCalls(f.Diagnostics, native)
	require.NoError(t, err)
	require.Equal(t, []Call{{Function: "balance", Caller: CallerContract}, {Function: "transfer", Caller: CallerContract}}, sub)
}

func TestClassicTransaction(t *testing.T) {
	f := loadFixture(t, fixClassic)
	_, _, ok, err := InvokeSource(f.EnvelopeXDR)
	require.NoError(t, err)
	require.False(t, ok)
	got := map[functionKey]*functionTally{}
	whole, err := transactionCalls(f.record(), native, got)
	require.NoError(t, err)
	require.True(t, whole)
	require.Empty(t, got)
}

// muxedFixture rewrites the fee bump fixture's inner source as a muxed
// account of the same key: the network accepts muxed sources, and no
// soroban transaction of the window used one.
func muxedFixture(t *testing.T) (string, string) {
	f := loadFixture(t, fixFeeBump)
	var env xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(f.EnvelopeXDR, &env))
	inner := env.FeeBump.Tx.InnerTx.V1
	base := inner.Tx.SourceAccount.ToAccountId()
	m, err := xdr.MuxedAccountFromAccountId(base.Address(), 42)
	require.NoError(t, err)
	inner.Tx.SourceAccount = m
	s, err := xdr.MarshalBase64(env)
	require.NoError(t, err)
	return s, base.Address()
}

func TestMuxedInvoker(t *testing.T) {
	env, base := muxedFixture(t)
	_, src, ok, err := InvokeSource(env)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, base, src, "a muxed source counts as its g account")
}

func TestTransactionCalls(t *testing.T) {
	f := loadFixture(t, fixFailed)
	got := map[functionKey]*functionTally{}
	whole, err := transactionCalls(f.record(), failing, got)
	require.NoError(t, err)
	require.True(t, whole)
	require.Equal(t, []FunctionDay{{Function: "balance", Caller: CallerContract, Failed: ptr(int64(2)), Accounts: 1}},
		capFunctions(got, true))

	// a server without diagnostic events gives the calls of accounts only
	rec := f.record()
	rec.Diagnostics = nil
	got = map[functionKey]*functionTally{}
	whole, err = transactionCalls(rec, router, got)
	require.NoError(t, err)
	require.False(t, whole)
	require.Equal(t, []FunctionDay{{Function: "deposit", Caller: CallerAccount, Successful: 1, Accounts: 1}},
		capFunctions(got, false))

	// events that do not form a call tree are not counted either
	rec = f.record()
	rec.Diagnostics = rec.Diagnostics[1:]
	got = map[functionKey]*functionTally{}
	whole, err = transactionCalls(rec, failing, got)
	require.NoError(t, err)
	require.False(t, whole)
}

func ptr[T any](v T) *T { return &v }

func diagnostic(t *testing.T, from *[32]byte, topics ...xdr.ScVal) string {
	t.Helper()
	ev := xdr.DiagnosticEvent{InSuccessfulContractCall: true, Event: xdr.ContractEvent{
		Type: xdr.ContractEventTypeDiagnostic,
		Body: xdr.ContractEventBody{V: 0, V0: &xdr.ContractEventV0{Topics: topics, Data: xdr.ScVal{Type: xdr.ScValTypeScvVoid}}},
	}}
	if from != nil {
		id := xdr.ContractId(*from)
		ev.Event.ContractId = &id
	}
	s, err := xdr.MarshalBase64(ev)
	require.NoError(t, err)
	return s
}

func fnCall(t *testing.T, from *[32]byte, to [32]byte, fn string) string {
	b := xdr.ScBytes(to[:])
	return diagnostic(t, from, sym("fn_call"), xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &b}, sym(fn))
}

func fnReturn(t *testing.T, from [32]byte, fn string) string {
	return diagnostic(t, &from, sym("fn_return"), sym(fn))
}

func TestContractCallsMalformed(t *testing.T) {
	a, b := [32]byte{1}, [32]byte{2}
	id, err := strkeyContract(b)
	require.NoError(t, err)
	// a return nobody called
	_, err = ContractCalls([]string{fnReturn(t, b, "f")}, id)
	require.Equal(t, CodeBadResponse, Code(err))
	// a call from a contract without an open call
	_, err = ContractCalls([]string{fnCall(t, &a, b, "f")}, id)
	require.Equal(t, CodeBadResponse, Code(err))
	// a function name that is no symbol
	_, err = ContractCalls([]string{fnCall(t, nil, a, "f"), fnCall(t, &a, b, "bad-name")}, id)
	require.Equal(t, CodeBadResponse, Code(err))
	// a nested call still open at the end failed
	got, err := ContractCalls([]string{fnCall(t, nil, a, "f"), fnCall(t, &a, b, "g")}, id)
	require.NoError(t, err)
	require.Equal(t, calls("g", CallerContract, true, 1), got)
}

func TestCapFunctions(t *testing.T) {
	by := map[functionKey]*functionTally{}
	accounts := make([]string, 3)
	for i := range accounts {
		accounts[i] = keypair.MustRandom().Address()
	}
	// MaxFunctions+2 functions: f000 has the most calls, the two with the
	// fewest go to {other}
	for i := range MaxFunctions + 2 {
		name := fmt.Sprintf("f%03d", i)
		for range MaxFunctions + 2 - i {
			addCall(by, Call{Function: name, Caller: CallerAccount}, accounts[i%3])
		}
	}
	addCall(by, Call{Function: "f065", Caller: CallerContract, Failed: true}, accounts[0])
	rows := capFunctions(by, true)
	byName := map[string]FunctionDay{}
	for _, r := range rows {
		byName[r.Caller+"/"+r.Function] = r
	}
	require.Len(t, byName, MaxFunctions+2)
	other := byName[CallerAccount+"/"+OtherFunctions]
	// f064 has 2 calls, f065 has 1: both from different accounts
	require.Equal(t, int64(3), other.Successful)
	require.Equal(t, int64(2), other.Accounts)
	require.Nil(t, other.Failed)
	oc := byName[CallerContract+"/"+OtherFunctions]
	require.Equal(t, int64(1), *oc.Failed)
	require.Contains(t, byName, CallerAccount+"/f063")
	require.NotContains(t, byName, CallerAccount+"/f064")
	// without the contract calls, they neither count toward the ranking
	// nor appear
	for _, r := range capFunctions(by, false) {
		require.Equal(t, CallerAccount, r.Caller)
	}
	require.True(t, validSymbol("__check_auth"))
	require.False(t, validSymbol(OtherFunctions))
	require.False(t, validSymbol(""))
	require.False(t, validSymbol("a234567890123456789012345678901234"))
}

func strkeyContract(id [32]byte) (string, error) {
	return strkey.Encode(strkey.VersionByteContract, id[:])
}

// TestContractDaysFunctions reads two days of the pool contract from a
// fake server that answers getTransaction with the mainnet fixtures: the
// pool is called by the router on day one; on day two the same answer
// comes without diagnostic events.
func TestContractDaysFunctions(t *testing.T) {
	day1 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	f := &fakeRPC{base: day1.AddDate(0, 0, -1), oldest: 100, latest: 4 * 14400}
	ev := func(ledger int, tx string) {
		f.events = append(f.events, map[string]any{
			"ledger": ledger, "ledgerClosedAt": f.closeOf(uint32(ledger)).Format(time.RFC3339), "contractId": failing,
			"txHash": tx, "operationIndex": 0, "topic": []string{},
		})
	}
	ev(14400+10, fixFailed)
	ev(2*14400+10, "bare")
	rec := loadFixture(t, fixFailed).record()
	bare := rec
	bare.Diagnostics = nil
	f.records = map[string]TxRecord{fixFailed: rec, "bare": bare}

	r, err := NewReader(NewHorizon(testEndpoint(t, http.NotFoundHandler())), NewRPC(testEndpoint(t, f)))
	require.NoError(t, err)
	_, h, err := r.RPCWindow(t.Context())
	require.NoError(t, err)
	got, err := r.ContractDays(t.Context(), failing, day1, day1.AddDate(0, 0, 2), h)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, FunctionsRead, got[0].FunctionsState)
	require.Equal(t, []FunctionDay{{Function: "balance", Caller: CallerContract, Failed: ptr(int64(2)), Accounts: 1}}, got[0].Functions)
	require.Equal(t, CodeNoCallTree, got[1].FunctionsState)
	require.Empty(t, got[1].Functions, "the contract is never called at the top level, and its calls by contracts are not read")
	require.Equal(t, FunctionsRead, got[2].FunctionsState)
	require.Empty(t, got[2].Functions)
}
