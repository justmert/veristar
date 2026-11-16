package gateway

import (
	"container/list"
	"errors"
	"sync"
)

// NetworkSightings remembers which network keys this node has already
// reported for a site and utc day, so each node reports a network once per
// site and day instead of once per event. the jobs task stores the keys of
// all nodes in a set, so a network two nodes both report still counts once.
// the table is a bounded lru: a key pushed out is reported again, which the
// set absorbs.
type NetworkSightings struct {
	entries int

	mu    sync.Mutex
	order *list.List // front is most recent; values are string keys
	index map[string]*list.Element
}

// NewNetworkSightings returns a table of at most entries keys.
func NewNetworkSightings(entries int) (*NetworkSightings, error) {
	if entries < 1 {
		return nil, errors.New("gateway: network sightings need a table size")
	}
	return &NetworkSightings{entries: entries, order: list.New(), index: map[string]*list.Element{}}, nil
}

// First records the network key for the site and day and reports whether
// this node had not reported it yet.
func (n *NetworkSightings) First(siteID, day, key string) bool {
	k := siteID + "\x00" + day + "\x00" + key
	n.mu.Lock()
	defer n.mu.Unlock()
	if el, ok := n.index[k]; ok {
		n.order.MoveToFront(el)
		return false
	}
	n.index[k] = n.order.PushFront(k)
	if n.order.Len() > n.entries {
		oldest := n.order.Back()
		n.order.Remove(oldest)
		delete(n.index, oldest.Value.(string)) //nolint:errcheck,forcetypeassert // only strings are stored
	}
	return true
}
