// Package chainactivity is the jobs task chain-activity (control-plane-api
// 15.6): it reads the daily ledger activity of every account and contract
// a project declared, from horizon for accounts and from stellar rpc
// events for contracts, and stores one row per id and completed utc day in
// agg.chain_daily. a day it could not read gets no row, and agg.chain_reads
// keeps the reason, so a failure shows as missing data and never as zero.
//
// the same reads give each day's initiating accounts, which go into the
// project's open iso week as keyed hashes (agg.chain_week_members); once a
// week is over the task stores its weekly active accounts, or the reason
// it has none, and deletes the hashes and the week's key (weeks.go).
package chainactivity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/chain"
)

// TaskName is the name of the task in the scheduler and in `jobs run`.
const TaskName = "chain-activity"

// Every is how often the task looks for ids whose last completed day is
// not read yet. completed days only change once a day; hourly runs catch
// up soon after a failure or a newly added id.
const Every = time.Hour

// AccountBackfillDays is how far back the first read of an account goes.
// sdf's horizon keeps about a year; 90 days bounds the first read's
// requests and covers the periods scf reviews.
const AccountBackfillDays = 90

// batch caps the ids of one run; the ones read longest ago go first.
const batch = 200

// retryAfter keeps a failed id from being read again within the same hour
// it failed, so one broken id cannot use up the request budget.
const retryAfter = 50 * time.Minute

// TxFunc runs fn in one fenced jobs transaction (scheduler.Leader.Tx).
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Task reads ledger activity.
type Task struct {
	newReader func() (*chain.Reader, error)
	now       func() time.Time
	log       *slog.Logger
	reads     *prometheus.CounterVec
}

// New returns a Task. newReader gives a fresh reader per run, so the
// ledger lookups it caches never outlive one run.
func New(newReader func() (*chain.Reader, error), now func() time.Time, log *slog.Logger, reg prometheus.Registerer) (*Task, error) {
	if newReader == nil || now == nil || log == nil || reg == nil {
		return nil, errors.New("chainactivity: needs a reader factory, a clock, a log and a registerer")
	}
	t := &Task{newReader: newReader, now: now, log: log, reads: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_chain_reads_total",
		Help: "Ledger activity reads by the chain-activity task, by kind (account, contract) and result (ok or the error code).",
	}, []string{"kind", "result"})}
	if err := reg.Register(t.reads); err != nil {
		return nil, fmt.Errorf("chainactivity: register metrics: %w", err)
	}
	return t, nil
}

// Result says what one run did.
type Result struct {
	IDs         int
	Days        int
	Failed      int
	WeeksClosed int
}

type due struct {
	tenant    uuid.UUID
	strkey    string
	kind      string
	coveredTo *time.Time
	// the first day of an open week stored without its initiators
	rereadFrom *time.Time
}

// window is a source's first whole day, read once per run.
type window struct {
	first time.Time
	root  chain.Root
	rpc   chain.Health
	err   error
	done  bool
}

// read is the outcome for one strkey, shared by every tenant that
// declared it.
type read struct {
	days       []chain.Day
	err        error
	homeDomain *string
	exists     *bool
}

// Run reads every due id once. an id that fails keeps its stored days and
// records the reason; the run goes on, and the error returned counts the
// failures so the scheduler marks the run failed.
func (t *Task) Run(ctx context.Context, tx TxFunc) (Result, error) {
	now := t.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	yesterday := today.AddDate(0, 0, -1)
	open := OpenFrom(today)
	if err := t.openWeeks(ctx, tx, today); err != nil {
		return Result{}, err
	}
	var list []due
	err := tx(ctx, func(x pgx.Tx) error {
		// an id is due when its last completed day is not read, or when a
		// day of an open week was stored before its initiators were read
		rows, err := x.Query(ctx, `
			select c.tenant_id, c.strkey, c.kind, r.covered_to,
			       (select min(d.day) from agg.chain_daily d
			        where d.tenant_id = c.tenant_id and d.strkey = c.strkey
			          and d.day >= @open_from::date and d.initiators is null
			          and not exists (select 1 from agg.chain_weeks w
			                          where w.tenant_id = d.tenant_id and w.week = date_trunc('week', d.day)::date
			                            and w.closed_at is not null)) as reread_from
			from cp.chain_ids c
			join cp.tenants t on t.id = c.tenant_id and t.status <> 'removed'
			left join agg.chain_reads r on r.tenant_id = c.tenant_id and r.strkey = c.strkey
			where (r.last_error_at is null or r.last_error_at < @retry_before::timestamptz)
			order by r.last_attempt_at nulls first, c.created_at`,
			pgx.NamedArgs{"open_from": open, "retry_before": now.Add(-retryAfter)})
		if err != nil {
			return err
		}
		all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
			var d due
			return d, r.Scan(&d.tenant, &d.strkey, &d.kind, &d.coveredTo, &d.rereadFrom)
		})
		for _, d := range all {
			if (d.coveredTo == nil || d.coveredTo.Before(yesterday) || d.rereadFrom != nil) && len(list) < batch {
				list = append(list, d)
			}
		}
		return err
	})
	if err != nil {
		return Result{}, fmt.Errorf("chainactivity: list due ids: %w", err)
	}
	reader, err := t.newReader()
	if err != nil {
		return Result{}, err
	}
	// sources on another network would store other accounts' activity
	// under the same strkeys; a source that cannot be read now fails per
	// id below, as before
	if err := chain.CheckNetwork(ctx, reader.Horizon, reader.RPC, chain.PublicNetwork); chain.Code(err) == chain.CodeWrongNetwork {
		return Result{}, fmt.Errorf("chainactivity: %w", err)
	}
	var horizon, rpc window
	cache := map[string]read{}
	res := Result{IDs: len(list)}
	var errs []error
	for _, d := range list {
		if ctx.Err() != nil {
			break
		}
		through := yesterday
		var from time.Time
		var w *window
		if d.kind == "account" {
			w = &horizon
			if !w.done {
				w.first, w.root, w.err = reader.HorizonWindow(ctx)
				w.done = true
			}
			from = maxDay(today.AddDate(0, 0, -AccountBackfillDays), w.first)
		} else {
			w = &rpc
			if !w.done {
				w.first, w.rpc, w.err = reader.RPCWindow(ctx)
				w.done = true
			}
			from = w.first
		}
		floor := from
		if d.coveredTo != nil {
			from = maxDay(from, d.coveredTo.AddDate(0, 0, 1))
		}
		// read again the days of open weeks that miss their initiators, as
		// far back as the source still holds them
		if d.rereadFrom != nil && d.rereadFrom.Before(from) {
			from = maxDay(floor, *d.rereadFrom)
		}
		key := d.kind + ":" + d.strkey + ":" + from.Format(time.DateOnly)
		r, ok := cache[key]
		if !ok {
			r = t.read(ctx, reader, w, d, from, through)
			cache[key] = r
		}
		result := "ok"
		if r.err != nil {
			result = chain.Code(r.err)
		}
		t.reads.WithLabelValues(d.kind, result).Inc()
		if err := t.store(ctx, tx, d, r, from, open, now); err != nil {
			t.log.Error("chain activity write failed", "tenant_id", d.tenant.String(), "strkey", d.strkey, "error", err.Error())
			errs = append(errs, err)
			continue
		}
		res.Days += len(r.days)
		if r.err != nil {
			res.Failed++
			t.log.Warn("chain activity read incomplete", "tenant_id", d.tenant.String(), "strkey", d.strkey,
				"code", chain.Code(r.err), "error", r.err.Error(), "days", len(r.days))
		}
	}
	closed, cerr := t.closeWeeks(ctx, tx, today, now)
	res.WeeksClosed = closed
	if cerr != nil {
		errs = append(errs, cerr)
	}
	t.log.Info("chain activity done", "ids", res.IDs, "days", res.Days, "failed", res.Failed, "weeks_closed", res.WeeksClosed)
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if len(errs) > 0 {
		return res, fmt.Errorf("chainactivity: %d writes failed: %w", len(errs), errors.Join(errs...))
	}
	if res.Failed > 0 {
		return res, fmt.Errorf("chainactivity: %d of %d ids could not be read completely", res.Failed, res.IDs)
	}
	return res, nil
}

func maxDay(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// read fetches one id's days and, for an account, its record.
func (t *Task) read(ctx context.Context, reader *chain.Reader, w *window, d due, from, through time.Time) read {
	if w.err != nil {
		return read{err: w.err}
	}
	var out read
	if d.kind == "account" {
		a, err := reader.Horizon.Account(ctx, d.strkey)
		exists := err == nil
		if err != nil && chain.Code(err) != chain.CodeAccountMissing {
			return read{err: err}
		}
		out.exists = &exists
		if !exists {
			// an account that does not exist has no operations to read and
			// no home domain
			none := ""
			return read{err: err, exists: &exists, homeDomain: &none}
		}
		hd := a.HomeDomain
		out.homeDomain = &hd
		out.days, out.err = reader.AccountDays(ctx, d.strkey, from, through, w.root)
		return out
	}
	out.days, out.err = reader.ContractDays(ctx, d.strkey, from, through, w.rpc)
	return out
}

// store writes the days and the read state in one transaction, under the
// tenant's shared advisory lock, and writes nothing for a removed tenant.
// the initiators of days in open weeks (from open on) go into their week.
func (t *Task) store(ctx context.Context, tx TxFunc, d due, r read, from, open, now time.Time) error {
	return tx(ctx, func(x pgx.Tx) error {
		if _, err := x.Exec(ctx, `select pg_advisory_xact_lock_shared(hashtextextended('agg.tenant:' || $1::text, 0))`,
			d.tenant.String()); err != nil {
			return fmt.Errorf("lock tenant: %w", err)
		}
		var status string
		if err := x.QueryRow(ctx, `select status from cp.tenants where id = $1`, d.tenant).Scan(&status); err != nil {
			return fmt.Errorf("tenant status: %w", err)
		}
		if status == "removed" {
			return nil
		}
		source := chain.SourceHorizon
		if d.kind == "contract" {
			source = chain.SourceRPCEvents
		}
		var first, last *time.Time
		weeks := weekKeys{}
		for _, day := range r.days {
			var initiators *string
			if !day.Day.Before(open) {
				v, err := weeks.add(ctx, x, d.tenant, d.strkey, day)
				if err != nil {
					return err
				}
				initiators = v
			}
			if _, err := x.Exec(ctx, `
				insert into agg.chain_daily (tenant_id, strkey, day, source, operations, transactions, counterparties, read_at, initiators)
				values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				on conflict (tenant_id, strkey, day) do update
				set source = excluded.source, operations = excluded.operations, transactions = excluded.transactions,
				    counterparties = excluded.counterparties, read_at = excluded.read_at,
				    initiators = coalesce(excluded.initiators, agg.chain_daily.initiators)`,
				d.tenant, d.strkey, day.Day, source, day.Operations, day.Transactions, day.Counterparties, now, initiators); err != nil {
				return fmt.Errorf("upsert day: %w", err)
			}
			if first == nil {
				v := day.Day
				first = &v
			}
			v := day.Day
			last = &v
		}
		// days of open weeks before the read's first day are outside what the
		// source holds now, so their initiators can no longer be read
		if _, err := x.Exec(ctx, `
			update agg.chain_daily set initiators = 'before_source_history'
			where tenant_id = $1 and strkey = $2 and day >= $3 and day < $4 and initiators is null`,
			d.tenant, d.strkey, open, from); err != nil {
			return fmt.Errorf("mark days without initiators: %w", err)
		}
		var code *string
		var success *time.Time
		if r.err != nil {
			c := chain.Code(r.err)
			code = &c
		} else {
			success = &now
		}
		_, err := x.Exec(ctx, `
			insert into agg.chain_reads (tenant_id, strkey, covered_from, covered_to, last_attempt_at, last_success_at,
			                             last_error, last_error_at, home_domain, account_exists)
			values (@tenant, @strkey, @first, @last, @now, @success, @code,
			        case when @code::text is null then null else @now::timestamptz end, @home, @exists)
			on conflict (tenant_id, strkey) do update set
			  covered_from = coalesce(least(agg.chain_reads.covered_from, excluded.covered_from),
			                          agg.chain_reads.covered_from, excluded.covered_from),
			  covered_to = coalesce(greatest(agg.chain_reads.covered_to, excluded.covered_to),
			                        agg.chain_reads.covered_to, excluded.covered_to),
			  last_attempt_at = excluded.last_attempt_at,
			  last_success_at = coalesce(excluded.last_success_at, agg.chain_reads.last_success_at),
			  last_error = excluded.last_error,
			  last_error_at = excluded.last_error_at,
			  home_domain = coalesce(excluded.home_domain, agg.chain_reads.home_domain),
			  account_exists = coalesce(excluded.account_exists, agg.chain_reads.account_exists)`,
			pgx.NamedArgs{"tenant": d.tenant, "strkey": d.strkey, "first": first, "last": last, "now": now,
				"success": success, "code": code, "home": r.homeDomain, "exists": r.exists})
		if err != nil {
			return fmt.Errorf("upsert read state: %w", err)
		}
		return nil
	})
}
