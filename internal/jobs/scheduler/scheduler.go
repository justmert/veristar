// Package scheduler runs the jobs tasks on exactly one node at a time. the
// daemon contends for the jobs leader lock and runs every task only while it
// holds it; `jobs run <name>` takes the same lock for a single run. every
// database transaction a task commits goes through Leader.Tx, which writes
// the lock's fencing token into cp.leader_fence first, so a former leader
// that paused past its lock's ttl cannot commit after its successor did
//.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/natsx"
)

// Leader is what a task gets while its node holds the jobs lock.
type Leader struct {
	lock *natsx.Lock
	db   *pdb.DB
}

// NewLeader binds a lock and the jobs pool. the lock must be named
// natsx.LockJobs, because that is the fence row Tx advances.
func NewLeader(lock *natsx.Lock, db *pdb.DB) (*Leader, error) {
	if lock == nil || db == nil {
		return nil, errors.New("scheduler: leader needs a lock and a database")
	}
	if lock.Name() != natsx.LockJobs {
		return nil, fmt.Errorf("scheduler: lock %q is not the jobs lock %q", lock.Name(), natsx.LockJobs)
	}
	return &Leader{lock: lock, db: db}, nil
}

// Tx runs fn in a jobs transaction that first advances the jobs fence to
// this holder's token. it fails with natsx.ErrFenced when a newer holder has
// written, and with natsx.ErrLockLost when this holder knows it lost the
// lock; in both cases nothing fn did is committed.
func (l *Leader) Tx(ctx context.Context, fn func(pgx.Tx) error) error {
	f := l.lock.Fence()
	if f.Revision == 0 {
		return fmt.Errorf("%w: no fencing token, the jobs lock is not held", natsx.ErrLockLost)
	}
	return l.db.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
		if err := natsx.CheckFence(ctx, tx, natsx.LockJobs, f); err != nil {
			return err
		}
		return fn(tx)
	})
}

// SystemTx is Tx for the tasks that run control plane logic (reconcile,
// umami-rotate): a cp_system transaction that first advances the
// same jobs fence, so those writes are fenced exactly like the data tasks'.
// the jobs daemon must have opened a cp_system pool.
func (l *Leader) SystemTx(ctx context.Context, fn func(pgx.Tx) error) error {
	f := l.lock.Fence()
	if f.Revision == 0 {
		return fmt.Errorf("%w: no fencing token, the jobs lock is not held", natsx.ErrLockLost)
	}
	return l.db.WithSystem(ctx, func(tx pgx.Tx) error {
		if err := natsx.CheckFence(ctx, tx, natsx.LockJobs, f); err != nil {
			return err
		}
		return fn(tx)
	})
}

// Task is one unit of work of the jobs daemon.
type Task struct {
	Name string
	// Every is the period of a periodic task. zero makes the task
	// continuous: Run is expected to block until its context ends, and is
	// started again after RestartWait if it returns early.
	Every time.Duration
	// Run is the daemon's entry point.
	Run func(ctx context.Context, l *Leader) error
	// Once is the entry point of `jobs run <name>`: one complete pass, then
	// return. a continuous task drains what is queued and stops.
	Once func(ctx context.Context, l *Leader) error
}

// RestartWait is the pause before a continuous task that returned, or a
// periodic task that failed, is run again.
const RestartWait = 5 * time.Second

// Metrics are the scheduler's prometheus metrics.
type Metrics struct {
	leading     prometheus.Gauge
	runs        *prometheus.CounterVec
	lastSuccess *prometheus.GaugeVec
}

// NewMetrics registers the scheduler metrics on reg.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	m := &Metrics{
		leading: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "jobs_leader",
			Help: "1 while this process holds the jobs lock and runs the tasks.",
		}),
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jobs_task_runs_total",
			Help: "task runs by task and result (ok, error). a continuous task counts one run per start.",
		}, []string{"task", "result"}),
		// alerts on a task that stopped succeeding (web aggregation, the
		// daily umami password rotation) read this; a counter alone cannot
		// tell "not run for a day" after a restart reset it
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "jobs_task_last_success_timestamp_seconds",
			Help: "unix time at which the task last finished a run without error in this process. absent until the first success.",
		}, []string{"task"}),
	}
	for _, c := range []prometheus.Collector{m.leading, m.runs, m.lastSuccess} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("scheduler: register metrics: %w", err)
		}
	}
	return m, nil
}

// Scheduler runs Tasks under the jobs lock.
type Scheduler struct {
	Lock    *natsx.Lock
	DB      *pdb.DB
	Tasks   []Task
	Log     *slog.Logger
	Metrics *Metrics
	// RetryEvery is how often a follower tries to take the lock.
	RetryEvery time.Duration
	// Buckets, when set, keeps the locks bucket in existence; the elector
	// asks it for a check at once when the lock cannot be taken or renewed.
	Buckets *natsx.BucketKeeper
}

func (s *Scheduler) check() error {
	if s.Lock == nil || s.DB == nil || s.Log == nil || s.Metrics == nil || s.RetryEvery <= 0 {
		return errors.New("scheduler: needs Lock, DB, Log, Metrics and RetryEvery")
	}
	seen := map[string]bool{}
	for _, t := range s.Tasks {
		if t.Name == "" || t.Run == nil || t.Once == nil || t.Every < 0 {
			return fmt.Errorf("scheduler: task %q needs a name, Run, Once and a period of zero or more", t.Name)
		}
		if seen[t.Name] {
			return fmt.Errorf("scheduler: task %q is listed twice", t.Name)
		}
		seen[t.Name] = true
	}
	return nil
}

// Run is the daemon: it contends for the lock until ctx ends and, while it
// leads, runs every task. losing the lock stops all tasks at once.
func (s *Scheduler) Run(ctx context.Context) error {
	if err := s.check(); err != nil {
		return err
	}
	leader, err := NewLeader(s.Lock, s.DB)
	if err != nil {
		return err
	}
	e := &natsx.Elector{Lock: s.Lock, RenewEvery: natsx.LockRenewEvery, RetryEvery: s.RetryEvery, Log: s.Log, Buckets: s.Buckets}
	return e.Run(ctx, func(lctx context.Context) error {
		s.Metrics.leading.Set(1)
		defer s.Metrics.leading.Set(0)
		var wg sync.WaitGroup
		for _, t := range s.Tasks {
			wg.Go(func() { s.runTask(lctx, leader, t) })
		}
		wg.Wait()
		// tasks only return when lctx ends; returning nil lets the elector
		// tell a lost lock from a shutdown
		return nil
	})
}

func (s *Scheduler) runTask(ctx context.Context, l *Leader, t Task) {
	for {
		start := time.Now()
		err := t.Run(ctx, l)
		if ctx.Err() != nil {
			return
		}
		var wait time.Duration
		if err != nil {
			s.Metrics.runs.WithLabelValues(t.Name, "error").Inc()
			s.Log.Error("task failed", "task", t.Name, "error", err.Error())
			wait = RestartWait
		} else {
			s.Metrics.runs.WithLabelValues(t.Name, "ok").Inc()
			s.Metrics.lastSuccess.WithLabelValues(t.Name).SetToCurrentTime()
			if t.Every == 0 {
				s.Log.Warn("continuous task returned, restarting", "task", t.Name)
				wait = RestartWait
			} else {
				// the period is measured from start to start
				wait = max(t.Every-time.Since(start), 0)
			}
		}
		if !sleep(ctx, wait) {
			return
		}
	}
}

// ErrUnknownTask is returned by RunOnce for a name no task has.
var ErrUnknownTask = errors.New("scheduler: unknown task")

// RunOnce takes the lock, runs one task's Once and releases the lock. it
// fails with natsx.ErrLockHeld while a daemon leads, because two writers of
// the same tables would defeat the fence.
func (s *Scheduler) RunOnce(ctx context.Context, name string) error {
	if err := s.check(); err != nil {
		return err
	}
	var task *Task
	for i := range s.Tasks {
		if s.Tasks[i].Name == name {
			task = &s.Tasks[i]
		}
	}
	if task == nil {
		return fmt.Errorf("%w %q", ErrUnknownTask, name)
	}
	leader, err := NewLeader(s.Lock, s.DB)
	if err != nil {
		return err
	}
	if _, err := s.Lock.Acquire(ctx); err != nil {
		if errors.Is(err, natsx.ErrLockHeld) {
			return fmt.Errorf("%w: a jobs daemon is leading; it runs %s itself", err, name)
		}
		return err
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), natsx.LockRenewEvery)
		defer cancel()
		if err := s.Lock.Release(rctx); err != nil {
			s.Log.Warn("lock release failed, it expires with the bucket ttl", "lock", s.Lock.Name(), "error", err.Error())
		}
	}()

	// renew while the task runs; a failed renewal cancels it
	rctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		t := time.NewTicker(natsx.LockRenewEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				c, cc := context.WithTimeout(rctx, natsx.LockRenewEvery)
				_, err := s.Lock.Renew(c)
				cc()
				if err != nil {
					cancel(err)
					return
				}
			}
		}
	})
	err = task.Once(rctx, leader)
	close(done)
	wg.Wait()
	if cause := context.Cause(rctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return fmt.Errorf("scheduler: %s stopped: %w", name, cause)
	}
	if err != nil {
		s.Metrics.runs.WithLabelValues(name, "error").Inc()
		return fmt.Errorf("scheduler: %s: %w", name, err)
	}
	s.Metrics.runs.WithLabelValues(name, "ok").Inc()
	return nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
