// Package alertmanager reads the live alerts of the alertmanager cluster
// through its v2 api (GET /api/v2/alerts), for the operator alerts page of
// the control plane (docs/reference/control-plane-api.md section 11).
//
// every member is asked, because a member that is down or cut off must show
// as such instead of hiding behind the one that answered. the answer of
// each member is checked against the fields the api documents as required
// (api/v2/openapi.yaml of alertmanager), so a proxy page or a changed api
// is reported as an error rather than read as "no alerts".
package alertmanager

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// alertsPath is the v2 route of the current alerts. without query
// parameters it returns every alert that has not resolved: active,
// silenced, inhibited and unprocessed. the caller filters, so the watchdog
// is seen even when someone silenced it.
const alertsPath = "/api/v2/alerts"

// maxBody bounds one member's answer. 37 rules over a few nodes stay far
// below it; a bigger body means something other than alertmanager answered.
const maxBody = 8 << 20

// DefaultTimeout bounds one member's request, so a member that accepts the
// connection and never answers does not hold the operator's page.
const DefaultTimeout = 5 * time.Second

// Alert states of the v2 api (alertStatus.state).
const (
	StateActive      = "active"
	StateSuppressed  = "suppressed"
	StateUnprocessed = "unprocessed"
)

// Alert is one alert as a member reports it.
type Alert struct {
	Fingerprint string
	Labels      map[string]string
	Annotations map[string]string
	StartsAt    time.Time
	State       string
	SilencedBy  []string
	InhibitedBy []string
	MutedBy     []string
}

// Name is the alertname label.
func (a Alert) Name() string { return a.Labels["alertname"] }

// Silenced reports whether a silence matches the alert.
func (a Alert) Silenced() bool { return len(a.SilencedBy) > 0 }

// Member is one member's answer: its alerts, or the reason it gave none.
type Member struct {
	// Address is the member's host and port, as configured.
	Address string
	Alerts  []Alert
	Err     error
}

// Client asks every configured member.
type Client struct {
	urls    []*url.URL
	hc      *http.Client
	timeout time.Duration
}

// New returns a client for the members at urls (base urls, no path). tlsCfg
// is used for https members and may be nil when all are plain http.
func New(urls []*url.URL, tlsCfg *tls.Config, timeout time.Duration) (*Client, error) {
	if len(urls) == 0 {
		return nil, errors.New("alertmanager: no member url")
	}
	if timeout <= 0 {
		return nil, errors.New("alertmanager: the timeout must be positive")
	}
	for _, u := range urls {
		if u.Scheme == "https" && tlsCfg == nil {
			return nil, fmt.Errorf("alertmanager: %s needs a tls config", u.Host)
		}
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("alertmanager: the default transport is not an *http.Transport")
	}
	tr := base.Clone()
	// only the members' own ca: never the system roots, never the
	// environment's proxy (the members are on the private network)
	tr.TLSClientConfig = tlsCfg
	tr.Proxy = nil
	return &Client{
		urls: urls,
		hc: &http.Client{
			Transport: tr,
			// the alerts route never redirects; following one would send
			// the client certificate somewhere else
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		timeout: timeout,
	}, nil
}

// Alerts asks every member at once and returns one answer per member, in
// the configured order.
func (c *Client) Alerts(ctx context.Context) []Member {
	out := make([]Member, len(c.urls))
	var wg sync.WaitGroup
	for i, u := range c.urls {
		wg.Go(func() {
			alerts, err := c.member(ctx, u)
			out[i] = Member{Address: u.Host, Alerts: alerts, Err: err}
		})
	}
	wg.Wait()
	return out
}

func (c *Client) member(ctx context.Context, base *url.URL) ([]Alert, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.JoinPath(alertsPath).String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxBody))
		return nil, fmt.Errorf("answered http %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("reading the answer: %w", err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("answered more than %d bytes", maxBody)
	}
	return Decode(body)
}

// wireAlert is gettableAlert of the v2 api. pointers tell a missing
// required field from an empty one.
type wireAlert struct {
	Fingerprint *string           `json:"fingerprint"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    *time.Time        `json:"startsAt"`
	Status      *wireStatus       `json:"status"`
}

type wireStatus struct {
	State       string   `json:"state"`
	SilencedBy  []string `json:"silencedBy"`
	InhibitedBy []string `json:"inhibitedBy"`
	MutedBy     []string `json:"mutedBy"`
}

// Decode reads a gettableAlerts body. it refuses anything that is not a
// json array of alerts carrying the required fields this package reads.
func Decode(body []byte) ([]Alert, error) {
	var wire []wireAlert
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("the answer is not a list of alerts: %w", err)
	}
	if wire == nil {
		return nil, errors.New("the answer is not a list of alerts: null")
	}
	out := make([]Alert, 0, len(wire))
	for i, w := range wire {
		switch {
		case w.Fingerprint == nil || *w.Fingerprint == "":
			return nil, fmt.Errorf("alert %d has no fingerprint", i)
		case w.Labels == nil || w.Labels["alertname"] == "":
			return nil, fmt.Errorf("alert %s has no alertname label", *w.Fingerprint)
		case w.Annotations == nil:
			return nil, fmt.Errorf("alert %s has no annotations", *w.Fingerprint)
		case w.StartsAt == nil || w.StartsAt.IsZero():
			return nil, fmt.Errorf("alert %s has no startsAt", *w.Fingerprint)
		case w.Status == nil:
			return nil, fmt.Errorf("alert %s has no status", *w.Fingerprint)
		}
		switch w.Status.State {
		case StateActive, StateSuppressed, StateUnprocessed:
		default:
			return nil, fmt.Errorf("alert %s has the unknown state %q", *w.Fingerprint, w.Status.State)
		}
		out = append(out, Alert{
			Fingerprint: *w.Fingerprint,
			Labels:      w.Labels,
			Annotations: w.Annotations,
			StartsAt:    w.StartsAt.UTC(),
			State:       w.Status.State,
			SilencedBy:  w.Status.SilencedBy,
			InhibitedBy: w.Status.InhibitedBy,
			MutedBy:     w.Status.MutedBy,
		})
	}
	return out, nil
}
