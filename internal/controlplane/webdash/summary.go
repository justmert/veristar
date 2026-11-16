package webdash

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/db/tenantq"
)

// Totals are the five sums of agg.web_daily over a range and the two
// figures umami's dashboard derives from them: bounce rate is
// bounces / visits and average visit duration totaltime / visits, in
// seconds; both are null without visits.
type Totals struct {
	Pageviews        int64    `json:"pageviews"`
	Visitors         int64    `json:"visitors"`
	Visits           int64    `json:"visits"`
	Bounces          int64    `json:"bounces"`
	Totaltime        int64    `json:"totaltime"`
	BounceRate       *float64 `json:"bounce_rate"`
	AvgVisitDuration *float64 `json:"avg_visit_duration"`
	// AggregatedDays counts the days of the range with at least one row
	AggregatedDays int `json:"aggregated_days"`
}

func ratio(a, b int64) *float64 {
	if b <= 0 {
		return nil
	}
	v := float64(a) / float64(b)
	return &v
}

func (t *Totals) derive() {
	t.BounceRate = ratio(t.Bounces, t.Visits)
	t.AvgVisitDuration = ratio(t.Totaltime, t.Visits)
}

// Point is one utc day. Aggregated is false while no selected site has a
// row for the day; its numbers are then null, never an invented zero.
type Point struct {
	Day              string   `json:"day"`
	Aggregated       bool     `json:"aggregated"`
	Pageviews        *int64   `json:"pageviews"`
	Visitors         *int64   `json:"visitors"`
	Visits           *int64   `json:"visits"`
	Bounces          *int64   `json:"bounces"`
	Totaltime        *int64   `json:"totaltime"`
	BounceRate       *float64 `json:"bounce_rate"`
	AvgVisitDuration *float64 `json:"avg_visit_duration"`
}

// Period is a range with its totals and its days.
type Period struct {
	Range  RangeJSON `json:"range"`
	Totals Totals    `json:"totals"`
	Series []Point   `json:"series"`
}

// OtherBand is the other band's page views and visitors over the current
// range.
type OtherBand struct {
	Band      string `json:"band"`
	Pageviews int64  `json:"pageviews"`
	Visitors  int64  `json:"visitors"`
}

// Summary is the answer of GET .../web/summary (contract 18.3).
type Summary struct {
	Band     string    `json:"band"`
	Site     *SiteRef  `json:"site"`
	Sites    []SiteRef `json:"sites"`
	Current  Period    `json:"current"`
	Previous Period    `json:"previous"`
	Other    OtherBand `json:"other_band"`
	// QuarantineShare is quarantined page views over all page views of the
	// range, the operator views' quarantine_share; null without page views
	QuarantineShare *float64         `json:"quarantine_share"`
	ReadAt          *httpx.Timestamp `json:"read_at"`
	Source          string           `json:"source"`
}

// SourceAggregate and SourceEngine name where an answer's numbers come from.
const (
	SourceAggregate = "aggregate"
	SourceEngine    = "engine"
)

// period reads the days of rg for the request's band and site.
func period(ctx context.Context, q *tenantq.Queries, req request, rg Days) (Period, *time.Time, error) {
	rows, err := q.WebDashDaily(ctx, tenantq.WebDashDailyParams{
		Band: req.band, FromDay: rg.From, ToDay: rg.To, SiteID: req.siteFilter(),
	})
	if err != nil {
		return Period{}, nil, fmt.Errorf("webdash: daily: %w", err)
	}
	days := rg.List()
	p := Period{Range: rg.JSON(), Series: make([]Point, len(days))}
	for i, d := range days {
		p.Series[i] = Point{Day: d.Format(time.DateOnly)}
	}
	var readAt *time.Time
	for _, row := range rows {
		i := int(UTCDay(row.Day).Sub(rg.From).Hours() / 24)
		if i < 0 || i >= len(days) {
			return Period{}, nil, fmt.Errorf("webdash: day %s outside the range", row.Day.Format(time.DateOnly))
		}
		pv, vs, vi, bo, tt := row.Pageviews, row.Visitors, row.Visits, row.Bounces, row.Totaltime
		p.Series[i] = Point{
			Day: p.Series[i].Day, Aggregated: true, Pageviews: &pv, Visitors: &vs, Visits: &vi,
			Bounces: &bo, Totaltime: &tt, BounceRate: ratio(bo, vi), AvgVisitDuration: ratio(tt, vi),
		}
		p.Totals.Pageviews += pv
		p.Totals.Visitors += vs
		p.Totals.Visits += vi
		p.Totals.Bounces += bo
		p.Totals.Totaltime += tt
		p.Totals.AggregatedDays++
		if readAt == nil || row.ReadAt.After(*readAt) {
			t := row.ReadAt.UTC()
			readAt = &t
		}
	}
	p.Totals.derive()
	return p, readAt, nil
}

func (h *Routes) summary(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req, err := h.parse(r, "from", "to", "site", "band")
	if err != nil {
		return err
	}
	rg, err := ParseDays(req.q, h.now())
	if err != nil {
		return err
	}
	out := Summary{Band: req.band, Sites: refs(req.sites), Source: SourceAggregate}
	if req.site != nil {
		ref := req.site.ref()
		out.Site = &ref
	}
	other := BandQuarantine
	if req.band == BandQuarantine {
		other = BandPrimary
	}
	if err := h.s.InTenant(ctx, req.access, func(tx pgx.Tx) error {
		q := tenantq.New(tx)
		var err error
		var readAt *time.Time
		if out.Current, readAt, err = period(ctx, q, req, rg); err != nil {
			return err
		}
		out.ReadAt = httpx.TimestampPtr(readAt)
		if out.Previous, _, err = period(ctx, q, req, rg.Previous()); err != nil {
			return err
		}
		ob, err := q.WebDashBandTotals(ctx, tenantq.WebDashBandTotalsParams{
			Band: other, FromDay: rg.From, ToDay: rg.To, SiteID: req.siteFilter(),
		})
		if err != nil {
			return fmt.Errorf("webdash: other band: %w", err)
		}
		quarantined, all := ob.Pageviews, ob.Pageviews+out.Current.Totals.Pageviews
		if req.band == BandQuarantine {
			quarantined = out.Current.Totals.Pageviews
		}
		out.Other = OtherBand{Band: other, Pageviews: ob.Pageviews, Visitors: ob.Visitors}
		out.QuarantineShare = ratio(quarantined, all)
		return nil
	}); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
