package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"
)

// EdgeStat is one outcome counter of one site in one window of one node.
// all counters of a window travel in one EDGE_STATS message whose
// Nats-Msg-Id is node plus window sequence, so a re-sent window is stored
// once and cp.stats_dedup can refuse it after the dedup window too.
type EdgeStat struct {
	Node      string `json:"node"`
	WindowSeq uint64 `json:"window_seq"`
	SiteID    string `json:"site_id"`
	Outcome   string `json:"outcome"`
	Count     int64  `json:"count"`
}

var (
	nodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// evidence_net_<class> counts accepted events by network class,
	// network_<key> records a network seen for the site today and
	// signal_<name> counts the page signals of tracker 1.3.0 on
	// (internal/integrity); script_<name> counts requests for the tracker
	// file with and without global privacy control (internal/coverage)
	outcomeRe = regexp.MustCompile(`^(accepted|quarantined|bot|dlq|rejected_[a-z0-9_]{1,40}|evidence_net_[a-z]{1,20}|network_[0-9a-f]{16}|signal_[a-z_]{1,30}|script_[a-z_]{1,20})$`)
)

// WindowSeq numbers the window that contains t. it is derived from the clock
// and not from a counter, so a restarted node never reuses a sequence that
// cp.stats_dedup already recorded.
func WindowSeq(t time.Time, window time.Duration) uint64 {
	return uint64(t.UnixNano() / int64(window)) //nolint:gosec // unix time after 1970 is positive
}

// EdgeStatMsgID is the Nats-Msg-Id of a node's window.
func EdgeStatMsgID(node string, seq uint64) string {
	return node + "." + strconv.FormatUint(seq, 10)
}

func (s EdgeStat) validate() error {
	if !nodeRe.MatchString(s.Node) {
		return fmt.Errorf("events: node %q is not a subject token", s.Node)
	}
	if _, ok := canonicalUUID(s.SiteID); !ok {
		return errors.New("events: edge stat site_id is not a canonical uuid")
	}
	if !outcomeRe.MatchString(s.Outcome) {
		return fmt.Errorf("events: outcome %q is not recognised", s.Outcome)
	}
	if s.Count <= 0 {
		return errors.New("events: edge stat count must be positive")
	}
	return nil
}

// EncodeEdgeStats encodes one window. every entry must share node and window
// sequence.
func EncodeEdgeStats(stats []EdgeStat) (subject, msgID string, data []byte, err error) {
	if len(stats) == 0 {
		return "", "", nil, errors.New("events: empty edge stats window")
	}
	node, seq := stats[0].Node, stats[0].WindowSeq
	for _, s := range stats {
		if s.Node != node || s.WindowSeq != seq {
			return "", "", nil, errors.New("events: edge stats of one message must share node and window")
		}
		if err := s.validate(); err != nil {
			return "", "", nil, err
		}
	}
	data, err = json.Marshal(stats)
	if err != nil {
		return "", "", nil, err
	}
	return "stats.web." + node, EdgeStatMsgID(node, seq), data, nil
}

// DecodeEdgeStats decodes and validates one window.
func DecodeEdgeStats(b []byte) ([]EdgeStat, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var stats []EdgeStat
	if err := dec.Decode(&stats); err != nil {
		return nil, fmt.Errorf("events: decode edge stats: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("events: trailing data after edge stats")
	}
	if len(stats) == 0 {
		return nil, errors.New("events: empty edge stats window")
	}
	for _, s := range stats {
		if s.Node != stats[0].Node || s.WindowSeq != stats[0].WindowSeq {
			return nil, errors.New("events: edge stats of one message must share node and window")
		}
		if err := s.validate(); err != nil {
			return nil, err
		}
	}
	return stats, nil
}
