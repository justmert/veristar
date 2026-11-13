package metricswriter

import (
	"errors"
	"regexp"
	"time"

	"github.com/justmert/veristar/internal/config"
)

// Settings is what the writer needs besides nats and the database. every
// value is required, so a deployment that forgets one fails at startup
// instead of running with a value nobody chose; deploy/compose/dev.yml and
// docs/reference/api-storage.md give the chosen values.
type Settings struct {
	// NodeID names this instance in the lock holder. unique per instance.
	NodeID string
	// OpsAddr is the private listen address of /healthz, /readyz, /metrics.
	OpsAddr string
	// Batch is the most API_METRICS messages applied in one transaction.
	Batch int
	// FirstPointThreshold is the age above which the first point of a
	// process the writer never saw becomes a baseline (the design).
	FirstPointThreshold time.Duration
	// RouteCap and ServiceCap bound the distinct routes and services a
	// tenant gets per utc day; the rest are stored as {other}.
	RouteCap   int
	ServiceCap int
}

var nodeIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,48}$`)

// maxBatch stays under the consumer's server-side max ack pending (1000 by
// default), so one fetch can always be filled.
const maxBatch = 900

// LoadSettings reads METRICSWRITER_NODE_ID, METRICSWRITER_OPS_ADDR,
// METRICSWRITER_BATCH, METRICSWRITER_FIRST_POINT_THRESHOLD,
// METRICSWRITER_ROUTE_CAP and METRICSWRITER_SERVICE_CAP.
func LoadSettings(e *config.Env) (Settings, error) {
	s := Settings{
		NodeID:              e.String("METRICSWRITER_NODE_ID"),
		OpsAddr:             e.ListenAddr("METRICSWRITER_OPS_ADDR"),
		Batch:               e.Int("METRICSWRITER_BATCH", 1, maxBatch),
		FirstPointThreshold: e.Duration("METRICSWRITER_FIRST_POINT_THRESHOLD"),
		RouteCap:            e.Int("METRICSWRITER_ROUTE_CAP", 1, 100_000),
		ServiceCap:          e.Int("METRICSWRITER_SERVICE_CAP", 1, 10_000),
	}
	err := e.Err()
	if s.NodeID != "" && !nodeIDRe.MatchString(s.NodeID) {
		err = errors.Join(err, &config.KeyError{Key: "METRICSWRITER_NODE_ID", Reason: "must be 1 to 48 letters, digits, - or _"})
	}
	return s, err
}

func (s Settings) valid() bool {
	return nodeIDRe.MatchString(s.NodeID) && s.Batch >= 1 && s.Batch <= maxBatch &&
		s.FirstPointThreshold > 0 && s.RouteCap >= 1 && s.ServiceCap >= 1
}
