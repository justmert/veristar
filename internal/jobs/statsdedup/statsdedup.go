// Package statsdedup is the jobs task that keeps cp.stats_dedup bounded. the
// table holds every EDGE_STATS message id the edgestats task applied; an id
// is only needed while its message could still be delivered, and EDGE_STATS
// drops messages after 7 days.
package statsdedup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/db/jobsq"
)

// TaskName is the name of the task in the scheduler and in `jobs run`.
const TaskName = "statsdedup-prune"

// Every is how often the daemon prunes. the table grows by a few windows per
// node and minute, so hourly keeps it small.
const Every = time.Hour

// MaxAge is how long an id is kept: the EDGE_STATS max age of 7 days plus a
// day of margin, so an id is never pruned while its message can still be
// redelivered.
const MaxAge = 8 * 24 * time.Hour

// TxFunc runs fn in one fenced jobs transaction (scheduler.Leader.Tx).
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Pruner deletes old ids.
type Pruner struct {
	now    func() time.Time
	log    *slog.Logger
	pruned prometheus.Counter
}

// New returns a Pruner. now is the clock; tests inject one.
func New(now func() time.Time, log *slog.Logger, reg prometheus.Registerer) (*Pruner, error) {
	if now == nil || log == nil || reg == nil {
		return nil, errors.New("statsdedup: needs a clock, a log and a registerer")
	}
	p := &Pruner{now: now, log: log, pruned: prometheus.NewCounter(prometheus.CounterOpts{
		Name: "jobs_statsdedup_pruned_total",
		Help: "cp.stats_dedup message ids deleted because they are older than the EDGE_STATS max age plus a day.",
	})}
	if err := reg.Register(p.pruned); err != nil {
		return nil, fmt.Errorf("statsdedup: register metrics: %w", err)
	}
	return p, nil
}

// Prune deletes ids applied more than MaxAge ago, inside one fenced
// transaction, and returns how many it deleted.
func (p *Pruner) Prune(ctx context.Context, tx TxFunc) (int64, error) {
	cutoff := p.now().Add(-MaxAge)
	var n int64
	err := tx(ctx, func(tx pgx.Tx) error {
		var err error
		n, err = jobsq.New(tx).PruneStatsDedup(ctx, cutoff)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("statsdedup: prune: %w", err)
	}
	p.pruned.Add(float64(n))
	p.log.Info("stats dedup ids pruned", "deleted", n, "cutoff", cutoff.UTC().Format(time.RFC3339))
	return n, nil
}
