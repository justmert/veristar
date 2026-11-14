package operator

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/db"
)

// operator api key management (contract 9.2). every route is
// behind the operator guard. writes, and the reads that name the creating
// user, run as cp_system, which owns the key table's full rights (contract
// 5.1: operator actions run as cp_system).

// audit actions and target of contract 3.3
const (
	ActionAPIKeyCreate = "operator_api_key.create"
	ActionAPIKeyRevoke = "operator_api_key.revoke"
)

// APIKeyTarget names a key in the audit log.
func APIKeyTarget(id uuid.UUID) string { return "operator_api_key:" + id.String() }

// ScopeMetricsRead is the only scope a key can hold: read access to the
// machine metrics api, for every tenant or for one.
const ScopeMetricsRead = "metrics:read"

// the values of cp.operator_api_keys.scope
const (
	dbScopeGlobal = "global"
	dbScopeTenant = "tenant"
)

// key lifetime in days: 90 by default, at most 365, a
// limit beyond the rfp so that a forgotten key cannot live forever.
const (
	DefaultKeyDays = 90
	MaxKeyDays     = 365
)

// usage range of GET /api-keys/{key_id}/usage, in utc days
const (
	defaultUsageDays = 7
	maxUsageDays     = 31
)

// FieldUnknownTenant is the 422 field code of a tenant_id that names no
// tenant, or a removed one (contract 2.5).
const FieldUnknownTenant = "unknown_tenant"

// key statuses, derived from revoked_at and expires_at
const (
	KeyActive  = "active"
	KeyExpired = "expired"
	KeyRevoked = "revoked"
)

const apiKeysRoute = "operator-api-keys"

// APIKeys serves the key management routes.
type APIKeys struct {
	db  *db.DB
	now func() time.Time
}

// NewAPIKeys needs a database with the cp_system pool.
func NewAPIKeys(d *db.DB) (*APIKeys, error) {
	if d == nil {
		return nil, errors.New("operator: database is required")
	}
	return &APIKeys{db: d, now: time.Now}, nil
}

// Routes registers the paths under the guarded /api/operator router.
func (h *APIKeys) Routes(r chi.Router) {
	r.Method(http.MethodPost, "/api-keys", httpx.HandlerFunc(h.create))
	r.Method(http.MethodGet, "/api-keys", httpx.HandlerFunc(h.list))
	r.Method(http.MethodGet, "/api-keys/{key_id}", httpx.HandlerFunc(h.get))
	r.Method(http.MethodGet, "/api-keys/{key_id}/usage", httpx.HandlerFunc(h.usage))
	r.Method(http.MethodDelete, "/api-keys/{key_id}", httpx.HandlerFunc(h.revoke))
}

// APIKey is the key object of contract 9.2. it never carries the key or
// its hash; prefix is the stored display part.
type APIKey struct {
	ID         uuid.UUID        `json:"id"`
	Name       string           `json:"name"`
	Prefix     string           `json:"prefix"`
	Scope      string           `json:"scope"`
	TenantID   *uuid.UUID       `json:"tenant_id"`
	Status     string           `json:"status"`
	CreatedBy  uuid.UUID        `json:"created_by"`
	CreatedAt  httpx.Timestamp  `json:"created_at"`
	ExpiresAt  httpx.Timestamp  `json:"expires_at"`
	RevokedAt  *httpx.Timestamp `json:"revoked_at"`
	LastUsedAt *httpx.Timestamp `json:"last_used_at"`
}

// CreatedAPIKey is the answer of a create: the key object and, this once,
// the key itself.
type CreatedAPIKey struct {
	APIKey
	Key string `json:"key"`
}

func keyStatus(revokedAt *time.Time, expiresAt, now time.Time) string {
	switch {
	case revokedAt != nil:
		return KeyRevoked
	case !expiresAt.After(now):
		return KeyExpired
	}
	return KeyActive
}

// keyCols are the columns scanKey reads, in its order.
const keyCols = `id, name, prefix, scope_tenant_id, created_by, created_at, expires_at, revoked_at, last_used_at`

func (h *APIKeys) scanKey(row pgx.Row) (APIKey, error) {
	var k APIKey
	var tenant uuid.NullUUID
	var created, expires time.Time
	var revoked, lastUsed *time.Time
	if err := row.Scan(&k.ID, &k.Name, &k.Prefix, &tenant, &k.CreatedBy, &created, &expires, &revoked, &lastUsed); err != nil {
		return APIKey{}, err
	}
	k.Scope = ScopeMetricsRead
	if tenant.Valid {
		id := tenant.UUID
		k.TenantID = &id
	}
	k.CreatedAt, k.ExpiresAt = httpx.Timestamp(created), httpx.Timestamp(expires)
	k.RevokedAt, k.LastUsedAt = httpx.TimestampPtr(revoked), httpx.TimestampPtr(lastUsed)
	k.Status = keyStatus(revoked, expires, h.now())
	return k, nil
}

// createBody is the request of POST /api-keys. pointers tell a missing
// field from a zero value.
type createBody struct {
	Name          *string `json:"name"`
	Scope         *string `json:"scope"`
	TenantID      *string `json:"tenant_id"`
	ExpiresInDays *int    `json:"expires_in_days"`
}

func (h *APIKeys) create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var b createBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	var f tenants.Fields
	var name string
	if b.Name == nil {
		f.Add("name", httpx.FieldRequired)
	} else {
		name = tenants.Text(&f, "name", *b.Name, 1, 100)
	}
	switch {
	case b.Scope == nil:
		f.Add("scope", httpx.FieldRequired)
	case *b.Scope != ScopeMetricsRead:
		f.Add("scope", httpx.FieldInvalidValue)
	}
	var tenant *uuid.UUID
	if b.TenantID != nil {
		if !canonicalUUID.MatchString(*b.TenantID) {
			f.Add("tenant_id", httpx.FieldInvalidFormat)
		} else {
			id := uuid.MustParse(*b.TenantID)
			tenant = &id
		}
	}
	days := DefaultKeyDays
	if b.ExpiresInDays != nil {
		days = *b.ExpiresInDays
		if days < 1 || days > MaxKeyDays {
			f.Add("expires_in_days", httpx.FieldInvalidValue)
		}
	}
	if err := f.Err(); err != nil {
		return err
	}
	k, err := GenerateKey()
	if err != nil {
		return err
	}
	scope := dbScopeGlobal
	if tenant != nil {
		scope = dbScopeTenant
	}
	var out APIKey
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		if tenant != nil {
			// a key for a tenant that does not exist would read nothing, and
			// one for a removed tenant only purged data; both are refused.
			// internal tenants are allowed: the probe's key is one.
			var status string
			err := tx.QueryRow(ctx, `select status from cp.tenants where id = $1`, *tenant).Scan(&status)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && status == "removed") {
				return httpx.Validation("tenant_id is "+FieldUnknownTenant, map[string]string{"tenant_id": FieldUnknownTenant})
			}
			if err != nil {
				return err
			}
		}
		// expires_at is computed from the same now() as created_at, so the
		// table's check expires_at > created_at holds by construction
		out, err = h.scanKey(tx.QueryRow(ctx, `insert into cp.operator_api_keys
			(name, prefix, hash, scope, scope_tenant_id, expires_at, created_by)
			values ($1, $2, $3, $4, $5, now() + make_interval(days => $6), $7)
			returning `+keyCols, name, k.Display, k.Hash[:], scope, tenant, days, u.ID))
		if err != nil {
			return fmt.Errorf("insert key: %w", err)
		}
		detail := map[string]any{
			"name": name, "prefix": k.Display, "scope": ScopeMetricsRead,
			"expires_at": httpx.Time(time.Time(out.ExpiresAt)), "tenant_id": nil,
		}
		if tenant != nil {
			// contract 5.9 filters the audit list on detail.tenant_id
			detail["tenant_id"] = tenant.String()
		}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: ActionAPIKeyCreate, Target: APIKeyTarget(out.ID), Detail: detail,
		})
	})
	if err != nil {
		return err
	}
	httpx.Created(w, "/api/operator/api-keys/"+out.ID.String(), CreatedAPIKey{APIKey: out, Key: k.Plain})
	return nil
}

func (h *APIKeys) list(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	for k := range r.URL.Query() {
		if k != "limit" && k != "cursor" {
			return httpx.BadQuery("unknown query parameter " + k)
		}
	}
	p, err := httpx.ParsePage(r, apiKeysRoute)
	if err != nil {
		return err
	}
	var afterAt *time.Time
	var afterID uuid.NullUUID
	if p.After != nil {
		afterAt = &p.After.At
		afterID = uuid.NullUUID{UUID: p.After.ID, Valid: true}
	}
	out := httpx.List[APIKey]{Items: []APIKey{}}
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		// newest first; the keyset follows the same order
		rows, err := tx.Query(ctx, `select `+keyCols+` from cp.operator_api_keys
			where $1::timestamptz is null or (created_at, id) < ($1::timestamptz, $2::uuid)
			order by created_at desc, id desc limit $3`, afterAt, afterID, p.Limit+1)
		if err != nil {
			return err
		}
		items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (APIKey, error) { return h.scanKey(row) })
		if err != nil {
			return err
		}
		if len(items) > p.Limit {
			items = items[:p.Limit]
			last := items[len(items)-1]
			out.NextCursor = httpx.NextCursor(apiKeysRoute, httpx.Cursor{At: time.Time(last.CreatedAt), ID: last.ID})
		}
		out.Items = items
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: list api keys: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// load reads one key by the path id. an unknown or malformed id is 404.
func (h *APIKeys) load(r *http.Request, tx pgx.Tx) (APIKey, error) {
	id, err := httpx.ParseID(chi.URLParam(r, "key_id"))
	if err != nil {
		return APIKey{}, err
	}
	k, err := h.scanKey(tx.QueryRow(r.Context(), `select `+keyCols+` from cp.operator_api_keys where id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, httpx.NotFound()
	}
	return k, err
}

func (h *APIKeys) get(w http.ResponseWriter, r *http.Request) error {
	if len(r.URL.Query()) > 0 {
		return httpx.BadQuery("this route takes no query parameters")
	}
	var k APIKey
	err := h.db.WithSystem(r.Context(), func(tx pgx.Tx) error {
		var err error
		k, err = h.load(r, tx)
		return err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, k)
	return nil
}

// UsageHour is one hour of cp.api_key_usage_hourly. requests counts every
// request the key made in that hour; rejected the ones refused after the
// key matched (expired, revoked, rate limited, out of scope).
type UsageHour struct {
	Hour     httpx.Timestamp `json:"hour"`
	Requests int64           `json:"requests"`
	Rejected int64           `json:"rejected"`
}

// UsageTotals sum the hours of a usage answer.
type UsageTotals struct {
	Requests int64 `json:"requests"`
	Rejected int64 `json:"rejected"`
}

// Usage is the answer of GET /api-keys/{key_id}/usage.
type Usage struct {
	KeyID  uuid.UUID   `json:"key_id"`
	From   string      `json:"from"`
	To     string      `json:"to"`
	Totals UsageTotals `json:"totals"`
	Hours  []UsageHour `json:"hours"`
}

func (h *APIKeys) usage(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	for k := range q {
		if k != "from" && k != "to" {
			return httpx.BadQuery("unknown query parameter " + k)
		}
	}
	rg, err := parseDayRange(q, h.now(), defaultUsageDays, maxUsageDays)
	if err != nil {
		return err
	}
	out := Usage{From: rg.From.Format(time.DateOnly), To: rg.To.Format(time.DateOnly), Hours: []UsageHour{}}
	ctx := r.Context()
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		k, err := h.load(r, tx)
		if err != nil {
			return err
		}
		out.KeyID = k.ID
		rows, err := tx.Query(ctx, `select hour, requests, rejected from cp.api_key_usage_hourly
			where key_id = $1 and hour >= $2 and hour < $3 order by hour`, k.ID, rg.From, rg.end())
		if err != nil {
			return err
		}
		out.Hours, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (UsageHour, error) {
			var u UsageHour
			var at time.Time
			err := row.Scan(&at, &u.Requests, &u.Rejected)
			u.Hour = httpx.Timestamp(at)
			return u, err
		})
		return err
	})
	if err != nil {
		return err
	}
	for _, u := range out.Hours {
		out.Totals.Requests += u.Requests
		out.Totals.Rejected += u.Rejected
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// revoke sets revoked_at once. the machine api reads the key row on every
// request, so the next request with the key is refused. revoking a revoked
// key changes nothing and writes no audit row.
func (h *APIKeys) revoke(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	if len(r.URL.Query()) > 0 {
		return httpx.BadQuery("this route takes no query parameters")
	}
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		k, err := h.load(r, tx)
		if err != nil {
			return err
		}
		var tenant uuid.NullUUID
		err = tx.QueryRow(ctx, `update cp.operator_api_keys set revoked_at = now()
			where id = $1 and revoked_at is null returning scope_tenant_id`, k.ID).Scan(&tenant)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		detail := map[string]any{"name": k.Name, "prefix": k.Prefix, "tenant_id": nil}
		if tenant.Valid {
			detail["tenant_id"] = tenant.UUID.String()
		}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: ActionAPIKeyRevoke, Target: APIKeyTarget(k.ID), Detail: detail,
		})
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
