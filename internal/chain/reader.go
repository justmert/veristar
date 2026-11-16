package chain

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Day is the activity of one id on one utc day.
type Day struct {
	Day            time.Time
	Operations     int64
	Transactions   int64
	Counterparties int64
}

// sources of agg.chain_daily
const (
	SourceHorizon   = "horizon"
	SourceRPCEvents = "rpc_events"
)

// read caps per id and run. a project account rarely has more than a few
// thousand operations a day; an id over the cap keeps the days it
// completed and fails with day_over_read_cap on the day it could not
// finish, so a partial day is never stored as a whole one.
const (
	MaxOperationPages = 100
	MaxEventPages     = 50
)

// Reader turns ledger records into daily counts.
type Reader struct {
	Horizon *Horizon
	RPC     *RPC

	mu      sync.Mutex
	horizon map[int64]int64
	rpc     map[int64]uint32
}

// NewReader needs both sources: horizon for accounts, rpc for contracts.
func NewReader(h *Horizon, r *RPC) (*Reader, error) {
	if h == nil || r == nil {
		return nil, errors.New("chain: the reader needs horizon and rpc")
	}
	return &Reader{Horizon: h, RPC: r, horizon: map[int64]int64{}, rpc: map[int64]uint32{}}, nil
}

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// firstAtOrAfter finds the smallest ledger in [lo, hi] that closed at or
// after t, by binary search over closeAt. hi+1 means none did.
func firstAtOrAfter(ctx context.Context, lo, hi int64, t time.Time, closeAt func(context.Context, int64) (time.Time, error)) (int64, error) {
	for lo <= hi {
		mid := lo + (hi-lo)/2
		at, err := closeAt(ctx, mid)
		if err != nil {
			return 0, err
		}
		if at.Before(t) {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return lo, nil
}

// HorizonWindow returns the first whole utc day horizon holds history for.
func (r *Reader) HorizonWindow(ctx context.Context) (time.Time, Root, error) {
	root, err := r.Horizon.Root(ctx)
	if err != nil {
		return time.Time{}, root, err
	}
	l, err := r.Horizon.Ledger(ctx, root.ElderLedger)
	if err != nil {
		return time.Time{}, root, err
	}
	return utcDay(l.ClosedAt).AddDate(0, 0, 1), root, nil
}

// horizonLedger is the first ledger of day, cached for the reader's life
// (one jobs run), since every account of a run starts on the same days.
func (r *Reader) horizonLedger(ctx context.Context, day time.Time, root Root) (int64, error) {
	key := day.Unix()
	r.mu.Lock()
	seq, ok := r.horizon[key]
	r.mu.Unlock()
	if ok {
		return seq, nil
	}
	seq, err := firstAtOrAfter(ctx, root.ElderLedger, root.LatestLedger, day, func(ctx context.Context, s int64) (time.Time, error) {
		l, err := r.Horizon.Ledger(ctx, s)
		return l.ClosedAt, err
	})
	if err != nil {
		return 0, err
	}
	r.mu.Lock()
	r.horizon[key] = seq
	r.mu.Unlock()
	return seq, nil
}

// tally counts one day.
type tally struct {
	ops     int64
	txs     map[string]struct{}
	parties map[string]struct{}
}

func newTally() *tally {
	return &tally{txs: map[string]struct{}{}, parties: map[string]struct{}{}}
}

// days returns one Day for every day of [from, through], zeros included.
func days(from, through time.Time, by map[int64]*tally) []Day {
	var out []Day
	for d := from; !d.After(through); d = d.AddDate(0, 0, 1) {
		x := Day{Day: d}
		if t, ok := by[d.Unix()]; ok {
			x.Operations, x.Transactions, x.Counterparties = t.ops, int64(len(t.txs)), int64(len(t.parties))
		}
		out = append(out, x)
	}
	return out
}

// AccountDays reads the completed utc days from..through of an account
// from horizon: successful operations that involve the account, the
// distinct transactions they belong to, and the distinct other accounts
// they name. it returns the days it completed; when it could not complete
// them all, the error says why and the days returned are still whole.
func (r *Reader) AccountDays(ctx context.Context, account string, from, through time.Time, root Root) ([]Day, error) {
	if through.Before(from) {
		return nil, nil
	}
	start, err := r.horizonLedger(ctx, from, root)
	if err != nil {
		return nil, err
	}
	by := map[int64]*tally{}
	cursor := strconv.FormatInt(start<<32, 10)
	var last time.Time
	for range MaxOperationPages {
		ops, err := r.Horizon.AccountOperations(ctx, account, cursor)
		if err != nil {
			return partial(from, last, by), err
		}
		for _, op := range ops {
			d := utcDay(op.CreatedAt)
			if d.After(through) {
				return days(from, through, by), nil
			}
			if d.Before(from) {
				continue
			}
			t := by[d.Unix()]
			if t == nil {
				t = newTally()
				by[d.Unix()] = t
			}
			t.ops++
			t.txs[op.TxHash] = struct{}{}
			for _, p := range op.Counterparties(account) {
				t.parties[p] = struct{}{}
			}
			last = d
			cursor = op.PagingToken
		}
		if len(ops) < MaxPage {
			// the newest operation is read; the latest ledger closed after
			// through, which is a day before today
			return days(from, through, by), nil
		}
	}
	return partial(from, last, by), fail(CodeDayTooLarge, "%s has more than %d operations to read in one run", account, MaxOperationPages*MaxPage)
}

// partial returns the whole days before last, the day being read when the
// read stopped.
func partial(from, last time.Time, by map[int64]*tally) []Day {
	if last.IsZero() || !last.After(from) {
		return nil
	}
	return days(from, last.AddDate(0, 0, -1), by)
}

// rpcMargin is how many ledgers (about ten minutes) above the server's
// oldest one the reader starts.
const rpcMargin = 100

// RPCWindow returns the first whole utc day the rpc server holds events
// for, and the server's health.
func (r *Reader) RPCWindow(ctx context.Context) (time.Time, Health, error) {
	h, err := r.RPC.Health(ctx)
	if err != nil {
		return time.Time{}, h, err
	}
	// the server drops its oldest ledgers continuously, so the oldest one
	// of getHealth can be gone a moment later; reading a few minutes of
	// ledgers above it keeps every later lookup inside the window
	if h.LatestLedger > h.OldestLedger+rpcMargin {
		h.OldestLedger += rpcMargin
	}
	at, err := r.RPC.LedgerClose(ctx, h.OldestLedger)
	if err != nil {
		return time.Time{}, h, err
	}
	return utcDay(at).AddDate(0, 0, 1), h, nil
}

func (r *Reader) rpcLedger(ctx context.Context, day time.Time, h Health) (uint32, error) {
	key := day.Unix()
	r.mu.Lock()
	seq, ok := r.rpc[key]
	r.mu.Unlock()
	if ok {
		return seq, nil
	}
	s, err := firstAtOrAfter(ctx, int64(h.OldestLedger), int64(h.LatestLedger), day, func(ctx context.Context, s int64) (time.Time, error) {
		return r.RPC.LedgerClose(ctx, uint32(s))
	})
	if err != nil {
		return 0, err
	}
	seq = uint32(s)
	r.mu.Lock()
	r.rpc[key] = seq
	r.mu.Unlock()
	return seq, nil
}

// cursorLedger reads the ledger of a getEvents cursor ("<toid>-<index>").
func cursorLedger(c string) (uint32, bool) {
	toid, _, _ := strings.Cut(c, "-")
	n, err := strconv.ParseInt(toid, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return uint32(n >> 32), true
}

// ContractDays reads the completed utc days from..through of a contract
// from the events it emitted in successful calls: operations are the
// distinct (transaction, operation) pairs with an event, transactions the
// distinct transactions, and counterparties the distinct accounts and
// contracts named in the event topics, other than the contract itself. a
// contract that emits no events shows no activity through this source.
func (r *Reader) ContractDays(ctx context.Context, contract string, from, through time.Time, h Health) ([]Day, error) {
	if through.Before(from) {
		return nil, nil
	}
	start, err := r.rpcLedger(ctx, from, h)
	if err != nil {
		return nil, err
	}
	by := map[int64]*tally{}
	ops := map[string]struct{}{}
	cursor := ""
	var last time.Time
	for range MaxEventPages {
		p, err := r.RPC.ContractEvents(ctx, contract, start, cursor)
		if err != nil {
			return partial(from, last, by), err
		}
		for _, e := range p.Events {
			at, err := time.Parse(time.RFC3339, e.LedgerClosedAt)
			if err != nil {
				return partial(from, last, by), fail(CodeBadResponse, "event without ledgerClosedAt")
			}
			d := utcDay(at)
			if d.After(through) {
				return days(from, through, by), nil
			}
			if d.Before(from) || (e.Successful != nil && !*e.Successful) {
				continue
			}
			t := by[d.Unix()]
			if t == nil {
				t = newTally()
				by[d.Unix()] = t
			}
			opKey := e.TxHash + "/" + strconv.FormatUint(uint64(e.OpIndex), 10)
			if _, seen := ops[opKey]; !seen {
				ops[opKey] = struct{}{}
				t.ops++
			}
			t.txs[e.TxHash] = struct{}{}
			for _, a := range e.TopicAddresses() {
				if a != contract {
					t.parties[a] = struct{}{}
				}
			}
			last = d
		}
		cl, ok := cursorLedger(p.Cursor)
		if !ok || p.Cursor == "" || cl >= p.LatestLedger {
			if close, ok := p.LatestClose(); ok && !close.Before(through.AddDate(0, 0, 1)) {
				return days(from, through, by), nil
			}
			return partial(from, last, by), fail(CodeUnavailable, "the rpc server's latest ledger is older than the end of %s", through.Format(time.DateOnly))
		}
		cursor = p.Cursor
	}
	return partial(from, last, by), fail(CodeDayTooLarge, "%s has more than %d events to read in one run", contract, MaxEventPages*EventLimit)
}
