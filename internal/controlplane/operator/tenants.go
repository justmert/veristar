package operator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/lifecycle"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/operatorq"
	"github.com/justmert/veristar/internal/db/systemq"
)

// Tenants serves the review reads of contract 5.3 and 5.4. tenant, site,
// ingest and removal reads run as cp_operator; users, members and the audit
// excerpt as cp_system (5.1).
type Tenants struct {
	db  *db.DB
	now func() time.Time
}

// NewTenants needs a database with the cp_operator and cp_system pools.
func NewTenants(d *db.DB) (*Tenants, error) {
	if d == nil {
		return nil, errors.New("operator: database is required")
	}
	return &Tenants{db: d, now: time.Now}, nil
}

// Routes registers the paths under the guarded /api/operator router.
func (h *Tenants) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/tenants", httpx.HandlerFunc(h.list))
	r.Method(http.MethodGet, "/tenants/{tenant_id}", httpx.HandlerFunc(h.detail))
}

const tenantsRoute = "operator-tenants"

var tenantStatuses = map[string]bool{
	lifecycle.StatusPending: true, lifecycle.StatusActive: true, lifecycle.StatusRejected: true,
	lifecycle.StatusSuspended: true, lifecycle.StatusRemoved: true,
}

var tenantSorts = map[string]bool{"created_at": true, "status_changed_at": true, "name": true}

// tenantCursor binds the keyset position to the sort and order it was made
// for, so a cursor reused with another sort is refused (contract 5.3).
type tenantCursor struct {
	R string    `json:"r"`
	S string    `json:"s"`
	O string    `json:"o"`
	K string    `json:"k"`
	I uuid.UUID `json:"i"`
}

func encodeTenantCursor(sort, order, k string, id uuid.UUID) *string {
	b, err := json.Marshal(tenantCursor{R: tenantsRoute, S: sort, O: order, K: k, I: id})
	if err != nil {
		panic(err) // strings and a uuid always encode
	}
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func oneValue(q map[string][]string, key string) (string, bool, error) {
	v, ok := q[key]
	if !ok {
		return "", false, nil
	}
	if len(v) != 1 {
		return "", false, httpx.BadQuery(key + " may be given once")
	}
	if !sqlText(v[0]) {
		return "", false, httpx.BadQuery(key + " must be utf-8 text without nul characters")
	}
	return v[0], true, nil
}

// sqlText reports whether s can reach postgresql as a text value:
// postgresql refuses a nul character and invalid utf-8 in text, so such a
// value, reached through a query parameter (%00, %ff) or a cursor a client
// edited, would otherwise fail the request with 500 instead of 400.
func sqlText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func parseTenantQuery(r *http.Request) (operatorq.OperatorTenantPageParams, string, string, int, error) {
	q := r.URL.Query()
	p := operatorq.OperatorTenantPageParams{Statuses: []string{}, Sort: "created_at", Descending: true}
	order := "desc"
	if v, ok, err := oneValue(q, "status"); err != nil {
		return p, "", "", 0, err
	} else if ok {
		for _, s := range strings.Split(v, ",") {
			if !tenantStatuses[s] {
				return p, "", "", 0, httpx.BadQuery("unknown status " + s)
			}
			p.Statuses = append(p.Statuses, s)
		}
	}
	if v, ok, err := oneValue(q, "review"); err != nil {
		return p, "", "", 0, err
	} else if ok {
		if v != "true" {
			return p, "", "", 0, httpx.BadQuery("review must be true when given")
		}
		p.Review = true
	}
	if v, ok, err := oneValue(q, "q"); err != nil {
		return p, "", "", 0, err
	} else if ok {
		if n := utf8.RuneCountInString(v); n < 1 || n > 100 {
			return p, "", "", 0, httpx.BadQuery("q must be 1 to 100 characters")
		}
		p.Q = &v
	}
	if v, ok, err := oneValue(q, "sort"); err != nil {
		return p, "", "", 0, err
	} else if ok {
		if !tenantSorts[v] {
			return p, "", "", 0, httpx.BadQuery("sort must be created_at, status_changed_at or name")
		}
		p.Sort = v
	}
	if v, ok, err := oneValue(q, "order"); err != nil {
		return p, "", "", 0, err
	} else if ok {
		if v != "asc" && v != "desc" {
			return p, "", "", 0, httpx.BadQuery("order must be asc or desc")
		}
		order = v
		p.Descending = v == "desc"
	}
	// limit is checked by httpx; the cursor is this route's own
	page, err := httpx.ParsePage(withoutCursor(r), tenantsRoute)
	if err != nil {
		return p, "", "", 0, err
	}
	if v, ok, err := oneValue(q, "cursor"); err != nil {
		return p, "", "", 0, httpx.BadCursor()
	} else if ok {
		raw, err := base64.RawURLEncoding.DecodeString(v)
		if err != nil {
			return p, "", "", 0, httpx.BadCursor()
		}
		var c tenantCursor
		if err := json.Unmarshal(raw, &c); err != nil || c.R != tenantsRoute || c.S != p.Sort || c.O != order ||
			c.I == uuid.Nil || !sqlText(c.K) {
			return p, "", "", 0, httpx.BadCursor()
		}
		p.AfterK = &c.K
		p.AfterID = uuid.NullUUID{UUID: c.I, Valid: true}
	}
	p.RowLimit = int32(page.Limit + 1) //nolint:gosec // limit is at most 200
	return p, p.Sort, order, page.Limit, nil
}

// withoutCursor drops the cursor so httpx.ParsePage checks only limit.
func withoutCursor(r *http.Request) *http.Request {
	c := r.Clone(r.Context())
	q := c.URL.Query()
	q.Del("cursor")
	c.URL.RawQuery = q.Encode()
	return c
}

func (h *Tenants) list(w http.ResponseWriter, r *http.Request) error {
	p, sort, order, limit, err := parseTenantQuery(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var rows []operatorq.OperatorTenantPageRow
	if err := h.db.WithOperator(ctx, func(tx pgx.Tx) error {
		var qerr error
		rows, qerr = operatorq.New(tx).OperatorTenantPage(ctx, p)
		return qerr
	}); err != nil {
		return fmt.Errorf("operator: tenant page: %w", err)
	}
	out := httpx.List[lifecycle.TenantSummary]{Items: make([]lifecycle.TenantSummary, 0, min(len(rows), limit))}
	for i, t := range rows {
		if i == limit {
			last := rows[limit-1]
			out.NextCursor = encodeTenantCursor(sort, order, last.K, last.ID)
			break
		}
		out.Items = append(out.Items, lifecycle.TenantSummary{
			ID: t.ID, Slug: t.Slug, Name: t.Name, Status: t.Status, StatusReason: t.StatusReason,
			Website: t.Website, CreatedAt: httpx.Timestamp(t.CreatedAt), StatusChangedAt: httpx.Timestamp(t.StatusChangedAt),
			SiteCount: t.SiteCount, SitesInReview: t.SitesInReview, SitesVerified: t.SitesVerified,
			SitesWithStellarToml: t.SitesWithStellarToml,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// UserRef is a user as the detail names it. each identity is null when the
// account does not hold it.
type UserRef struct {
	ID             uuid.UUID `json:"id"`
	GitHubID       *int64    `json:"github_id"`
	GitHubLogin    *string   `json:"github_login"`
	StellarAddress *string   `json:"stellar_address"`
	PasskeyAddress *string   `json:"passkey_address"`
	DisplayName    string    `json:"display_name"`
}

// DetailTenant is the tenant object of contract 5.4.
type DetailTenant struct {
	ID                   uuid.UUID       `json:"id"`
	Slug                 string          `json:"slug"`
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	Status               string          `json:"status"`
	StatusReason         *string         `json:"status_reason"`
	EnrollmentAnswers    json.RawMessage `json:"enrollment_answers"`
	AcceptedTermsVersion *string         `json:"accepted_terms_version"`
	Provisioning         string          `json:"provisioning"`
	CreatedBy            *UserRef        `json:"created_by"`
	ApprovedBy           *UserRef        `json:"approved_by"`
	CreatedAt            httpx.Timestamp `json:"created_at"`
	UpdatedAt            httpx.Timestamp `json:"updated_at"`
	StatusChangedAt      httpx.Timestamp `json:"status_changed_at"`
}

// StellarID is one enrollment id re-checked with strkey.
type StellarID struct {
	ID    string  `json:"id"`
	Type  *string `json:"type"`
	Valid bool    `json:"valid"`
}

// DetailMember is one member of the tenant.
type DetailMember struct {
	GitHubID       *int64          `json:"github_id"`
	GitHubLogin    *string         `json:"github_login"`
	StellarAddress *string         `json:"stellar_address"`
	PasskeyAddress *string         `json:"passkey_address"`
	DisplayName    string          `json:"display_name"`
	Role           string          `json:"role"`
	CreatedAt      httpx.Timestamp `json:"created_at"`
}

// Ingest holds the outcome sums of the last 24 full hours and 30 full days.
type Ingest struct {
	Last24h map[string]int64 `json:"last_24h"`
	Last30d map[string]int64 `json:"last_30d"`
}

// Removal is the removal object of contract 5.4.
type Removal struct {
	RequestedAt httpx.Timestamp `json:"requested_at"`
	RequestedBy *uuid.UUID      `json:"requested_by"`
	// the github login of the requester, null for a wallet or passkey
	// account, a deleted account or no requester
	RequestedByLogin *string `json:"requested_by_login"`
	// the console name of the requester (login or short address),
	// DeletedAccountName for a deleted account, null for no requester
	RequestedByDisplayName *string          `json:"requested_by_display_name"`
	Reason                 string           `json:"reason"`
	KeepAggregates         bool             `json:"keep_aggregates"`
	CompletedAt            *httpx.Timestamp `json:"completed_at"`
	Steps                  json.RawMessage  `json:"steps"`
}

// TenantDetail is the answer of GET /api/operator/tenants/{tenant_id}.
type TenantDetail struct {
	Tenant         DetailTenant             `json:"tenant"`
	StellarIDs     []StellarID              `json:"stellar_ids"`
	Members        []DetailMember           `json:"members"`
	Sites          []lifecycle.OperatorSite `json:"sites"`
	Ingest         Ingest                   `json:"ingest"`
	Removal        *Removal                 `json:"removal"`
	RecentAudit    []AuditEntry             `json:"recent_audit"`
	AllowedActions []string                 `json:"allowed_actions"`
}

var strkeyTypes = map[strkey.VersionByte]string{
	strkey.VersionByteAccountID:    "account",
	strkey.VersionByteMuxedAccount: "muxed_account",
	strkey.VersionByteContract:     "contract",
}

// recheckIDs re-validates the stored enrollment ids; the stored answers are
// shown as entered, this adds what each one is.
func recheckIDs(answers []byte) []StellarID {
	var a struct {
		StellarIDs []string `json:"stellar_ids"`
	}
	out := []StellarID{}
	if json.Unmarshal(answers, &a) != nil {
		return out
	}
	for _, id := range a.StellarIDs {
		s := StellarID{ID: id}
		if vb, _, err := strkey.DecodeAny(id); err == nil {
			if t, ok := strkeyTypes[vb]; ok {
				s.Type, s.Valid = &t, true
			}
		}
		out = append(out, s)
	}
	return out
}

const recentAudit = 20

func sums(rows []operatorq.OperatorTenantIngestRow) map[string]int64 {
	out := map[string]int64{}
	for _, r := range rows {
		out[r.Outcome] = r.Total
	}
	return out
}

func (h *Tenants) detail(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.ParseID(chi.URLParam(r, "tenant_id"))
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out TenantDetail
	var t operatorq.CpTenant
	hour := h.now().UTC().Truncate(time.Hour)
	day := h.now().UTC().Truncate(24 * time.Hour)
	err = h.db.WithOperator(ctx, func(tx pgx.Tx) error {
		q := operatorq.New(tx)
		var err error
		if t, err = q.OperatorTenant(ctx, id); err != nil {
			return err
		}
		sites, err := q.OperatorTenantSites(ctx, id)
		if err != nil {
			return err
		}
		out.Sites = make([]lifecycle.OperatorSite, 0, len(sites))
		for _, s := range sites {
			out.Sites = append(out.Sites, operatorSite(s))
		}
		h24, err := q.OperatorTenantIngest(ctx, operatorq.OperatorTenantIngestParams{TenantID: id, FromAt: hour.Add(-24 * time.Hour), ToAt: hour})
		if err != nil {
			return err
		}
		d30, err := q.OperatorTenantIngest(ctx, operatorq.OperatorTenantIngestParams{TenantID: id, FromAt: day.AddDate(0, 0, -30), ToAt: day})
		if err != nil {
			return err
		}
		out.Ingest = Ingest{Last24h: sums(h24), Last30d: sums(d30)}
		rm, err := q.OperatorTenantRemoval(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		steps := json.RawMessage(rm.Steps)
		if len(steps) == 0 {
			steps = json.RawMessage("{}")
		}
		var by *uuid.UUID
		if rm.RequestedBy.Valid {
			by = &rm.RequestedBy.UUID
		}
		out.Removal = &Removal{
			RequestedAt: httpx.Timestamp(rm.RequestedAt), RequestedBy: by, Reason: rm.Reason,
			KeepAggregates: rm.KeepAggregates, CompletedAt: httpx.TimestampPtr(rm.CompletedAt), Steps: steps,
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// unknown and internal tenants alike
		return httpx.NotFound()
	}
	if err != nil {
		return fmt.Errorf("operator: tenant detail: %w", err)
	}

	// the public demo names no person and shows no audit row: its members,
	// creator and approver are real accounts (contract 21.4)
	demo := session.Demo(ctx)
	if demo {
		out.Members, out.RecentAudit = []DetailMember{}, []AuditEntry{}
	}
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		if demo {
			return nil
		}
		q := systemq.New(tx)
		var err error
		if out.Tenant.CreatedBy, err = userRef(ctx, q, t.CreatedBy); err != nil {
			return err
		}
		if out.Tenant.ApprovedBy, err = userRef(ctx, q, t.ApprovedBy); err != nil {
			return err
		}
		members, err := q.OperatorTenantMembers(ctx, id)
		if err != nil {
			return err
		}
		out.Members = make([]DetailMember, 0, len(members))
		for _, m := range members {
			out.Members = append(out.Members, DetailMember{
				GitHubID: m.GithubID, GitHubLogin: m.GithubLogin, StellarAddress: m.StellarAddress,
				PasskeyAddress: m.PasskeyAddress,
				DisplayName:    identity.DisplayName(m.GithubLogin, m.StellarAddress, m.PasskeyAddress),
				Role:           m.Role, CreatedAt: httpx.Timestamp(m.CreatedAt),
			})
		}
		tid := id.String()
		rows, err := q.OperatorAuditPage(ctx, systemq.OperatorAuditPageParams{TenantID: &tid, Lim: recentAudit})
		if err != nil {
			return err
		}
		out.RecentAudit = make([]AuditEntry, 0, len(rows))
		for _, row := range rows {
			out.RecentAudit = append(out.RecentAudit, ToAuditEntry(row))
		}
		if err := resolveAuditNames(ctx, tx, out.RecentAudit); err != nil {
			return err
		}
		if out.Removal != nil && out.Removal.RequestedBy != nil {
			by, err := userRef(ctx, q, uuid.NullUUID{UUID: *out.Removal.RequestedBy, Valid: true})
			if err != nil {
				return err
			}
			name := DeletedAccountName
			if by != nil {
				out.Removal.RequestedByLogin, name = by.GitHubLogin, by.DisplayName
			}
			out.Removal.RequestedByDisplayName = &name
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: tenant detail: %w", err)
	}

	prov := "ready"
	if !t.UmamiTeamID.Valid {
		prov = "pending"
	}
	answers := json.RawMessage(t.EnrollmentAnswers)
	out.Tenant = DetailTenant{
		ID: t.ID, Slug: t.Slug, Name: t.Name, Description: t.Description, Status: t.Status,
		StatusReason: t.StatusReason, EnrollmentAnswers: answers, AcceptedTermsVersion: t.AcceptedTermsVersion,
		Provisioning: prov, CreatedBy: out.Tenant.CreatedBy, ApprovedBy: out.Tenant.ApprovedBy,
		CreatedAt: httpx.Timestamp(t.CreatedAt), UpdatedAt: httpx.Timestamp(t.UpdatedAt),
		StatusChangedAt: httpx.Timestamp(t.StatusChangedAt),
	}
	out.StellarIDs = recheckIDs(t.EnrollmentAnswers)
	out.AllowedActions = lifecycle.AllowedActions(t.Status)
	if demo {
		out.AllowedActions = []string{}
		if out.Removal != nil {
			out.Removal.RequestedBy = nil
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func userRef(ctx context.Context, q *systemq.Queries, id uuid.NullUUID) (*UserRef, error) {
	if !id.Valid {
		return nil, nil
	}
	u, err := q.OperatorUserRef(ctx, id.UUID)
	if errors.Is(err, pgx.ErrNoRows) {
		// the account was deleted (contract 4.1.6); its row names no one
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &UserRef{
		ID: u.ID, GitHubID: u.GithubID, GitHubLogin: u.GithubLogin, StellarAddress: u.StellarAddress,
		PasskeyAddress: u.PasskeyAddress,
		DisplayName:    identity.DisplayName(u.GithubLogin, u.StellarAddress, u.PasskeyAddress),
	}, nil
}

func operatorSite(s operatorq.OperatorTenantSitesRow) lifecycle.OperatorSite {
	toml := json.RawMessage("null")
	if len(s.StellarToml) > 0 {
		toml = json.RawMessage(s.StellarToml)
	}
	origins := s.Origins
	if origins == nil {
		origins = []string{}
	}
	return lifecycle.OperatorSite{
		ID: s.ID, Name: s.Name, Domain: s.Domain, Origins: origins, Status: s.Status,
		StatusReason: s.StatusReason, InReview: s.InReview, DomainPreviouslyRemoved: s.DomainPreviouslyRemoved,
		Verification: lifecycle.Verification(s.VerifiedAt, s.LastVerifyAt, s.LastVerifyError), VerifyMethod: s.VerifyMethod,
		VerifiedAt: httpx.TimestampPtr(s.VerifiedAt), LastVerifyAt: httpx.TimestampPtr(s.LastVerifyAt),
		LastVerifyError: s.LastVerifyError, StellarToml: toml,
		StellarTomlCheckedAt: httpx.TimestampPtr(s.StellarTomlCheckedAt),
		CreatedAt:            httpx.Timestamp(s.CreatedAt), UpdatedAt: httpx.Timestamp(s.UpdatedAt),
	}
}
