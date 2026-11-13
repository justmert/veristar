package operator

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/operatorq"
	"github.com/justmert/veristar/internal/db/systemq"
)

// audit action and target of a removal setting change (contract 3.3 and 5.13)
const (
	ActionRemovalKeepAggregates = "settings.removal_keep_aggregates"
	TargetRemovalKeepAggregates = "setting:removal_keep_aggregates"
)

// RemovalKeepAggregates serves GET and PUT
// /api/operator/settings/removal-keep-aggregates (contract 5.13). a removal
// copies the value when it starts, so a change never alters one in progress.
type RemovalKeepAggregates struct {
	db *db.DB
}

// NewRemovalKeepAggregates needs the cp_operator pool (read) and the
// cp_system pool (change and audit row).
func NewRemovalKeepAggregates(d *db.DB) (*RemovalKeepAggregates, error) {
	if d == nil {
		return nil, errors.New("operator: removal setting needs a database")
	}
	return &RemovalKeepAggregates{db: d}, nil
}

// KeepBody is the request and response body of the route.
type KeepBody struct {
	Keep bool `json:"keep"`
}

// Routes registers the paths under the guarded /api/operator router.
func (h *RemovalKeepAggregates) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/settings/removal-keep-aggregates", httpx.HandlerFunc(h.get))
	r.Method(http.MethodPut, "/settings/removal-keep-aggregates", httpx.HandlerFunc(h.put))
}

// parseKeep reads the stored setting, which must be a json boolean. a
// broken value is a 500 with the cause in the log, never read as false.
func parseKeep(raw []byte) (bool, error) {
	var v *bool
	if err := json.Unmarshal(raw, &v); err != nil || v == nil {
		return false, fmt.Errorf("operator: setting removal_keep_aggregates is %s, not a boolean", raw)
	}
	return *v, nil
}

func (h *RemovalKeepAggregates) get(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var raw []byte
	if err := h.db.WithOperator(ctx, func(tx pgx.Tx) error {
		var qerr error
		raw, qerr = operatorq.New(tx).OperatorRemovalKeepAggregates(ctx)
		return qerr
	}); err != nil {
		return fmt.Errorf("operator: read removal_keep_aggregates: %w", err)
	}
	keep, err := parseKeep(raw)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, KeepBody{Keep: keep})
	return nil
}

type keepPut struct {
	Keep *bool `json:"keep"`
}

func (h *RemovalKeepAggregates) put(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var b keepPut
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	if b.Keep == nil {
		return httpx.Validation("keep is required", map[string]string{"keep": httpx.FieldRequired})
	}
	want := *b.Keep
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		raw, err := q.RemovalKeepAggregatesForUpdate(ctx)
		if err != nil {
			return fmt.Errorf("operator: lock removal_keep_aggregates: %w", err)
		}
		var old any = json.RawMessage(raw)
		if cur, perr := parseKeep(raw); perr == nil {
			if cur == want {
				// the same value changes nothing, so no audit row, as for
				// the enrollment mode (contract 5.7)
				return nil
			}
			old = cur
		}
		if err := q.SetRemovalKeepAggregates(ctx, want); err != nil {
			return fmt.Errorf("operator: set removal_keep_aggregates: %w", err)
		}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: ActionRemovalKeepAggregates, Target: TargetRemovalKeepAggregates,
			Detail: map[string]any{"keep": audit.Change(old, want)},
		})
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, KeepBody{Keep: want})
	return nil
}
