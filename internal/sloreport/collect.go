package sloreport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DefaultPageSize matches the maximum-number-of-results the probe's gatus is
// configured with: one week of 30 s results.
const DefaultPageSize = 20160

// maxBody bounds one gatus answer. a full page of 20160 results is about
// 3 MB of json; anything far beyond that is not a gatus status page.
const maxBody = 64 << 20

// Collector reads raw results from a gatus instance.
type Collector struct {
	// Base is the gatus root url, for example https://status.<BASE_DOMAIN>.
	Base string
	// PageSize is the page size asked for. gatus caps page 1 at its
	// maximum-number-of-results, which Collect detects and follows.
	PageSize int
	HTTP     *http.Client
	Now      func() time.Time
	// Until, when set, collects a day that has not ended up to this time
	// and marks the file partial.
	Until time.Time
}

// gatusResult is the part of gatus' endpoint.Result that is kept
// (config/endpoint/result.go at v5.37.0).
type gatusResult struct {
	Success   bool      `json:"success"`
	Timestamp time.Time `json:"timestamp"`
	Errors    []string  `json:"errors"`
}

type gatusStatus struct {
	Key     string        `json:"key"`
	Results []gatusResult `json:"results"`
}

// errNotFound is returned for an endpoint gatus does not know, which means
// the gatus config and the prober disagree; it is never treated as "no
// results".
var errNotFound = errors.New("gatus does not know this endpoint")

func (c *Collector) page(ctx context.Context, key string, page, size int) ([]gatusResult, error) {
	u := strings.TrimRight(c.Base, "/") + "/api/v1/endpoints/" + url.PathEscape(key) +
		"/statuses?page=" + strconv.Itoa(page) + "&pageSize=" + strconv.Itoa(size)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gatus %s: %w", key, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("gatus %s: %w", key, err)
	}
	if len(b) > maxBody {
		return nil, fmt.Errorf("gatus %s: answer larger than %d bytes", key, maxBody)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%s: %w", key, errNotFound)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("gatus %s: status %d", key, resp.StatusCode)
	}
	var s gatusStatus
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("gatus %s: %w", key, err)
	}
	if s.Key != key {
		return nil, fmt.Errorf("gatus %s: answered for key %q", key, s.Key)
	}
	return s.Results, nil
}

// endpoint fetches every result of key back to the day start. gatus returns
// pages newest first, results oldest first inside a page, and caps only
// page 1 at its maximum-number-of-results (api/util.go), so the first page's
// length becomes the page size of the following pages: their offsets then
// line up with page 1 whatever the cap is.
func (c *Collector) endpoint(ctx context.Context, key string, start time.Time) ([]gatusResult, error) {
	first, err := c.page(ctx, key, 1, c.PageSize)
	if err != nil {
		return nil, err
	}
	size := len(first)
	all := first
	more := false
	oldest := func(rs []gatusResult) time.Time { return rs[0].Timestamp }
	for p := 2; size > 0; p++ {
		if !oldest(all).After(start) {
			return all, nil
		}
		next, err := c.page(ctx, key, p, size)
		if err != nil {
			return nil, err
		}
		if len(next) == 0 {
			break
		}
		more = true
		all = append(slices.Clone(next), all...)
		if len(next) < size {
			break
		}
	}
	// the data ran out after the day start: either the endpoint started
	// reporting during the day, or gatus already dropped the older results.
	// gatus trims each endpoint back to its maximum-number-of-results once
	// it holds 10 more (storage/store/sql), so a full first page, or more
	// results than gatus put on page 1, means older results were dropped.
	// PageSize must therefore equal the gatus maximum: with a larger one, a
	// store trimmed to exactly its maximum would look complete.
	if size > 0 && (size == c.PageSize || more) {
		return nil, fmt.Errorf("gatus %s: its %d stored results start at %s, after the day start %s; the day was trimmed and is not written",
			key, len(all), oldest(all).UTC().Format(time.RFC3339), start.Format(time.RFC3339))
	}
	return all, nil
}

// Collect reads one ended utc day of every slo endpoint, or with Until set
// the part of a day before Until.
func (c *Collector) Collect(ctx context.Context, day time.Time) (Day, error) {
	if c.Base == "" || c.PageSize < 1 || c.HTTP == nil || c.Now == nil {
		return Day{}, errors.New("sloreport: collector needs Base, PageSize, HTTP and Now")
	}
	start := day.UTC()
	end := start.AddDate(0, 0, 1)
	now := c.Now().UTC()
	var until *time.Time
	if !c.Until.IsZero() {
		u := c.Until.UTC()
		if !u.After(start) || u.After(end) || u.After(now) {
			return Day{}, fmt.Errorf("until %s must be inside %s and not in the future", u.Format(time.RFC3339), start.Format(dayLayout))
		}
		until, end = &u, u
	} else if now.Before(end) {
		return Day{}, fmt.Errorf("%s: %w (it ends at %s)", start.Format(dayLayout), errNotEnded, end.Format(time.RFC3339))
	}
	out := Day{
		Day:             start.Format(dayLayout),
		CollectedAt:     now.Truncate(time.Second),
		Source:          strings.TrimRight(c.Base, "/"),
		IntervalSeconds: IntervalSeconds,
		Endpoints:       map[string][]Result{},
		Until:           until,
	}
	for _, key := range Keys {
		rs, err := c.endpoint(ctx, key, start)
		if err != nil {
			return Day{}, err
		}
		kept := []Result{}
		for _, r := range rs {
			t := r.Timestamp.UTC()
			if t.Before(start) || !t.Before(end) {
				continue
			}
			var errs []string
			if len(r.Errors) > 0 {
				errs = slices.Clone(r.Errors)
			}
			kept = append(kept, Result{Timestamp: t, Success: r.Success, Errors: errs})
		}
		slices.SortStableFunc(kept, func(a, b Result) int { return a.Timestamp.Compare(b.Timestamp) })
		out.Endpoints[key] = kept
	}
	return out, nil
}
