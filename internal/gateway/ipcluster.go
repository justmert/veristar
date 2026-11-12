package gateway

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/justmert/stellar-analytics/internal/natsx"
)

// IPClusterWindow is the sliding window of the same-ip check the design
// 4.4 step 9).
const IPClusterWindow = time.Hour

// IPCluster tracks, per site and truncated-address key, the distinct visitor
// tokens seen in the last hour across all gateways. each node publishes every
// token that is new to it on edge.ipcluster and merges what its peers
// publish, so every node counts the same set.
//
// the threshold is reached when the set holds IPCLUSTER_THRESHOLD distinct
// tokens. from that event on, every event for the key is quarantined until
// one window has passed since the threshold was last reached. a set never needs
// more than threshold tokens to decide, so it stores at most that many, and
// a node publishes at most threshold messages per key and window.
type IPCluster struct {
	nc        *nats.Conn
	node      string
	threshold int
	maxKeys   int
	now       func() time.Time

	mu   sync.Mutex
	keys map[clusterKey]*list.Element // of *clusterSet, front is most recently used
	lru  *list.List
	sub  *nats.Subscription

	evicted  atomic.Uint64
	rejected atomic.Uint64
}

type clusterKey struct{ site, prefix string }

type clusterSet struct {
	key clusterKey
	// tokens maps a token to when this node first learned it. an entry
	// leaves the window one hour after that.
	tokens map[string]time.Time
	// published holds when this node published, newest last, within the
	// window
	published []time.Time
	// quarantinedUntil is set when the threshold is reached
	quarantinedUntil time.Time
	lastUsed         time.Time
}

// ipClusterMsg is one token on edge.ipcluster. the prefix key is an hmac of
// the truncated network under a daily secret, never the network itself.
type ipClusterMsg struct {
	Node   string `json:"n"`
	Site   string `json:"s"`
	Prefix string `json:"k"`
	Token  string `json:"t"`
}

// NewIPCluster returns a tracker. node is this gateway's id, so its own
// messages can be told apart; threshold is IPCLUSTER_THRESHOLD; maxKeys
// bounds the number of (site, prefix) sets held in memory.
func NewIPCluster(nc *nats.Conn, node string, threshold, maxKeys int, now func() time.Time) (*IPCluster, error) {
	switch {
	case nc == nil || now == nil:
		return nil, errors.New("gateway: NewIPCluster needs a connection and a clock")
	case node == "":
		return nil, errors.New("gateway: ip-cluster node id is empty")
	case threshold < 1:
		return nil, fmt.Errorf("gateway: IPCLUSTER_THRESHOLD %d is below 1", threshold)
	case maxKeys < 1:
		return nil, fmt.Errorf("gateway: ip-cluster key bound %d is below 1", maxKeys)
	}
	return &IPCluster{
		nc:        nc,
		node:      node,
		threshold: threshold,
		maxKeys:   maxKeys,
		now:       now,
		keys:      make(map[clusterKey]*list.Element),
		lru:       list.New(),
	}, nil
}

// Start subscribes to edge.ipcluster and prunes expired entries once a
// minute until ctx ends. every node subscribes without a queue group,
// because every node must see every token.
func (c *IPCluster) Start(ctx context.Context, log *slog.Logger) error {
	sub, err := c.nc.Subscribe(natsx.SubjectIPCluster, func(m *nats.Msg) {
		if err := c.merge(m.Data); err != nil {
			c.rejected.Add(1)
			log.Warn("ip-cluster message refused", "error", err.Error())
		}
	})
	if err != nil {
		return fmt.Errorf("gateway: subscribe %s: %w", natsx.SubjectIPCluster, err)
	}
	c.mu.Lock()
	c.sub = sub
	c.mu.Unlock()
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = sub.Unsubscribe()
				return
			case <-t.C:
				c.Prune()
			}
		}
	}()
	return nil
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := range len(s) {
		b := s[i]
		if (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			return false
		}
	}
	return true
}

func (c *IPCluster) merge(data []byte) error {
	var m ipClusterMsg
	if err := json.Unmarshal(data, &m); err != nil {
		return errors.New("gateway: ip-cluster message is not json")
	}
	if m.Node == c.node {
		// our own publish, already counted
		return nil
	}
	if m.Site == "" || len(m.Site) > 64 || !isHex64(m.Prefix) || !isHex64(m.Token) {
		return errors.New("gateway: ip-cluster message has a malformed field")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	s := c.setLocked(clusterKey{m.Site, m.Prefix}, now)
	s.add(m.Token, now, c.threshold)
	return nil
}

// setLocked returns the set for k, creating it and evicting the least
// recently used set when the bound is reached.
func (c *IPCluster) setLocked(k clusterKey, now time.Time) *clusterSet {
	if el, ok := c.keys[k]; ok {
		c.lru.MoveToFront(el)
		s, _ := el.Value.(*clusterSet)
		s.expire(now)
		s.lastUsed = now
		return s
	}
	for len(c.keys) >= c.maxKeys {
		el := c.lru.Back()
		old, _ := el.Value.(*clusterSet)
		c.lru.Remove(el)
		delete(c.keys, old.key)
		// counted, because a flood of keys can push a quarantined key out
		c.evicted.Add(1)
	}
	s := &clusterSet{key: k, tokens: make(map[string]time.Time), lastUsed: now}
	c.keys[k] = c.lru.PushFront(s)
	return s
}

// expire drops tokens and publish records older than the window.
func (s *clusterSet) expire(now time.Time) {
	cut := now.Add(-IPClusterWindow)
	for t, at := range s.tokens {
		if !at.After(cut) {
			delete(s.tokens, t)
		}
	}
	i := 0
	for i < len(s.published) && !s.published[i].After(cut) {
		i++
	}
	s.published = s.published[i:]
}

func (s *clusterSet) quarantined(now time.Time) bool {
	return now.Before(s.quarantinedUntil)
}

// add records a token and reports whether it was new to the set. once the
// set is full the threshold is reached and further tokens are not stored.
func (s *clusterSet) add(token string, now time.Time, threshold int) bool {
	if _, ok := s.tokens[token]; ok {
		return false
	}
	if len(s.tokens) >= threshold {
		return false
	}
	s.tokens[token] = now
	if len(s.tokens) >= threshold {
		// reaching the threshold again inside a quarantine extends it
		s.quarantinedUntil = now.Add(IPClusterWindow)
	}
	return true
}

// Observe records that an event with visitor token vt arrived for site from
// the network keyed by prefix (Visitor.PrefixKey), and reports whether the
// event belongs in the quarantine band. a token new to this node is
// published to the peers at once. a publish error is returned with the
// band, which is still correct for this node.
func (c *IPCluster) Observe(site, prefix, vt string) (bool, error) {
	if site == "" || len(site) > 64 || !isHex64(prefix) || !isHex64(vt) {
		return false, errors.New("gateway: ip-cluster observe with a malformed site, prefix key or token")
	}
	c.mu.Lock()
	now := c.now()
	s := c.setLocked(clusterKey{site, prefix}, now)
	isNew := s.add(vt, now, c.threshold)
	q := s.quarantined(now)
	publish := isNew && len(s.published) < c.threshold
	if publish {
		s.published = append(s.published, now)
	}
	c.mu.Unlock()
	if !publish {
		return q, nil
	}
	b, err := json.Marshal(ipClusterMsg{Node: c.node, Site: site, Prefix: prefix, Token: vt})
	if err != nil {
		return q, fmt.Errorf("gateway: encode ip-cluster message: %w", err)
	}
	if err := c.nc.Publish(natsx.SubjectIPCluster, b); err != nil {
		return q, fmt.Errorf("gateway: publish %s: %w", natsx.SubjectIPCluster, err)
	}
	return q, nil
}

// Quarantined reports whether events for site from the network keyed by
// prefix are in the quarantine band now, without recording anything.
func (c *IPCluster) Quarantined(site, prefix string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.keys[clusterKey{site, prefix}]
	if !ok {
		return false
	}
	s, _ := el.Value.(*clusterSet)
	return s.quarantined(c.now())
}

// Distinct is how many distinct tokens this node holds for the key within
// the window, at most the threshold.
func (c *IPCluster) Distinct(site, prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.keys[clusterKey{site, prefix}]
	if !ok {
		return 0
	}
	s, _ := el.Value.(*clusterSet)
	s.expire(c.now())
	return len(s.tokens)
}

// Prune drops expired tokens and removes sets that hold nothing and are no
// longer quarantined, so idle keys do not stay in memory.
func (c *IPCluster) Prune() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, el := range c.keys {
		s, _ := el.Value.(*clusterSet)
		s.expire(now)
		if len(s.tokens) == 0 && len(s.published) == 0 && !s.quarantined(now) {
			c.lru.Remove(el)
			delete(c.keys, k)
		}
	}
}

// Keys is the number of sets held in memory.
func (c *IPCluster) Keys() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.keys)
}

// Evicted counts sets pushed out by the key bound since start.
func (c *IPCluster) Evicted() uint64 { return c.evicted.Load() }

// Rejected counts malformed messages received on edge.ipcluster since start.
func (c *IPCluster) Rejected() uint64 { return c.rejected.Load() }
