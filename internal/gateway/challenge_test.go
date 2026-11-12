package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/events"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/tests/fixtures/natsctr"
)

// pfClock is an injected clock that tests move across utc midnight.
type pfClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *pfClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *pfClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func (c *pfClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type pfNATS struct {
	srv *natsctr.Server
	js  jetstream.JetStream
	kv  jetstream.KeyValue
}

// pfStartNATS starts a real nats server with the platform topology applied.
func pfStartNATS(t *testing.T) pfNATS {
	t.Helper()
	srv := natsctr.StartSingle(t)
	nc, err := nats.Connect(srv.URL, nats.MaxReconnects(-1), nats.ReconnectWait(100*time.Millisecond))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(ctx, js, spec)
	require.NoError(t, err)
	kv, err := js.KeyValue(ctx, natsx.BucketEdgeSecrets)
	require.NoError(t, err)
	return pfNATS{srv: srv, js: js, kv: kv}
}

func pfKeyStore(ctx context.Context, t *testing.T, js jetstream.JetStream, clock *pfClock) *KeyStore {
	t.Helper()
	ks, err := NewKeyStore(ctx, js, KeyStoreOptions{Now: clock.Now, Refresh: 10 * time.Second})
	require.NoError(t, err)
	return ks
}

// pfSolve finds the smallest nonce, as the tracker does.
func pfSolve(t *testing.T, c Challenge) events.Proof {
	t.Helper()
	var p events.Proof
	p.IAT, p.Bits = c.IAT, c.Bits
	_, err := hex.Decode(p.Salt[:], []byte(c.Salt))
	require.NoError(t, err)
	_, err = hex.Decode(p.Tag[:], []byte(c.Tag))
	require.NoError(t, err)
	for n := uint64(0); ; n++ {
		d := sha256.Sum256(proofInput(c.W, c.IAT, p.Salt[:], n))
		if leadingZeroBits(d[:]) >= c.Bits {
			p.Nonce = n
			return p
		}
	}
}

func pfIssue(t *testing.T, ch *Challenger, site string) Challenge {
	t.Helper()
	b, err := ch.Issue(t.Context(), site)
	require.NoError(t, err)
	var c Challenge
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&c))
	return c
}

func pfReason(t *testing.T, err error) Reason {
	t.Helper()
	require.Error(t, err)
	r, ok := ReasonOf(err)
	require.True(t, ok, "not a reject error: %v", err)
	return r
}

var (
	lowerHex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)
	lowerHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// TestChallengeRoundTrip issues a challenge, checks its json against the
// wire document, wraps the solved proof in a wire body that events.ParseWire
// accepts, and verifies it.
func TestChallengeRoundTrip(t *testing.T) {
	n := pfStartNATS(t)
	ctx := t.Context()
	clock := &pfClock{t: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
	ks := pfKeyStore(ctx, t, n.js, clock)
	ch, err := NewChallenger(ks, 12)
	require.NoError(t, err)
	site := uuid.NewString()

	raw, err := ch.Issue(ctx, site)
	require.NoError(t, err)
	require.Regexp(t, `^\{"w":"[^"]+","iat":\d+,"salt":"[0-9a-f]{32}","bits":12,"tag":"[0-9a-f]{64}"\}$`, string(raw))
	var c Challenge
	require.NoError(t, json.Unmarshal(raw, &c))
	require.Equal(t, site, c.W)
	require.Equal(t, clock.Now().Unix(), c.IAT)
	require.Regexp(t, lowerHex32, c.Salt)
	require.Regexp(t, lowerHex64, c.Tag)

	p := pfSolve(t, c)
	body := fmt.Sprintf(`{"v":1,"w":%q,"t":"pv","h":"site.example","u":"/","r":"","ti":"t","s":"1x1","l":"en","ts":1,`+
		`"p":{"iat":%d,"salt":%q,"bits":%d,"tag":%q,"nonce":%d}}`, site, c.IAT, c.Salt, c.Bits, c.Tag, p.Nonce)
	w, err := events.ParseWire([]byte(body))
	require.NoError(t, err)
	require.Equal(t, p, w.Proof)

	v, err := NewProofVerifier(ks, 50, 1000)
	require.NoError(t, err)
	require.NoError(t, v.Verify(ctx, w.SiteID, w.Proof))

	// the salt is fresh on every challenge
	require.NotEqual(t, c.Salt, pfIssue(t, ch, site).Salt)

	// malformed site ids never reach the tag input
	for _, bad := range []string{"", "x|y", "8F6B1C3E-2A4D-4E5F-9A7B-0C1D2E3F4A5B", site + "|1"} {
		_, err := ch.Issue(ctx, bad)
		require.Equal(t, ReasonMalformed, pfReason(t, err))
	}
	_, err = NewChallenger(ks, MaxProofBits+1)
	require.Error(t, err)
	_, err = NewChallenger(ks, -1)
	require.Error(t, err)
}

// TestKeyRotationOverlap crosses utc midnight with an injected clock and two
// key stores standing in for two edge nodes on one bucket.
func TestKeyRotationOverlap(t *testing.T) {
	n := pfStartNATS(t)
	ctx := t.Context()
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	clock := &pfClock{t: day.Add(22 * time.Hour)}
	nodeA, nodeB := pfKeyStore(ctx, t, n.js, clock), pfKeyStore(ctx, t, n.js, clock)
	chA, err := NewChallenger(nodeA, 4)
	require.NoError(t, err)
	vB, err := NewProofVerifier(nodeB, 50, 1000)
	require.NoError(t, err)
	site := uuid.NewString()

	// 22:00: node a creates the day's key, node b verifies with the same key
	c1 := pfIssue(t, chA, site)
	require.NoError(t, vB.Verify(ctx, site, pfSolve(t, c1)))
	dayKey, err := n.kv.Get(ctx, ProofKeyName(day))
	require.NoError(t, err)
	require.Len(t, dayKey.Value(), proofKeyBytes)

	// the key lives until the end of the following utc day: 26 h from 22:00
	msg, err := pfStream(t, n.js).GetLastMsgForSubject(ctx, "$KV."+natsx.BucketEdgeSecrets+"."+ProofKeyName(day))
	require.NoError(t, err)
	require.Equal(t, "26h0m0s", msg.Header.Get(jetstream.MsgTTLHeader))

	// 23:30 challenge, then cross midnight
	clock.Set(day.Add(23*time.Hour + 30*time.Minute))
	c2 := pfIssue(t, chA, site)
	clock.Set(day.Add(27 * time.Hour)) // 03:00 next day
	c3 := pfIssue(t, chA, site)
	nextKey, err := n.kv.Get(ctx, ProofKeyName(day.AddDate(0, 0, 1)))
	require.NoError(t, err)
	require.NotEqual(t, dayKey.Value(), nextKey.Value(), "a new utc day gets a new key")
	msg, err = pfStream(t, n.js).GetLastMsgForSubject(ctx, "$KV."+natsx.BucketEdgeSecrets+"."+ProofKeyName(day.AddDate(0, 0, 1)))
	require.NoError(t, err)
	require.Equal(t, "45h0m0s", msg.Header.Get(jetstream.MsgTTLHeader))

	// within the overlap yesterday's key still verifies, on either node
	require.NoError(t, vB.Verify(ctx, site, pfSolve(t, c1)))
	require.NoError(t, vB.Verify(ctx, site, pfSolve(t, c2)))
	require.NoError(t, vB.Verify(ctx, site, pfSolve(t, c3)))

	// 04:00:01 next day: c1 is 6 h 1 s old
	clock.Set(day.Add(28*time.Hour + time.Second))
	require.Equal(t, ReasonChallengeExpired, pfReason(t, vB.Verify(ctx, site, pfSolve(t, c1))))
	require.NoError(t, vB.Verify(ctx, site, pfSolve(t, c2)))
	// exactly 6 h is still valid, one second more is not
	clock.Set(day.Add(29*time.Hour + 30*time.Minute))
	require.NoError(t, vB.Verify(ctx, site, pfSolve(t, c2)))
	clock.Add(time.Second)
	require.Equal(t, ReasonChallengeExpired, pfReason(t, vB.Verify(ctx, site, pfSolve(t, c2))))

	// a challenge from ahead of this node's clock: within the skew it is
	// accepted, beyond it refused
	clock.Set(day.Add(30 * time.Hour))
	c4 := pfIssue(t, chA, site)
	clock.Add(-MaxClockSkew)
	require.NoError(t, vB.Verify(ctx, site, pfSolve(t, c4)))
	clock.Add(-time.Second)
	require.Equal(t, ReasonChallengeFuture, pfReason(t, vB.Verify(ctx, site, pfSolve(t, c4))))
}

// TestKeyLost covers a whole-cluster restart, which loses the memory bucket:
// after the refresh interval nodes stop accepting challenges of the lost key
// and the next challenge creates a new one.
func TestKeyLost(t *testing.T) {
	n := pfStartNATS(t)
	ctx := t.Context()
	clock := &pfClock{t: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	ks := pfKeyStore(ctx, t, n.js, clock)
	ch, err := NewChallenger(ks, 0)
	require.NoError(t, err)
	v, err := NewProofVerifier(ks, 50, 1000)
	require.NoError(t, err)
	site := uuid.NewString()
	c := pfIssue(t, ch, site)
	before, err := n.kv.Get(ctx, ProofKeyName(clock.Now()))
	require.NoError(t, err)

	require.NoError(t, n.kv.Purge(ctx, ProofKeyName(clock.Now())))
	// inside the refresh interval the cached key still answers
	require.NoError(t, v.Verify(ctx, site, pfSolve(t, c)))
	clock.Add(11 * time.Second)
	require.Equal(t, ReasonChallengeTag, pfReason(t, v.Verify(ctx, site, pfSolve(t, c))))

	c2 := pfIssue(t, ch, site)
	after, err := n.kv.Get(ctx, ProofKeyName(clock.Now()))
	require.NoError(t, err)
	require.NotEqual(t, before.Value(), after.Value())
	require.NoError(t, v.Verify(ctx, site, pfSolve(t, c2)))
	require.NoError(t, ks.Ready(ctx))
}

// TestKeyStoreFailures covers a stored key of the wrong length and a bucket
// that cannot be reached.
func TestKeyStoreFailures(t *testing.T) {
	n := pfStartNATS(t)
	ctx := t.Context()
	clock := &pfClock{t: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	site := uuid.NewString()

	_, err := n.kv.Create(ctx, ProofKeyName(clock.Now()), []byte("short"))
	require.NoError(t, err)
	bad := pfKeyStore(ctx, t, n.js, clock)
	badCh, err := NewChallenger(bad, 0)
	require.NoError(t, err)
	_, err = badCh.Issue(ctx, site)
	require.Equal(t, ReasonUnavailable, pfReason(t, err))
	require.ErrorIs(t, err, ErrBadProofKey)
	require.NoError(t, n.kv.Purge(ctx, ProofKeyName(clock.Now())))

	warm := pfKeyStore(ctx, t, n.js, clock)
	cold := pfKeyStore(ctx, t, n.js, clock)
	warmCh, err := NewChallenger(warm, 0)
	require.NoError(t, err)
	coldCh, err := NewChallenger(cold, 0)
	require.NoError(t, err)
	pfIssue(t, warmCh, site)
	require.NoError(t, warm.LastError())

	n.srv.Stop(t)
	clock.Add(time.Minute)
	short := func() context.Context {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		t.Cleanup(cancel)
		return ctx
	}
	// the warm node keeps signing with the day's key it already holds and
	// reports the bucket error; the cold node has no key and refuses
	_, err = warmCh.Issue(short(), site)
	require.NoError(t, err)
	require.Error(t, warm.LastError())
	_, err = coldCh.Issue(short(), site)
	require.Equal(t, ReasonUnavailable, pfReason(t, err))
	require.Error(t, cold.Ready(short()))
}

func TestKeyTTL(t *testing.T) {
	day := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	require.Equal(t, 48*time.Hour, keyTTL(day, day))
	require.Equal(t, 24*time.Hour+time.Second, keyTTL(day, day.Add(24*time.Hour-500*time.Millisecond)))
	require.Equal(t, time.Second, keyTTL(day, day.Add(48*time.Hour)))
	require.Equal(t, "proof.20261231", ProofKeyName(day.Add(23*time.Hour)))
	require.Equal(t, "proof.20270101", ProofKeyName(day.Add(24*time.Hour)))
}

func pfStream(t *testing.T, js jetstream.JetStream) jetstream.Stream {
	t.Helper()
	s, err := js.Stream(t.Context(), "KV_"+natsx.BucketEdgeSecrets)
	require.NoError(t, err)
	return s
}
