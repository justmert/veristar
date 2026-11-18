package replayer

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

type fakeEntry struct {
	jetstream.KeyValueEntry
	value []byte
	rev   uint64
}

func (e fakeEntry) Value() []byte    { return e.value }
func (e fakeEntry) Revision() uint64 { return e.rev }

// laggingKV answers the first stale gets like a replica that has not applied
// the last put yet: with the record before it, or with none.
type laggingKV struct {
	jetstream.KeyValue
	stale   int
	missing bool
	gets    int
}

func (k *laggingKV) Get(context.Context, string) (jetstream.KeyValueEntry, error) {
	k.gets++
	if k.gets <= k.stale {
		if k.missing {
			return nil, jetstream.ErrKeyNotFound
		}
		return fakeEntry{value: []byte(`{"websites":{"w":{"cache":"old","last_event_ts":1}}}`), rev: 4}, nil
	}
	return fakeEntry{value: []byte(`{"websites":{"w":{"cache":"new","last_event_ts":2}}}`), rev: 5}, nil
}

// the record this instance wrote last is what the next event of the visitor
// goes out with, also when a replica that lags answers first.
func TestVisitLoadWaitsForOwnWrite(t *testing.T) {
	for _, missing := range []bool{false, true} {
		kv := &laggingKV{stale: 3, missing: missing}
		rec, rev, err := visitStore{kv: kv}.load(t.Context(), "s", "v", 5)
		if err != nil {
			t.Fatal(err)
		}
		if rev != 5 || rec == nil || rec.Websites["w"].Cache != "new" {
			t.Fatalf("missing=%v: got revision %d record %+v, want the revision 5 record", missing, rev, rec)
		}
	}

	// without a write of its own the first answer counts
	kv := &laggingKV{stale: 3}
	rec, rev, err := visitStore{kv: kv}.load(t.Context(), "s", "v", 0)
	if err != nil || rev != 4 || rec.Websites["w"].Cache != "old" || kv.gets != 1 {
		t.Fatalf("got revision %d record %+v after %d gets, %v", rev, rec, kv.gets, err)
	}

	// a record removed for good (an erasure) is accepted after the bound
	kv = &laggingKV{stale: 1 << 30, missing: true}
	start := time.Now()
	rec, _, err = visitStore{kv: kv}.load(t.Context(), "s", "v", 5)
	if err != nil || rec != nil || kv.gets != staleReads {
		t.Fatalf("got %+v after %d gets, %v", rec, kv.gets, err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("waited %s for a removed record", d)
	}
}
