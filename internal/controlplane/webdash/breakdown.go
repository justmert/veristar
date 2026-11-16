package webdash

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/db/tenantq"
	"github.com/justmert/veristar/internal/umami"
)

// a dimension of contract 18.4: where it is read and what its values count
type dimension struct {
	// aggregate is the agg.web_daily_dim dimension; empty for an engine read
	aggregate string
	// metric is umami's /metrics type of an engine read
	metric string
	// unit is what a value counts: distinct visitors (umami's
	// count(distinct session_id)) or custom events (count(*))
	unit string
	// perSite keeps values of different sites apart: the same path on two
	// sites is two pages
	perSite bool
	// utm dimensions read only sites that collect utm values
	utm bool
}

const (
	unitVisitors = "visitors"
	unitEvents   = "events"
)

// Dimensions are the breakdowns of the dashboard.
var Dimensions = map[string]dimension{
	"referrer":     {aggregate: "referrer", unit: unitVisitors},
	"country":      {aggregate: "country", unit: unitVisitors},
	"browser":      {aggregate: "browser", unit: unitVisitors},
	"os":           {aggregate: "os", unit: unitVisitors},
	"device":       {aggregate: "device", unit: unitVisitors},
	"page":         {metric: "path", unit: unitVisitors, perSite: true},
	"entry_page":   {metric: "entry", unit: unitVisitors, perSite: true},
	"exit_page":    {metric: "exit", unit: unitVisitors, perSite: true},
	"event":        {metric: "event", unit: unitEvents},
	"utm_source":   {metric: "utmSource", unit: unitVisitors, utm: true},
	"utm_medium":   {metric: "utmMedium", unit: unitVisitors, utm: true},
	"utm_campaign": {metric: "utmCampaign", unit: unitVisitors, utm: true},
	"utm_content":  {metric: "utmContent", unit: unitVisitors, utm: true},
	"utm_term":     {metric: "utmTerm", unit: unitVisitors, utm: true},
}

// DimensionNames lists the dimensions in the order the contract gives them.
var DimensionNames = []string{
	"referrer", "country", "browser", "os", "device", "page", "entry_page", "exit_page", "event",
	"utm_source", "utm_medium", "utm_campaign", "utm_content", "utm_term",
}

// breakdown limits
const (
	DefaultBreakdownLimit = 10
	MaxBreakdownLimit     = 100
	// umami's /metrics orders only by its count, so equal counts have no
	// stable order across offset pages. the whole ranking is read in one
	// page instead, doubling the limit while a page comes back full, as the
	// aggregate-web task does
	firstMetricsLimit = 1000
	maxMetricsLimit   = 64000
)

// Item is one value of a breakdown. Site is set for page dimensions, whose
// values belong to one site each.
type Item struct {
	Key   string     `json:"key"`
	Value int64      `json:"value"`
	Site  *uuid.UUID `json:"site_id,omitempty"`
}

// Breakdown is the answer of GET .../web/breakdown.
type Breakdown struct {
	Dimension string    `json:"dimension"`
	Band      string    `json:"band"`
	Range     RangeJSON `json:"range"`
	Source    string    `json:"source"`
	Unit      string    `json:"unit"`
	Items     []Item    `json:"items"`
	TotalKeys int64     `json:"total_keys"`
	// Sites are the sites an engine read covered; null for an aggregate read,
	// which covers every row of the selection
	Sites []SiteRef `json:"sites"`
}

func parseLimit(q url.Values) (int, error) {
	v, ok := q["limit"]
	if !ok {
		return DefaultBreakdownLimit, nil
	}
	n, err := strconv.Atoi(v[0])
	if err != nil || n < 1 || n > MaxBreakdownLimit {
		return 0, httpx.BadQuery(fmt.Sprintf("limit must be an integer between 1 and %d", MaxBreakdownLimit))
	}
	return n, nil
}

func (h *Routes) breakdown(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req, err := h.parse(r, "dimension", "from", "to", "site", "band", "limit")
	if err != nil {
		return err
	}
	name := req.q.Get("dimension")
	dim, ok := Dimensions[name]
	if !ok {
		return httpx.BadQuery("dimension must be one of " + strings.Join(DimensionNames, ", "))
	}
	rg, err := ParseDays(req.q, h.now())
	if err != nil {
		return err
	}
	limit, err := parseLimit(req.q)
	if err != nil {
		return err
	}
	out := Breakdown{Dimension: name, Band: req.band, Range: rg.JSON(), Unit: dim.unit, Items: []Item{}}
	if dim.aggregate != "" {
		out.Source = SourceAggregate
		err = h.s.InTenant(ctx, req.access, func(tx pgx.Tx) error {
			rows, err := tenantq.New(tx).WebDashDimension(ctx, tenantq.WebDashDimensionParams{
				Band: req.band, Dimension: dim.aggregate, FromDay: rg.From, ToDay: rg.To,
				SiteID: req.siteFilter(), RowLimit: int32(limit), //nolint:gosec // at most MaxBreakdownLimit
			})
			if err != nil {
				return fmt.Errorf("webdash: dimension: %w", err)
			}
			for _, row := range rows {
				out.Items = append(out.Items, Item{Key: row.Key, Value: row.Value})
				out.TotalKeys = row.TotalKeys
			}
			return nil
		})
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, out)
		return nil
	}

	out.Source = SourceEngine
	sites := req.engineSites()
	if dim.utm {
		if req.site != nil && !req.site.CollectsUTM {
			return httpx.BadQuery("this site does not collect utm values; turn on utm collection in its settings first")
		}
		kept := sites[:0:0]
		for _, s := range sites {
			if s.CollectsUTM {
				kept = append(kept, s)
			}
		}
		sites = kept
	}
	out.Sites = refs(sites)
	if len(sites) == 0 {
		httpx.WriteJSON(w, http.StatusOK, out)
		return nil
	}
	if err := h.engine(req); err != nil {
		return err
	}
	merged := map[mergeKey]int64{}
	for _, s := range sites {
		website, ok := s.website(req.band)
		if !ok {
			// the site has no quarantine website yet, so nothing was
			// quarantined for it
			continue
		}
		rows, err := h.metrics(ctx, req, s, website, dim.metric, rg)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.X == "" {
				continue
			}
			k := mergeKey{key: row.X}
			if dim.perSite {
				k.site = s.ID
			}
			merged[k] += row.Y
		}
	}
	out.Items, out.TotalKeys = top(merged, limit)
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

type mergeKey struct {
	site uuid.UUID
	key  string
}

// top orders the merged values largest first, ties by key and then site,
// and keeps limit of them.
func top(m map[mergeKey]int64, limit int) ([]Item, int64) {
	keys := make([]mergeKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if m[a] != m[b] {
			return m[a] > m[b]
		}
		if a.key != b.key {
			return a.key < b.key
		}
		return a.site.String() < b.site.String()
	})
	items := []Item{}
	for i, k := range keys {
		if i == limit {
			break
		}
		it := Item{Key: k.key, Value: m[k]}
		if k.site != uuid.Nil {
			id := k.site
			it.Site = &id
		}
		items = append(items, it)
	}
	return items, int64(len(keys))
}

// metrics reads the whole ranking of one metric type for a website over
// the range.
func (h *Routes) metrics(ctx context.Context, req request, s site, website, metric string, rg Days) ([]umami.MetricRow, error) {
	r := rg.Umami()
	for limit := firstMetricsLimit; ; limit *= 2 {
		q := url.Values{}
		q.Set("startAt", strconv.FormatInt(r.Start.UnixMilli(), 10))
		q.Set("endAt", strconv.FormatInt(r.End.UnixMilli(), 10))
		q.Set("type", metric)
		q.Set("limit", strconv.Itoa(limit))
		var raw []struct {
			X *string `json:"x"`
			Y flexInt `json:"y"`
		}
		if err := h.read(ctx, req, s, website, "/metrics", q, &raw); err != nil {
			return nil, err
		}
		if len(raw) < limit {
			out := make([]umami.MetricRow, 0, len(raw))
			for _, row := range raw {
				x := ""
				if row.X != nil {
					x = *row.X
				}
				out = append(out, umami.MetricRow{X: x, Y: int64(row.Y)})
			}
			return out, nil
		}
		if limit*2 > maxMetricsLimit {
			return nil, httpx.BadQuery(fmt.Sprintf("more than %d distinct values in this range; choose a shorter range", limit))
		}
	}
}
