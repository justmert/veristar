package chainactivity_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/chain"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/chainactivity"
	"github.com/justmert/veristar/internal/jobs/jobstest"
)

// a ledger closes every 6 seconds from base, so a utc day holds 14400
const perDay = 14400

// ledgers is the fake ledger: horizon serves accounts, operations and
// ledgers; rpc serves health, ledgers and contract events.
type ledgers struct {
	base     time.Time
	latest   int64
	account  string
	ops      []map[string]any
	contract string
	events   []map[string]any
	// horizonDown answers 503 to every operations request
	horizonDown atomic.Bool
}

func (l *ledgers) closeOf(seq int64) time.Time {
	return l.base.Add(time.Duration(seq) * 6 * time.Second)
}

func (l *ledgers) horizon(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/":
		_ = json.NewEncoder(w).Encode(map[string]any{"history_latest_ledger": l.latest, "history_elder_ledger": 1})
	case strings.HasPrefix(p, "/ledgers/"):
		seq, _ := strconv.ParseInt(strings.TrimPrefix(p, "/ledgers/"), 10, 64)
		_ = json.NewEncoder(w).Encode(map[string]any{"sequence": seq, "closed_at": l.closeOf(seq).Format(time.RFC3339)})
	case p == "/accounts/"+l.account:
		_ = json.NewEncoder(w).Encode(map[string]any{"account_id": l.account, "home_domain": "alpha.example.org"})
	case p == "/accounts/"+l.account+"/operations":
		if l.horizonDown.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		cur, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
		page := []map[string]any{}
		for _, op := range l.ops {
			tok, _ := strconv.ParseInt(op["paging_token"].(string), 10, 64)
			if tok > cur && len(page) < chain.MaxPage {
				page = append(page, op)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"_embedded": map[string]any{"records": page}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (l *ledgers) rpc(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     int64          `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	// the server keeps the last 7 days
	oldest := l.latest - 7*perDay
	var result any
	switch req.Method {
	case "getHealth":
		result = map[string]any{"status": "healthy", "oldestLedger": oldest, "latestLedger": l.latest}
	case "getLedgers":
		seq := int64(req.Params["startLedger"].(float64))
		result = map[string]any{"ledgers": []map[string]any{{"sequence": seq, "ledgerCloseTime": strconv.FormatInt(l.closeOf(seq).Unix(), 10)}}}
	case "getEvents":
		start := int64(0)
		if v, ok := req.Params["startLedger"].(float64); ok {
			start = int64(v)
		}
		page := []map[string]any{}
		for _, e := range l.events {
			if e["ledger"].(int64) >= start {
				page = append(page, e)
			}
		}
		result = map[string]any{"events": page, "cursor": strconv.FormatInt(l.latest<<32, 10),
			"latestLedger": l.latest, "oldestLedger": oldest,
			"latestLedgerCloseTime": strconv.FormatInt(l.closeOf(l.latest).Unix(), 10)}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

type dayRow struct {
	Strkey string
	Day    string
	Source string
	Ops    int64
	Txs    int64
	Peers  int64
}

func TestRun(t *testing.T) {
	ctx := t.Context()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleJobs, pdb.RoleCPSystem)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, pcfg, "chainactivity-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	tx := func(ctx context.Context, fn func(pgx.Tx) error) error { return d.WithRole(ctx, pdb.RoleJobs, fn) }

	today := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	now := today.Add(9 * time.Hour)
	account := keypair.MustRandom().Address()
	peer := keypair.MustRandom().Address()
	contract, err := strkey.Encode(strkey.VersionByteContract, make([]byte, 32))
	require.NoError(t, err)

	// ledger 0 closes 100 days before today, the latest at 09:00 today
	l := &ledgers{base: today.AddDate(0, 0, -100), account: account, contract: contract}
	l.latest = 100*perDay + 9*perDay/24
	day := func(back int) int64 { return int64(100-back) * perDay }
	op := func(seq int64, idx int, hash string, fields map[string]any) {
		m := map[string]any{"paging_token": strconv.FormatInt(seq<<32+int64(idx), 10), "transaction_hash": hash,
			"created_at": l.closeOf(seq).Format(time.RFC3339)}
		for k, v := range fields {
			m[k] = v
		}
		l.ops = append(l.ops, m)
	}
	// three days ago: two operations in one transaction with one peer
	op(day(3)+10, 1, "h1", map[string]any{"source_account": account, "to": peer})
	op(day(3)+10, 2, "h1", map[string]any{"source_account": account})
	// yesterday: one; today: one that is not complete yet
	op(day(1)+5, 1, "h2", map[string]any{"source_account": peer, "from": peer, "to": account})
	op(day(0)+5, 1, "h3", map[string]any{"source_account": account})
	l.events = []map[string]any{
		{"ledger": day(2) + 3, "ledgerClosedAt": l.closeOf(day(2) + 3).Format(time.RFC3339), "contractId": contract,
			"txHash": "e1", "operationIndex": 0, "topic": []string{}},
	}
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

	// two tenants; enrollment declares the ids and the trigger copies them
	var owner, tenantA, tenantB uuid.UUID
	require.NoError(t, d.WithSystem(ctx, func(x pgx.Tx) error {
		if err := x.QueryRow(ctx, `insert into cp.users (github_id, github_login) values (9300001, 'chain-owner') returning id`).Scan(&owner); err != nil {
			return err
		}
		ins := `insert into cp.tenants (slug, name, status, created_by, enrollment_answers) values ($1, 'P', 'active', $2, $3) returning id`
		if err := x.QueryRow(ctx, ins, "chain-a", owner, map[string]any{"stellar_ids": []string{account, contract}}).Scan(&tenantA); err != nil {
			return err
		}
		return x.QueryRow(ctx, ins, "chain-b", owner, map[string]any{"stellar_ids": []string{account, "MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVAGZB"}}).Scan(&tenantB)
	}))

	clock := func() time.Time { return now }
	task, err := chainactivity.New(reader, clock, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	res, err := task.Run(ctx, tx)
	require.NoError(t, err)
	require.Equal(t, 3, res.IDs)
	require.Zero(t, res.Failed)

	rows := func() []dayRow {
		var out []dayRow
		require.NoError(t, d.WithRole(ctx, pdb.RoleJobs, func(x pgx.Tx) error {
			r, err := x.Query(ctx, `select strkey, day::text, source, operations, transactions, counterparties
				from agg.chain_daily where tenant_id = $1 and operations > 0 order by strkey, day`, tenantA)
			if err != nil {
				return err
			}
			out, err = pgx.CollectRows(r, pgx.RowToStructByPos[dayRow])
			return err
		}))
		return out
	}
	yesterday := today.AddDate(0, 0, -1).Format(time.DateOnly)
	require.Equal(t, []dayRow{
		{contract, today.AddDate(0, 0, -2).Format(time.DateOnly), "rpc_events", 1, 1, 0},
		{account, today.AddDate(0, 0, -3).Format(time.DateOnly), "horizon", 2, 1, 1},
		{account, yesterday, "horizon", 1, 1, 1},
	}, rows())

	var accountDays, contractDays int
	var home *string
	var covFrom, covTo string
	require.NoError(t, d.WithRole(ctx, pdb.RoleJobs, func(x pgx.Tx) error {
		if err := x.QueryRow(ctx, `select count(*) from agg.chain_daily where tenant_id = $1 and strkey = $2`, tenantA, account).Scan(&accountDays); err != nil {
			return err
		}
		if err := x.QueryRow(ctx, `select count(*) from agg.chain_daily where tenant_id = $1 and strkey = $2`, tenantA, contract).Scan(&contractDays); err != nil {
			return err
		}
		return x.QueryRow(ctx, `select home_domain, covered_from::text, covered_to::text from agg.chain_reads where tenant_id = $1 and strkey = $2`,
			tenantA, account).Scan(&home, &covFrom, &covTo)
	}))
	// zeros are stored for every read day: 90 days of account backfill,
	// the contract from the first whole day the rpc server keeps
	require.Equal(t, chainactivity.AccountBackfillDays, accountDays)
	require.Equal(t, 6, contractDays)
	require.Equal(t, "alpha.example.org", *home)
	require.Equal(t, yesterday, covTo)
	require.Equal(t, today.AddDate(0, 0, -chainactivity.AccountBackfillDays).Format(time.DateOnly), covFrom)

	// the muxed id of tenant b is not copied, its g account is read for b too
	var bIDs int
	require.NoError(t, d.WithSystem(ctx, func(x pgx.Tx) error {
		return x.QueryRow(ctx, `select count(*) from cp.chain_ids where tenant_id = $1`, tenantB).Scan(&bIDs)
	}))
	require.Equal(t, 1, bIDs)

	// nothing is due on the same day
	res, err = task.Run(ctx, tx)
	require.NoError(t, err)
	require.Zero(t, res.IDs)

	// the next day the source fails: no row for the new day, the reason is
	// stored, and the older days stay
	now = now.Add(24 * time.Hour)
	l.latest += perDay
	l.horizonDown.Store(true)
	res, err = task.Run(ctx, tx)
	require.Error(t, err)
	require.Equal(t, 2, res.Failed)
	var lastErr *string
	var newDay int
	require.NoError(t, d.WithRole(ctx, pdb.RoleJobs, func(x pgx.Tx) error {
		if err := x.QueryRow(ctx, `select last_error from agg.chain_reads where tenant_id = $1 and strkey = $2`, tenantA, account).Scan(&lastErr); err != nil {
			return err
		}
		return x.QueryRow(ctx, `select count(*) from agg.chain_daily where tenant_id = $1 and strkey = $2 and day = $3`,
			tenantA, account, today).Scan(&newDay)
	}))
	require.Equal(t, chain.CodeUnavailable, *lastErr)
	require.Zero(t, newDay)
	require.Len(t, rows(), 3)

	// a removed tenant gets no new rows
	require.NoError(t, d.WithSystem(ctx, func(x pgx.Tx) error {
		_, err := x.Exec(ctx, `update cp.tenants set status = 'removed' where id = $1`, tenantB)
		return err
	}))
	now = now.Add(2 * time.Hour)
	l.horizonDown.Store(false)
	res, err = task.Run(ctx, tx)
	require.NoError(t, err)
	require.Equal(t, 1, res.IDs, "the account of tenant a; its contract was read the run before")
}
