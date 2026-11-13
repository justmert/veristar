package replayer

import (
	"sync"
	"time"

	"github.com/justmert/stellar-analytics/internal/natsx"
)

// write faults. umami's collection probe proves that /api/send
// reaches the database for a read (the website lookup), not that it can
// write: a full data disk, a connection to a read-only node, lock or
// statement timeouts on website_event, or prisma's pool running dry fail
// every insert while the probe passes. a probe that writes would leave rows
// in umami (a canary website's events, removed only later by retain-web) or
// need umami admin credentials in the replayer to delete them, so the
// replayer infers the fault from the sends themselves instead: when, within
// writeFaultWindow, at least writeFaultMinVisitors distinct visitors had a
// send fail on a healthy process and they are at least as many as the sends
// that succeeded on it, the process is treated as unable to write. failures
// then say nothing about the events: they are not counted towards
// maxHealthyFailures and nothing is dead-lettered for them; the events are
// retried every writeFaultRetryMax at most, and all of them are woken by the
// first send that succeeds on the process again. one poison event cannot
// look like a fault: it is one visitor, and the other visitors' sends keep
// succeeding.
const (
	writeFaultWindow      = time.Minute
	writeFaultMinVisitors = 3
	writeFaultRetryMax    = 30 * time.Second
)

// writeWatch is the recent send record of one umami process.
type writeWatch struct {
	mu sync.Mutex
	// failed maps a visitor key to its last failed send on a healthy process
	failed map[string]time.Time
	// okSec and okCount count successful sends per second over the window,
	// as a ring indexed by the unix second
	okSec   [60]int64
	okCount [60]int64
}

func (w *writeWatch) prune(now time.Time) {
	for k, at := range w.failed {
		if now.Sub(at) >= writeFaultWindow {
			delete(w.failed, k)
		}
	}
}

func (w *writeWatch) succeededRecently(now time.Time) int64 {
	var n int64
	sec := now.Unix()
	for i := range w.okSec {
		if sec-w.okSec[i] < int64(writeFaultWindow/time.Second) {
			n += w.okCount[i]
		}
	}
	return n
}

// fail records a failed send of a visitor while the process's health checks
// passed.
func (w *writeWatch) fail(key string, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed == nil {
		w.failed = map[string]time.Time{}
	}
	w.failed[key] = now
}

// succeed records a send umami stored. it reports whether visitors had
// failed within the window, so the caller wakes the ones waiting out a
// write fault.
func (w *writeWatch) succeed(key string, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	sec := now.Unix()
	i := sec % int64(len(w.okSec))
	if w.okSec[i] != sec {
		w.okSec[i], w.okCount[i] = sec, 0
	}
	w.okCount[i]++
	delete(w.failed, key)
	w.prune(now)
	return len(w.failed) > 0
}

// faulted reports whether the process looks unable to write at now.
func (w *writeWatch) faulted(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prune(now)
	f := int64(len(w.failed))
	return f >= writeFaultMinVisitors && f >= w.succeededRecently(now)
}

// writeFaultWaiter is the gate key of visitors waiting out a write fault of
// an umami process. it is below noTarget, so the health probe, which passes
// during a write fault, never wakes them; a successful send does.
func writeFaultWaiter(target int) int { return noTarget - 1 - target }

// writeFaultDelay is the pause before retrying during a write fault: the
// usual backoff, capped so the retries notice the end of the fault soon.
func writeFaultDelay(attempt uint64) time.Duration {
	return min(natsx.RetryDelay(attempt), writeFaultRetryMax)
}
