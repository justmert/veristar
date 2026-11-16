//go:build live

// the live check reads the public mainnet ledger apis this package is
// configured with by default. it needs the internet, so it runs only with
// -tags live and never gates ci; docs/reference/onchain-sources.md records
// the result of a run.
package chain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
)

const (
	// circle's usdc issuer: an account with a home domain
	liveIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	// the stellar asset contract of usdc: instance storage with an admin,
	// and more events a day than one run reads
	liveSAC = "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75"
	// a contract with millions of events a day (checked 2026-11-16: about
	// 1000 every 20 seconds); its day is left out at the read cap
	liveBusy = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
)

func TestLiveSources(t *testing.T) {
	cfg, err := config.LoadStellar(config.FromMap(nil))
	require.NoError(t, err)
	h, r, err := Sources(cfg)
	require.NoError(t, err)
	reader, err := NewReader(h, r)
	require.NoError(t, err)
	ctx := t.Context()

	first, root, err := reader.HorizonWindow(ctx)
	require.NoError(t, err)
	t.Logf("horizon %s: ledgers %d to %d, first whole day %s", h.Endpoint().URL(), root.ElderLedger, root.LatestLedger, first.Format(time.DateOnly))

	acct, err := h.Account(ctx, liveIssuer)
	require.NoError(t, err)
	t.Logf("account %s: home_domain %q", liveIssuer, acct.HomeDomain)
	require.Equal(t, "circle.com", acct.HomeDomain)

	rfirst, health, err := reader.RPCWindow(ctx)
	require.NoError(t, err)
	t.Logf("rpc %s: ledgers %d to %d (window %d), first whole day %s", r.Endpoint().URL(), health.OldestLedger, health.LatestLedger,
		health.RetentionWindow, rfirst.Format(time.DateOnly))

	now := time.Now().UTC()
	yesterday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	days, err := reader.ContractDays(ctx, liveSAC, yesterday, yesterday, health)
	t.Logf("contract %s on %s: %+v err=%v", liveSAC, yesterday.Format(time.DateOnly), days, err)
	if err != nil {
		require.Equal(t, CodeDayTooLarge, Code(err), "a busy contract may exceed the read cap; any other error fails")
	}

	_, err = reader.ContractDays(ctx, liveBusy, yesterday, yesterday, health)
	require.Equal(t, CodeDayTooLarge, Code(err))

	// a quiet contract, found among the contracts that emitted events in
	// the last few ledgers, the one with the fewest first
	var recent EventPage
	require.NoError(t, r.call(ctx, "getEvents", map[string]any{
		"startLedger": health.LatestLedger - 20, "filters": []map[string]any{{"type": "contract"}},
		"pagination": map[string]any{"limit": 1000}, "xdrFormat": "base64",
	}, &recent))
	seen := map[string]int{}
	for _, e := range recent.Events {
		seen[e.ContractID]++
	}
	quiet := ""
	for c, n := range seen {
		if n == 1 && c != liveSAC && c != liveBusy {
			days, err = reader.ContractDays(ctx, c, yesterday, yesterday, health)
			t.Logf("contract %s on %s: %+v err=%v", c, yesterday.Format(time.DateOnly), days, err)
			if err == nil {
				quiet = c
				break
			}
		}
	}
	require.NotEmpty(t, quiet, "no contract among the recent ones could be read whole")
	require.Len(t, days, 1)
	// the initiators of the quiet contract's day come from the envelopes of
	// its transactions, through batched getTransaction calls
	t.Logf("contract %s on %s: %d initiators, missing %q", quiet, yesterday.Format(time.DateOnly),
		len(days[0].Initiators), days[0].InitiatorsMissing)
	if days[0].InitiatorsMissing == "" && days[0].Operations > 0 {
		require.NotEmpty(t, days[0].Initiators)
		require.LessOrEqual(t, int64(len(days[0].Initiators)), days[0].Operations)
	}

	days, err = reader.AccountDays(ctx, liveIssuer, yesterday, yesterday, root)
	t.Logf("account %s on %s: %+v err=%v", liveIssuer, yesterday.Format(time.DateOnly), days, err)
	require.NoError(t, err)
	require.Len(t, days, 1)
	if days[0].Operations > 0 {
		require.NotEmpty(t, days[0].Initiators)
	}

	// a week of the issuer account: the distinct initiators over seven
	// days, the weekly active accounts the chain-activity task stores
	weekEnd := yesterday
	weekStart := weekEnd.AddDate(0, 0, -6)
	week, err := reader.AccountDays(ctx, liveIssuer, weekStart, weekEnd, root)
	require.NoError(t, err)
	require.Len(t, week, 7)
	distinct := map[string]struct{}{}
	var ops int64
	for _, d := range week {
		ops += d.Operations
		for _, a := range d.Initiators {
			distinct[a] = struct{}{}
		}
	}
	t.Logf("account %s, %s to %s: %d operations, %d distinct initiators", liveIssuer,
		weekStart.Format(time.DateOnly), weekEnd.Format(time.DateOnly), ops, len(distinct))

	entry, err := r.Instance(ctx, liveSAC)
	require.NoError(t, err)
	admins, err := Admins(entry)
	require.NoError(t, err)
	t.Logf("contract %s admins: %+v", liveSAC, admins)
}
