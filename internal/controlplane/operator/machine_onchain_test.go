package operator

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
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
}
