package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/integrity"
	"github.com/justmert/veristar/internal/natsx"
)

// secretLen is the size of each daily secret.
const secretLen = 32

// key prefixes in edge_secrets. the suffix is the utc day as yyyymmdd, so
// every day has its own key and a key is never overwritten.
const (
	secretKindVisitor   = "visitor"
	secretKindIPCluster = "ipcluster"
)

// SecretKey is the edge_secrets key of one kind of daily secret for the utc
// day of t.
func SecretKey(kind string, t time.Time) string {
	return kind + "." + t.UTC().Format("20060102")
}

// UntilMidnight is the time from t to the next utc midnight, rounded up to
// whole seconds and at least one second, because the server counts per-key
// ttl in whole seconds and refuses anything shorter than one.
func UntilMidnight(t time.Time) time.Duration {
	t = t.UTC()
	next := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
	d := next.Sub(t)
	if r := d % time.Second; r != 0 {
		d += time.Second - r
	}
	return max(d, time.Second)
}

// EnsureDailySecret returns the secret stored under key, creating it with
// random bytes and the given ttl when it is absent. every gateway calls this,
// and the atomic create makes the first one win, so all of them use the same
// bytes. the value never leaves memory: edge_secrets is a memory bucket.
func EnsureDailySecret(ctx context.Context, kv jetstream.KeyValue, key string, ttl time.Duration) ([]byte, error) {
	fresh := make([]byte, secretLen)
	if _, err := rand.Read(fresh); err != nil {
		return nil, fmt.Errorf("gateway: random secret: %w", err)
	}
	_, cerr := kv.Create(ctx, key, fresh, jetstream.KeyTTL(ttl))
	if cerr == nil {
		return fresh, nil
	}
	clear(fresh)
	// another gateway created it first. the create can also fail with a
	// wrong-revision error when the key's last entry is an expiry marker and
	// two creates race, so any create error is followed by a read. a direct
	// get may be answered by a replica that has not applied the create yet,
	// so a missing key is read again a few times before giving up.
	var gerr error
	for attempt := range 10 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		var e jetstream.KeyValueEntry
		e, gerr = kv.Get(ctx, key)
		if gerr == nil {
			v := e.Value()
			if len(v) != secretLen {
				return nil, fmt.Errorf("gateway: edge secret %s holds %d bytes, want %d", key, len(v), secretLen)
			}
			return v, nil
		}
		if !errors.Is(gerr, jetstream.ErrKeyNotFound) {
			break
		}
	}
	return nil, fmt.Errorf("gateway: edge secret %s: create: %w; read: %w", key, cerr, gerr)
}

// Visitor computes visitor tokens and ip-cluster keys from the two daily
// secrets in edge_secrets. it holds only the current utc day's secrets and
// zeroes them when the day changes, so after midnight no token of the
// previous day can be computed again from this process.
type Visitor struct {
	kv  jetstream.KeyValue
	now func() time.Time

	mu        sync.Mutex
	day       time.Time // utc midnight of the loaded secrets, zero when none
	visitor   []byte
	ipcluster []byte
}

// NewVisitor returns a Visitor on the edge_secrets bucket. now is the clock;
// tests pass a controllable one, services pass time.Now.
func NewVisitor(kv jetstream.KeyValue, now func() time.Time) (*Visitor, error) {
	if kv == nil || now == nil {
		return nil, errors.New("gateway: NewVisitor needs a bucket and a clock")
	}
	if kv.Bucket() != natsx.BucketEdgeSecrets {
		return nil, fmt.Errorf("gateway: visitor secrets belong in %s, not %s", natsx.BucketEdgeSecrets, kv.Bucket())
	}
	return &Visitor{kv: kv, now: now}, nil
}

func secretDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// withSecrets runs fn with the current day's secrets, loading them first when
// the day changed. fn runs under the lock, so a rotation cannot zero the
// bytes while fn reads them.
func (v *Visitor) withSecrets(ctx context.Context, fn func(visitor, ipcluster []byte) error) error {
	now := v.now()
	day := secretDay(now)
	v.mu.Lock()
	if v.day.Equal(day) {
		defer v.mu.Unlock()
		return fn(v.visitor, v.ipcluster)
	}
	if day.Before(v.day) {
		v.mu.Unlock()
		// the clock went back over midnight. yesterday's secret was zeroed
		// and recreating it would hand out tokens no one else agrees with
		return fmt.Errorf("gateway: clock moved back to %s after %s was loaded", day.Format(time.DateOnly), v.day.Format(time.DateOnly))
	}
	v.mu.Unlock()

	// network calls happen outside the lock, so requests on other days are
	// not held up; two loads for the same day read the same stored bytes
	ttl := UntilMidnight(now)
	vs, err := EnsureDailySecret(ctx, v.kv, SecretKey(secretKindVisitor, now), ttl)
	if err != nil {
		return err
	}
	cs, err := EnsureDailySecret(ctx, v.kv, SecretKey(secretKindIPCluster, now), ttl)
	if err != nil {
		clear(vs)
		return err
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	switch {
	case v.day.Equal(day):
		// a concurrent call loaded the same day first
		clear(vs)
		clear(cs)
	case v.day.After(day):
		clear(vs)
		clear(cs)
		return fmt.Errorf("gateway: secrets for %s arrived after %s was loaded", day.Format(time.DateOnly), v.day.Format(time.DateOnly))
	default:
		clear(v.visitor)
		clear(v.ipcluster)
		v.day, v.visitor, v.ipcluster = day, vs, cs
	}
	return fn(v.visitor, v.ipcluster)
}

// Ensure loads today's secrets, creating them when absent. /readyz uses it.
func (v *Visitor) Ensure(ctx context.Context) error {
	return v.withSecrets(ctx, func(_, _ []byte) error { return nil })
}

// Day is the utc day of the loaded secrets, zero when none are loaded.
func (v *Visitor) Day() time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.day
}

// Rotate zeroes the loaded secrets when the clock has passed their day and
// loads the new day's. Run calls it every second; it is exported so a test
// with an injected clock can rotate without waiting.
func (v *Visitor) Rotate(ctx context.Context) error {
	v.mu.Lock()
	stale := !v.day.IsZero() && secretDay(v.now()).After(v.day)
	if stale {
		// zero first, so the old day's bytes are gone even if loading the
		// new day fails
		clear(v.visitor)
		clear(v.ipcluster)
		v.day, v.visitor, v.ipcluster = time.Time{}, nil, nil
	}
	v.mu.Unlock()
	return v.Ensure(ctx)
}

// VerifyEvery is how often the loaded secrets are compared with the bucket.
// it matches DefaultKeyRefresh of the proof keys, so after a restart that
// emptied edge_secrets every gateway agrees on the new secrets again within
// it.
const VerifyEvery = DefaultKeyRefresh

// Verify reads the current day's secrets from the bucket and replaces the
// loaded ones when they differ or are gone. edge_secrets is memory-backed,
// so a nats restart can empty it while this process keeps its copy; another
// gateway then creates new secrets, and without this check the two would
// compute different tokens for the same visitor until midnight. a key that
// is gone is created again (or another gateway's new one adopted) with
// EnsureDailySecret. replaced reports a swap. a read error keeps the loaded
// secrets, because they are still the day's secrets as far as anyone knows.
func (v *Visitor) Verify(ctx context.Context) (replaced bool, err error) {
	now := v.now()
	day := secretDay(now)
	v.mu.Lock()
	loaded := v.day.Equal(day)
	v.mu.Unlock()
	if !loaded {
		// nothing loaded for today; Rotate loads it from the bucket
		return false, nil
	}
	same := func(kind string, have func() []byte) (bool, error) {
		e, err := v.kv.Get(ctx, SecretKey(kind, now))
		switch {
		case errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted):
			return false, nil
		case err != nil:
			return false, fmt.Errorf("gateway: verify edge secret %s: %w", SecretKey(kind, now), err)
		}
		v.mu.Lock()
		defer v.mu.Unlock()
		return hmac.Equal(e.Value(), have()), nil
	}
	vOK, err := same(secretKindVisitor, func() []byte { return v.visitor })
	if err != nil {
		return false, err
	}
	cOK, err := same(secretKindIPCluster, func() []byte { return v.ipcluster })
	if err != nil {
		return false, err
	}
	if vOK && cOK {
		return false, nil
	}
	ttl := UntilMidnight(now)
	vs, err := EnsureDailySecret(ctx, v.kv, SecretKey(secretKindVisitor, now), ttl)
	if err != nil {
		return false, err
	}
	cs, err := EnsureDailySecret(ctx, v.kv, SecretKey(secretKindIPCluster, now), ttl)
	if err != nil {
		clear(vs)
		return false, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.day.Equal(day) || (hmac.Equal(vs, v.visitor) && hmac.Equal(cs, v.ipcluster)) {
		// the day changed meanwhile and Rotate loaded the new one, or the
		// read above reached a replica that had not applied the keys yet and
		// the create-if-absent found them in place
		clear(vs)
		clear(cs)
		return false, nil
	}
	clear(v.visitor)
	clear(v.ipcluster)
	v.visitor, v.ipcluster = vs, cs
	return true, nil
}

// Run rotates once a second until ctx ends, so the old secret is zeroed at
// midnight even when no request arrives, and verifies the loaded secrets
// against the bucket every VerifyEvery. load failures are logged and
// retried on the next tick; requests fail until a load succeeds.
func (v *Visitor) Run(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	verifyEvery := int(VerifyEvery / time.Second)
	for tick := 1; ; tick++ {
		if err := v.Rotate(ctx); err != nil && ctx.Err() == nil {
			log.Error("daily edge secrets unavailable", "error", err.Error())
		}
		if tick%verifyEvery == 0 {
			replaced, err := v.Verify(ctx)
			switch {
			case err != nil && ctx.Err() == nil:
				log.Warn("daily edge secrets not verified, the loaded ones stay in use", "error", err.Error())
			case replaced:
				// the documented trade-off of a memory bucket: visitors seen
				// before the loss are counted once more today
				log.Warn("daily edge secrets were lost from the bucket and replaced")
			}
		}
		select {
		case <-ctx.Done():
			v.mu.Lock()
			clear(v.visitor)
			clear(v.ipcluster)
			v.day, v.visitor, v.ipcluster = time.Time{}, nil, nil
			v.mu.Unlock()
			return
		case <-t.C:
		}
	}
}

func checkPart(name, s string) error {
	if s == "" {
		return fmt.Errorf("gateway: %s is empty", name)
	}
	// 0x00 separates the parts, so a part containing it could collide with
	// another split of the same bytes
	if strings.IndexByte(s, 0) >= 0 {
		return fmt.Errorf("gateway: %s contains a zero byte", name)
	}
	return nil
}

// Token returns hex(hmac-sha256(daily_secret, site || 0x00 || ip || 0x00 ||
// ua)) for the current utc day. ip is the canonical text of the address and
// ua the user agent header as received.
func (v *Visitor) Token(ctx context.Context, site string, ip ClientIP, ua string) (string, error) {
	if err := checkPart("site", site); err != nil {
		return "", err
	}
	if !ip.IsValid() {
		return "", errors.New("gateway: visitor token without a client address")
	}
	if err := checkPart("user agent", ua); err != nil {
		return "", err
	}
	var out string
	err := v.withSecrets(ctx, func(secret, _ []byte) error {
		m := hmac.New(sha256.New, secret)
		m.Write([]byte(site))
		m.Write([]byte{0})
		m.Write([]byte(ip.canonical()))
		m.Write([]byte{0})
		m.Write([]byte(ua))
		out = hex.EncodeToString(m.Sum(nil))
		return nil
	})
	return out, err
}

// PrefixKey returns hex(hmac-sha256(ipcluster_secret, network)) for the /24
// or /48 network of the address. the ip-cluster sets and the per-prefix rate
// limits are keyed by it, so neither holds the network itself, and the key
// cannot be linked to a network after the day's secret is gone.
func (v *Visitor) PrefixKey(ctx context.Context, ip ClientIP) (string, error) {
	if !ip.IsValid() {
		return "", errors.New("gateway: prefix key without a client address")
	}
	var out string
	err := v.withSecrets(ctx, func(_, secret []byte) error {
		m := hmac.New(sha256.New, secret)
		m.Write([]byte(ip.truncated().String()))
		out = hex.EncodeToString(m.Sum(nil))
		return nil
	})
	return out, err
}

// NetworkKey is the keyed hash of an autonomous system number under the
// day's ip-cluster secret, cut to integrity.NetworkKeyHexLen hex digits. the
// distinct network count of a site and day is counted on these keys, so the
// number itself never leaves the gateway, and after midnight no key can be
// linked to a network again. the "asn:" prefix keeps the input apart from
// the prefix strings PrefixKey hashes under the same secret.
func (v *Visitor) NetworkKey(ctx context.Context, asn uint32) (string, error) {
	if asn == 0 {
		return "", errors.New("gateway: network key without an autonomous system")
	}
	var out string
	err := v.withSecrets(ctx, func(_, secret []byte) error {
		m := hmac.New(sha256.New, secret)
		m.Write([]byte("asn:" + strconv.FormatUint(uint64(asn), 10)))
		out = hex.EncodeToString(m.Sum(nil))[:integrity.NetworkKeyHexLen]
		return nil
	})
	return out, err
}

// VisitorTokenRequest is what the control plane sends on edge.visitor-token
// for a visitor_today erasure. it carries the data subject's
// address and user agent, so neither side may log it.
type VisitorTokenRequest struct {
	Site      string `json:"site"`
	Address   string `json:"addr"`
	UserAgent string `json:"agent"`
}

// VisitorTokenReply answers a VisitorTokenRequest. Day is the utc day the
// token belongs to (yyyy-mm-dd); it holds only for that day. Error is set
// instead of the token when the request could not be answered.
type VisitorTokenReply struct {
	Token string `json:"vt,omitempty"`
	Day   string `json:"day,omitempty"`
	Error string `json:"error,omitempty"`
}

// requestTimeout bounds one answer, which may need to create today's secret.
const requestTimeout = 5 * time.Second

// ServeTokenRequests answers edge.visitor-token requests until the returned
// subscription is drained. the gateways share a queue group, so one of them
// answers each request; they all hold the same secret, so any one is right.
func (v *Visitor) ServeTokenRequests(nc *nats.Conn, log *slog.Logger) (*nats.Subscription, error) {
	return nc.QueueSubscribe(natsx.SubjectVisitorToken, natsx.ServiceGateway, func(m *nats.Msg) {
		reply := v.answer(m.Data)
		b, err := json.Marshal(reply)
		if err != nil {
			log.Error("visitor token reply encoding failed", "error", err.Error())
			return
		}
		if err := m.Respond(b); err != nil {
			log.Error("visitor token reply failed", "error", err.Error())
		}
	})
}

func (v *Visitor) answer(data []byte) VisitorTokenReply {
	var req VisitorTokenRequest
	if err := json.Unmarshal(data, &req); err != nil {
		// the decode error can quote the payload, so it is not passed on
		return VisitorTokenReply{Error: "request is not a json visitor token request"}
	}
	ip, err := ParseClientIP(req.Address)
	if err != nil {
		return VisitorTokenReply{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	// the day is read before the token, and the token is refused if
	// midnight passed in between, so the reply never names the wrong day
	before := secretDay(v.now())
	vt, err := v.Token(ctx, req.Site, ip, req.UserAgent)
	if err != nil {
		return VisitorTokenReply{Error: err.Error()}
	}
	if after := secretDay(v.now()); !after.Equal(before) {
		return VisitorTokenReply{Error: "utc day changed while answering, send the request again"}
	}
	return VisitorTokenReply{Token: vt, Day: before.Format(time.DateOnly)}
}
