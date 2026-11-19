package tenants

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db/systemq"
	"github.com/justmert/veristar/internal/db/tenantq"
)

// enrollment limit of contract 4.2.1
const (
	maxTenantsPerDay = 10
	tenantsWindow    = 24 * time.Hour
)

// Tenant is the tenant object of contract 3.1.
type Tenant struct {
	ID                   uuid.UUID       `json:"id"`
	Slug                 string          `json:"slug"`
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	Status               string          `json:"status"`
	StatusReason         *string         `json:"status_reason"`
	EnrollmentAnswers    json.RawMessage `json:"enrollment_answers"`
	AcceptedTermsVersion *string         `json:"accepted_terms_version"`
	Provisioning         string          `json:"provisioning"`
	Role                 string          `json:"role"`
	CreatedAt            httpx.Timestamp `json:"created_at"`
	UpdatedAt            httpx.Timestamp `json:"updated_at"`
	StatusChangedAt      httpx.Timestamp `json:"status_changed_at"`
}

// row is the column set of cp.tenants; systemq and tenantq generate
// identical structs for it, so both convert to this one.
type row = tenantq.CpTenant

func toTenant(t row, role string) Tenant {
	prov := "pending"
	if t.UmamiTeamID.Valid {
		prov = "ready"
	}
	return Tenant{
		ID: t.ID, Slug: t.Slug, Name: t.Name, Description: t.Description,
		Status: t.Status, StatusReason: t.StatusReason,
		EnrollmentAnswers:    json.RawMessage(t.EnrollmentAnswers),
		AcceptedTermsVersion: t.AcceptedTermsVersion,
		Provisioning:         prov, Role: role,
		CreatedAt: httpx.Timestamp(t.CreatedAt), UpdatedAt: httpx.Timestamp(t.UpdatedAt),
		StatusChangedAt: httpx.Timestamp(t.StatusChangedAt),
	}
}

// Routes are the tenant routes. every route needs a session.
type Routes struct {
	s       *Scope
	onEnrol []EnrollHook
}

// NewRoutes returns the tenant routes over a scope.
func NewRoutes(s *Scope) *Routes { return &Routes{s: s} }

// EnrollHook runs inside the enrollment transaction (cp_system), after the
// project, its owner and the audit row are written; an error rolls the
// enrollment back. scfawards.Hooks matches the new project to the scf
// award directory and writes its cohort proposal.
type EnrollHook interface {
	AfterEnroll(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error
}

// OnEnroll adds hooks that run in each enrollment's transaction.
func (h *Routes) OnEnroll(hooks ...EnrollHook) *Routes {
	h.onEnrol = append(h.onEnrol, hooks...)
	return h
}

// Routes registers the paths under a router mounted at /api.
func (h *Routes) Routes(r chi.Router) {
	r.Method(http.MethodPost, "/tenants", httpx.HandlerFunc(h.create))
	r.Method(http.MethodGet, "/tenants", httpx.HandlerFunc(h.list))
	r.Method(http.MethodGet, "/tenants/{tenant_id}", httpx.HandlerFunc(h.get))
	r.Method(http.MethodPatch, "/tenants/{tenant_id}", httpx.HandlerFunc(h.patch))
	r.Method(http.MethodPost, "/tenants/{tenant_id}/terms", httpx.HandlerFunc(h.acceptTerms))
	r.Method(http.MethodPut, "/tenants/{tenant_id}/enrollment", httpx.HandlerFunc(h.resubmit))
	r.Method(http.MethodGet, "/tenants/{tenant_id}/integrity", httpx.HandlerFunc(h.integrity))
}

// enrollment is the enrollment object, stored as enrollment_answers. the
// field names are the stored keys.
type enrollment struct {
	Website      *string  `json:"website"`
	StellarUsage *string  `json:"stellar_usage"`
	StellarIDs   []string `json:"stellar_ids"`
	SCFRoundURL  *string  `json:"scf_round_url"`
}

type storedEnrollment struct {
	Website      string   `json:"website"`
	StellarUsage string   `json:"stellar_usage"`
	StellarIDs   []string `json:"stellar_ids"`
	SCFRoundURL  *string  `json:"scf_round_url"`
}

// checkEnrollment validates the enrollment object (contract 3.2) for
// enrollment and resubmission alike, and returns what is stored.
func checkEnrollment(f *Fields, e *enrollment) storedEnrollment {
	stored := storedEnrollment{StellarIDs: []string{}}
	if e == nil {
		f.Add("enrollment", httpx.FieldRequired)
		return stored
	}
	if e.Website == nil {
		f.Add("enrollment.website", httpx.FieldRequired)
	} else {
		stored.Website = *e.Website
		HTTPSURL(f, "enrollment.website", stored.Website)
	}
	if e.StellarUsage == nil {
		f.Add("enrollment.stellar_usage", httpx.FieldRequired)
	} else {
		stored.StellarUsage = Text(f, "enrollment.stellar_usage", *e.StellarUsage, 1, 2000)
	}
	if e.StellarIDs != nil {
		StellarIDs(f, "enrollment.stellar_ids", e.StellarIDs)
		stored.StellarIDs = e.StellarIDs
	}
	if e.SCFRoundURL != nil {
		HTTPSURL(f, "enrollment.scf_round_url", *e.SCFRoundURL)
		stored.SCFRoundURL = e.SCFRoundURL
	}
	return stored
}

type createBody struct {
	Name                 *string     `json:"name"`
	Slug                 *string     `json:"slug"`
	Description          *string     `json:"description"`
	Enrollment           *enrollment `json:"enrollment"`
	AcceptedTermsVersion *string     `json:"accepted_terms_version"`
	// an optional cohort proposal (contract 19.1)
	Cohort *CohortInput `json:"cohort"`
	// an optional award of the scf award search (4.2.7): its program,
	// round, category and award date become the cohort proposal
	SCFAwardID *string `json:"scf_award_id"`
}

func (h *Routes) create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var b createBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	var f Fields
	v := CheckNewTenant(&f, NewTenantInput{
		Name: b.Name, Slug: b.Slug, Description: b.Description, Enrollment: b.Enrollment, Cohort: b.Cohort,
	}, time.Now().UTC())
	name, slug, desc, stored, cohort := v.Name, v.Slug, v.Description, v.Enrollment, v.Cohort
	var awardID uuid.UUID
	if b.SCFAwardID != nil {
		id, err := uuid.Parse(*b.SCFAwardID)
		switch {
		case err != nil || len(*b.SCFAwardID) != 36:
			f.Add("scf_award_id", httpx.FieldInvalidFormat)
		case b.Cohort != nil:
			// the award is the proposal; a second one would contradict it
			f.Add("scf_award_id", httpx.FieldInvalidValue)
		default:
			awardID = id
		}
	}
	if b.AcceptedTermsVersion == nil {
		f.Add("accepted_terms_version", httpx.FieldRequired)
	}
	if err := f.Err(); err != nil {
		return err
	}
	if *b.AcceptedTermsVersion != h.s.termsVersion {
		return httpx.Conflict(httpx.CodeTermsVersionMismatch, "accepted terms version is not the current one")
	}
	answers, err := v.Answers()
	if err != nil {
		return err
	}

	var t systemq.CpTenant
	err = h.s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		// one enrollment at a time per user, so the daily limit holds under
		// concurrent requests
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('cp.enroll:' || $1::text, 0))`, u.ID); err != nil {
			return err
		}
		recent, err := q.TenantsCreatedSince(ctx, systemq.TenantsCreatedSinceParams{
			CreatedBy: uuid.NullUUID{UUID: u.ID, Valid: true}, Since: time.Now().Add(-tenantsWindow),
		})
		if err != nil {
			return err
		}
		if recent.Created >= maxTenantsPerDay {
			return httpx.RateLimited(time.Until(recent.Oldest.Add(tenantsWindow)))
		}
		status, err := enrollStatus(ctx, q)
		if err != nil {
			return err
		}
		t, err = q.EnrollTenant(ctx, systemq.EnrollTenantParams{
			Slug: slug, Name: name, Description: desc, Status: status,
			EnrollmentAnswers: answers, AcceptedTermsVersion: &h.s.termsVersion,
			CreatedBy: uuid.NullUUID{UUID: u.ID, Valid: true},
		})
		if err != nil {
			if isUnique(err, "tenants_slug_key") {
				return httpx.Conflict(httpx.CodeSlugTaken, "slug "+slug+" is taken")
			}
			return err
		}
		if err := q.AddTenantMember(ctx, systemq.AddTenantMemberParams{TenantID: t.ID, UserID: u.ID, Role: RoleOwner}); err != nil {
			return err
		}
		detail := map[string]any{
			"slug": t.Slug, "name": t.Name, "status": t.Status,
			"accepted_terms_version": h.s.termsVersion, "enrollment": stored,
		}
		if awardID != uuid.Nil {
			cohort, err = awardCohort(ctx, tx, awardID)
			if err != nil {
				return err
			}
			detail["scf_award_id"] = awardID.String()
		}
		if !cohort.Empty() {
			if err := ProposeCohort(ctx, tx, t.ID, u.ID, cohort); err != nil {
				return err
			}
			detail["cohort_proposal"] = cohort.AuditDetail()
		}
		if err := audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: audit.TenantCreate, Target: audit.TenantTarget(t.ID), Detail: detail,
		}); err != nil {
			return err
		}
		for _, hook := range h.onEnrol {
			if err := hook.AfterEnroll(ctx, tx, t.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	h.s.Reconcile(ctx, t.ID)
	httpx.Created(w, "/api/tenants/"+t.ID.String(), toTenant(row(t), RoleOwner))
	return nil
}

// enrollment modes of cp.settings key enrollment_mode (contract 5.7)
const (
	ModeOpen     = "open"
	ModeApproval = "approval"
)

// ParseEnrollmentMode decodes the stored enrollment_mode value. a value that
// is not a json string or not a known mode is an error, never read as one of
// the modes, so a broken setting stops enrollment instead of guessing.
func ParseEnrollmentMode(raw []byte) (string, error) {
	var mode string
	if err := json.Unmarshal(raw, &mode); err != nil {
		return "", fmt.Errorf("tenants: enrollment_mode is not a json string: %w", err)
	}
	if mode != ModeOpen && mode != ModeApproval {
		return "", fmt.Errorf("tenants: unknown enrollment_mode %q", mode)
	}
	return mode, nil
}

// enrollStatus maps the enrollment_mode setting to a new tenant's status. the
// setting is read in the enrollment transaction, so a switch applies to the
// next enrollment without a restart and never touches existing tenants.
func enrollStatus(ctx context.Context, q *systemq.Queries) (string, error) {
	raw, err := q.GetSetting(ctx, "enrollment_mode")
	if err != nil {
		return "", fmt.Errorf("tenants: read enrollment_mode: %w", err)
	}
	mode, err := ParseEnrollmentMode(raw)
	if err != nil {
		return "", err
	}
	if mode == ModeApproval {
		return "pending", nil
	}
	return "active", nil
}

func isUnique(err error, constraint string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == constraint
}

// IsUnique reports a unique violation of the named constraint, for the
// other route packages.
func IsUnique(err error, constraint string) bool { return isUnique(err, constraint) }

const listRoute = "tenants"

func (h *Routes) list(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	p, err := httpx.ParsePage(r, listRoute)
	if err != nil {
		return err
	}
	params := systemq.ListUserTenantsPageParams{UserID: u.ID, RowLimit: int32(p.Limit + 1)} //nolint:gosec // limit is at most 200
	if p.After != nil {
		params.AfterCreatedAt = &p.After.At
		params.AfterID = uuid.NullUUID{UUID: p.After.ID, Valid: true}
	}
	var rows []systemq.ListUserTenantsPageRow
	if err := h.s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		var qerr error
		rows, qerr = systemq.New(tx).ListUserTenantsPage(ctx, params)
		return qerr
	}); err != nil {
		return err
	}
	out := httpx.List[Tenant]{Items: []Tenant{}}
	for i, t := range rows {
		if i == p.Limit {
			last := rows[i-1]
			out.NextCursor = httpx.NextCursor(listRoute, httpx.Cursor{At: last.CreatedAt, ID: last.ID})
			break
		}
		out.Items = append(out.Items, toTenant(row{
			ID: t.ID, Slug: t.Slug, Name: t.Name, Description: t.Description, Status: t.Status,
			IsInternal: t.IsInternal, EnrollmentAnswers: t.EnrollmentAnswers,
			AcceptedTermsVersion: t.AcceptedTermsVersion, UmamiTeamID: t.UmamiTeamID,
			CreatedBy: t.CreatedBy, ApprovedBy: t.ApprovedBy, StatusReason: t.StatusReason,
			CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, StatusChangedAt: t.StatusChangedAt,
		}, t.Role))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *Routes) get(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	var t tenantq.CpTenant
	if err := h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		var qerr error
		t, qerr = tenantq.New(tx).GetTenant(ctx)
		return qerr
	}); err != nil {
		return notFoundOnNoRows(err)
	}
	httpx.WriteJSON(w, http.StatusOK, toTenant(t, a.Role))
	return nil
}

type patchBody struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (h *Routes) patch(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	if err := a.RequireOwner(); err != nil {
		return err
	}
	if err := a.RequireTerms(); err != nil {
		return err
	}
	var b patchBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	var f Fields
	if b.Name != nil {
		v := Text(&f, "name", *b.Name, 1, 100)
		b.Name = &v
	}
	if b.Description != nil {
		v := Text(&f, "description", *b.Description, 0, 1000)
		b.Description = &v
	}
	if err := f.Err(); err != nil {
		return err
	}
	var t tenantq.CpTenant
	err = h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		q := tenantq.New(tx)
		old, err := q.GetTenant(ctx)
		if err != nil {
			return err
		}
		detail := map[string]any{}
		if b.Name != nil && *b.Name != old.Name {
			detail["name"] = audit.Change(old.Name, *b.Name)
		}
		if b.Description != nil && *b.Description != old.Description {
			detail["description"] = audit.Change(old.Description, *b.Description)
		}
		if len(detail) == 0 {
			// nothing changes, so nothing is written and nothing audited
			t = old
			return nil
		}
		t, err = q.UpdateTenantProfile(ctx, tenantq.UpdateTenantProfileParams{Name: b.Name, Description: b.Description})
		if err != nil {
			return err
		}
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: audit.TenantUpdate, Target: audit.TenantTarget(a.TenantID), Detail: detail,
		})
	})
	if err != nil {
		return notFoundOnNoRows(err)
	}
	httpx.WriteJSON(w, http.StatusOK, toTenant(t, a.Role))
	return nil
}

type termsBody struct {
	Version *string `json:"version"`
}

func (h *Routes) acceptTerms(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	if err := a.RequireOwner(); err != nil {
		return err
	}
	var b termsBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	if b.Version == nil {
		return httpx.Validation("version is required", map[string]string{"version": httpx.FieldRequired})
	}
	if *b.Version != h.s.termsVersion {
		return httpx.Conflict(httpx.CodeTermsVersionMismatch, "version is not the current terms version")
	}
	var t tenantq.CpTenant
	err = h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		q := tenantq.New(tx)
		old, err := q.GetTenant(ctx)
		if err != nil {
			return err
		}
		t, err = q.AcceptTenantTerms(ctx, b.Version)
		if err != nil {
			return err
		}
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: audit.TenantTermsAccept, Target: audit.TenantTarget(a.TenantID),
			Detail: map[string]any{"accepted_terms_version": audit.Change(old.AcceptedTermsVersion, *b.Version)},
		})
	})
	if err != nil {
		return notFoundOnNoRows(err)
	}
	httpx.WriteJSON(w, http.StatusOK, toTenant(t, a.Role))
	return nil
}

// notFoundOnNoRows turns a row that vanished between the membership check
// and the tenant transaction (a concurrent removal) into the usual 404.
func notFoundOnNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NotFound()
	}
	return err
}

// NotFoundOnNoRows is notFoundOnNoRows for the other route packages.
func NotFoundOnNoRows(err error) error { return notFoundOnNoRows(err) }

// awardCohort reads the cohort of an award of the scf award directory
// (00050) as a proposal: 422 unknown_scf_award when no award has the id.
func awardCohort(ctx context.Context, tx pgx.Tx, id uuid.UUID) (CohortValues, error) {
	var program, round, category string
	var date *time.Time
	err := tx.QueryRow(ctx, `select program, round, category, award_date from cp.scf_awards where id = $1`, id).
		Scan(&program, &round, &category, &date)
	if errors.Is(err, pgx.ErrNoRows) {
		return CohortValues{}, httpx.Validation("no scf award has this id", map[string]string{"scf_award_id": httpx.FieldUnknownSCFAward})
	}
	if err != nil {
		return CohortValues{}, err
	}
	return CohortValues{Program: &program, Round: &round, Category: &category, AwardDate: date}, nil
}
