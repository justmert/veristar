package operator

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/onchain"
	"github.com/justmert/veristar/internal/jobs/chainactivity"
)

// TestMachineOnchain reads the on-chain routes of the machine api with a
// key for all tenants and a key scoped to one, validating every answer
// against the openapi document.
func TestMachineOnchain(t *testing.T) {
	e := newMachineEnv(t)
	f := e.f
	ops := seedChain(t, f)
	aurora, borealis := f.tenants["aurora"], f.tenants["borealis"]
	global := e.createKey(t, `{"name":"chain warehouse","scope":"metrics:read"}`)
	scoped := e.createKey(t, fmt.Sprintf(`{"name":"aurora chain","scope":"metrics:read","tenant_id":%q,"expires_in_days":30}`, aurora))

	ids := list[MachineOnchainID](t, e, global.Key, "/onchain/ids", 1)
	require.Len(t, ids, 3)
	byKey := map[string]MachineOnchainID{}
	for _, x := range ids {
		byKey[x.Strkey] = x
	}
	require.True(t, byKey[auroraAccount].Verified)
	require.Equal(t, "sep53", *byKey[auroraAccount].VerifyMethod)
	require.Equal(t, "s0.aurora.example", *byKey[auroraAccount].HomeDomain)
	require.False(t, byKey[auroraContract].Verified)
	require.Equal(t, "source_unavailable", *byKey[auroraContract].LastError)
	require.Equal(t, borealis, byKey[borealisAcct].TenantID)

	rq := "?" + f.rangeQuery()
	days := list[MachineOnchainDay](t, e, global.Key, "/onchain/daily"+rq, 4)
	sum := map[string]int64{}
	for _, d := range days {
		sum[d.TenantID.String()] += d.Operations
		require.Equal(t, "horizon", d.Source)
	}
	require.Equal(t, ops[aurora], sum[aurora.String()])
	require.Equal(t, ops[borealis], sum[borealis.String()])

	// a scoped key reads its own tenant only
	own := list[MachineOnchainDay](t, e, scoped.Key, "/onchain/daily"+rq, 50)
	for _, d := range own {
		require.Equal(t, aurora, d.TenantID)
	}
	require.Len(t, list[MachineOnchainID](t, e, scoped.Key, "/onchain/ids", 50), 2)
	e.get(t, scoped.Key, "/onchain/ids?tenant="+borealis.String(), http.StatusForbidden)
	e.get(t, global.Key, "/onchain/daily?tenant="+f.tenants["probe"].String(), http.StatusNotFound)
	e.get(t, global.Key, "/onchain/daily?band=primary", http.StatusBadRequest)

	// weekly active accounts: a closed week with a count, a closed week
	// with a reason, and the open current week
	this := chainactivity.WeekStart(f.today)
	superExec(t, f.pg, `insert into agg.chain_weeks (tenant_id, week, active_accounts, closed_at) values ($1, $2, 9, now())`,
		aurora, this.AddDate(0, 0, -14))
	superExec(t, f.pg, `insert into agg.chain_weeks (tenant_id, week, missing, closed_at) values ($1, $2, 'no_verified_ids', now())`,
		borealis, this.AddDate(0, 0, -14))
	superExec(t, f.pg, `insert into agg.chain_weeks (tenant_id, week, hash_key) values ($1, $2, $3)`, aurora, this, make([]byte, 32))
	wq := "?from=" + this.AddDate(0, 0, -20).Format(time.DateOnly) + "&to=" + f.today.Format(time.DateOnly)
	weeks := list[MachineOnchainWeek](t, e, global.Key, "/onchain/weekly"+wq, 1)
	require.Len(t, weeks, 3)
	for _, w := range weeks {
		switch {
		case w.TenantID == aurora && w.From == this.AddDate(0, 0, -14).Format(time.DateOnly):
			require.Equal(t, int64(9), *w.ActiveAccounts)
			require.Nil(t, w.Missing)
			require.NotNil(t, w.ClosedAt)
			require.Equal(t, onchain.WeekLabel(this.AddDate(0, 0, -14)), w.Week.Week)
		case w.TenantID == aurora:
			require.Nil(t, w.ActiveAccounts)
			require.Equal(t, onchain.MissingWeekNotComplete, *w.Missing)
			require.Nil(t, w.ClosedAt)
		default:
			require.Equal(t, borealis, w.TenantID)
			require.Nil(t, w.ActiveAccounts, "a missing week is never a zero")
			require.Equal(t, "no_verified_ids", *w.Missing)
		}
	}
	for _, w := range list[MachineOnchainWeek](t, e, scoped.Key, "/onchain/weekly"+wq, 50) {
		require.Equal(t, aurora, w.TenantID)
	}
	e.get(t, scoped.Key, "/onchain/weekly?tenant="+borealis.String(), http.StatusForbidden)
}
