package operator

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/docs/reference/openapi"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/operatorq"
	"github.com/justmert/veristar/internal/umami"
)

// the machine metrics api (contract section 9,
// docs/reference/openapi/metrics-v1.yaml). it is mounted next to /api, not
// inside it, so the session middleware never runs: a console cookie means
// nothing here, and an operator api key means nothing anywhere else. every
// read runs as cp_operator.

// MachinePath is where the machine api is mounted.
const MachinePath = "/api/metrics/v1"

// rate limit per key and control plane instance (10
// requests per second). the burst lets a client fetch a few pages at once.
const (
	DefaultMachineRate  = 10
	DefaultMachineBurst = 20
)

// WebReader is the part of the umami admin client /web/hourly uses.
type WebReader interface {
	GetWebsite(ctx context.Context, id string) (umami.Website, bool, error)
	Stats(ctx context.Context, websiteID string, r umami.Range) (umami.Stats, error)
}

// MachineConfig are the parts of the machine api.
type MachineConfig struct {
	DB    *db.DB
	Umami WebReader
	Log   *slog.Logger
	// Now is the clock of ranges, expiry and the rate limit; time.Now when nil.
	Now func() time.Time
	// Rate (tokens per second) and Burst of each key's token bucket; the
	// defaults above when zero.
	Rate  float64
	Burst int
}

// Machine serves /api/metrics/v1.
type Machine struct {
	db      *db.DB
	umami   WebReader
	log     *slog.Logger
	now     func() time.Time
	limiter *keyLimiter
}

// NewMachine checks the configuration.
func NewMachine(c MachineConfig) (*Machine, error) {
	if c.DB == nil || c.Umami == nil || c.Log == nil {
		return nil, errors.New("operator: the machine api needs a database, an umami reader and a log")
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Rate == 0 {
		c.Rate = DefaultMachineRate
	}
	if c.Burst == 0 {
		c.Burst = DefaultMachineBurst
	}
	if c.Rate < 0 || c.Burst < 1 {
		return nil, errors.New("operator: the machine api rate must be positive and the burst at least 1")
	}
	return &Machine{
		db: c.DB, umami: c.Umami, log: c.Log, now: c.Now,
		limiter: &keyLimiter{rate: c.Rate, burst: float64(c.Burst), buckets: map[uuid.UUID]*tokenBucket{}},
	}, nil
}

// Handler returns the api for mounting at MachinePath. httpx.Base gives
// every answer a request id and no-store and logs without the query
// string, so neither a key nor a filter value reaches the log.
func (m *Machine) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.Base(m.log), m.authenticate)
	r.NotFound(httpx.NotFoundHandler)
	r.MethodNotAllowed(httpx.MethodNotAllowedHandler)
	r.Method(http.MethodGet, "/tenants", httpx.HandlerFunc(m.tenants))
	r.Method(http.MethodGet, "/web/daily", httpx.HandlerFunc(m.webDaily))
	r.Method(http.MethodGet, "/web/dimensions", httpx.HandlerFunc(m.webDimensions))
	r.Method(http.MethodGet, "/web/hourly", httpx.HandlerFunc(m.webHourly))
	r.Method(http.MethodGet, "/api/daily", httpx.HandlerFunc(m.apiDaily))
	r.Method(http.MethodGet, "/api/routes/daily", httpx.HandlerFunc(m.apiRoutesDaily))
	r.Method(http.MethodGet, "/api/hourly", httpx.HandlerFunc(m.apiHourly))
	r.Method(http.MethodGet, "/openapi.yaml", httpx.HandlerFunc(m.document))
	return r
}

// CodeScopeForbidden is the 403 of a key scoped to another tenant
// (contract 2.4).
const CodeScopeForbidden = "scope_forbidden"

// machineKey is the authenticated key of a request. Tenant is set for a
// key scoped to one tenant.
type machineKey struct {
	ID     uuid.UUID
	Tenant *uuid.UUID
}

type machineKeyCtx struct{}

func keyFrom(ctx context.Context) machineKey {
	k, ok := ctx.Value(machineKeyCtx{}).(machineKey)
	if !ok {
		// every route is behind authenticate, so this is a wiring mistake
		panic("operator: machine route without an authenticated key")
	}
	return k
}

// unauthenticated is 401 with the challenge rfc 6750 asks for. every key
// problem gets the same code; only the message says which, and only for a
// key that matched, so a guesser learns nothing from it.
func unauthenticated(w http.ResponseWriter, message string) error {
	w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
	return httpx.E(http.StatusUnauthorized, httpx.CodeUnauthenticated, message)
}

// storedKey is the row of a presented key.
type storedKey struct {
	ID        uuid.UUID
	Hash      []byte
	Tenant    uuid.NullUUID
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// authenticate checks the key on every request: format, hash lookup,
// revocation, expiry, the rate limit and the tenant scope, in that order.
// nothing is cached, so a revocation or expiry applies to the very next
// request. every request of a known key is counted in
// cp.api_key_usage_hourly before it is answered, refused ones also as
// rejected; a failed count fails the request rather than leaving it
// uncounted.
func (m *Machine) authenticate(next http.Handler) http.Handler {
	return httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		hdr := r.Header.Values("Authorization")
		if len(hdr) != 1 {
			return unauthenticated(w, "an operator api key is required: Authorization: Bearer sa_op_...")
		}
		hash, err := ParseBearer(hdr[0])
		if err != nil {
			return unauthenticated(w, "the authorization header does not hold an operator api key")
		}
		k, found, err := m.lookup(ctx, hash)
		if err != nil {
			return err
		}
		if !found {
			return unauthenticated(w, "unknown operator api key")
		}
		now := m.now()
		refuse := func(e error) error {
			if err := m.record(ctx, k.ID, now, true); err != nil {
				return err
			}
			return e
		}
		switch {
		case k.RevokedAt != nil:
			return refuse(unauthenticated(w, "this operator api key was revoked"))
		case !k.ExpiresAt.After(now):
			return refuse(unauthenticated(w, "this operator api key expired"))
		}
		if ok, wait := m.limiter.take(k.ID, now); !ok {
			return refuse(httpx.RateLimited(wait))
		}
		key := machineKey{ID: k.ID}
		if k.Tenant.Valid {
			t := k.Tenant.UUID
			key.Tenant = &t
			// the scope is decided here, before the route runs, so the
			// refusal is counted like every other one. a malformed tenant
			// value is left to the route, which answers 400.
			if v := r.URL.Query()["tenant"]; len(v) == 1 && canonicalUUID.MatchString(v[0]) && v[0] != t.String() {
				return refuse(httpx.Forbidden(CodeScopeForbidden, "this key reads only tenant "+t.String()))
			}
		}
		if err := m.record(ctx, k.ID, now, false); err != nil {
			return err
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, machineKeyCtx{}, key)))
		return nil
	})
}

// lookup finds a key by the sha-256 of what was presented. the index
// lookup compares hashes, never keys: its timing can only tell an attacker
// about the hash of a 238-bit random value, which says nothing about any
// stored key. the stored hash is compared again in constant time, so a
// row is accepted only on an exact match.
func (m *Machine) lookup(ctx context.Context, hash [32]byte) (storedKey, bool, error) {
	var k storedKey
	err := m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select id, hash, scope_tenant_id, expires_at, revoked_at
			from cp.operator_api_keys where hash = $1`, hash[:]).
			Scan(&k.ID, &k.Hash, &k.Tenant, &k.ExpiresAt, &k.RevokedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return storedKey{}, false, nil
	}
	if err != nil {
		return storedKey{}, false, fmt.Errorf("operator: api key lookup: %w", err)
	}
	if subtle.ConstantTimeCompare(k.Hash, hash[:]) != 1 {
		return storedKey{}, false, nil
	}
	return k, true, nil
}

// record counts one request of a key in its hour, and for an accepted one
// stamps last_used_at, in one transaction.
func (m *Machine) record(ctx context.Context, id uuid.UUID, at time.Time, rejected bool) error {
	err := m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		q := operatorq.New(tx)
		if err := q.RecordAPIKeyUse(ctx, operatorq.RecordAPIKeyUseParams{KeyID: id, At: at, Rejected: rejected}); err != nil {
			return err
		}
		if rejected {
			return nil
		}
		n, err := q.TouchOperatorAPIKey(ctx, operatorq.TouchOperatorAPIKeyParams{At: at, ID: id})
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("last_used_at of key %s: %d rows", id, n)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: api key usage: %w", err)
	}
	return nil
}

// document serves the embedded openapi document, the contract itself.
func (m *Machine) document(w http.ResponseWriter, r *http.Request) error {
	if len(r.URL.Query()) > 0 {
		return httpx.BadQuery("this route takes no query parameters")
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openapi.MetricsV1)
	return nil
}

// keyLimiter holds one token bucket per key. buckets are per process: with
// two control plane instances behind the load balancer a key can reach
// twice the rate, which docs/operators/api-keys.md states.
type keyLimiter struct {
	mu        sync.Mutex
	rate      float64
	burst     float64
	buckets   map[uuid.UUID]*tokenBucket
	lastPrune time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// take spends one token of key id. when none is left it returns false and
// how long until the next token arrives.
func (l *keyLimiter) take(id uuid.UUID, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now)
	b, ok := l.buckets[id]
	if !ok {
		b = &tokenBucket{tokens: l.burst, last: now}
		l.buckets[id] = b
	}
	if el := now.Sub(b.last); el > 0 {
		b.tokens = min(l.burst, b.tokens+el.Seconds()*l.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// prune drops, at most once a minute, the buckets that have refilled
// completely: a new bucket starts full, so forgetting them changes nothing
// and the map stays as small as the set of recently active keys.
func (l *keyLimiter) prune(now time.Time) {
	if now.Sub(l.lastPrune) < time.Minute {
		return
	}
	l.lastPrune = now
	full := time.Duration(l.burst / l.rate * float64(time.Second))
	for id, b := range l.buckets {
		if now.Sub(b.last) >= full {
			delete(l.buckets, id)
		}
	}
}
