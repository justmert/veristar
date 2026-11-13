package replayer

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

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
	// UmamiURLs are the umami processes on the private network, where
	// /api/send is reachable. one umami process tops out near 280 events a
	// second (docs/reference/drills/2026-11-13.md), so a core node runs
	// several, and each visitor's events go to one of them, picked by the
	// same hash that picks the visitor's lane.
	UmamiURLs []*url.URL
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

// maxUmamiURLs bounds the list; more processes than lanes would leave some
// without work, and the lane count is at most 256.
const maxUmamiURLs = 64

// LoadSettings reads REPLAYER_NODE_ID, REPLAYER_OPS_ADDR, UMAMI_URL,
// REPLAYER_WORKERS and REPLAYER_MAX_IN_FLIGHT. UMAMI_URL is one url or a
// comma-separated list of distinct urls, one per umami process.
func LoadSettings(e *config.Env) (Settings, error) {
	s := Settings{
		NodeID:      e.String("REPLAYER_NODE_ID"),
		OpsAddr:     e.ListenAddr("REPLAYER_OPS_ADDR"),
		Workers:     e.Int("REPLAYER_WORKERS", 1, 256),
		MaxInFlight: e.Int("REPLAYER_MAX_IN_FLIGHT", 1, maxInFlightLimit),
	}
	raw := e.String("UMAMI_URL")
	err := e.Err()
	if raw != "" {
		urls, uerr := parseUmamiURLs(raw)
		err = errors.Join(err, uerr)
		s.UmamiURLs = urls
	}
	if s.NodeID != "" && !nodeIDRe.MatchString(s.NodeID) {
		err = errors.Join(err, &config.KeyError{Key: "REPLAYER_NODE_ID", Reason: "must be 1 to 55 letters, digits, - or _ (it becomes a nats subject token)"})
	}
	if s.Workers > 0 && len(s.UmamiURLs) > s.Workers {
		err = errors.Join(err, &config.KeyError{Key: "UMAMI_URL", Reason: fmt.Sprintf("lists %d urls, more than REPLAYER_WORKERS (%d); some umami processes would get no work", len(s.UmamiURLs), s.Workers)})
	}
	if s.Workers > 0 && s.MaxInFlight > 0 && s.MaxInFlight < s.Workers {
		err = errors.Join(err, &config.KeyError{Key: "REPLAYER_MAX_IN_FLIGHT", Reason: fmt.Sprintf("must be at least REPLAYER_WORKERS (%d), or some lanes never get work", s.Workers)})
	}
	return s, err
}

// parseUmamiURLs splits UMAMI_URL. every entry must be an absolute http or
// https url, and no entry may repeat: a repeated url would double one
// process's share without anyone having chosen that.
func parseUmamiURLs(raw string) ([]*url.URL, error) {
	parts := strings.Split(raw, ",")
	if len(parts) > maxUmamiURLs {
		return nil, &config.KeyError{Key: "UMAMI_URL", Reason: fmt.Sprintf("lists more than %d urls", maxUmamiURLs)}
	}
	seen := map[string]bool{}
	out := make([]*url.URL, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		u, err := url.Parse(p)
		if p == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, &config.KeyError{Key: "UMAMI_URL", Reason: "must be absolute http or https urls, separated by commas"}
		}
		if seen[u.String()] {
			return nil, &config.KeyError{Key: "UMAMI_URL", Reason: "lists " + u.String() + " twice"}
		}
		seen[u.String()] = true
		out = append(out, u)
	}
	return out, nil
}
