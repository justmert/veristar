package webdash

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/justmert/veristar/internal/controlplane/httpx"
)

// flexInt decodes a json number or a string holding one: umami returns
// postgresql counts as numbers or numeric strings depending on the query.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("umami returned %q where an integer belongs", s)
		}
		*f = flexInt(n)
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("umami returned %s where an integer belongs", b)
	}
	if v != math.Trunc(v) || v < 0 {
		return fmt.Errorf("umami returned %s where a count belongs", b)
	}
	*f = flexInt(v)
	return nil
}

// bucketTime reads umami's bucket start. umami formats it as an iso
// timestamp or as a local date time in the requested zone, which is utc here.
func bucketTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04:05.000"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("umami returned bucket %q, not a time", s)
}

// series is umami's /pageviews answer: page views and distinct sessions per
// bucket, only buckets with page views.
type series struct {
	Pageviews []struct {
		X string  `json:"x"`
		Y flexInt `json:"y"`
	} `json:"pageviews"`
	Sessions []struct {
		X string  `json:"x"`
		Y flexInt `json:"y"`
	} `json:"sessions"`
}

// BucketPoint is one bucket of an engine series: page views and visitors
// (umami's distinct sessions in the bucket).
type BucketPoint struct {
	At        httpx.Timestamp `json:"at"`
	Pageviews int64           `json:"pageviews"`
	Visitors  int64           `json:"visitors"`
}

// fill adds a /pageviews answer into pts, whose buckets start at from and
// are step apart.
func fill(pts []BucketPoint, from time.Time, step time.Duration, s series) error {
	idx := func(x string) (int, error) {
		t, err := bucketTime(x)
		if err != nil {
			return 0, err
		}
		d := t.Sub(from)
		if d < 0 || d%step != 0 || int(d/step) >= len(pts) {
			// a bucket off the grid means the unit or zone differs from
			// what was asked; never drop it silently
			return 0, fmt.Errorf("umami bucket %s is outside the grid from %s by %s", x, from.Format(time.RFC3339), step)
		}
		return int(d / step), nil
	}
	for _, p := range s.Pageviews {
		i, err := idx(p.X)
		if err != nil {
			return err
		}
		pts[i].Pageviews += int64(p.Y)
	}
	for _, p := range s.Sessions {
		i, err := idx(p.X)
		if err != nil {
			return err
		}
		pts[i].Visitors += int64(p.Y)
	}
	return nil
}

func grid(from time.Time, step time.Duration, n int) []BucketPoint {
	pts := make([]BucketPoint, n)
	for i := range pts {
		pts[i].At = httpx.Timestamp(from.Add(time.Duration(i) * step))
	}
	return pts
}

func seriesQuery(start, end time.Time, unit string) url.Values {
	q := url.Values{}
	q.Set("startAt", strconv.FormatInt(start.UnixMilli(), 10))
	q.Set("endAt", strconv.FormatInt(end.UnixMilli(), 10))
	q.Set("unit", unit)
	q.Set("timezone", "UTC")
	return q
}

// Hourly is the answer of GET .../web/hourly.
type Hourly struct {
	Day    string        `json:"day"`
	Band   string        `json:"band"`
	Source string        `json:"source"`
	Sites  []SiteRef     `json:"sites"`
	Points []BucketPoint `json:"points"`
}

func (h *Routes) hourly(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req, err := h.parse(r, "day", "site", "band")
	if err != nil {
		return err
	}
	today := UTCDay(h.now())
	day := today
	if d, ok, err := parseDay(req.q, "day"); err != nil {
		return err
	} else if ok {
		day = d
	}
	if day.After(today) {
		return httpx.BadQuery("day must not be after today (utc)")
	}
	sites := req.engineSites()
	out := Hourly{Day: day.Format(time.DateOnly), Band: req.band, Source: SourceEngine, Sites: refs(sites),
		Points: grid(day, time.Hour, 24)}
	if len(sites) > 0 {
		if err := h.engine(req); err != nil {
			return err
		}
	}
	rg := Days{From: day, To: day}.Umami()
	for _, s := range sites {
		website, ok := s.website(req.band)
		if !ok {
			continue
		}
		var raw series
		if err := h.read(ctx, req, s, website, "/pageviews", seriesQuery(rg.Start, rg.End, "hour"), &raw); err != nil {
			return err
		}
		if err := fill(out.Points, day, time.Hour, raw); err != nil {
			return fmt.Errorf("webdash: hourly of site %s: %w", s.ID, err)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// realtime windows: umami's /active counts distinct sessions of the last
// five minutes; the minute series covers the last half hour
const (
	ActiveWindow  = 5 * time.Minute
	MinutesWindow = 30
)

// SiteActive is one site's visitors right now.
type SiteActive struct {
	Site     SiteRef `json:"site"`
	Visitors int64   `json:"visitors"`
}

// Realtime is the answer of GET .../web/realtime: the primary band only,
// because quarantined traffic is what the dashboard leaves out.
type Realtime struct {
	At       httpx.Timestamp `json:"at"`
	Source   string          `json:"source"`
	Visitors int64           `json:"visitors"`
	Sites    []SiteActive    `json:"sites"`
	Minutes  []BucketPoint   `json:"minutes"`
}

func (h *Routes) realtime(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req, err := h.parse(r, "site")
	if err != nil {
		return err
	}
	now := h.now().UTC()
	// the minute of now is the last bucket; it is still filling
	last := now.Truncate(time.Minute)
	first := last.Add(-(MinutesWindow - 1) * time.Minute)
	sites := req.engineSites()
	out := Realtime{At: httpx.Timestamp(now), Source: SourceEngine, Sites: []SiteActive{}, Minutes: grid(first, time.Minute, MinutesWindow)}
	if len(sites) > 0 {
		if err := h.engine(req); err != nil {
			return err
		}
	}
	for _, s := range sites {
		var active struct {
			Visitors flexInt `json:"visitors"`
		}
		if err := h.read(ctx, req, s, s.ID.String(), "/active", nil, &active); err != nil {
			return err
		}
		out.Sites = append(out.Sites, SiteActive{Site: s.ref(), Visitors: int64(active.Visitors)})
		out.Visitors += int64(active.Visitors)
		var raw series
		if err := h.read(ctx, req, s, s.ID.String(), "/pageviews", seriesQuery(first, now, "minute"), &raw); err != nil {
			return err
		}
		if err := fill(out.Minutes, first, time.Minute, raw); err != nil {
			return fmt.Errorf("webdash: minutes of site %s: %w", s.ID, err)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
