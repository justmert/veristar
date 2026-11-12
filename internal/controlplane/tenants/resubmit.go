package tenants

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/controlplane/audit"
	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	"github.com/justmert/stellar-analytics/internal/db/systemq"
)

// ActionResubmit is the audit action of a resubmission.
const ActionResubmit = "tenant.resubmit"

type resubmitBody struct {
	Enrollment  *enrollment `json:"enrollment"`
	Name        *string     `json:"name"`
	Description *string     `json:"description"`
}

// resubmit is PUT /api/tenants/{tenant_id}/enrollment (contract 5.7): the
// owner of a rejected tenant edits the enrollment, and the tenant returns
// to pending for an operator.
func (h *Routes) resubmit(w http.ResponseWriter, r *http.Request) error {
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
	var b resubmitBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	var f Fields
	stored := checkEnrollment(&f, b.Enrollment)
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
	answers, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	var t systemq.CpTenant
	err = h.s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		old, err := q.ResubmitLockTenant(ctx, a.TenantID)
		if err != nil {
			return err
		}
		if old.Status != "rejected" {
			return httpx.Conflict(httpx.CodeInvalidTransition, "the project is "+old.Status+", only a rejected project can be resubmitted")
		}
		t, err = q.ResubmitTenant(ctx, systemq.ResubmitTenantParams{
			EnrollmentAnswers: answers, Name: b.Name, Description: b.Description, ID: a.TenantID,
		})
		if err != nil {
			return err
		}
		detail := map[string]any{
			"tenant_id": a.TenantID,
			"status":    audit.Change(old.Status, t.Status),
		}
		if !jsonEqual(old.EnrollmentAnswers, t.EnrollmentAnswers) {
			detail["enrollment"] = audit.Change(json.RawMessage(old.EnrollmentAnswers), json.RawMessage(t.EnrollmentAnswers))
		}
		if old.Name != t.Name {
			detail["name"] = audit.Change(old.Name, t.Name)
		}
		if old.Description != t.Description {
			detail["description"] = audit.Change(old.Description, t.Description)
		}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: ActionResubmit, Target: audit.TenantTarget(a.TenantID), Detail: detail,
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NotFound()
	}
	if err != nil {
		return err
	}
	h.s.Reconcile(ctx, a.TenantID)
	httpx.WriteJSON(w, http.StatusOK, toTenant(row(t), a.Role))
	return nil
}

// jsonEqual compares two jsonb values by meaning, not by bytes.
func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}
