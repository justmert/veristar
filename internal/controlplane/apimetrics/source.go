package apimetrics

import (
	"context"
	"fmt"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/db/tenantq"
	"github.com/justmert/veristar/internal/trafficclass"
)

// the two values of the source column
const (
	SourceMetric = "metric"
	SourceSpan   = "span"
)

// Source is the source object of contract 7.3.
type Source struct {
	Service         string `json:"service"`
	Source          string `json:"source"`
	SpanDataIgnored bool   `json:"span_data_ignored"`
}

// Choose is the per-service source rule, shared by the tenant dashboard,
// the operator views and the machine api: metric rows count every request, span rows undercount when traces are
// sampled, so metric rows win whenever a service has any in the range. the
// two are never added together.
func Choose(service string, hasMetric, hasSpan bool) Source {
	if hasMetric {
		return Source{Service: service, Source: SourceMetric, SpanDataIgnored: hasSpan}
	}
	return Source{Service: service, Source: SourceSpan}
}

// Selection is the chosen source of every service in a range. Metric and
// Span hold the services for each source; the aggregate queries take them
// as their @metric_services and @span_services, so each row they read
// belongs to the source its service was given.
type Selection struct {
	Sources []Source
	Metric  []string
	Span    []string
}

// Select reads which sources each service has in the range at r's
// resolution and applies Choose. only the service filter narrows it: a
// route or method filter must not switch a service's source.
func Select(ctx context.Context, q *tenantq.Queries, r Range, service *string) (Selection, error) {
	rows, err := q.APIMetricSources(ctx, tenantq.APIMetricSourcesParams{
		Resolution: string(r.Resolution), RangeFrom: r.From, RangeTo: r.To, Service: service,
	})
	if err != nil {
		return Selection{}, fmt.Errorf("apimetrics: source selection: %w", err)
	}
	// empty, not nil: pgx sends a nil slice as sql null
	sel := Selection{Sources: []Source{}, Metric: []string{}, Span: []string{}}
	for _, row := range rows {
		if !row.HasMetric && !row.HasSpan {
			// the column check allows only these two values, so a row of
			// neither means the schema changed under this code
			return Selection{}, fmt.Errorf("apimetrics: service %q has rows of neither source", row.Service)
		}
		s := Choose(row.Service, row.HasMetric, row.HasSpan)
		sel.Sources = append(sel.Sources, s)
		if s.Source == SourceMetric {
			sel.Metric = append(sel.Metric, s.Service)
		} else {
			sel.Span = append(sel.Span, s.Service)
		}
	}
	return sel, nil
}

// trafficClasses reads the rule set in force (00038). the first version's
// applied_from is when infrastructure requests started to be counted apart.
func trafficClasses(ctx context.Context, q *tenantq.Queries) (TrafficClasses, error) {
	rows, err := q.TrafficClassRules(ctx)
	if err != nil {
		return TrafficClasses{}, fmt.Errorf("apimetrics: traffic class rules: %w", err)
	}
	out := TrafficClasses{RuleSetVersion: trafficclass.Version}
	if len(rows) > 0 {
		at := httpx.Timestamp(rows[0].AppliedFrom)
		out.AppliedFrom = &at
	}
	return out, nil
}
