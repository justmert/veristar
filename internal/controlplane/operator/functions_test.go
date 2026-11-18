package operator

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/onchain"
	"github.com/justmert/veristar/internal/controlplane/session"
)

// a verified contract of aurora with calls per function on two days
const auroraFnContract = "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD"

// seedFunctions declares auroraFnContract, verified through its admin,
// with three read days: day one has both callers, day two the calls of
// accounts only, day three none (zero calls); a fourth day is over the
// read cap. it returns the contract's chain id.
func seedFunctions(t *testing.T, f *globalFixture) (uuid.UUID, time.Time) {
	t.Helper()
	a := f.tenants["aurora"]
	var id uuid.UUID
	require.NoError(t, superRow(t, f, `insert into cp.chain_ids (tenant_id, strkey, kind, added_by, verify_method, verified_at, verified_via)
		values ($1, $2, 'contract', 'console', 'contract_admin', now(), $3) returning id`, a, auroraFnContract, auroraAccount).Scan(&id))
	first := f.today.AddDate(0, 0, -4)
	for i, state := range []string{"read", "no_call_tree", "read", "day_over_read_cap"} {
		superExec(t, f.pg, `insert into agg.chain_daily (tenant_id, strkey, day, source, operations, transactions, counterparties, functions)
			values ($1, $2, $3, 'rpc_events', 3, 3, 0, $4)`, a, auroraFnContract, first.AddDate(0, 0, i), state)
	}
	superExec(t, f.pg, `insert into agg.chain_function_daily (tenant_id, strkey, day, function_name, caller, successful, failed, accounts)
		values ($1, $2, $3, 'swap', 'account', 5, null, 3), ($1, $2, $3, 'swap', 'contract', 2, 1, 1),
		       ($1, $2, $3, '{other}', 'account', 1, null, 1), ($1, $2, $4, 'swap', 'account', 4, null, 4)`,
		a, auroraFnContract, first, first.AddDate(0, 0, 1))
	return id, first
}

func TestOnchainFunctions(t *testing.T) {
	f := newGlobalFixture(t)
	seedChain(t, f)
	fnID, first := seedFunctions(t, f)
	oc, err := NewOnchain(f.g.db, time.Now)
	require.NoError(t, err)
	ex, err := NewExports(f.g)
	require.NoError(t, err)
	opUser := uuid.Must(uuid.NewV7())
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(session.WithUser(req.Context(), session.User{ID: opUser})))
		})
	})
	r.Route("/api/operator", func(sub chi.Router) {
		oc.Routes(sub)
		ex.Routes(sub)
	})
	aurora := f.tenants["aurora"]
	q := "?from=" + first.Format(time.DateOnly) + "&to=" + f.today.Format(time.DateOnly)

	code, body := call(t, r, "/api/operator/tenants/"+aurora.String()+"/onchain"+q)
	require.Equal(t, http.StatusOK, code, string(body))
	v := decode[onchain.View](t, body)
	for _, x := range v.IDs {
		if x.Strkey != auroraFnContract {
			require.Nil(t, x.Functions, "%s: only a verified contract carries calls per function", x.Strkey)
			continue
		}
		require.NotNil(t, x.Functions)
		fs := *x.Functions
		require.Equal(t, 2, fs.DaysRead)
		require.Equal(t, 1, fs.DaysNoCallTree)
		require.Equal(t, 1, fs.DaysMissing, "the day over the read cap; today is not complete")
		require.Len(t, fs.Functions, 3)
		swap := fs.Functions[0]
		require.Equal(t, "swap", swap.Function)
		require.Equal(t, "account", swap.Caller)
		require.Equal(t, int64(9), swap.Successful)
		require.Nil(t, swap.Failed)
		require.Equal(t, int64(4), swap.PeakDailyAccounts)
		require.Equal(t, 2, swap.DaysWithCalls)
		require.Nil(t, swap.Series, "the view carries no daily series")
	}

	path := fmt.Sprintf("/api/operator/tenants/%s/onchain/ids/%s/functions%s", aurora, fnID, q)
	code, body = call(t, r, path)
	require.Equal(t, http.StatusOK, code, string(body))
	c := decode[onchain.ContractFunctions](t, body)
	require.True(t, c.Verified)
	require.Len(t, c.Days, 5)
	require.Equal(t, "read", *c.Days[0].State)
	require.Equal(t, "no_call_tree", *c.Days[1].State)
	require.Equal(t, "day_over_read_cap", *c.Days[3].Missing)
	require.Equal(t, onchain.MissingNotComplete, *c.Days[4].Missing)
	var contractSwap *onchain.FunctionUsage
	for i := range c.Functions.Functions {
		if c.Functions.Functions[i].Caller == "contract" {
			contractSwap = &c.Functions.Functions[i]
		}
	}
	require.NotNil(t, contractSwap)
	require.Equal(t, int64(1), *contractSwap.Failed)
	s := contractSwap.Series
	require.Equal(t, int64(2), *s[0].Successful)
	require.Nil(t, s[1].Successful, "calls of contracts are not read on a no_call_tree day")
	require.Equal(t, int64(0), *s[2].Successful, "a read day without calls is a zero")
	require.Equal(t, int64(0), *s[2].Failed)
	require.Nil(t, s[3].Successful)

	// the export holds the same cells, empty where a caller was not read
	code, body = call(t, r, fmt.Sprintf("/api/operator/tenants/%s/onchain/ids/%s/functions.csv%s", aurora, fnID, q))
	require.Equal(t, http.StatusOK, code, string(body))
	recs := readCSV(t, body)
	require.Equal(t, []string{"day", "strkey", "function", "caller", "day_state", "day_missing", "successful", "failed", "accounts"}, recs[0])
	require.Len(t, recs, 1+5*3)
	cells := map[string][]string{}
	for _, rec := range recs[1:] {
		cells[rec[0]+"/"+rec[3]+"/"+rec[2]] = rec
	}
	d1 := first.AddDate(0, 0, 1).Format(time.DateOnly)
	require.Equal(t, []string{"", "", ""}, cells[d1+"/contract/swap"][6:], "calls of contracts were not read that day")
	require.Equal(t, []string{"4", "", "4"}, cells[d1+"/account/swap"][6:])
	require.Equal(t, "no_call_tree", cells[d1+"/account/swap"][4])
	var audited int
	require.NoError(t, superRow(t, f, `select count(*) from cp.audit_log where action = 'export.csv' and target = 'operator:contract_functions'`).Scan(&audited))
	require.Equal(t, 1, audited)

	// an unverified contract and an account show no calls; another
	// tenant's id is 404
	ids := map[string]uuid.UUID{}
	for _, x := range v.IDs {
		ids[x.Strkey] = x.ID
	}
	code, body = call(t, r, fmt.Sprintf("/api/operator/tenants/%s/onchain/ids/%s/functions%s", aurora, ids[auroraContract], q))
	require.Equal(t, http.StatusOK, code, string(body))
	u := decode[onchain.ContractFunctions](t, body)
	require.False(t, u.Verified)
	require.Empty(t, u.Functions.Functions)
	require.Equal(t, onchain.MissingNotVerified, *u.Days[0].Missing)
	code, body = call(t, r, fmt.Sprintf("/api/operator/tenants/%s/onchain/ids/%s/functions%s", aurora, ids[auroraAccount], q))
	require.Equal(t, http.StatusOK, code, string(body))
	acct := decode[onchain.ContractFunctions](t, body)
	require.Equal(t, "account", acct.Kind)
	require.Empty(t, acct.Functions.Functions)
	require.Zero(t, acct.DaysMissing)
	require.Equal(t, onchain.MissingNotAContract, *acct.Days[0].Missing)
	code, _ = call(t, r, fmt.Sprintf("/api/operator/tenants/%s/onchain/ids/%s/functions%s", f.tenants["borealis"], fnID, q))
	require.Equal(t, http.StatusNotFound, code)
}

// TestMachineOnchainFunctions reads GET /onchain/functions with a global
// and a scoped key, validated against the openapi document.
func TestMachineOnchainFunctions(t *testing.T) {
	e := newMachineEnv(t)
	f := e.f
	seedChain(t, f)
	_, first := seedFunctions(t, f)
	aurora, borealis := f.tenants["aurora"], f.tenants["borealis"]
	// an unverified contract's rows never count
	superExec(t, f.pg, `insert into agg.chain_daily (tenant_id, strkey, day, source, operations, transactions, counterparties, functions)
		values ($1, $2, $3, 'rpc_events', 1, 1, 0, 'read')`, aurora, auroraContract, first)
	superExec(t, f.pg, `insert into agg.chain_function_daily (tenant_id, strkey, day, function_name, caller, successful, failed, accounts)
		values ($1, $2, $3, 'mint', 'account', 1, null, 1)`, aurora, auroraContract, first)
	global := e.createKey(t, `{"name":"fn warehouse","scope":"metrics:read"}`)
	scoped := e.createKey(t, fmt.Sprintf(`{"name":"aurora fn","scope":"metrics:read","tenant_id":%q,"expires_in_days":30}`, aurora))

	q := "?from=" + first.Format(time.DateOnly) + "&to=" + f.today.Format(time.DateOnly)
	rows := list[MachineOnchainFunction](t, e, global.Key, "/onchain/functions"+q, 1)
	require.Len(t, rows, 4)
	for _, x := range rows {
		require.Equal(t, auroraFnContract, x.Strkey)
		require.Equal(t, x.Caller == "contract", x.Failed != nil)
	}
	require.Len(t, list[MachineOnchainFunction](t, e, scoped.Key, "/onchain/functions"+q, 2), 4)
	e.get(t, scoped.Key, "/onchain/functions?tenant="+borealis.String(), http.StatusForbidden)

	days := list[MachineOnchainDay](t, e, global.Key, "/onchain/daily"+q, 50)
	states := map[string]string{}
	for _, d := range days {
		switch d.Strkey {
		case auroraFnContract:
			states[d.Day] = *d.Functions
		case auroraAccount:
			require.Nil(t, d.Functions)
		}
	}
	require.Equal(t, "no_call_tree", states[first.AddDate(0, 0, 1).Format(time.DateOnly)])
}
