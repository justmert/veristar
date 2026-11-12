package operator

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/controlplane/audit"
	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	"github.com/justmert/stellar-analytics/internal/controlplane/session"
	"github.com/justmert/stellar-analytics/internal/controlplane/tenants"
	"github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/operatorq"
	"github.com/justmert/stellar-analytics/internal/db/systemq"
)

// audit action and target of a mode switch (contract 3.3 and 5.7)
const (
	ActionEnrollmentMode = "settings.enrollment_mode"
	TargetEnrollmentMode = "setting:enrollment_mode"
)

// EnrollmentMode serves GET and PUT /api/operator/settings/enrollment-mode
// (contract 5.7). the mode is only a row in cp.settings that enrollment
// reads in its own transaction, so a switch needs no migration or restart
// and changes nothing for tenants that already exist.
type EnrollmentMode struct {
	db *db.DB
}

// NewEnrollmentMode needs a database with the cp_operator pool (reads) and
// the cp_system pool (the switch and its audit row).
func NewEnrollmentMode(d *db.DB) (*EnrollmentMode, error) {
	if d == nil {
		return nil, errors.New("operator: enrollment mode needs a database")
	}
	return &EnrollmentMode{db: d}, nil
}

// ModeBody is the request and response body of the route.
type ModeBody struct {
	Mode string `json:"mode"`
}

// Routes registers the paths under the guarded /api/operator router.
func (h *EnrollmentMode) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/settings/enrollment-mode", httpx.HandlerFunc(h.get))
	r.Method(http.MethodPut, "/settings/enrollment-mode", httpx.HandlerFunc(h.put))
}

func (h *EnrollmentMode) get(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var raw []byte
	if err := h.db.WithOperator(ctx, func(tx pgx.Tx) error {
		var qerr error
		raw, qerr = operatorq.New(tx).OperatorEnrollmentMode(ctx)
		return qerr
	}); err != nil {
		return fmt.Errorf("operator: read enrollment_mode: %w", err)
	}
	// a broken stored value is a 500 with the cause in the log, never shown
	// as one of the two modes
	mode, err := tenants.ParseEnrollmentMode(raw)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, ModeBody{Mode: mode})
	return nil
}

type putBody struct {
	Mode *string `json:"mode"`
}

func (h *EnrollmentMode) put(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var b putBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	switch {
	case b.Mode == nil:
		return httpx.Validation("mode is required", map[string]string{"mode": httpx.FieldRequired})
	case *b.Mode != tenants.ModeOpen && *b.Mode != tenants.ModeApproval:
		return httpx.Validation("mode must be open or approval", map[string]string{"mode": httpx.FieldInvalidValue})
	}
	want := *b.Mode
	next, err := json.Marshal(want)
	if err != nil {
		return err
	}
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		raw, err := q.LockEnrollmentMode(ctx)
		if err != nil {
			return fmt.Errorf("operator: lock enrollment_mode: %w", err)
		}
		// the old value goes into the audit row as stored, so a broken value
		// that an operator overwrites is still on record
		var old any = json.RawMessage(raw)
		if cur, perr := tenants.ParseEnrollmentMode(raw); perr == nil {
			if cur == want {
				// setting the current value changes nothing and writes no
				// audit row (contract 5.7)
				return nil
			}
			old = cur
		}
		if err := q.SetEnrollmentMode(ctx, next); err != nil {
			return fmt.Errorf("operator: set enrollment_mode: %w", err)
		}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: ActionEnrollmentMode, Target: TargetEnrollmentMode,
			Detail: map[string]any{"mode": audit.Change(old, want)},
		})
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, ModeBody{Mode: want})
	return nil
}
