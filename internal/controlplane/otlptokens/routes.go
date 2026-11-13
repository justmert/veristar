package otlptokens

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/db/tenantq"
	"github.com/justmert/veristar/internal/registry"
)

// maxActive bounds the active tokens of one tenant, so a leaked session
// cannot fill the registry
const maxActive = 50

const tenantSuspended = "suspended"

// environment labels are short machine-like names (production, staging-eu)
var environmentRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,31})?$`)

// Token is the token object of contract 6.1. the plaintext is never part
// of it; Created adds it to the create answer only.
type Token struct {
	ID          uuid.UUID        `json:"id"`
	TenantID    uuid.UUID        `json:"tenant_id"`
	Name        string           `json:"name"`
	Environment string           `json:"environment"`
	Prefix      string           `json:"prefix"`
	Status      string           `json:"status"`
	CreatedBy   uuid.UUID        `json:"created_by"`
	CreatedAt   httpx.Timestamp  `json:"created_at"`
	RevokedAt   *httpx.Timestamp `json:"revoked_at"`
	LastSeenAt  *httpx.Timestamp `json:"last_seen_at"`
}

// Created is the create answer: the token object plus the plaintext,
// shown this once.
type Created struct {
	Token
	Plain string `json:"token"`
}

func toToken(r tenantq.GetOTLPTokenRow) Token {
	return Token{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Environment: r.Environment, Prefix: r.Prefix,
		Status: r.Status, CreatedBy: r.CreatedBy, CreatedAt: httpx.Timestamp(r.CreatedAt),
		RevokedAt: httpx.TimestampPtr(r.RevokedAt), LastSeenAt: httpx.TimestampPtr(r.LastSeenAt),
	}
}

// Routes are the otlp token routes.
type Routes struct {
	s   *tenants.Scope
	reg *registry.Publisher
}

// NewRoutes needs a publisher bound to registry_otlp: a token the
// collector does not know about is useless, so the routes are not built
// without one.
func NewRoutes(s *tenants.Scope, reg *registry.Publisher) (*Routes, error) {
	if s == nil || reg == nil {
		return nil, errors.New("otlptokens: scope and registry publisher are required")
	}
	return &Routes{s: s, reg: reg}, nil
}

// Routes registers the paths under a router mounted at /api.
func (h *Routes) Routes(r chi.Router) {
	r.Method(http.MethodPost, "/tenants/{tenant_id}/otlp-tokens", httpx.HandlerFunc(h.create))
	r.Method(http.MethodGet, "/tenants/{tenant_id}/otlp-tokens", httpx.HandlerFunc(h.list))
	r.Method(http.MethodGet, "/tenants/{tenant_id}/otlp-tokens/{token_id}", httpx.HandlerFunc(h.get))
	r.Method(http.MethodDelete, "/tenants/{tenant_id}/otlp-tokens/{token_id}", httpx.HandlerFunc(h.revoke))
}

// refuseInactive answers 409 for a tenant that may not take api metrics
// (contract 6.4): suspended keeps its own code from 5.11.
func refuseInactive(status string) error {
	switch status {
	case tenantActive:
		return nil
	case tenantSuspended:
		return httpx.Conflict(httpx.CodeTenantSuspended, "the project is suspended")
	default:
		return httpx.Conflict(httpx.CodeTenantNotActive, "the project is "+status+"; tokens can be created once it is active")
	}
}

// unavailable logs the registry failure and answers 503: the database
// change was rolled back, so the client can retry.
func unavailable(r *http.Request, err error) error {
	httpx.Logger(r.Context()).Error("otlp registry write failed", "error", err.Error())
	return httpx.Unavailable("the token registry is unreachable")
}

type createBody struct {
	Name        *string `json:"name"`
	Environment *string `json:"environment"`
}

func (h *Routes) create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	if err := a.RequireTerms(); err != nil {
		return err
	}
	if err := refuseInactive(a.TenantStatus); err != nil {
		return err
	}
	var b createBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	var f tenants.Fields
	var name, env string
	if b.Name == nil {
		f.Add("name", httpx.FieldRequired)
	} else {
		name = tenants.Text(&f, "name", *b.Name, 1, 100)
	}
	if b.Environment != nil {
		env = *b.Environment
		if env != "" && !environmentRe.MatchString(env) {
			f.Add("environment", httpx.FieldInvalidFormat)
		}
	}
	if err := f.Err(); err != nil {
		return err
	}
	g, err := Generate()
	if err != nil {
		return err
	}
	published := false
	var row tenantq.InsertOTLPTokenRow
	err = h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		q := tenantq.New(tx)
		status, err := q.LockTenantStatus(ctx)
		if err != nil {
			return err
		}
		if err := refuseInactive(status); err != nil {
			return err
		}
		n, err := q.CountActiveOTLPTokens(ctx)
		if err != nil {
			return err
		}
		if n >= maxActive {
			return httpx.Validation("a project has at most 50 active tokens", map[string]string{"name": httpx.FieldTooMany})
		}
		row, err = q.InsertOTLPToken(ctx, tenantq.InsertOTLPTokenParams{
			Name: name, Environment: env, Prefix: g.Display, Hash: g.Hash[:], CreatedBy: a.User.ID,
		})
		if err != nil {
			return err
		}
		if err := audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: audit.OTLPTokenCreate, Target: audit.OTLPTokenTarget(row.ID),
			Detail: map[string]any{"tenant_id": a.TenantID, "name": name, "environment": env, "prefix": g.Display},
		}); err != nil {
			return err
		}
		e, err := Entry(a.TenantID, row.ID, g.Hash[:], row.Status, status)
		if err != nil {
			return err
		}
		// published before commit and under the tenant row lock: the token
		// works once the answer arrives, and a suspension waits for it
		if err := h.reg.PutOTLPToken(ctx, e); err != nil {
			return unavailable(r, err)
		}
		published = true
		return nil
	})
	if err != nil {
		if published {
			// the row did not commit, so no one may ever use the key. the
			// plaintext was never shown, but the key is removed anyway
			if derr := h.reg.DeleteOTLPToken(context.WithoutCancel(ctx), g.HexHash()); derr != nil {
				httpx.Logger(ctx).Error("otlp registry key of an uncommitted token left behind; it cannot be used because its token was never shown",
					"token_id", row.ID.String(), "error", derr.Error())
			}
		}
		return err
	}
	httpx.Created(w, "/api/tenants/"+a.TenantID.String()+"/otlp-tokens/"+row.ID.String(),
		Created{Token: toToken(tenantq.GetOTLPTokenRow(row)), Plain: g.Plain})
	return nil
}

const listRoute = "otlp-tokens"

func (h *Routes) list(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	p, err := httpx.ParsePage(r, listRoute)
	if err != nil {
		return err
	}
	params := tenantq.ListOTLPTokensPageParams{RowLimit: int32(p.Limit + 1)} //nolint:gosec // limit is at most 200
	if v, ok := r.URL.Query()["include_revoked"]; ok {
		switch {
		case len(v) == 1 && v[0] == "true":
			params.IncludeRevoked = true
		case len(v) == 1 && v[0] == "false":
		default:
			return httpx.BadQuery("include_revoked must be true or false")
		}
	}
	if p.After != nil {
		params.AfterCreatedAt = &p.After.At
		params.AfterID = uuid.NullUUID{UUID: p.After.ID, Valid: true}
	}
	var rows []tenantq.ListOTLPTokensPageRow
	if err := h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		var qerr error
		rows, qerr = tenantq.New(tx).ListOTLPTokensPage(ctx, params)
		return qerr
	}); err != nil {
		return err
	}
	out := httpx.List[Token]{Items: []Token{}}
	for i, t := range rows {
		if i == p.Limit {
			last := rows[i-1]
			out.NextCursor = httpx.NextCursor(listRoute, httpx.Cursor{At: last.CreatedAt, ID: last.ID})
			break
		}
		out.Items = append(out.Items, toToken(tenantq.GetOTLPTokenRow(t)))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// load reads the tenant and token id. under cp_tenant a token of another
// tenant does not exist, so a guessed id is the same 404.
func (h *Routes) load(r *http.Request) (tenants.Access, uuid.UUID, error) {
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return tenants.Access{}, uuid.Nil, err
	}
	id, err := httpx.ParseID(chi.URLParam(r, "token_id"))
	if err != nil {
		return tenants.Access{}, uuid.Nil, err
	}
	return a, id, nil
}

func (h *Routes) get(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, id, err := h.load(r)
	if err != nil {
		return err
	}
	var t tenantq.GetOTLPTokenRow
	if err := h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		var qerr error
		t, qerr = tenantq.New(tx).GetOTLPToken(ctx, id)
		if errors.Is(qerr, pgx.ErrNoRows) {
			return httpx.NotFound()
		}
		return qerr
	}); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toToken(t))
	return nil
}

// revoke is allowed in every status a member can reach (suspended too) and
// without current terms: taking a credential out of use must never be
// blocked. repeating it answers 204 again and rewrites the registry entry,
// so a retry after a registry failure heals it.
func (h *Routes) revoke(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, id, err := h.load(r)
	if err != nil {
		return err
	}
	err = h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		q := tenantq.New(tx)
		t, err := q.LockOTLPToken(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound()
		}
		if err != nil {
			return err
		}
		if t.Status == StatusActive {
			if err := q.SetOTLPTokenRevoked(ctx, id); err != nil {
				return err
			}
			if err := audit.InTenant(ctx, tx, audit.Entry{
				Actor: audit.User(a.User.ID), Action: audit.OTLPTokenRevoke, Target: audit.OTLPTokenTarget(id),
				Detail: map[string]any{"tenant_id": a.TenantID, "status": audit.Change(StatusActive, StatusRevoked)},
			}); err != nil {
				return err
			}
		}
		e, err := Entry(a.TenantID, id, t.Hash, StatusRevoked, a.TenantStatus)
		if err != nil {
			return err
		}
		// before commit: if nats is down the revocation fails visibly
		// instead of leaving a revoked row whose key still says active
		if err := h.reg.PutOTLPToken(ctx, e); err != nil {
			return unavailable(r, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
