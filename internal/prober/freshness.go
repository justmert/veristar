package prober

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Freshness decides whether every event the probe got accepted at least
// MaxDelay ago is visible in the machine api. the api returns counts per
// hour, not events, so it keeps a baseline: the count over [BaseHour, now]
// read at BaseAt, plus every accepted send after BaseAt. the pipeline is
// fresh when the count now is at least the baseline plus the sends that are
// due. events that were in flight at the baseline or replayed twice only
// raise the count, so they can hide nothing that is late.
type Freshness struct {
	MaxDelay time.Duration
	// Window is how many hours a baseline may span before it is moved
	// forward; /web/hourly answers at most 24 hours.
	Window time.Duration

	set       bool
	baseHour  time.Time
	baseAt    time.Time
	baseCount int64
	sends     []time.Time
}

// Sent records an accepted send that started at t.
func (f *Freshness) Sent(t time.Time) {
	if f.set && t.After(f.baseAt) {
		f.sends = append(f.sends, t)
	}
}

// From is the first hour the next read must cover.
func (f *Freshness) From(now time.Time) time.Time {
	if !f.set || f.rebase(now) {
		return now.UTC().Truncate(time.Hour).Add(-time.Hour)
	}
	return f.baseHour
}

func (f *Freshness) rebase(now time.Time) bool {
	return now.Sub(f.baseHour) > f.Window
}

// Verdict is the result of one check.
type Verdict struct {
	// Baseline is true when this read set a new baseline; nothing is due
	// yet, so it counts as fresh.
	Baseline bool
	Fresh    bool
	Expected int64
	Visible  int64
}

// Observe takes the count over [From(readAt), now] read at readAt, the time
// the read started.
func (f *Freshness) Observe(readAt time.Time, from time.Time, visible int64) Verdict {
	if !f.set || !from.Equal(f.baseHour) {
		f.set, f.baseHour, f.baseAt, f.baseCount, f.sends = true, from, readAt, visible, nil
		return Verdict{Baseline: true, Fresh: true, Expected: visible, Visible: visible}
	}
	due := int64(0)
	cut := readAt.Add(-f.MaxDelay)
	for _, t := range f.sends {
		if !t.After(cut) {
			due++
		}
	}
	exp := f.baseCount + due
	v := Verdict{Fresh: visible >= exp, Expected: exp, Visible: visible}
	return v
}

// Backlog is how many due events are missing.
func (v Verdict) Backlog() int64 { return max(v.Expected-v.Visible, 0) }

// hourParam formats an hour for the machine api (contract Hour)
func hourParam(t time.Time) string { return t.UTC().Format("2006-01-02T15:00:00Z") }

func (p *Prober) machineGet(ctx context.Context, path string, q url.Values, out any) error {
	u := p.cfg.MachineURL.JoinPath(path)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.MachineKey.Reveal())
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("machine api %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("machine api %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		if len(b) > 300 {
			b = b[:300]
		}
		return fmt.Errorf("machine api %s: status %d %s", path, resp.StatusCode, b)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("machine api %s: %w", path, err)
	}
	return nil
}

// webVisible sums the pageviews of the probe tenant over both bands.
func (p *Prober) webVisible(ctx context.Context, from, to time.Time) (int64, error) {
	var r struct {
		Items []struct {
			Pageviews int64 `json:"pageviews"`
		} `json:"items"`
	}
	q := url.Values{"from": {hourParam(from)}, "to": {hourParam(to)}}
	if err := p.machineGet(ctx, "/web/hourly", q, &r); err != nil {
		return 0, err
	}
	var n int64
	for _, it := range r.Items {
		n += it.Pageviews
	}
	return n, nil
}

// apiVisible sums the requests of the probe tenant, following the cursor.
func (p *Prober) apiVisible(ctx context.Context, from, to time.Time) (int64, error) {
	var n int64
	cursor := ""
	for range 100 {
		var r struct {
			Items []struct {
				Requests int64 `json:"requests"`
			} `json:"items"`
			NextCursor *string `json:"next_cursor"`
		}
		q := url.Values{"from": {hourParam(from)}, "to": {hourParam(to)}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		if err := p.machineGet(ctx, "/api/hourly", q, &r); err != nil {
			return 0, err
		}
		for _, it := range r.Items {
			n += it.Requests
		}
		if r.NextCursor == nil || *r.NextCursor == "" {
			return n, nil
		}
		cursor = *r.NextCursor
	}
	return 0, fmt.Errorf("machine api /api/hourly: more than 100 pages")
}
