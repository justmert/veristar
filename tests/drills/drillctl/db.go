package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// the superuser password comes from the environment (the drill scripts
// decrypt it from the local sops file), never from a flag, so it does not
// show in the process list
func pgPassword() (string, error) {
	p := os.Getenv("SA_DRILL_PG_PASSWORD")
	if p == "" {
		return "", errors.New("SA_DRILL_PG_PASSWORD is not set")
	}
	return p, nil
}

// leader asks each core node's patroni which one is the primary, as haproxy
// does
func leader(ctx context.Context, inv inventory) (string, error) {
	tr, err := privateTransport()
	if err != nil {
		return "", err
	}
	hc := &http.Client{Timeout: 2 * time.Second, Transport: tr}
	for _, c := range inv.group("core") {
		resp, err := httpGet(ctx, hc, "https://"+c.Private+":8008/primary")
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return c.Private, nil
		}
	}
	return "", errors.New("no patroni primary answers")
}

func connect(ctx context.Context, addr, db string) (*pgx.Conn, error) {
	pw, err := pgPassword()
	if err != nil {
		return nil, err
	}
	// postgresql admits tls only (roles/core pg_hba); the certificate names
	// the private address the drill dials
	cfg, err := pgx.ParseConfig(fmt.Sprintf("host=%s port=5432 dbname=%s user=postgres sslmode=verify-full sslrootcert=%s connect_timeout=5 application_name=drillctl",
		addr, db, filepath.Join(os.Getenv("SA_DRILL_TLS_DIR"), "ca.crt")))
	if err != nil {
		return nil, err
	}
	cfg.Password = pw
	return pgx.ConnectConfig(ctx, cfg)
}

func connectLeader(ctx context.Context, inv inventory, db string) (*pgx.Conn, error) {
	addr, err := leader(ctx, inv)
	if err != nil {
		return nil, err
	}
	return connect(ctx, addr, db)
}

type verifyResult struct {
	Run             string   `json:"run"`
	WebAcked        int      `json:"web_acked"`
	WebStored       int      `json:"web_stored_distinct"`
	WebRows         int      `json:"web_rows"`
	WebAckedLost    int      `json:"web_acked_lost"`
	WebDuplicates   int      `json:"web_duplicate_rows"`
	WebAmbiguous    int      `json:"web_ambiguous"`
	WebAmbStored    int      `json:"web_ambiguous_stored"`
	WebUnackedStore int      `json:"web_stored_not_acked_not_ambiguous"`
	APIAcked        int64    `json:"api_acked_requests"`
	APIAmbiguous    int64    `json:"api_ambiguous_requests"`
	APIStored       int64    `json:"api_stored_requests"`
	APIAckedLostMin int64    `json:"api_acked_lost_lower_bound"`
	DrainedAfterS   float64  `json:"complete_after_load_end_s"`
	Complete        bool     `json:"complete"`
	LostSample      []string `json:"lost_sample,omitempty"`
}

// runVerify waits until every acknowledged event of a ledger is stored, or
// the wait runs out, and reports what is missing and what is doubled
func runVerify(ctx context.Context, args []string) (any, error) {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	invPath := fs.String("inv", "", "inventory json")
	lpath := fs.String("ledger", "", "ledger from load")
	wait := fs.Duration("wait", 10*time.Minute, "how long to wait for the backlog")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	inv, err := loadInventory(*invPath)
	if err != nil {
		return nil, err
	}
	var lg ledger
	if err := readJSON(*lpath, &lg); err != nil {
		return nil, err
	}
	res := verifyResult{Run: lg.Run, WebAcked: len(lg.Web.Acked), WebAmbiguous: len(lg.Web.Ambiguous)}
	for _, v := range lg.API.Acked {
		res.APIAcked += v
	}
	for _, v := range lg.API.Ambiguous {
		res.APIAmbiguous += v
	}
	deadline := time.Now().Add(*wait)
	for {
		err := verifyOnce(ctx, inv, &lg, &res)
		if err == nil && res.WebAckedLost == 0 && res.APIStored >= res.APIAcked {
			res.Complete = true
			res.DrainedAfterS = time.Since(lg.End).Seconds()
			return res, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			res.DrainedAfterS = time.Since(lg.End).Seconds()
			return res, fmt.Errorf("incomplete after %s (last error %w)", *wait, err)
		}
		time.Sleep(5 * time.Second)
	}
}

func verifyOnce(ctx context.Context, inv inventory, lg *ledger, res *verifyResult) error {
	uc, err := connectLeader(ctx, inv, "umami")
	if err != nil {
		return err
	}
	defer uc.Close(context.WithoutCancel(ctx))
	rows, err := uc.Query(ctx, `select url_path, count(*) from website_event where url_path like $1 group by url_path`, "/d/"+lg.Run+"/%")
	if err != nil {
		return err
	}
	stored := map[string]int{}
	total := 0
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			return err
		}
		stored[p] = n
		total += n
	}
	if err := rows.Err(); err != nil {
		return err
	}
	res.WebStored, res.WebRows = len(stored), total
	res.WebAckedLost, res.WebDuplicates, res.WebAmbStored, res.LostSample = 0, 0, 0, nil
	known := map[string]bool{}
	for _, p := range lg.Web.Acked {
		known[p] = true
		if stored[p] == 0 {
			res.WebAckedLost++
			if len(res.LostSample) < 10 {
				res.LostSample = append(res.LostSample, p)
			}
		}
	}
	for _, p := range lg.Web.Ambiguous {
		known[p] = true
		if stored[p] > 0 {
			res.WebAmbStored++
		}
	}
	res.WebUnackedStore = 0
	for p, n := range stored {
		res.WebDuplicates += n - 1
		if !known[p] {
			res.WebUnackedStore++
		}
	}

	pc, err := connectLeader(ctx, inv, "platform")
	if err != nil {
		return err
	}
	defer pc.Close(context.WithoutCancel(ctx))
	if err := pc.QueryRow(ctx, `select coalesce(sum(count), 0)::bigint from api.metrics_minute where service = $1`, lg.API.Service).Scan(&res.APIStored); err != nil {
		return err
	}
	res.APIAckedLostMin = max(0, res.APIAcked-res.APIStored)
	return nil
}

// runCommits inserts one row per interval through whichever node patroni
// names primary and records which inserts were acknowledged. with -check it
// reads the table on the current primary and reports acknowledged rows that
// are missing. the table lives in a database of its own, which -drop removes.
func runCommits(ctx context.Context, args []string) (any, error) {
	fs := flag.NewFlagSet("commits", flag.ContinueOnError)
	invPath := fs.String("inv", "", "inventory json")
	dur := fs.Duration("duration", 3*time.Minute, "how long to write")
	every := fs.Duration("every", 20*time.Millisecond, "one insert per interval")
	out := fs.String("out", "", "file for the acknowledged ids")
	check := fs.String("check", "", "check the acknowledged ids in this file")
	drop := fs.Bool("drop", false, "drop the drill database")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	inv, err := loadInventory(*invPath)
	if err != nil {
		return nil, err
	}
	if *drop {
		c, err := connectLeader(ctx, inv, "postgres")
		if err != nil {
			return nil, err
		}
		defer c.Close(context.WithoutCancel(ctx))
		_, err = c.Exec(ctx, `drop database if exists drill with (force)`)
		return map[string]bool{"dropped": err == nil}, err
	}
	if *check != "" {
		var ids []int64
		if err := readJSON(*check, &ids); err != nil {
			return nil, err
		}
		c, err := connectLeader(ctx, inv, "drill")
		if err != nil {
			return nil, err
		}
		defer c.Close(context.WithoutCancel(ctx))
		have := map[int64]bool{}
		rows, err := c.Query(ctx, `select id from commits`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			have[id] = true
		}
		missing := 0
		for _, id := range ids {
			if !have[id] {
				missing++
			}
		}
		res := map[string]int{"acknowledged": len(ids), "stored": len(have), "acknowledged_missing": missing}
		if missing > 0 {
			return res, fmt.Errorf("%d acknowledged commits are missing", missing)
		}
		return res, nil
	}

	admin, err := connectLeader(ctx, inv, "postgres")
	if err != nil {
		return nil, err
	}
	var exists bool
	_ = admin.QueryRow(ctx, `select exists(select 1 from pg_database where datname = 'drill')`).Scan(&exists)
	if !exists {
		if _, err := admin.Exec(ctx, `create database drill`); err != nil {
			return nil, err
		}
	}
	_ = admin.Close(context.WithoutCancel(ctx))
	var conn *pgx.Conn
	var acked []int64
	var errs int
	var firstErr, lastErr time.Time
	var id int64
	end := time.Now().Add(*dur)
	for time.Now().Before(end) && ctx.Err() == nil {
		if conn == nil {
			c, err := connectLeader(ctx, inv, "drill")
			if err != nil {
				errs++
				if firstErr.IsZero() {
					firstErr = time.Now()
				}
				lastErr = time.Now()
				time.Sleep(200 * time.Millisecond)
				continue
			}
			if _, err := c.Exec(ctx, `create table if not exists commits (id bigint primary key, at timestamptz not null default clock_timestamp())`); err != nil {
				_ = c.Close(context.WithoutCancel(ctx))
				time.Sleep(200 * time.Millisecond)
				continue
			}
			conn = c
		}
		id++
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := conn.Exec(cctx, `insert into commits (id) values ($1)`, id)
		cancel()
		if err != nil {
			errs++
			if firstErr.IsZero() {
				firstErr = time.Now()
			}
			lastErr = time.Now()
			_ = conn.Close(context.WithoutCancel(ctx))
			conn = nil
			continue
		}
		acked = append(acked, id)
		time.Sleep(*every)
	}
	if conn != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
	}
	res := map[string]any{"attempted": id, "acknowledged": len(acked), "errors": errs}
	if !firstErr.IsZero() {
		res["write_gap_s"] = lastErr.Sub(firstErr).Seconds()
		res["first_error"] = firstErr.UTC().Format(time.RFC3339Nano)
		res["last_error"] = lastErr.UTC().Format(time.RFC3339Nano)
	}
	return res, writeJSON(*out, acked)
}

// the tables whose checksums the restore drill compares: the raw web data
// in umami, and the platform's api rows and control-plane tables
var checksumTables = map[string][]string{
	"umami":    {"website_event", "session", "website", "event_data"},
	"platform": {"api.metrics_minute", "cp.tenants", "cp.sites", "cp.otlp_tokens", "cp.audit_log"},
}

type tableSum struct {
	Rows int64  `json:"rows"`
	Sum  string `json:"sum"`
}

// an order-independent checksum: the row count and the sum of the first 60
// bits of each row's md5 (60 bits keep the cast to bigint positive)
func checksum(ctx context.Context, tx pgx.Tx, table string) (tableSum, error) {
	var s tableSum
	q := fmt.Sprintf(`select count(*), coalesce(sum(('x' || substr(md5(t::text), 1, 15))::bit(60)::bigint), 0)::text from %s t`, table)
	err := tx.QueryRow(ctx, q).Scan(&s.Rows, &s.Sum)
	return s, err
}

// runSnapshot takes the checksums at one instant T. it locks every listed
// table against writes in both databases (readers go on), then reads T and
// the checksums while the locks are held, so nothing committed after the
// state it sums commits before T. writers wait a few seconds, then go on.
func runSnapshot(ctx context.Context, args []string) (any, error) {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	invPath := fs.String("inv", "", "inventory json")
	out := fs.String("out", "", "snapshot file")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	inv, err := loadInventory(*invPath)
	if err != nil {
		return nil, err
	}
	addr, err := leader(ctx, inv)
	if err != nil {
		return nil, err
	}
	txs := map[string]pgx.Tx{}
	for db, tables := range checksumTables {
		c, err := connect(ctx, addr, db)
		if err != nil {
			return nil, err
		}
		defer c.Close(context.WithoutCancel(ctx))
		tx, err := c.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		if _, err := tx.Exec(ctx, "lock table "+strings.Join(tables, ", ")+" in exclusive mode"); err != nil {
			return nil, err
		}
		txs[db] = tx
	}
	snap := map[string]any{}
	var at time.Time
	if err := txs["umami"].QueryRow(ctx, `select clock_timestamp()`).Scan(&at); err != nil {
		return nil, err
	}
	sums := map[string]tableSum{}
	for db, tables := range checksumTables {
		for _, t := range tables {
			s, err := checksum(ctx, txs[db], t)
			if err != nil {
				return nil, err
			}
			sums[db+"."+t] = s
		}
	}
	snap["t"] = at.UTC().Format("2006-01-02 15:04:05.999999") + "+00"
	snap["leader"] = addr
	snap["sums"] = sums
	return snap, writeJSON(*out, snap)
}

// runChecksum computes the same checksums on a given server, the restored
// one, and compares them with a snapshot
func runChecksum(ctx context.Context, args []string) (any, error) {
	fs := flag.NewFlagSet("checksum", flag.ContinueOnError)
	addr := fs.String("addr", "", "postgresql address")
	snapPath := fs.String("snapshot", "", "snapshot file")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	var snap struct {
		T    string              `json:"t"`
		Sums map[string]tableSum `json:"sums"`
	}
	if err := readJSON(*snapPath, &snap); err != nil {
		return nil, err
	}
	res := map[string]any{"t": snap.T}
	mismatch := 0
	got := map[string]tableSum{}
	for db, tables := range checksumTables {
		c, err := connect(ctx, *addr, db)
		if err != nil {
			return nil, err
		}
		tx, err := c.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if err != nil {
			return nil, err
		}
		for _, t := range tables {
			s, err := checksum(ctx, tx, t)
			if err != nil {
				return nil, err
			}
			got[db+"."+t] = s
			if s != snap.Sums[db+"."+t] {
				mismatch++
			}
		}
		_ = tx.Rollback(ctx)
		_ = c.Close(context.WithoutCancel(ctx))
	}
	res["restored"] = got
	res["expected"] = snap.Sums
	res["mismatches"] = mismatch
	if mismatch > 0 {
		return res, fmt.Errorf("%d tables differ from the snapshot", mismatch)
	}
	return res, nil
}
