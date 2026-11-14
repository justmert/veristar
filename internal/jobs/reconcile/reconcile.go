// Package reconcile is the jobs task that runs the provisioning reconciler
// for every tenant. the control plane reconciles a tenant
// after each change; this task repairs whatever such a run missed, and
// whatever was changed directly in umami or the registry, within Every.
package reconcile

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/justmert/veristar/internal/controlplane/provision"
	"github.com/justmert/veristar/internal/jobs/scheduler"
)

// TaskName is the name of the task in the jobs daemon.
const TaskName = "reconcile"

// Every is the task's period (every 10 minutes).
const Every = 10 * time.Minute

// Task runs the reconciler with the jobs leader's fenced cp_system
// transactions.
type Task struct {
	base provision.Reconciler
	log  *slog.Logger
}

// New returns the task. base carries everything but Tx, which each run
// takes from the leader.
func New(base provision.Reconciler, log *slog.Logger) (*Task, error) {
	if log == nil {
		return nil, errors.New("reconcile: logger is required")
	}
	base.Tx = nil
	return &Task{base: base, log: log}, nil
}

// Run reconciles every tenant once.
func (t *Task) Run(ctx context.Context, l *scheduler.Leader) error {
	r := t.base
	r.Tx = l.SystemTx
	changes, err := r.ReconcileAll(ctx)
	counts := map[string]int{}
	for _, c := range changes {
		counts[c.Action]++
	}
	if len(changes) > 0 {
		t.log.Info("reconcile changed umami or the registry", "changes", len(changes), "by_action", counts)
	}
	return err
}
