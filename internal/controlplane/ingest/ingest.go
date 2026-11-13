// Package ingest serves a site's hourly outcome counts from
// cp.web_ingest_hourly (control-plane-api 4.3.7). the jobs task edgestats
// writes those rows from the gateways' and the replayer's counters.
package ingest

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/db/tenantq"
)

// range limits per bucket (contract 4.3.7)
var maxRange = map[string]time.Duration{
	"hour": 31 * 24 * time.Hour,
	"day":  400 * 24 * time.Hour,
}

const defaultRange = 24 * time.Hour

// Point is one bucket. Counts holds only non-zero outcomes.
type Point struct {
	At     httpx.Timestamp  `json:"at"`
	Counts map[string]int64 `json:"counts"`
}

// Response is the body of the route.
type Response struct {
	SiteID uuid.UUID        `json:"site_id"`
	From   httpx.Timestamp  `json:"from"`
	To     httpx.Timestamp  `json:"to"`
	Bucket string           `json:"bucket"`
	Series []Point          `json:"series"`
	Totals map[string]int64 `json:"totals"`
}

// Routes is the ingest route.
type Routes struct {
	s   *tenants.Scope
	now func() time.Time
}

// NewRoutes returns the route over a scope.
func NewRoutes(s *tenants.Scope) *Routes { return &Routes{s: s, now: time.Now} }

// Routes registers the path under a router mounted at /api.
func (h *Routes) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/sites/{site_id}/ingest", httpx.HandlerFunc(h.get))
}

func wholeHour(t time.Time) bool { return t.Equal(t.Truncate(time.Hour)) }

func parseHour(name, v string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil || !wholeHour(t) {
		return time.Time{}, httpx.BadQuery(name + " must be an rfc 3339 time on a whole hour")
	}
	return t.UTC(), nil
}

// parseRange reads from, to and bucket. without from and to it is the last
// 24 full hours.
func parseRange(r *http.Request, now time.Time) (from, to time.Time, bucket string, err error) {
	q := r.URL.Query()
	for _, k := range []string{"from", "to", "bucket"} {
		if len(q[k]) > 1 {
			return from, to, "", httpx.BadQuery(k + " is given more than once")
		}
	}
	bucket = "hour"
	if v, ok := q["bucket"]; ok {
		bucket = v[0]
		if _, known := maxRange[bucket]; !known {
			return from, to, "", httpx.BadQuery("bucket must be hour or day")
		}
	}
	to = now.UTC().Truncate(time.Hour)
	if v, ok := q["to"]; ok {
		if to, err = parseHour("to", v[0]); err != nil {
			return from, to, "", err
		}
	}
	from = to.Add(-defaultRange)
	if v, ok := q["from"]; ok {
		if from, err = parseHour("from", v[0]); err != nil {
			return from, to, "", err
		}
	}
	if !from.Before(to) {
		return from, to, "", httpx.BadQuery("from must be before to")
	}
	if to.Sub(from) > maxRange[bucket] {
		return from, to, "", httpx.BadQuery("range is too long for bucket " + bucket)
	}
	return from, to, bucket, nil
}

func (h *Routes) get(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, siteID, err := h.s.Site(r, "site_id")
	if err != nil {
		return err
	}
	from, to, bucket, err := parseRange(r, h.now())
	if err != nil {
		return err
	}
	var rows []tenantq.IngestCountsRow
	if err := h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		var qerr error
		rows, qerr = tenantq.New(tx).IngestCounts(ctx, tenantq.IngestCountsParams{
			Bucket: bucket, SiteID: siteID, FromHour: from, ToHour: to,
		})
		return qerr
	}); err != nil {
		return err
	}
	out := Response{SiteID: siteID, From: httpx.Timestamp(from), To: httpx.Timestamp(to), Bucket: bucket,
		Series: []Point{}, Totals: map[string]int64{}}
	for _, row := range rows {
		if n := len(out.Series); n == 0 || !time.Time(out.Series[n-1].At).Equal(row.At) {
			out.Series = append(out.Series, Point{At: httpx.Timestamp(row.At), Counts: map[string]int64{}})
		}
		out.Series[len(out.Series)-1].Counts[row.Outcome] = row.Total
		out.Totals[row.Outcome] += row.Total
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
