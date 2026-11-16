package operator

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/onchain"
	"github.com/justmert/veristar/internal/db"
)

// the on-chain parts of the operator views (contract 15.7): the column
// of the global lists, the compare series and the tenant detail section.
// every read runs as cp_operator over cp.chain_ids, agg.chain_daily,
// agg.chain_reads and cp.operator_sites.

func (d dayRange) onchain() onchain.Range { return onchain.Range{From: d.From, To: d.To} }

// CompareOnchain is a compared project's on-chain activity: the summary
// over the range and one point per day.
type CompareOnchain struct {
	Totals onchain.Summary    `json:"totals"`
	Series []onchain.DayTotal `json:"series"`
}

func compareOnchain(ctx context.Context, tx pgx.Tx, ids []uuid.UUID, rg dayRange, now time.Time) (map[uuid.UUID]CompareOnchain, error) {
	sums, err := onchain.Summaries(ctx, tx, ids, rg.onchain(), utcDay(now))
	if err != nil {
		return nil, err
	}
	series, err := onchain.DailyTotals(ctx, tx, ids, rg.onchain())
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]CompareOnchain, len(ids))
	for _, id := range ids {
		out[id] = CompareOnchain{Totals: sums[id], Series: series[id]}
	}
	return out, nil
}

// Onchain serves GET /api/operator/tenants/{tenant_id}/onchain, the
// on-chain section of the tenant detail.
type Onchain struct {
	db  *db.DB
	now func() time.Time
}

// NewOnchain needs a database with the cp_operator pool.
func NewOnchain(d *db.DB, now func() time.Time) (*Onchain, error) {
	if d == nil || now == nil {
		return nil, errors.New("operator: database and clock are required")
	}
	return &Onchain{db: d, now: now}, nil
}

// Routes registers the path under the guarded /api/operator router.
func (o *Onchain) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/tenants/{tenant_id}/onchain", httpx.HandlerFunc(o.detail))
}

func (o *Onchain) detail(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.ParseID(chi.URLParam(r, "tenant_id"))
	if err != nil {
		return err
	}
	now := o.now()
	rg, err := onchain.ParseRange(r.URL.Query(), now, onchain.DefaultRangeDays, onchain.MaxRangeDays)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var v onchain.View
	err = o.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		// an unknown or internal tenant is 404, as on every operator route
		if _, err := compareTenants(ctx, tx, []uuid.UUID{id}); err != nil {
			return err
		}
		var lerr error
		v, lerr = onchain.Load(ctx, tx, onchain.OperatorSites, id, rg, utcDay(now))
		return lerr
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, v)
	return nil
}
