package tenants

import (
	"sync"

	"github.com/google/uuid"
)

// Inflight counts the checks of one object (a site, a declared id) that
// run in this process and are not yet recorded. a rate limit that counts
// recorded attempts in the database adds them, so a burst of concurrent
// requests cannot all pass the count before the first one records itself.
type Inflight struct {
	mu sync.Mutex
	n  map[uuid.UUID]int
}

// Reserve takes a slot for id. count reads how many attempts are recorded;
// it runs after the slot is taken, so an attempt that records itself in the
// meantime is seen there or still holds its slot. ok is false when the
// recorded attempts and the other running ones already reach limit; the
// slot is given back then. release gives the slot back and must run once
// the attempt is recorded or failed.
func (f *Inflight) Reserve(id uuid.UUID, limit int, count func() (int, error)) (ok bool, release func(), err error) {
	f.mu.Lock()
	if f.n == nil {
		f.n = map[uuid.UUID]int{}
	}
	f.n[id]++
	f.mu.Unlock()
	var once sync.Once
	release = func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.n[id]--; f.n[id] <= 0 {
				delete(f.n, id)
			}
		})
	}
	recorded, err := count()
	if err != nil {
		release()
		return false, func() {}, err
	}
	f.mu.Lock()
	others := f.n[id] - 1
	f.mu.Unlock()
	if recorded+others >= limit {
		release()
		return false, func() {}, nil
	}
	return true, release, nil
}
