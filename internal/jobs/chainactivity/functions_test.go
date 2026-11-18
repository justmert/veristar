package chainactivity_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/chain"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/chainactivity"
	"github.com/justmert/veristar/internal/jobs/jobstest"
)

// mainnet transaction 9ab4a86a...361d (ledger 65,520,023, read from
// mainnet.sorobanrpc.com on 2026-11-17): an account calls deposit on the
// router CBQD...6QUK, which calls deposit on a pool; the pool calls
// balance on CARC...BRU3W twice and both calls fail while the transaction
// succeeds. the fixture is shared with package chain.
const (
	fnTx      = "9ab4a86a900cb2d5ec61fc77b1a86962acef14e74679b7c9d88bb868122a361d"
	fnRouter  = "CBQDHNBFBZYE4MKPWBSJOPIYLW4SFSXAXUTSXJN76GNKYVYPCKWC6QUK"
	fnFailing = "CARCKZ66U4AI2545NS4RAF47QVEXG3PRRCDA52H4Q3FDRAGSMP4BRU3W"
)

func fixtureRecord(t *testing.T, hash string) chain.TxRecord {
	t.Helper()
	b, err := os.ReadFile("../../chain/testdata/tx-" + hash + ".json")
	require.NoError(t, err)
	var f struct {
		Status      string   `json:"status"`
		EnvelopeXDR string   `json:"envelopeXdr"`
		Diagnostics []string `json:"diagnosticEventsXdr"`
	}
	require.NoError(t, json.Unmarshal(b, &f))
	return chain.TxRecord{Status: f.Status, EnvelopeXDR: f.EnvelopeXDR, Diagnostics: f.Diagnostics}
}

type fnRow struct {
	Strkey     string
	Day        string
	Function   string
	Caller     string
	Successful int64
	Failed     *int64
	Accounts   int64
}

func TestRunFunctions(t *testing.T) {
	ctx := t.Context()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleJobs, pdb.RoleCPSystem)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, pcfg, "chainfunctions-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	tx := func(ctx context.Context, fn func(pgx.Tx) error) error { return d.WithRole(ctx, pdb.RoleJobs, fn) }

	today := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	now := today.Add(9 * time.Hour)
	l := &ledgers{base: today.AddDate(0, 0, -100)}
	l.latest = 100*perDay + 9*perDay/24
	day := func(back int) int64 { return int64(100-back) * perDay }
	ev := func(seq int64, contract, hash string) {
		l.events = append(l.events, map[string]any{"ledger": seq, "ledgerClosedAt": l.closeOf(seq).Format(time.RFC3339),
			"contractId": contract, "txHash": hash, "operationIndex": 0, "topic": []string{}})
	}
	// two days ago both contracts emitted events in the mainnet
	// transaction; yesterday the router did in a transaction the server
	// answers without diagnostic events
	rec := fixtureRecord(t, fnTx)
	bare := rec
	bare.Diagnostics = nil
	l.records = map[string]chain.TxRecord{fnTx: rec, "bare": bare}
	ev(day(2)+3, fnRouter, fnTx)
	ev(day(2)+3, fnFailing, fnTx)
	ev(day(1)+3, fnRouter, "bare")
	hz := httptest.NewServer(http.HandlerFunc(l.horizon))
	t.Cleanup(hz.Close)
	rp := httptest.NewServer(http.HandlerFunc(l.rpc))
	t.Cleanup(rp.Close)
	reader := func() (*chain.Reader, error) {
		he, err := chain.NewEndpoint(hz.URL, 1000, "test")
		if err != nil {
			return nil, err
		}
		re, err := chain.NewEndpoint(rp.URL, 1000, "test")
		if err != nil {
			return nil, err
		}
		instant := func(context.Context, time.Duration) error { return nil }
		return chain.NewReader(chain.NewHorizon(he.WithSleep(instant)), chain.NewRPC(re.WithSleep(instant)))
	}

	var owner, tenant uuid.UUID
	require.NoError(t, d.WithSystem(ctx, func(x pgx.Tx) error {
		if err := x.QueryRow(ctx, `insert into cp.users (github_id, github_login) values (9300101, 'fn-owner') returning id`).Scan(&owner); err != nil {
			return err
		}
		return x.QueryRow(ctx, `insert into cp.tenants (slug, name, status, created_by, enrollment_answers) values ('fn-a', 'P', 'active', $1, $2) returning id`,
			owner, map[string]any{"stellar_ids": []string{fnRouter, fnFailing}}).Scan(&tenant)
	}))
	task, err := chainactivity.New(reader, func() time.Time { return now }, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	res, err := task.Run(ctx, tx)
	require.NoError(t, err)
	require.Equal(t, 2, res.IDs)

	rows := func() []fnRow {
		var out []fnRow
		require.NoError(t, tx(ctx, func(x pgx.Tx) error {
			r, err := x.Query(ctx, `select strkey, day::text, function_name, caller, successful, failed, accounts
				from agg.chain_function_daily where tenant_id = $1 order by strkey, day, caller, function_name`, tenant)
			if err != nil {
				return err
			}
			out, err = pgx.CollectRows(r, pgx.RowToStructByPos[fnRow])
			return err
		}))
		return out
	}
	states := func(strkey string) map[string]*string {
		out := map[string]*string{}
		require.NoError(t, tx(ctx, func(x pgx.Tx) error {
			r, err := x.Query(ctx, `select day::text, functions from agg.chain_daily where tenant_id = $1 and strkey = $2`, tenant, strkey)
			if err != nil {
				return err
			}
			for r.Next() {
				var day string
				var s *string
				if err := r.Scan(&day, &s); err != nil {
					return err
				}
				out[day] = s
			}
			return r.Err()
		}))
		return out
	}
	two, one := today.AddDate(0, 0, -2).Format(time.DateOnly), today.AddDate(0, 0, -1).Format(time.DateOnly)
	two64 := int64(2)
	want := []fnRow{
		{fnFailing, two, "balance", chain.CallerContract, 0, &two64, 1},
		{fnRouter, two, "deposit", chain.CallerAccount, 1, nil, 1},
		{fnRouter, one, "deposit", chain.CallerAccount, 1, nil, 1},
	}
	require.Equal(t, want, rows())
	router := states(fnRouter)
	require.Equal(t, chain.FunctionsRead, *router[two])
	require.Equal(t, chain.CodeNoCallTree, *router[one], "yesterday's transaction came without its call tree")
	for day, s := range states(fnFailing) {
		require.Equal(t, chain.FunctionsRead, *s, day)
	}

	// a day stored before its function calls were read is read again
	// while the window holds it; an older one is marked out of reach
	old := today.AddDate(0, 0, -20)
	require.NoError(t, tx(ctx, func(x pgx.Tx) error {
		if _, err := x.Exec(ctx, `update agg.chain_daily set functions = null where tenant_id = $1 and strkey = $2 and day = $3`,
			tenant, fnRouter, today.AddDate(0, 0, -2)); err != nil {
			return err
		}
		if _, err := x.Exec(ctx, `delete from agg.chain_function_daily where tenant_id = $1 and strkey = $2 and day = $3`,
			tenant, fnRouter, today.AddDate(0, 0, -2)); err != nil {
			return err
		}
		_, err := x.Exec(ctx, `insert into agg.chain_daily (tenant_id, strkey, day, source, operations, transactions, counterparties)
			values ($1, $2, $3, 'rpc_events', 4, 4, 0)`, tenant, fnRouter, old)
		return err
	}))
	now = now.Add(2 * time.Hour)
	res, err = task.Run(ctx, tx)
	require.NoError(t, err)
	require.Equal(t, 1, res.IDs, "only the router has a day to read again")
	require.Equal(t, want, rows(), "the read again restores the rows and adds none twice")
	router = states(fnRouter)
	require.Equal(t, chain.FunctionsRead, *router[two])
	require.Equal(t, "before_source_history", *router[old.Format(time.DateOnly)])

	// nothing is due after that
	res, err = task.Run(ctx, tx)
	require.NoError(t, err)
	require.Zero(t, res.IDs)
}
