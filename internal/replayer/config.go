package replayer

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"

	"github.com/justmert/stellar-analytics/internal/config"
)

// Settings is what the replayer needs besides nats. every value is required,
// so a deployment that forgets one fails at startup instead of running with a
// value nobody chose.
type Settings struct {
	// NodeID names this instance. it must be unique per instance and stable
	// across restarts: it is part of the lock holder and of the EDGE_STATS
	// node token ("replayer-" + NodeID).
	NodeID string
	// OpsAddr is the private listen address of /healthz, /readyz, /metrics.
	OpsAddr string
	// UmamiURL is umami on the private network, where /api/send is reachable.
	UmamiURL *url.URL
	// Workers is the fixed number of ordered delivery lanes.
	Workers int
	// MaxInFlight bounds the messages held between fetch and ack. it stays
	// below the consumer's server-side max ack pending (1000 by default).
	MaxInFlight int
}

// nodeIDRe leaves room for the "replayer-" prefix inside the 64 character
// subject token limit of EDGE_STATS.
var nodeIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,55}$`)

// maxInFlightLimit stays under the server default max ack pending of a
// consumer (1000), so the server never stops delivering to a leader that is
// below its own bound.
const maxInFlightLimit = 900

// LoadSettings reads REPLAYER_NODE_ID, REPLAYER_OPS_ADDR, UMAMI_URL,
// REPLAYER_WORKERS and REPLAYER_MAX_IN_FLIGHT.
func LoadSettings(e *config.Env) (Settings, error) {
	s := Settings{
		NodeID:      e.String("REPLAYER_NODE_ID"),
		OpsAddr:     e.ListenAddr("REPLAYER_OPS_ADDR"),
		UmamiURL:    e.URL("UMAMI_URL"),
		Workers:     e.Int("REPLAYER_WORKERS", 1, 256),
		MaxInFlight: e.Int("REPLAYER_MAX_IN_FLIGHT", 1, maxInFlightLimit),
	}
	err := e.Err()
	if s.NodeID != "" && !nodeIDRe.MatchString(s.NodeID) {
		err = errors.Join(err, &config.KeyError{Key: "REPLAYER_NODE_ID", Reason: "must be 1 to 55 letters, digits, - or _ (it becomes a nats subject token)"})
	}
	if s.Workers > 0 && s.MaxInFlight > 0 && s.MaxInFlight < s.Workers {
		err = errors.Join(err, &config.KeyError{Key: "REPLAYER_MAX_IN_FLIGHT", Reason: fmt.Sprintf("must be at least REPLAYER_WORKERS (%d), or some lanes never get work", s.Workers)})
	}
	return s, err
}
