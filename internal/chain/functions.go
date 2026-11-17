package chain

import (
	"cmp"
	"slices"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// who called a contract function (agg.chain_function_daily.caller)
const (
	// CallerAccount: the operation's host function invokes the contract,
	// so the account that submitted the operation called it
	CallerAccount = "account"
	// CallerContract: another contract called it during an invocation
	CallerContract = "contract"
)

// how much of a contract day's function calls was read
// (agg.chain_daily.functions)
const (
	// FunctionsRead: the calls of accounts and of contracts are counted
	FunctionsRead = "read"
	// CodeNoCallTree: at least one transaction of the day came without
	// the diagnostic events that hold its call tree, so only the calls of
	// accounts, which the envelope names, are counted
	CodeNoCallTree = "no_call_tree"
)

// MaxFunctions caps the functions stored per contract and day. the busiest
// ones keep their own rows; the rest add up under OtherFunctions, so a
// contract that exposes many entry points, or a caller that names many
// symbols, cannot grow the table without bound.
const MaxFunctions = 64

// OtherFunctions is the row of the functions beyond MaxFunctions. braces
// are not allowed in a soroban symbol, so no function has this name.
const OtherFunctions = "{other}"

// Call is one call into a contract function found in a transaction.
type Call struct {
	Function string
	Caller   string
	Failed   bool
}

// validSymbol says whether s is a soroban symbol: 1 to 32 characters of
// a-z, A-Z, 0-9 and _. the network rejects any other function name, so a
// name that fails here comes from a malformed record.
func validSymbol(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// contractHash decodes a contract strkey.
func contractHash(contract string) ([32]byte, error) {
	var out [32]byte
	raw, err := strkey.Decode(strkey.VersionByteContract, contract)
	if err != nil {
		return out, fail(CodeBadResponse, "%s is not a contract id", contract)
	}
	copy(out[:], raw)
	return out, nil
}

// InvokeSource returns the index and initiating account of the first
// invoke_host_function operation of an envelope that calls a contract
// function. ok is false for a transaction without one, such as a classic
// payment whose asset contract emitted an event.
func InvokeSource(envelopeXDR string) (op uint32, source string, ok bool, err error) {
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(envelopeXDR, &env); err != nil {
		return 0, "", false, fail(CodeBadResponse, "transaction envelope: %v", err)
	}
	for i, o := range env.Operations() {
		ih, isInvoke := o.Body.GetInvokeHostFunctionOp()
		if !isInvoke || ih.HostFunction.Type != xdr.HostFunctionTypeHostFunctionTypeInvokeContract {
			continue
		}
		src, err := OperationSource(envelopeXDR, uint32(i))
		if err != nil {
			return 0, "", false, err
		}
		return uint32(i), src, true, nil
	}
	return 0, "", false, nil
}

// TopLevelCalls lists the calls an envelope's operations make into
// contract directly: an invoke_host_function operation whose host
// function invokes the contract. for a fee bump, the inner transaction
// carries the operations.
func TopLevelCalls(envelopeXDR, contract string) ([]Call, error) {
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(envelopeXDR, &env); err != nil {
		return nil, fail(CodeBadResponse, "transaction envelope: %v", err)
	}
	want, err := contractHash(contract)
	if err != nil {
		return nil, err
	}
	var out []Call
	for _, o := range env.Operations() {
		ih, ok := o.Body.GetInvokeHostFunctionOp()
		if !ok || ih.HostFunction.Type != xdr.HostFunctionTypeHostFunctionTypeInvokeContract || ih.HostFunction.InvokeContract == nil {
			continue
		}
		args := ih.HostFunction.InvokeContract
		if args.ContractAddress.Type != xdr.ScAddressTypeScAddressTypeContract || args.ContractAddress.ContractId == nil ||
			[32]byte(*args.ContractAddress.ContractId) != want {
			continue
		}
		fn := string(args.FunctionName)
		if !validSymbol(fn) {
			return nil, fail(CodeBadResponse, "invocation of a function named %q", cut(fn, 40))
		}
		out = append(out, Call{Function: fn, Caller: CallerAccount})
	}
	return out, nil
}

// frame is an open call of the diagnostic call tree.
type frame struct {
	contract [32]byte
	function string
	// fromContract: a contract made the call, not the host
	fromContract bool
}

// ContractCalls lists the calls other contracts make into contract, read
// from a transaction's diagnostic events (base64 xdr DiagnosticEvent, as
// getTransaction returns them in diagnosticEventsXdr).
//
// the host emits the events of a call tree depth first: fn_call when a
// call starts, from the calling contract (none when the host invokes the
// operation's function), with the callee's id and the function name as
// topics; fn_return from the callee when the call returns. a call that
// fails has no fn_return: the caller's next event (another fn_call or its
// own fn_return) closes it, and a call still open at the end failed. a
// contract cannot be re-entered, so a contract id names at most one open
// call. a sequence that fits none of this is a bad_response.
func ContractCalls(diagnostics []string, contract string) ([]Call, error) {
	want, err := contractHash(contract)
	if err != nil {
		return nil, err
	}
	var out []Call
	var stack []frame
	pop := func(failed bool) {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.fromContract && f.contract == want {
			out = append(out, Call{Function: f.function, Caller: CallerContract, Failed: failed})
		}
	}
	// unwind closes, as failed, the calls above the open call of id
	unwind := func(id [32]byte) bool {
		at := slices.IndexFunc(stack, func(f frame) bool { return f.contract == id })
		if at < 0 {
			return false
		}
		for len(stack) > at+1 {
			pop(true)
		}
		return true
	}
	for _, s := range diagnostics {
		var d xdr.DiagnosticEvent
		if err := xdr.SafeUnmarshalBase64(s, &d); err != nil {
			return nil, fail(CodeBadResponse, "diagnostic event: %v", err)
		}
		if d.Event.Type != xdr.ContractEventTypeDiagnostic || d.Event.Body.V0 == nil {
			continue
		}
		topics := d.Event.Body.V0.Topics
		if len(topics) == 0 {
			continue
		}
		name, ok := topics[0].GetSym()
		if !ok {
			continue
		}
		switch name {
		case "fn_call":
			if len(topics) < 3 {
				return nil, fail(CodeBadResponse, "fn_call event with %d topics", len(topics))
			}
			callee, ok := topics[1].GetBytes()
			fn, okFn := topics[2].GetSym()
			if !ok || !okFn || len(callee) != 32 || !validSymbol(string(fn)) {
				return nil, fail(CodeBadResponse, "fn_call event without a contract id and a function name")
			}
			f := frame{contract: [32]byte(callee), function: string(fn)}
			if d.Event.ContractId != nil {
				// the caller's call is the innermost open one: calls above
				// it failed
				f.fromContract = true
				if !unwind([32]byte(*d.Event.ContractId)) {
					return nil, fail(CodeBadResponse, "fn_call from a contract without an open call")
				}
			}
			stack = append(stack, f)
		case "fn_return":
			if d.Event.ContractId == nil || len(topics) < 2 {
				return nil, fail(CodeBadResponse, "fn_return event without a contract id")
			}
			fn, ok := topics[1].GetSym()
			id := [32]byte(*d.Event.ContractId)
			if !ok || !unwind(id) || stack[len(stack)-1].function != string(fn) {
				return nil, fail(CodeBadResponse, "fn_return without a matching call")
			}
			pop(false)
		}
	}
	for len(stack) > 0 {
		pop(true)
	}
	return out, nil
}

// FunctionDay is one stored row of a contract day: the calls to one
// function by one kind of caller. Failed is nil for the calls of accounts
// (see Reader.ContractDays); Accounts are the distinct accounts that
// initiated the operations the calls belong to.
type FunctionDay struct {
	Function   string
	Caller     string
	Successful int64
	Failed     *int64
	Accounts   int64
}

// functionTally gathers a day's calls before the cap applies.
type functionTally struct {
	successful int64
	failed     int64
	accounts   map[string]struct{}
}

type functionKey struct{ function, caller string }

// addCall counts one call initiated by source.
func addCall(by map[functionKey]*functionTally, c Call, source string) {
	k := functionKey{c.Function, c.Caller}
	t := by[k]
	if t == nil {
		t = &functionTally{accounts: map[string]struct{}{}}
		by[k] = t
	}
	if c.Failed {
		t.failed++
	} else {
		t.successful++
	}
	t.accounts[source] = struct{}{}
}

// capFunctions turns a day's tallies into rows: the MaxFunctions functions
// with the most calls (both callers, failed ones included; ties by name)
// keep their rows, the others are merged into OtherFunctions per caller,
// their accounts counted once. withContracts false leaves out the calls of
// contracts, which were not read whole.
func capFunctions(by map[functionKey]*functionTally, withContracts bool) []FunctionDay {
	total := map[string]int64{}
	for k, t := range by {
		if k.caller == CallerContract && !withContracts {
			continue
		}
		total[k.function] += t.successful + t.failed
	}
	names := make([]string, 0, len(total))
	for n := range total {
		names = append(names, n)
	}
	slices.SortFunc(names, func(a, b string) int {
		if c := cmp.Compare(total[b], total[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	keep := map[string]bool{}
	for i, n := range names {
		keep[n] = i < MaxFunctions
	}
	merged := map[functionKey]*functionTally{}
	for k, t := range by {
		if k.caller == CallerContract && !withContracts {
			continue
		}
		if !keep[k.function] {
			k.function = OtherFunctions
		}
		m := merged[k]
		if m == nil {
			m = &functionTally{accounts: map[string]struct{}{}}
			merged[k] = m
		}
		m.successful += t.successful
		m.failed += t.failed
		for a := range t.accounts {
			m.accounts[a] = struct{}{}
		}
	}
	var out []FunctionDay
	for k, t := range merged {
		row := FunctionDay{Function: k.function, Caller: k.caller, Successful: t.successful, Accounts: int64(len(t.accounts))}
		if k.caller == CallerContract {
			n := t.failed
			row.Failed = &n
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b FunctionDay) int {
		return cmp.Or(cmp.Compare(a.Caller, b.Caller), cmp.Compare(a.Function, b.Function))
	})
	return out
}
