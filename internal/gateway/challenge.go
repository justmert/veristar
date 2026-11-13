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
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/natsx"
)

const (
	// ChallengeTTL is how long a challenge is valid, in server time
	//.
	ChallengeTTL = 6 * time.Hour
	// MaxClockSkew is how far a challenge's iat may lie ahead of this
	// node's clock. another edge node may have issued it, and node clocks
	// agree only within what time sync gives.
	MaxClockSkew = 30 * time.Second
	// MaxProofBits mirrors MAX_PROOF_BITS in packages/tracker/src/wire.ts:
	// the tracker refuses a harder challenge, so issuing one would stop all
	// events.
	MaxProofBits = 24
	// DefaultKeyRefresh is how long a node trusts its cached proof key
	// before it reads the bucket again. a restart of the whole nats cluster
	// loses the memory bucket and another node may create a new key; the
	// refresh bounds how long this node keeps signing with the lost one.
	DefaultKeyRefresh = 10 * time.Second

	secretKindProof = "proof"
	proofKeyBytes   = 32
	saltBytes       = 16
)

var (
	// ErrNoProofKey means the bucket holds no proof key for the day asked.
	ErrNoProofKey = errors.New("gateway: no proof key for that day")
	// ErrBadProofKey means the stored key has the wrong length. it is never
	// replaced silently, because other nodes may already sign with it.
	ErrBadProofKey = errors.New("gateway: stored proof key has the wrong length")
)

// ProofKeyName is the edge_secrets key of the proof key of t's utc day,
// named like the visitor secrets (SecretKey in visitor.go).
func ProofKeyName(t time.Time) string { return SecretKey(secretKindProof, t) }

// keyTTL is the time from now to the end of the utc day after day, rounded
// up to whole seconds because the server reads the ttl in seconds. the key
// of day d must outlive d by the 6 hour challenge validity; the end of d+1
// covers that with room for skew.
func keyTTL(day, now time.Time) time.Duration {
	y, m, d := day.UTC().Date()
	ttl := time.Date(y, m, d+2, 0, 0, 0, 0, time.UTC).Sub(now)
	if r := ttl % time.Second; r != 0 {
		ttl += time.Second - r
	}
	return max(ttl, time.Second)
}

// KeyStoreOptions tunes a KeyStore. zero values take the defaults.
type KeyStoreOptions struct {
	// Now is the clock. tests inject one to cross utc midnight.
	Now func() time.Time
	// Refresh is DefaultKeyRefresh when zero.
	Refresh time.Duration
}

type cachedKey struct {
	key     []byte // nil when the bucket had no key at fetched
	fetched time.Time
}

// KeyStore holds the daily proof keys of the edge_secrets bucket in memory.
// the key of a day is created by whichever node needs it first, with an
// atomic create-if-absent, so every node signs with the same key. it lives
// until the end of the following utc day, so yesterday's challenges still
// verify during the 6 hour overlap after midnight.
type KeyStore struct {
	kv      jetstream.KeyValue
	now     func() time.Time
	refresh time.Duration

	mu      sync.Mutex
	keys    map[string]*cachedKey
	lastErr error
}

// NewKeyStore binds the edge_secrets bucket.
func NewKeyStore(ctx context.Context, js jetstream.JetStream, opts KeyStoreOptions) (*KeyStore, error) {
	kv, err := js.KeyValue(ctx, natsx.BucketEdgeSecrets)
	if err != nil {
		return nil, fmt.Errorf("gateway: bind %s: %w", natsx.BucketEdgeSecrets, err)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Refresh == 0 {
		opts.Refresh = DefaultKeyRefresh
	}
	if opts.Refresh < 0 {
		return nil, errors.New("gateway: key refresh must be positive")
	}
	return &KeyStore{kv: kv, now: opts.Now, refresh: opts.Refresh, keys: map[string]*cachedKey{}}, nil
}

// Now returns the store's clock, which challenges and proofs share.
func (s *KeyStore) Now() time.Time { return s.now() }

// Ready makes sure today's key exists and is cached. /readyz calls it.
func (s *KeyStore) Ready(ctx context.Context) error {
	now := s.now()
	_, err := s.mac(ctx, now, now, true, nil)
	return err
}

// LastError returns the last bucket error that a cached key covered for,
// or nil. it lets /readyz and metrics see a bucket outage that requests do
// not see yet.
func (s *KeyStore) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// mac computes hmac-sha256 under the key of day's utc day. create allows
// creating the key, which only today's key may need. the hmac runs under
// the lock so the key bytes never leave the store and can be zeroed when
// the day is dropped.
func (s *KeyStore) mac(ctx context.Context, day, now time.Time, create bool, input []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(now)
	key, err := s.load(ctx, day, now, create)
	if err != nil {
		return nil, err
	}
	m := hmac.New(sha256.New, key)
	m.Write(input)
	return m.Sum(nil), nil
}

// prune drops and zeroes cached keys other than yesterday's, today's and
// tomorrow's. tomorrow's appears only when a node whose clock is slightly
// ahead issued a challenge just after its midnight.
func (s *KeyStore) prune(now time.Time) {
	keep := map[string]bool{
		ProofKeyName(now.AddDate(0, 0, -1)): true,
		ProofKeyName(now):                   true,
		ProofKeyName(now.AddDate(0, 0, 1)):  true,
	}
	for name, c := range s.keys {
		if !keep[name] {
			clear(c.key)
			delete(s.keys, name)
		}
	}
}

// load returns the cached key of day, reading or creating it when the cache
// entry is missing or older than the refresh interval. the caller holds mu.
func (s *KeyStore) load(ctx context.Context, day, now time.Time, create bool) ([]byte, error) {
	name := ProofKeyName(day)
	c := s.keys[name]
	fresh := c != nil && now.Sub(c.fetched) < s.refresh && now.Sub(c.fetched) >= 0
	if fresh && (c.key != nil || !create) {
		if c.key == nil {
			return nil, ErrNoProofKey
		}
		return c.key, nil
	}
	key, err := s.fetch(ctx, name, day, now, create)
	switch {
	case err == nil:
		s.lastErr = nil
		s.store(name, key, now)
		return key, nil
	case errors.Is(err, ErrNoProofKey):
		s.lastErr = nil
		s.store(name, nil, now)
		return nil, err
	case c != nil && c.key != nil:
		// the bucket is unreachable but the key read before is still the
		// day's key; keys never change within a day unless the cluster
		// lost them. the error stays visible through LastError, and the
		// next read is tried after another refresh interval.
		s.lastErr = err
		c.fetched = now
		return c.key, nil
	default:
		s.lastErr = err
		return nil, err
	}
}

func (s *KeyStore) store(name string, key []byte, now time.Time) {
	if old := s.keys[name]; old != nil && old.key != nil && !hmac.Equal(old.key, key) {
		clear(old.key)
	}
	s.keys[name] = &cachedKey{key: key, fetched: now}
}

func (s *KeyStore) fetch(ctx context.Context, name string, day, now time.Time, create bool) ([]byte, error) {
	e, err := s.kv.Get(ctx, name)
	switch {
	case err == nil:
		return checkKey(e.Value())
	case !errors.Is(err, jetstream.ErrKeyNotFound) && !errors.Is(err, jetstream.ErrKeyDeleted):
		return nil, fmt.Errorf("gateway: read proof key %s: %w", name, err)
	case !create:
		return nil, ErrNoProofKey
	}
	// the same atomic create-if-absent as the visitor secrets, which also
	// handles a lost create race and a replica that lags behind the create
	key, err := EnsureDailySecret(ctx, s.kv, name, keyTTL(day, now))
	if err != nil {
		return nil, fmt.Errorf("gateway: proof key %s: %w", name, err)
	}
	return checkKey(key)
}

func checkKey(v []byte) ([]byte, error) {
	if len(v) != proofKeyBytes {
		return nil, ErrBadProofKey
	}
	return append([]byte(nil), v...), nil
}

// tagInput is w|iat|salt|bits, salt as lowercase hex (docs/reference/wire-v1.md).
func tagInput(w string, iat int64, salt []byte, bits int) []byte {
	b := make([]byte, 0, len(w)+80)
	b = append(b, w...)
	b = append(b, '|')
	b = strconv.AppendInt(b, iat, 10)
	b = append(b, '|')
	b = hex.AppendEncode(b, salt)
	b = append(b, '|')
	return strconv.AppendInt(b, int64(bits), 10)
}

// Challenge is the json body of GET /v1/c. field order follows the wire
// documentation.
type Challenge struct {
	W    string `json:"w"`
	IAT  int64  `json:"iat"`
	Salt string `json:"salt"`
	Bits int    `json:"bits"`
	Tag  string `json:"tag"`
}

// Challenger issues challenges with the current PROOF_BITS.
type Challenger struct {
	keys *KeyStore
	bits int
}

// NewChallenger checks bits against what the tracker accepts.
func NewChallenger(keys *KeyStore, bits int) (*Challenger, error) {
	if keys == nil {
		return nil, errors.New("gateway: challenger needs a key store")
	}
	if bits < 0 || bits > MaxProofBits {
		return nil, fmt.Errorf("gateway: proof bits %d outside 0 to %d", bits, MaxProofBits)
	}
	return &Challenger{keys: keys, bits: bits}, nil
}

// Issue returns the challenge json for a site. siteID must be a canonical
// uuid, which also keeps '|' out of the tag input. checking that the site
// exists and is active is the caller's job, because it owns the registry
// snapshot. a missing key that cannot be created is ReasonUnavailable.
func (c *Challenger) Issue(ctx context.Context, siteID string) ([]byte, error) {
	if u, err := uuid.Parse(siteID); err != nil || u.String() != siteID {
		return nil, reject(ReasonMalformed)
	}
	now := c.keys.now()
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return nil, &RejectError{Reason: ReasonUnavailable, Err: err}
	}
	iat := now.Unix()
	tag, err := c.keys.mac(ctx, now, now, true, tagInput(siteID, iat, salt, c.bits))
	if err != nil {
		return nil, &RejectError{Reason: ReasonUnavailable, Err: err}
	}
	return json.Marshal(Challenge{
		W:    siteID,
		IAT:  iat,
		Salt: hex.EncodeToString(salt),
		Bits: c.bits,
		Tag:  hex.EncodeToString(tag),
	})
}
