package gateway

import (
	"container/list"
	"errors"
	"fmt"
	"sync"
	"time"
)

// VisitorVolumeWindow is the window VISITOR_EVENTS_PER_HOUR counts in.
const VisitorVolumeWindow = time.Hour

// VisitorVolume counts events per site and visitor token on this node in a
// window of one hour from the visitor's first event. same-ip skepticism
// (IPCluster) counts distinct visitor tokens only, so one scripted client
// with one address and one user agent stays one token and could send at the
// prefix rate into the primary band; past the limit its events go to the
// quarantine band for the rest of the window. the table is a bounded
// lru, like the replay table, so forged tokens cannot grow memory; a visitor
// pushed out starts counting again.
type VisitorVolume struct {
	limit   int
	entries int
	now     func() time.Time

	mu    sync.Mutex
	order *list.List // front is most recent; values are *volumeEntry
	index map[string]*list.Element
}

type volumeEntry struct {
	key   string
	start time.Time
	count int
}

// NewVisitorVolume takes the events one visitor may send per hour on this
// node before the rest go to quarantine, and the table size.
func NewVisitorVolume(limit, entries int, now func() time.Time) (*VisitorVolume, error) {
	if limit < 1 {
		return nil, fmt.Errorf("gateway: visitor events per hour %d must be at least 1", limit)
	}
	if entries < 1 || now == nil {
		return nil, errors.New("gateway: visitor volume needs a table size and a clock")
	}
	return &VisitorVolume{limit: limit, entries: entries, now: now, order: list.New(), index: map[string]*list.Element{}}, nil
}

// Observe counts one queued event of the visitor and reports whether it is
// past the hourly limit. vt is already a keyed hash, so the key holds no
// address.
func (v *VisitorVolume) Observe(siteID, vt string) bool {
	key := siteID + "\x00" + vt
	now := v.now()
	v.mu.Lock()
	defer v.mu.Unlock()
	if el, ok := v.index[key]; ok {
		v.order.MoveToFront(el)
		e := el.Value.(*volumeEntry) //nolint:errcheck,forcetypeassert // only *volumeEntry is stored
		if now.Sub(e.start) >= VisitorVolumeWindow {
			e.start, e.count = now, 0
		}
		e.count++
		return e.count > v.limit
	}
	v.index[key] = v.order.PushFront(&volumeEntry{key: key, start: now, count: 1})
	if v.order.Len() > v.entries {
		oldest := v.order.Back()
		v.order.Remove(oldest)
		delete(v.index, oldest.Value.(*volumeEntry).key) //nolint:errcheck,forcetypeassert // only *volumeEntry is stored
	}
	return 1 > v.limit
}
