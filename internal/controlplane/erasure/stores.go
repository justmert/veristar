package erasure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/gateway"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/replayer"
)

// tokenTimeout bounds the visitor-token request to the gateways. a gateway
// answers in milliseconds unless it has to create today's secret first.
const tokenTimeout = 10 * time.Second

// errNoGateway means no gateway answered the visitor-token request.
var errNoGateway = errors.New("erasure: no gateway answered the visitor token request")

// visitorToken asks the gateways for today's visitor token of an address and
// user agent on a site. the request carries both, so neither the request nor
// any error built here quotes them.
func visitorToken(ctx context.Context, nc *nats.Conn, site, addr, agent string) (vt string, day time.Time, err error) {
	b, err := json.Marshal(gateway.VisitorTokenRequest{Site: site, Address: addr, UserAgent: agent})
	if err != nil {
		return "", time.Time{}, errors.New("erasure: encode visitor token request")
	}
	ctx, cancel := context.WithTimeout(ctx, tokenTimeout)
	defer cancel()
	m, err := nc.RequestWithContext(ctx, natsx.SubjectVisitorToken, b)
	clear(b)
	switch {
	case errors.Is(err, nats.ErrNoResponders), errors.Is(err, context.DeadlineExceeded), errors.Is(err, nats.ErrTimeout):
		return "", time.Time{}, errNoGateway
	case err != nil:
		return "", time.Time{}, fmt.Errorf("erasure: visitor token request: %w", err)
	}
	var r gateway.VisitorTokenReply
	if err := json.Unmarshal(m.Data, &r); err != nil {
		return "", time.Time{}, errors.New("erasure: visitor token reply is not json")
	}
	if r.Error != "" {
		// the gateway's errors never quote the address or user agent
		// (gateway.ParseClientIP, Visitor.Token)
		return "", time.Time{}, fmt.Errorf("erasure: gateway refused the visitor token request: %s", r.Error)
	}
	day, err = time.Parse(time.DateOnly, r.Day)
	if err != nil || len(r.Token) != 64 {
		return "", time.Time{}, errors.New("erasure: visitor token reply is incomplete")
	}
	return r.Token, day, nil
}

// queuePollEvery is how often the queue is checked while waiting.
const queuePollEvery = 500 * time.Millisecond

// lastSeq is the last sequence of WEB_EVENTS: every event accepted before
// this moment has a sequence at or below it.
func lastSeq(ctx context.Context, events jetstream.Stream) (uint64, error) {
	info, err := events.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("erasure: %s info: %w", natsx.StreamWebEvents, err)
	}
	return info.State.LastSeq, nil
}

// errQueueBehind means the replayer did not take the site's queued events
// within the configured wait.
var errQueueBehind = errors.New("erasure: queued events did not drain in time")

// waitQueue returns once WEB_EVENTS holds no message of the site with a
// sequence at or below snap, on any of its subjects: every partition of the
// site and, while an upgrade drains it, the legacy subject. WEB_EVENTS is a
// work queue: the replayer acks a message after umami stored it or after
// moving it to WEB_DLQ, and an acked message leaves the stream. so when no
// old message of the site is left, every event accepted before the request
// is in umami or in the dlq, where the run finds and deletes it.
func waitQueue(ctx context.Context, events jetstream.Stream, site string, snap uint64, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		behind := false
		for _, subject := range natsx.WebEventsSite(site) {
			m, err := events.GetMsg(ctx, 1, jetstream.WithGetMsgSubject(subject))
			switch {
			case errors.Is(err, jetstream.ErrMsgNotFound):
			case err != nil:
				return fmt.Errorf("erasure: read %s: %w", natsx.StreamWebEvents, err)
			case m.Sequence <= snap:
				behind = true
			}
		}
		if !behind {
			return nil
		}
		if time.Now().After(deadline) {
			return errQueueBehind
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(queuePollEvery):
		}
	}
}

// deleteDLQ securely deletes every WEB_DLQ message of the site for which
// match returns true, and returns how many went. secure delete overwrites the
// stored bytes, so a deleted event does not stay in the stream's files.
func deleteDLQ(ctx context.Context, dlq jetstream.Stream, site string, match func(data []byte) bool) (int64, error) {
	subject := natsx.Subject(natsx.SubjectWebDLQ, site)
	var n int64
	for seq := uint64(1); ; {
		m, err := dlq.GetMsg(ctx, seq, jetstream.WithGetMsgSubject(subject))
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			return n, nil
		}
		if err != nil {
			return n, fmt.Errorf("erasure: read %s: %w", natsx.StreamWebDLQ, err)
		}
		if match(m.Data) {
			if err := dlq.SecureDeleteMsg(ctx, m.Sequence); err != nil {
				// a message that expired between the read and the delete is
				// gone as well; anything else is a failure
				if _, gerr := dlq.GetMsg(ctx, m.Sequence); !errors.Is(gerr, jetstream.ErrMsgNotFound) {
					return n, fmt.Errorf("erasure: delete %s message %d: %w", natsx.StreamWebDLQ, m.Sequence, err)
				}
			} else {
				n++
			}
		}
		clear(m.Data)
		seq = m.Sequence + 1
	}
}

// containsToken matches a dlq message by visitor token. the token is 64 hex
// characters, so it appears in a message only when the message carries it,
// whether or not the rest of the message still decodes as a web event.
func containsToken(vt string) func([]byte) bool {
	t := []byte(vt)
	return func(data []byte) bool { return bytes.Contains(data, t) }
}

// visitRecord reads the replayer's record of a visitor on a site; nil when
// there is none (visit_state keeps records 24 hours after the last event).
// it reads through the stream leader (leaderGet): the run reads the record
// right after the replayer acked the visitor's last event, and a replica
// that has not applied the replayer's last put would answer with an older
// record that misses the newest session.
func visitRecord(ctx context.Context, st stores, site, vt string) (*replayer.VisitRecord, error) {
	v, ok, err := leaderGet(ctx, st.nc, replayer.VisitKey(site, vt))
	if err != nil || !ok {
		return nil, err
	}
	var r replayer.VisitRecord
	if err := json.Unmarshal(v, &r); err != nil {
		return nil, fmt.Errorf("erasure: visit record of the visitor is not readable: %w", err)
	}
	return &r, nil
}

// msgGetSubject is the jetstream api that reads a stream message on the
// stream leader. visit_state allows direct gets, which any replica
// answers; this api is never answered by a replica.
const msgGetSubject = "$JS.API.STREAM.MSG.GET.KV_" + natsx.BucketVisitState

// leaderGet reads the newest value of a visit_state key on the stream
// leader. ok is false when the key has no value or its newest entry is a
// delete or purge marker.
func leaderGet(ctx context.Context, nc *nats.Conn, key string) (value []byte, ok bool, err error) {
	req, err := json.Marshal(map[string]string{"last_by_subj": "$KV." + natsx.BucketVisitState + "." + key})
	if err != nil {
		return nil, false, err
	}
	m, err := nc.RequestWithContext(ctx, msgGetSubject, req)
	if err != nil {
		return nil, false, fmt.Errorf("erasure: read %s: %w", natsx.BucketVisitState, err)
	}
	var resp struct {
		Error *struct {
			Code    int    `json:"code"`
			ErrCode int    `json:"err_code"`
			Desc    string `json:"description"`
		} `json:"error"`
		Message *struct {
			Header []byte `json:"hdrs"`
			Data   []byte `json:"data"`
		} `json:"message"`
	}
	if err := json.Unmarshal(m.Data, &resp); err != nil {
		return nil, false, fmt.Errorf("erasure: read %s: answer is not json: %w", natsx.BucketVisitState, err)
	}
	switch {
	case resp.Error != nil && resp.Error.Code == 404:
		return nil, false, nil
	case resp.Error != nil:
		return nil, false, fmt.Errorf("erasure: read %s: %d %s", natsx.BucketVisitState, resp.Error.ErrCode, resp.Error.Desc)
	case resp.Message == nil:
		return nil, false, fmt.Errorf("erasure: read %s: answer holds no message", natsx.BucketVisitState)
	}
	if len(resp.Message.Header) > 0 {
		h, err := nats.DecodeHeadersMsg(resp.Message.Header)
		if err != nil {
			return nil, false, fmt.Errorf("erasure: read %s: headers: %w", natsx.BucketVisitState, err)
		}
		switch h.Get("KV-Operation") {
		case "DEL", "PURGE":
			return nil, false, nil
		}
	}
	return resp.Message.Data, true, nil
}

// purgeVisit removes a visitor's record with its history. without it the
// replayer would send the visitor's next event with the cache token of the
// deleted session, and umami would store that event under a session id it
// no longer has.
func purgeVisit(ctx context.Context, kv jetstream.KeyValue, site, vt string) error {
	if err := kv.Purge(ctx, replayer.VisitKey(site, vt)); err != nil {
		return fmt.Errorf("erasure: remove visit record: %w", err)
	}
	return nil
}

// pendingKey holds the sessions a visitor_today run is deleting, written
// before the visit record goes and removed once every session is gone. a
// run that fails in between leaves it, so the next request of the same day
// still finds the sessions. it lives in visit_state next to the
// record it replaces, with the same 24 hour ttl, and holds nothing the
// record did not.
func pendingKey(site, vt string) string { return "erasing." + site + "." + vt }

// pendingErasure reads the progress record of an earlier run; nil when
// none. it reads through the stream leader, as visitRecord does.
func pendingErasure(ctx context.Context, st stores, site, vt string) (*replayer.VisitRecord, error) {
	v, ok, err := leaderGet(ctx, st.nc, pendingKey(site, vt))
	if err != nil || !ok {
		return nil, err
	}
	var r replayer.VisitRecord
	if err := json.Unmarshal(v, &r); err != nil {
		return nil, fmt.Errorf("erasure: progress record of the visitor is not readable: %w", err)
	}
	return &r, nil
}

func savePending(ctx context.Context, kv jetstream.KeyValue, site, vt string, r *replayer.VisitRecord) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("erasure: encode progress record: %w", err)
	}
	if _, err := kv.Put(ctx, pendingKey(site, vt), b); err != nil {
		return fmt.Errorf("erasure: write progress record: %w", err)
	}
	return nil
}

func clearPending(ctx context.Context, kv jetstream.KeyValue, site, vt string) error {
	if err := kv.Purge(ctx, pendingKey(site, vt)); err != nil {
		return fmt.Errorf("erasure: remove progress record: %w", err)
	}
	return nil
}

// bucketSince is when visit_state was created. the bucket is memory-backed,
// so a nats restart empties it and the replayer creates it again; a time
// after the start of the day means links of that day may be lost.
func bucketSince(ctx context.Context, kv jetstream.KeyValue) (time.Time, error) {
	st, err := kv.Status(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("erasure: %s status: %w", natsx.BucketVisitState, err)
	}
	bs, ok := st.(*jetstream.KeyValueBucketStatus)
	if !ok {
		return time.Time{}, fmt.Errorf("erasure: %s status has no stream info", natsx.BucketVisitState)
	}
	return bs.StreamInfo().Created, nil
}

// siteVisits lists the visitor tokens that have a record on the site.
func siteVisits(ctx context.Context, kv jetstream.KeyValue, site string) ([]string, error) {
	return siteKeys(ctx, kv, "visit."+site+".")
}

// sitePending lists the visitor tokens with a progress record on the site.
func sitePending(ctx context.Context, kv jetstream.KeyValue, site string) ([]string, error) {
	return siteKeys(ctx, kv, "erasing."+site+".")
}

func siteKeys(ctx context.Context, kv jetstream.KeyValue, prefix string) ([]string, error) {
	l, err := kv.ListKeysFiltered(ctx, prefix+"*")
	if err != nil {
		return nil, fmt.Errorf("erasure: list visit records: %w", err)
	}
	// the lister closes the channel after the last key, or when ctx ends
	var out []string
	for k := range l.Keys() {
		if vt, ok := strings.CutPrefix(k, prefix); ok {
			out = append(out, vt)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
