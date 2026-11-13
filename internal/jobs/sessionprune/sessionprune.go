// Package sessionprune is the jobs task that deletes expired console
// sessions from cp.sessions. an expired row is already refused at sign-in
// time (FindSession filters on expiry), so the task only keeps the table
// from growing; it runs in the jobs daemon rather than in every control
// plane instance so one holder of the jobs lock does the deleting.
package sessionprune

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/db/systemq"
)

// TaskName is the name of the task in the scheduler and in `jobs run`.
const TaskName = "session-prune"

// Every is how often the daemon prunes. sessions idle out after 24 hours,
// so hourly keeps at most about a day of dead rows.
const Every = time.Hour

// TxFunc runs fn in one fenced cp_system transaction
// (scheduler.Leader.SystemTx).
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Pruner deletes expired sessions.
type Pruner struct {
	now    func() time.Time
	log    *slog.Logger
	pruned prometheus.Counter
}

// New returns a Pruner. now is the clock; tests inject one.
func New(now func() time.Time, log *slog.Logger, reg prometheus.Registerer) (*Pruner, error) {
	if now == nil || log == nil || reg == nil {
		return nil, errors.New("sessionprune: needs a clock, a log and a registerer")
	}
	p := &Pruner{now: now, log: log, pruned: prometheus.NewCounter(prometheus.CounterOpts{
		Name: "jobs_sessions_pruned_total",
		Help: "Expired console sessions deleted from cp.sessions.",
	})}
	if err := reg.Register(p.pruned); err != nil {
		return nil, fmt.Errorf("sessionprune: register metrics: %w", err)
	}
	return p, nil
}

// Prune deletes sessions whose expiry is at or before now, inside one
// fenced transaction, and returns how many it deleted.
func (p *Pruner) Prune(ctx context.Context, tx TxFunc) (int64, error) {
	cutoff := p.now()
	var n int64
	err := tx(ctx, func(tx pgx.Tx) error {
		var err error
		n, err = systemq.New(tx).PruneSessions(ctx, cutoff)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("sessionprune: prune: %w", err)
	}
	p.pruned.Add(float64(n))
	p.log.Info("expired sessions pruned", "deleted", n, "cutoff", cutoff.UTC().Format(time.RFC3339))
	return n, nil
}
