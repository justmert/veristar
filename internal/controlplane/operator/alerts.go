package operator

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/justmert/veristar/internal/alertmanager"
	"github.com/justmert/veristar/internal/controlplane/httpx"
)

// WatchdogAlertName is the always-firing rule of files/rules/platform.yml.
// the route leaves it out of the list and reports whether it is there.
const WatchdogAlertName = "Watchdog"

// AlertReader is what the alerts route reads: one answer per alertmanager
// member. *alertmanager.Client is the real one.
type AlertReader interface {
	Alerts(ctx context.Context) []alertmanager.Member
}

// Alerts serves GET /api/operator/alerts (contract 11): the live alerts of
// the alertmanager cluster, for the console's alerts page. it replaces push
// delivery for deployments without a receiver (no discord webhook
// configured).
type Alerts struct {
	src   AlertReader
	guard *Guard
	now   func() time.Time
}

// NewAlerts takes the configured source, or nil when ALERTMANAGER_URLS is
// unset; the route then answers alert_source_not_configured on every call,
// so the page never shows "all clear" without having asked anyone. the
// guard limits the route to operator admins: alert labels and descriptions
// name nodes and internal addresses.
func NewAlerts(src AlertReader, guard *Guard, now func() time.Time) (*Alerts, error) {
	if guard == nil {
		return nil, errors.New("operator: the alerts route needs the guard")
	}
	if now == nil {
		now = time.Now
	}
	return &Alerts{src: src, guard: guard, now: now}, nil
}

// Routes registers the path under the guarded /api/operator router, behind
// the operator admin check.
func (h *Alerts) Routes(r chi.Router) {
	r.With(h.guard.RequireAdmin).Method(http.MethodGet, "/alerts", httpx.HandlerFunc(h.list))
}

// AlertsAnswer is the body of contract 11.1.
type AlertsAnswer struct {
	CheckedAt httpx.Timestamp `json:"checked_at"`
	// WatchdogFiring is false when no member that answered holds the
	// always-firing Watchdog: prometheus is not evaluating rules or cannot
	// reach alertmanager, so the list below can be missing alerts.
	WatchdogFiring bool                `json:"watchdog_firing"`
	Sources        []AlertSourceStatus `json:"sources"`
	Alerts         []AlertItem         `json:"alerts"`
}

// AlertSourceStatus is one alertmanager member and whether it answered.
type AlertSourceStatus struct {
	Address        string  `json:"address"`
	Reachable      bool    `json:"reachable"`
	WatchdogFiring bool    `json:"watchdog_firing"`
	Error          *string `json:"error"`
}

// AlertItem is one firing alert of contract 11.1.
type AlertItem struct {
	Fingerprint string            `json:"fingerprint"`
	Name        string            `json:"name"`
	Severity    *string           `json:"severity"`
	Summary     *string           `json:"summary"`
	Description *string           `json:"description"`
	Node        *string           `json:"node"`
	Instance    *string           `json:"instance"`
	Service     *string           `json:"service"`
	StartsAt    httpx.Timestamp   `json:"starts_at"`
	State       string            `json:"state"`
	InhibitedBy []string          `json:"inhibited_by"`
	Runbook     *string           `json:"runbook"`
	Labels      map[string]string `json:"labels"`
}

func (h *Alerts) list(w http.ResponseWriter, r *http.Request) error {
	if h.src == nil {
		return httpx.E(http.StatusServiceUnavailable, httpx.CodeAlertSourceNotConfigured,
			"no alertmanager is configured for the control plane (ALERTMANAGER_URLS)")
	}
	members := h.src.Alerts(r.Context())
	answer, err := buildAlerts(members, h.now())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, answer)
	return nil
}

// buildAlerts merges the members' answers. every member holds the whole set
// (each prometheus sends to all of them), so the same fingerprint seen
// twice is one alert: the first member in the configured order gives its
// fields and the earliest start wins. silenced alerts are left out, and so
// is the watchdog, whose presence is reported instead.
func buildAlerts(members []alertmanager.Member, now time.Time) (AlertsAnswer, error) {
	answer := AlertsAnswer{
		CheckedAt: httpx.Timestamp(now),
		Sources:   make([]AlertSourceStatus, 0, len(members)),
		Alerts:    []AlertItem{},
	}
	var failures []string
	byFingerprint := map[string]int{}
	for _, m := range members {
		src := AlertSourceStatus{Address: m.Address, Reachable: m.Err == nil}
		if m.Err != nil {
			msg := m.Err.Error()
			src.Error = &msg
			failures = append(failures, m.Address+": "+msg)
			answer.Sources = append(answer.Sources, src)
			continue
		}
		for _, a := range m.Alerts {
			if a.Name() == WatchdogAlertName {
				src.WatchdogFiring = true
				continue
			}
			if a.Silenced() {
				continue
			}
			if i, ok := byFingerprint[a.Fingerprint]; ok {
				if start := httpx.Timestamp(a.StartsAt); time.Time(start).Before(time.Time(answer.Alerts[i].StartsAt)) {
					answer.Alerts[i].StartsAt = start
				}
				continue
			}
			byFingerprint[a.Fingerprint] = len(answer.Alerts)
			answer.Alerts = append(answer.Alerts, toAlertItem(a))
		}
		answer.WatchdogFiring = answer.WatchdogFiring || src.WatchdogFiring
		answer.Sources = append(answer.Sources, src)
	}
	if len(failures) == len(members) {
		return AlertsAnswer{}, httpx.E(http.StatusServiceUnavailable, httpx.CodeAlertSourceUnreachable,
			"no alertmanager answered: "+strings.Join(failures, "; "))
	}
	sort.SliceStable(answer.Alerts, func(i, j int) bool {
		a, b := answer.Alerts[i], answer.Alerts[j]
		if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
			return ra < rb
		}
		if !time.Time(a.StartsAt).Equal(time.Time(b.StartsAt)) {
			return time.Time(a.StartsAt).Before(time.Time(b.StartsAt))
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Fingerprint < b.Fingerprint
	})
	return answer, nil
}

// severityRank orders the severities of the rules (files/rules/*.yml):
// page means act now, ticket within a working day. a severity the rules do
// not use yet sorts after both, and an alert without one last.
func severityRank(s *string) int {
	switch {
	case s == nil:
		return 3
	case *s == "page":
		return 0
	case *s == "ticket":
		return 1
	default:
		return 2
	}
}

func toAlertItem(a alertmanager.Alert) AlertItem {
	inhibited := a.InhibitedBy
	if inhibited == nil {
		inhibited = []string{}
	}
	return AlertItem{
		Fingerprint: a.Fingerprint,
		Name:        a.Name(),
		Severity:    optional(a.Labels, "severity"),
		Summary:     optional(a.Annotations, "summary"),
		Description: optional(a.Annotations, "description"),
		Node:        optional(a.Labels, "node"),
		Instance:    optional(a.Labels, "instance"),
		// the scrape job names the service in every rule of the platform
		Service:     optional(a.Labels, "job"),
		StartsAt:    httpx.Timestamp(a.StartsAt),
		State:       a.State,
		InhibitedBy: inhibited,
		Runbook:     optional(a.Annotations, "runbook"),
		Labels:      a.Labels,
	}
}

func optional(m map[string]string, key string) *string {
	v, ok := m[key]
	if !ok || v == "" {
		return nil
	}
	return &v
}
