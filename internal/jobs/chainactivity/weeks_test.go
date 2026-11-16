package chainactivity_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestWeekStart(t *testing.T) {
	mon := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	for i := range 7 {
		require.Equal(t, mon, chainactivity.WeekStart(mon.AddDate(0, 0, i).Add(23*time.Hour)))
	}
	require.Equal(t, mon.AddDate(0, 0, 7), chainactivity.WeekStart(mon.AddDate(0, 0, 7)))
	// on the monday after a week, that week is still open; a week later
	// it is past its grace days
	require.Equal(t, mon, chainactivity.OpenFrom(mon.AddDate(0, 0, 7)))
	require.Equal(t, mon, chainactivity.OpenFrom(mon.AddDate(0, 0, 13)))
	require.Equal(t, mon.AddDate(0, 0, 7), chainactivity.OpenFrom(mon.AddDate(0, 0, 14)))
	// one key, one account, one hash; another key, another hash
	k1, k2 := make([]byte, 32), make([]byte, 32)
	k2[0] = 1
	a := keypair.MustRandom().Address()
	require.Equal(t, chainactivity.Member(k1, a), chainactivity.Member(k1, a))
	require.NotEqual(t, chainactivity.Member(k1, a), chainactivity.Member(k2, a))
	require.Len(t, chainactivity.Member(k1, a), 32)
}

type weekRow struct {
	Week    string
	Count   *int64
	Missing *string
	Closed  bool
	HasKey  bool
}

// TestWeeks runs the task once a day over an iso week and the days after
// it: the week counts the distinct initiators of the project's verified
// account and contract, keeps only keyed hashes while it is open, and
// keeps nothing but the count once it is closed. a project without a
// verified id closes the same week as missing with its reason.
func TestWeeks(t *testing.T) {
	ctx := t.Context()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleJobs, pdb.RoleCPSystem)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, pcfg, "chainactivity-weeks-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	tx := func(ctx context.Context, fn func(pgx.Tx) error) error { return d.WithRole(ctx, pdb.RoleJobs, fn) }

	// the week of monday 2026-11-09; the task first runs on tuesday
	week := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	base := week.AddDate(0, 0, -100)
	account := keypair.MustRandom().Address()
	s1, s2, s3 := keypair.MustRandom().Address(), keypair.MustRandom().Address(), keypair.MustRandom().Address()
	contract, err := strkey.Encode(strkey.VersionByteContract, append(make([]byte, 31), 9))
	require.NoError(t, err)
	l := &ledgers{base: base, account: account, contract: contract}
	seqOf := func(at time.Time) int64 { return int64(at.Sub(base) / (6 * time.Second)) }
	op := func(at time.Time, idx int, hash, source string) {
		seq := seqOf(at)
		l.ops = append(l.ops, map[string]any{"paging_token": strconv.FormatInt(seq<<32+int64(idx), 10),
			"transaction_hash": hash, "created_at": l.closeOf(seq).Format(time.RFC3339), "source_account": source})
	}
	ev := func(at time.Time, hash string) {
		seq := seqOf(at)
		l.events = append(l.events, map[string]any{"ledger": seq, "ledgerClosedAt": l.closeOf(seq).Format(time.RFC3339),
			"contractId": contract, "txHash": hash, "operationIndex": 0, "topic": []string{}})
	}
	// monday: s1; wednesday: s1 again and s2; sunday: the account itself
	op(week.Add(3*time.Hour), 1, "a1", s1)
	op(week.AddDate(0, 0, 2).Add(time.Hour), 1, "a2", s1)
	op(week.AddDate(0, 0, 2).Add(2*time.Hour), 1, "a3", s2)
	op(week.AddDate(0, 0, 6).Add(time.Hour), 1, "a4", account)
	// tuesday: s2 calls the contract; thursday: s3
	ev(week.AddDate(0, 0, 1).Add(time.Hour), "c1")
	ev(week.AddDate(0, 0, 3).Add(time.Hour), "c2")
	l.envelopes = map[string]string{"c1": envelope(t, s2), "c2": envelope(t, s3)}

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

	var owner, verified, unverified uuid.UUID
	require.NoError(t, d.WithSystem(ctx, func(x pgx.Tx) error {
		if err := x.QueryRow(ctx, `insert into cp.users (github_id, github_login) values (9300101, 'weeks-owner') returning id`).Scan(&owner); err != nil {
			return err
		}
		ins := `insert into cp.tenants (slug, name, status, created_by, enrollment_answers) values ($1, 'P', 'active', $2, $3) returning id`
		if err := x.QueryRow(ctx, ins, "weeks-a", owner, map[string]any{"stellar_ids": []string{account, contract}}).Scan(&verified); err != nil {
			return err
		}
		if err := x.QueryRow(ctx, ins, "weeks-b", owner, map[string]any{"stellar_ids": []string{account}}).Scan(&unverified); err != nil {
			return err
		}
		_, err := x.Exec(ctx, `update cp.chain_ids set verify_method = 'sep53', verified_at = now() where tenant_id = $1`, verified)
		return err
	}))

	var now time.Time
	task, err := chainactivity.New(reader, func() time.Time { return now }, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	runOn := func(day time.Time) chainactivity.Result {
		now = day.Add(9 * time.Hour)
		l.latest = seqOf(now)
		res, err := task.Run(ctx, tx)
		require.NoError(t, err)
		return res
	}
	weeks := func(tenant uuid.UUID) []weekRow {
		var out []weekRow
		require.NoError(t, d.WithRole(ctx, pdb.RoleJobs, func(x pgx.Tx) error {
			r, err := x.Query(ctx, `select week::text, active_accounts, missing, closed_at is not null, hash_key is not null
				from agg.chain_weeks where tenant_id = $1 order by week`, tenant)
			if err != nil {
				return err
			}
			out, err = pgx.CollectRows(r, pgx.RowToStructByPos[weekRow])
			return err
		}))
		return out
	}
	members := func(tenant uuid.UUID, w time.Time) (n int64, raw int64) {
		require.NoError(t, d.WithRole(ctx, pdb.RoleJobs, func(x pgx.Tx) error {
			if err := x.QueryRow(ctx, `select count(*) from agg.chain_week_members where tenant_id = $1 and week = $2`, tenant, w).Scan(&n); err != nil {
				return err
			}
			// no member is an address in the clear
			return x.QueryRow(ctx, `select count(*) from agg.chain_week_members
				where tenant_id = $1 and member = any($2::bytea[])`, tenant,
				[][]byte{[]byte(account), []byte(s1), []byte(s2), []byte(s3)}).Scan(&raw)
		}))
		return n, raw
	}

	for i := 1; i <= 6; i++ {
		runOn(week.AddDate(0, 0, i))
	}
	// sunday: the week is open, with its key and hashed members: s1 and s2
	// for the account so far, s2 and s3 for the contract
	open := weeks(verified)
	require.Equal(t, week.Format(time.DateOnly), open[len(open)-1].Week)
	require.False(t, open[len(open)-1].Closed)
	require.True(t, open[len(open)-1].HasKey)
	n, raw := members(verified, week)
	require.Equal(t, int64(4), n)
	require.Zero(t, raw)

	// the monday after: sunday is read and the week closes with s1, s2,
	// s3 and the account itself, counted once each across both ids
	res := runOn(week.AddDate(0, 0, 7))
	require.GreaterOrEqual(t, res.WeeksClosed, 1)
	var got *weekRow
	for _, w := range weeks(verified) {
		if w.Week == week.Format(time.DateOnly) {
			got = &w
		}
	}
	require.NotNil(t, got)
	require.True(t, got.Closed)
	require.False(t, got.HasKey, "the key goes with the week")
	require.Nil(t, got.Missing)
	require.Equal(t, int64(4), *got.Count)
	n, _ = members(verified, week)
	require.Zero(t, n, "the hashes go with the week")

	// the week before the first run started before the rpc server's
	// window, so it closed as missing with that reason, never as a count
	prev := weeks(verified)[0]
	require.Equal(t, week.AddDate(0, 0, -7).Format(time.DateOnly), prev.Week)
	require.True(t, prev.Closed)
	require.Nil(t, prev.Count)
	require.Equal(t, chain.CodeBeforeHistory, *prev.Missing)

	// the project without a verified id keeps its week open through the
	// grace days, in case it verifies one, then closes it as missing
	for _, w := range weeks(unverified) {
		if w.Week == week.Format(time.DateOnly) {
			require.False(t, w.Closed)
		}
	}
	for i := 8; i <= 14; i++ {
		runOn(week.AddDate(0, 0, i))
	}
	found := false
	for _, w := range weeks(unverified) {
		if w.Week == week.Format(time.DateOnly) {
			found = true
			require.True(t, w.Closed)
			require.Nil(t, w.Count)
			require.Equal(t, chainactivity.MissingNoVerifiedIDs, *w.Missing)
		}
	}
	require.True(t, found)
}
