package operator

import (
	"context"
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

// the cohort routes (contract 19.1). operators read and set a project's
// cohort, confirm or dismiss its proposal, and list the cohort values in
// use; a member reads its own project's cohort and proposes one. every
// change writes an audit row in its own transaction.

// audit actions of the cohort routes (contract 3.3)
const (
	ActionCohortSet             = "cohort.set"
	ActionCohortConfirm         = "cohort.confirm"
	ActionCohortProposalDismiss = "cohort.proposal_dismiss"
	ActionCohortPropose         = "cohort.propose"
)

// CohortRecord is a project's cohort row: the confirmed values and the
// pending proposal.
type CohortRecord struct {
	TenantID    uuid.UUID        `json:"tenant_id"`
	Confirmed   *Cohort          `json:"confirmed"`
	ConfirmedAt *httpx.Timestamp `json:"confirmed_at"`
	Proposal    *Cohort          `json:"proposal"`
	ProposedAt  *httpx.Timestamp `json:"proposed_at"`
	// ProposalSource says who made the pending proposal: member (the
	// project) or scf_import (the award directory, section 19.4); null
	// without a proposal
	ProposalSource *string `json:"proposal_source"`
}

func tsPtr(t *time.Time) *httpx.Timestamp {
	if t == nil {
		return nil
	}
	v := httpx.Timestamp(*t)
	return &v
}

// readCohort reads one project's row under whatever role tx has; a
// project without a row has an empty record.
func readCohort(ctx context.Context, tx pgx.Tx, id uuid.UUID) (CohortRecord, error) {
	out := CohortRecord{TenantID: id}
	var c, p Cohort
	var award, pAward *time.Time
	var confirmedAt, proposedAt *time.Time
	var source *string
	err := tx.QueryRow(ctx, `select program, round, category, award_date, confirmed_at,
		       proposed_program, proposed_round, proposed_category, proposed_award_date, proposed_at, proposed_source
		from cp.tenant_cohorts where tenant_id = $1`, id).Scan(&c.Program, &c.Round, &c.Category, &award, &confirmedAt,
		&p.Program, &p.Round, &p.Category, &pAward, &proposedAt, &source)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("cohort: %w", err)
	}
	if confirmedAt != nil {
		c.AwardDate = dateString(award)
		out.Confirmed, out.ConfirmedAt = &c, tsPtr(confirmedAt)
	}
	if proposedAt != nil {
		p.AwardDate = dateString(pAward)
		out.Proposal, out.ProposedAt, out.ProposalSource = &p, tsPtr(proposedAt), source
	}
	return out, nil
}

// loadCohorts reads the confirmed cohort of every named project; a
// project without one has an empty Cohort.
func loadCohorts(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (map[uuid.UUID]Cohort, error) {
	out := make(map[uuid.UUID]Cohort, len(ids))
	for _, id := range ids {
		out[id] = Cohort{}
	}
	rows, err := tx.Query(ctx, `select tenant_id, program, round, category, award_date from cp.tenant_cohorts
		where tenant_id = any($1::uuid[]) and confirmed_at is not null`, ids)
	if err != nil {
		return nil, fmt.Errorf("cohorts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var c Cohort
		var award *time.Time
		if err := rows.Scan(&id, &c.Program, &c.Round, &c.Category, &award); err != nil {
			return nil, fmt.Errorf("cohorts: %w", err)
		}
		c.AwardDate = dateString(award)
		out[id] = c
	}
	return out, rows.Err()
}

// Cohorts serves the operator cohort routes.
type Cohorts struct {
	db  *db.DB
	now func() time.Time
}

// NewCohorts needs a database with the cp_operator and cp_system pools.
func NewCohorts(d *db.DB, now func() time.Time) (*Cohorts, error) {
	if d == nil || now == nil {
		return nil, errors.New("operator: cohorts need a database and a clock")
	}
	return &Cohorts{db: d, now: now}, nil
}

// Routes registers the paths under the guarded /api/operator router.
func (h *Cohorts) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/cohorts", httpx.HandlerFunc(h.list))
	r.Method(http.MethodGet, "/tenants/{tenant_id}/cohort", httpx.HandlerFunc(h.get))
	r.Method(http.MethodPut, "/tenants/{tenant_id}/cohort", httpx.HandlerFunc(h.put))
	r.Method(http.MethodPost, "/tenants/{tenant_id}/cohort/confirm", httpx.HandlerFunc(h.confirm))
	r.Method(http.MethodDelete, "/tenants/{tenant_id}/cohort/proposal", httpx.HandlerFunc(h.dismiss))
}

// CohortValue is one value in use and how many projects are confirmed with it.
type CohortValue struct {
	Value    string `json:"value"`
	Projects int64  `json:"projects"`
}

// CohortProposal is a pending proposal of a listed project.
type CohortProposal struct {
	Tenant     TenantRef       `json:"tenant"`
	Confirmed  *Cohort         `json:"confirmed"`
	Proposal   Cohort          `json:"proposal"`
	ProposedAt httpx.Timestamp `json:"proposed_at"`
	// Source is member or scf_import
	Source string `json:"source"`
}

// CohortList is the answer of GET /api/operator/cohorts.
type CohortList struct {
	Programs   []CohortValue    `json:"programs"`
	Rounds     []CohortValue    `json:"rounds"`
	Categories []CohortValue    `json:"categories"`
	Proposals  []CohortProposal `json:"proposals"`
}

func (h *Cohorts) list(w http.ResponseWriter, r *http.Request) error {
	if len(r.URL.Query()) > 0 {
		return httpx.BadQuery("this route takes no query parameters")
	}
	ctx := r.Context()
	out := CohortList{Programs: []CohortValue{}, Rounds: []CohortValue{}, Categories: []CohortValue{}, Proposals: []CohortProposal{}}
	err := h.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		for _, c := range []struct {
			col string
			dst *[]CohortValue
		}{{"program", &out.Programs}, {"round", &out.Rounds}, {"category", &out.Categories}} {
			// the column name comes from the fixed list above
			rows, err := tx.Query(ctx, `select co.`+c.col+`, count(*)::bigint
				from cp.tenant_cohorts co join cp.tenants t on t.id = co.tenant_id
				where not t.is_internal and t.status <> 'removed' and co.`+c.col+` is not null
				group by co.`+c.col+` order by co.`+c.col)
			if err != nil {
				return err
			}
			vals, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (CohortValue, error) {
				var v CohortValue
				return v, row.Scan(&v.Value, &v.Projects)
			})
			if err != nil {
				return err
			}
			*c.dst = append(*c.dst, vals...)
		}
		rows, err := tx.Query(ctx, `select t.id, t.slug, t.name, t.status, t.created_at,
			       co.confirmed_at is not null, co.program, co.round, co.category, co.award_date,
			       co.proposed_program, co.proposed_round, co.proposed_category, co.proposed_award_date, co.proposed_at,
			       co.proposed_source
			from cp.tenant_cohorts co join cp.tenants t on t.id = co.tenant_id
			where not t.is_internal and t.status <> 'removed' and co.proposed_at is not null
			order by co.proposed_at, t.id`)
		if err != nil {
			return err
		}
		props, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (CohortProposal, error) {
			var p CohortProposal
			var at, proposedAt time.Time
			var confirmed bool
			var c Cohort
			var award, pAward *time.Time
			err := row.Scan(&p.Tenant.ID, &p.Tenant.Slug, &p.Tenant.Name, &p.Tenant.Status, &at, &confirmed,
				&c.Program, &c.Round, &c.Category, &award,
				&p.Proposal.Program, &p.Proposal.Round, &p.Proposal.Category, &pAward, &proposedAt, &p.Source)
			p.Tenant.CreatedAt = httpx.Timestamp(at)
			p.ProposedAt = httpx.Timestamp(proposedAt)
			p.Proposal.AwardDate = dateString(pAward)
			if confirmed {
				c.AwardDate = dateString(award)
				p.Confirmed = &c
			}
			return p, err
		})
		if err != nil {
			return err
		}
		out.Proposals = append(out.Proposals, props...)
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: cohorts: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// operatorTenant reads the path's tenant id and answers 404 for an unknown
// or internal project, as every operator route does.
func operatorTenant(ctx context.Context, tx pgx.Tx, r *http.Request) (uuid.UUID, error) {
	id, err := httpx.ParseID(chi.URLParam(r, "tenant_id"))
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := compareTenants(ctx, tx, []uuid.UUID{id}); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func (h *Cohorts) get(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var out CohortRecord
	err := h.db.WithOperator(ctx, func(tx pgx.Tx) error {
		id, err := operatorTenant(ctx, tx, r)
		if err != nil {
			return err
		}
		out, err = readCohort(ctx, tx, id)
		return err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// change runs fn under cp_system on the path's tenant row, locked, and
// answers the record after it.
func (h *Cohorts) change(w http.ResponseWriter, r *http.Request, fn func(tx pgx.Tx, id uuid.UUID, before CohortRecord, actor string) error) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var out CohortRecord
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		id, err := operatorTenant(ctx, tx, r)
		if err != nil {
			return err
		}
		// one change of a project's cohort at a time, so the audit's old
		// values are the ones the change replaced
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('cp.cohort:' || $1::text, 0))`, id); err != nil {
			return err
		}
		// the project writes its proposal without that lock, so the row is
		// locked too: a proposal changed after the read below would
		// otherwise be the one a confirm copies, while the audit row names
		// the one read
		if _, err := tx.Exec(ctx, `select 1 from cp.tenant_cohorts where tenant_id = $1 for update`, id); err != nil {
			return err
		}
		before, err := readCohort(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := fn(tx, id, before, audit.User(u.ID)); err != nil {
			return err
		}
		out, err = readCohort(ctx, tx, id)
		return err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func cohortDetail(c *Cohort) any {
	if c == nil {
		return nil
	}
	return map[string]any{"program": c.Program, "round": c.Round, "category": c.Category, "award_date": c.AwardDate}
}

func (h *Cohorts) put(w http.ResponseWriter, r *http.Request) error {
	var b tenants.CohortInput
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	var f tenants.Fields
	v := tenants.CheckCohort(&f, "cohort", &b, utcDay(h.now()), true)
	if err := f.Err(); err != nil {
		return err
	}
	return h.change(w, r, func(tx pgx.Tx, id uuid.UUID, before CohortRecord, actor string) error {
		var err error
		if v.Empty() {
			_, err = tx.Exec(r.Context(), `update cp.tenant_cohorts set program = null, round = null, category = null,
				award_date = null, confirmed_by = null, confirmed_at = null, updated_at = now() where tenant_id = $1`, id)
		} else {
			_, err = tx.Exec(r.Context(), `
				insert into cp.tenant_cohorts (tenant_id, program, round, category, award_date, confirmed_by, confirmed_at)
				values ($1, $2, $3, $4, $5, $6, now())
				on conflict (tenant_id) do update set program = excluded.program, round = excluded.round,
				  category = excluded.category, award_date = excluded.award_date,
				  confirmed_by = excluded.confirmed_by, confirmed_at = excluded.confirmed_at, updated_at = now()`,
				id, v.Program, v.Round, v.Category, v.AwardDate, userOf(actor))
		}
		if err != nil {
			return fmt.Errorf("operator: set cohort: %w", err)
		}
		return audit.InSystem(r.Context(), tx, audit.Entry{
			Actor: actor, Action: ActionCohortSet, Target: audit.TenantTarget(id),
			Detail: map[string]any{"tenant_id": id.String(), "cohort": audit.Change(cohortDetail(before.Confirmed), v.AuditDetail())},
		})
	})
}

func (h *Cohorts) confirm(w http.ResponseWriter, r *http.Request) error {
	if err := noBody(r); err != nil {
		return err
	}
	return h.change(w, r, func(tx pgx.Tx, id uuid.UUID, before CohortRecord, actor string) error {
		if before.Proposal == nil {
			return httpx.Conflict(CodeNoProposal, "this project has no cohort proposal to confirm")
		}
		if _, err := tx.Exec(r.Context(), `update cp.tenant_cohorts set
			program = proposed_program, round = proposed_round, category = proposed_category,
			award_date = proposed_award_date, confirmed_by = $2, confirmed_at = now(),
			proposed_program = null, proposed_round = null, proposed_category = null, proposed_award_date = null,
			proposed_by = null, proposed_at = null, proposed_source = null, updated_at = now()
			where tenant_id = $1`, id, userOf(actor)); err != nil {
			return fmt.Errorf("operator: confirm cohort: %w", err)
		}
		return audit.InSystem(r.Context(), tx, audit.Entry{
			Actor: actor, Action: ActionCohortConfirm, Target: audit.TenantTarget(id),
			Detail: map[string]any{"tenant_id": id.String(), "cohort": audit.Change(cohortDetail(before.Confirmed), cohortDetail(before.Proposal)),
				"source": before.ProposalSource},
		})
	})
}

func (h *Cohorts) dismiss(w http.ResponseWriter, r *http.Request) error {
	return h.change(w, r, func(tx pgx.Tx, id uuid.UUID, before CohortRecord, actor string) error {
		if before.Proposal == nil {
			return httpx.Conflict(CodeNoProposal, "this project has no cohort proposal to dismiss")
		}
		if _, err := tx.Exec(r.Context(), `update cp.tenant_cohorts set
			proposed_program = null, proposed_round = null, proposed_category = null, proposed_award_date = null,
			proposed_by = null, proposed_at = null, proposed_source = null, updated_at = now()
			where tenant_id = $1`, id); err != nil {
			return fmt.Errorf("operator: dismiss cohort proposal: %w", err)
		}
		return audit.InSystem(r.Context(), tx, audit.Entry{
			Actor: actor, Action: ActionCohortProposalDismiss, Target: audit.TenantTarget(id),
			Detail: map[string]any{"tenant_id": id.String(), "proposal": cohortDetail(before.Proposal), "source": before.ProposalSource},
		})
	})
}

// CodeNoProposal is the 409 of a confirm or dismiss without a proposal.
const CodeNoProposal = "no_proposal"

// userOf turns an audit actor "user:<uuid>" back into the user id.
func userOf(actor string) uuid.UUID {
	return uuid.MustParse(actor[len("user:"):])
}

// noBody refuses a request body on a route that takes none.
func noBody(r *http.Request) error {
	if r.ContentLength > 0 {
		return httpx.BadBody("this route takes no body")
	}
	return nil
}

// TenantCohort serves the member's cohort routes: GET
// /api/tenants/{tenant_id}/cohort and PUT .../cohort/proposal.
type TenantCohort struct {
	scope *tenants.Scope
	now   func() time.Time
}

// NewTenantCohort needs the tenant scope and a clock.
func NewTenantCohort(s *tenants.Scope, now func() time.Time) (*TenantCohort, error) {
	if s == nil || now == nil {
		return nil, errors.New("operator: the tenant cohort routes need a scope and a clock")
	}
	return &TenantCohort{scope: s, now: now}, nil
}

// Routes registers the paths under a router mounted at /api.
func (h *TenantCohort) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/tenants/{tenant_id}/cohort", httpx.HandlerFunc(h.get))
	r.Method(http.MethodPut, "/tenants/{tenant_id}/cohort/proposal", httpx.HandlerFunc(h.propose))
}

func (h *TenantCohort) get(w http.ResponseWriter, r *http.Request) error {
	a, err := h.scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out CohortRecord
	err = h.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		var err error
		out, err = readCohort(ctx, tx, a.TenantID)
		return err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *TenantCohort) propose(w http.ResponseWriter, r *http.Request) error {
	a, err := h.scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	if err := a.RequireOwner(); err != nil {
		return err
	}
	// a mutating tenant route: refused until the current terms are
	// accepted (contract 4.2.6)
	if err := a.RequireTerms(); err != nil {
		return err
	}
	var b tenants.CohortInput
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	var f tenants.Fields
	v := tenants.CheckCohort(&f, "cohort", &b, utcDay(h.now()), false)
	if err := f.Err(); err != nil {
		return err
	}
	c := r.Context()
	var out CohortRecord
	err = h.scope.InTenant(c, a, func(tx pgx.Tx) error {
		// the row lock an operator's cohort change takes too, so the audit's
		// old proposal is the one this write replaces
		if _, err := tx.Exec(c, `select 1 from cp.tenant_cohorts where tenant_id = $1 for update`, a.TenantID); err != nil {
			return err
		}
		before, err := readCohort(c, tx, a.TenantID)
		if err != nil {
			return err
		}
		if err := tenants.ProposeCohort(c, tx, a.TenantID, a.User.ID, v); err != nil {
			return fmt.Errorf("operator: propose cohort: %w", err)
		}
		if err := audit.InTenant(c, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: ActionCohortPropose, Target: audit.TenantTarget(a.TenantID),
			Detail: map[string]any{"tenant_id": a.TenantID.String(), "proposal": audit.Change(cohortDetail(before.Proposal), v.AuditDetail())},
		}); err != nil {
			return err
		}
		out, err = readCohort(c, tx, a.TenantID)
		return err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
