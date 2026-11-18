package replayer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// VisitGap is the longest pause, in seconds, after which a visit still
// continues with umami's cache token. it equals
// umami's own 30 minute visit expiry, which umami skips when a timestamp is
// supplied.
const VisitGap = 1800

// VisitEntry is the continuity of one visitor on one umami website.
type VisitEntry struct {
	// SessionID is umami's session id, kept so erasure can delete the
	// session without recomputing the visitor token.
	SessionID string `json:"session_id"`
	// Cache is the cache token of umami's last answer. it is sent again only
	// while the gap to the previous event is at most VisitGap.
	Cache string `json:"cache"`
	// LastEventTS is the acceptance time, in unix seconds, of the last event
	// umami stored for this visitor on this website.
	LastEventTS int64 `json:"last_event_ts"`
}

// VisitRecord is the value of key visit.<site id>.<visitor token>. it holds
// one entry per umami website, because the primary and the quarantine band of
// a site are two websites and umami ignores a cache token of another website
// (docs/reference/umami-contract.md item 7).
type VisitRecord struct {
	Websites map[string]VisitEntry `json:"websites"`
}

// VisitKey is the visit_state key of a visitor on a site.
func VisitKey(siteID, vt string) string { return "visit." + siteID + "." + vt }

// SentKey is the duplicate guard key of an event.
func SentKey(eventID string) string { return "sent." + eventID }

// cacheFor returns the cache token to send for an event at ts (unix seconds)
// on website, or "" when the visit has to start again. a gap over VisitGap
// drops the token; umami then derives the visit from the session and the utc
// hour, so two events in the hour the visit started still join (umami's own
// rule, docs/reference/umami-contract.md item 7).
func (r *VisitRecord) cacheFor(website string, ts int64) string {
	if r == nil {
		return ""
	}
	e, ok := r.Websites[website]
	if !ok || e.Cache == "" || ts-e.LastEventTS > VisitGap {
		return ""
	}
	return e.Cache
}

// errBadVisitRecord means the stored record cannot be decoded. it costs only
// visit continuity, so the caller logs it and sends without a cache token;
// the next write replaces the record.
var errBadVisitRecord = errors.New("replayer: visit state record is not readable")

type visitStore struct{ kv jetstream.KeyValue }

const (
	// staleReads and stalePause bound how long load reads a record again
	// that is older than the revision this instance wrote last: about a
	// second, far longer than a replica takes to catch up.
	staleReads = 40
	stalePause = 25 * time.Millisecond
)

// load reads the visitor's record and its revision. the bucket allows
// direct gets, which any replica answers, and a replica that has not
// applied the last write yet answers with the record before it or none.
// minRev is the revision this instance wrote last for the visitor, 0 when
// none: an older answer is read again for up to a second, after which the
// answer counts as it is (an erasure removed the record in between). a
// visitor's events delivered back to back would otherwise go out with the
// visit state of an earlier event.
func (v visitStore) load(ctx context.Context, siteID, vt string, minRev uint64) (*VisitRecord, uint64, error) {
	var (
		e   jetstream.KeyValueEntry
		err error
	)
	for i := range staleReads {
		e, err = v.kv.Get(ctx, VisitKey(siteID, vt))
		stale := minRev > 0 && ((err == nil && e.Revision() < minRev) || errors.Is(err, jetstream.ErrKeyNotFound))
		if !stale || i == staleReads-1 || !sleepCtx(ctx, stalePause) {
			break
		}
	}
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("replayer: read visit state: %w", err)
	}
	var r VisitRecord
	if err := json.Unmarshal(e.Value(), &r); err != nil {
		return nil, e.Revision(), fmt.Errorf("%w: %w", errBadVisitRecord, err)
	}
	return &r, e.Revision(), nil
}

// save writes the visitor's record and returns its revision. one visitor's
// events are handled by one goroutine at a time, so a plain put cannot lose
// a concurrent update.
func (v visitStore) save(ctx context.Context, siteID, vt string, r *VisitRecord) (uint64, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return 0, err
	}
	rev, err := v.kv.Put(ctx, VisitKey(siteID, vt), b)
	if err != nil {
		return 0, fmt.Errorf("replayer: write visit state: %w", err)
	}
	return rev, nil
}

// bucketFull reports whether a visit_state write was refused because the
// bucket reached natsx.VisitStateMaxBytes. the bucket keeps
// keys for 24 hours and refuses new ones when full, so this lasts until old
// keys expire. the server answers a stream store failure (10077) naming the
// byte limit.
func bucketFull(err error) bool {
	var api *jetstream.APIError
	if errors.As(err, &api) && api.ErrorCode == jsErrCodeStreamStoreFailed {
		return strings.Contains(api.Description, "maximum bytes")
	}
	return strings.Contains(err.Error(), "maximum bytes exceeded")
}

// jsErrCodeStreamStoreFailed is the server's error code for a message the
// stream store refused (nats-server JSStreamStoreFailedF); nats.go 1.55.0
// has no constant for it.
const jsErrCodeStreamStoreFailed jetstream.ErrorCode = 10077

func (v visitStore) sent(ctx context.Context, eventID string) (bool, error) {
	_, err := v.kv.Get(ctx, SentKey(eventID))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, jetstream.ErrKeyNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("replayer: read duplicate guard: %w", err)
	}
}

func (v visitStore) markSent(ctx context.Context, eventID string) error {
	if _, err := v.kv.Put(ctx, SentKey(eventID), []byte{'1'}); err != nil {
		return fmt.Errorf("replayer: write duplicate guard: %w", err)
	}
	return nil
}
