// Package jetstreamexporter is the jetstream exporter of the scf-otelcol
// distribution (the design). it splits a metrics payload by tenant,
// checks every field against the published schema once more, marshals each
// part as otlp protobuf and publishes it to api.metrics.<tenant> on
// API_METRICS with Nats-Msg-Id set to the sha-256 of the payload, waiting
// for the acknowledgement. there is no sending queue and no retry inside the
// collector: the client is answered only after jetstream stored the data, and
// a failure goes back to the client as Unavailable, which it retries.
package jetstreamexporter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/justmert/stellar-analytics/collector/internal/natsconn"
	"github.com/justmert/stellar-analytics/collector/internal/schema"
	"github.com/justmert/stellar-analytics/internal/natsx"
)

// Type is the component type used in otelcol.yaml.
var Type = component.MustNewType("jetstream")

// NewFactory returns the jetstream exporter factory.
func NewFactory() exporter.Factory {
	return exporter.NewFactory(Type, func() component.Config { return &Config{} },
		exporter.WithMetrics(createMetrics, component.StabilityLevelBeta))
}

type metricsExporter struct {
	consumer.Metrics
	cfg *Config
	log *zap.Logger
	nlg *slog.Logger

	mu sync.RWMutex
	nc *nats.Conn
	js jetstream.JetStream
}

func createMetrics(_ context.Context, set exporter.Settings, cfg component.Config) (exporter.Metrics, error) {
	c, ok := cfg.(*Config)
	if !ok {
		return nil, fmt.Errorf("jetstream exporter: unexpected config type %T", cfg)
	}
	e := &metricsExporter{cfg: c, log: set.Logger, nlg: natsconn.Logger(set.ID.String())}
	m, err := consumer.NewMetrics(e.push, consumer.WithCapabilities(consumer.Capabilities{MutatesData: false}))
	if err != nil {
		return nil, err
	}
	e.Metrics = m
	return e, nil
}

func (e *metricsExporter) Start(context.Context, component.Host) error {
	nc, err := e.cfg.NATS.Connect(e.nlg)
	if err != nil {
		return err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return err
	}
	e.mu.Lock()
	e.nc, e.js = nc, js
	e.mu.Unlock()
	return nil
}

func (e *metricsExporter) Shutdown(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.nc != nil {
		// drain flushes anything in flight before closing
		_ = e.nc.Drain()
		e.nc = nil
		e.js = nil
	}
	return nil
}

// errNoTenant means a resource reached the exporter without the stamp of
// tenantguard. that is a configuration error, not something a retry fixes.
var errNoTenant = errors.New("jetstream exporter: resource without a valid scf.tenant.id")

func (e *metricsExporter) push(ctx context.Context, md pmetric.Metrics) error {
	parts, dropped, err := Split(md)
	if err != nil {
		return consumererror.NewPermanent(err)
	}
	if dropped > 0 {
		// a field outside the schema reached the exporter; the processors in
		// front should have removed it. it is dropped, and logged so the
		// configuration gets fixed.
		e.log.Error("dropped data outside the published schema", zap.Int("items", dropped))
	}
	e.mu.RLock()
	js := e.js
	e.mu.RUnlock()
	if js == nil {
		return status.Error(codes.Unavailable, "jetstream exporter not started")
	}
	for _, p := range parts {
		if err := e.publish(ctx, js, p); err != nil {
			return err
		}
	}
	return nil
}

func (e *metricsExporter) publish(ctx context.Context, js jetstream.JetStream, p Part) error {
	ctx, cancel := context.WithTimeout(ctx, e.cfg.PublishTimeout)
	defer cancel()
	ack, err := natsx.PublishDedup(ctx, js, natsx.StreamAPIMetrics, natsx.Subject(natsx.SubjectAPIMetrics, p.TenantID), p.MsgID, p.Data, nil)
	if err != nil {
		e.log.Warn("publish to jetstream failed", zap.String("tenant_id", p.TenantID), zap.Error(err))
		return status.Error(codes.Unavailable, "metrics queue unavailable, retry later")
	}
	if ack.Duplicate {
		e.log.Debug("duplicate publish acknowledged", zap.String("tenant_id", p.TenantID))
	}
	return nil
}

// Part is the payload for one tenant.
type Part struct {
	TenantID string
	MsgID    string
	Data     []byte
}

// Split groups resources by scf.tenant.id, filters every part to the
// published schema and marshals it. dropped counts metrics, data points and
// attributes that were removed by the filter. a resource without a tenant is
// an error: tenantguard stamps every resource, so its absence means the
// pipeline is misconfigured.
func Split(md pmetric.Metrics) ([]Part, int, error) {
	byTenant := map[string]pmetric.Metrics{}
	var order []string
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)
		v, ok := rm.Resource().Attributes().Get(schema.ResTenantID)
		if !ok || v.Type() != pcommon.ValueTypeStr {
			return nil, 0, errNoTenant
		}
		tenant := v.Str()
		if u, err := uuid.Parse(tenant); err != nil || u.String() != tenant {
			return nil, 0, errNoTenant
		}
		out, ok := byTenant[tenant]
		if !ok {
			out = pmetric.NewMetrics()
			byTenant[tenant] = out
			order = append(order, tenant)
		}
		rm.CopyTo(out.ResourceMetrics().AppendEmpty())
	}
	var parts []Part
	var dropped int
	var m pmetric.ProtoMarshaler
	for _, tenant := range order {
		out := byTenant[tenant]
		dropped += filter(out)
		if out.DataPointCount() == 0 {
			continue
		}
		b, err := m.MarshalMetrics(out)
		if err != nil {
			return nil, dropped, fmt.Errorf("jetstream exporter: marshal: %w", err)
		}
		sum := sha256.Sum256(b)
		parts = append(parts, Part{TenantID: tenant, MsgID: hex.EncodeToString(sum[:]), Data: b})
	}
	return parts, dropped, nil
}

func filterAttrs(m pcommon.Map, allowed map[string]bool) int {
	n := 0
	m.RemoveIf(func(k string, _ pcommon.Value) bool {
		if !allowed[k] {
			n++
			return true
		}
		return false
	})
	return n
}

// filter is the last line of defence: only the two published metric names,
// explicit histograms in seconds, and allowlisted attributes leave the
// collector.
func filter(md pmetric.Metrics) int {
	dropped := 0
	md.ResourceMetrics().RemoveIf(func(rm pmetric.ResourceMetrics) bool {
		dropped += filterAttrs(rm.Resource().Attributes(), schema.ResourceAllowed)
		rm.ScopeMetrics().RemoveIf(func(sm pmetric.ScopeMetrics) bool {
			// the scope names the producing library; nothing downstream needs it
			sc := sm.Scope()
			dropped += sc.Attributes().Len()
			sc.Attributes().Clear()
			sc.SetName("")
			sc.SetVersion("")
			sm.Metrics().RemoveIf(func(m pmetric.Metric) bool {
				if !schema.PublishedMetrics[m.Name()] || m.Type() != pmetric.MetricTypeHistogram || m.Unit() != "s" {
					dropped++
					return true
				}
				dropped += m.Metadata().Len()
				m.Metadata().Clear()
				dps := m.Histogram().DataPoints()
				for i := 0; i < dps.Len(); i++ {
					dp := dps.At(i)
					dropped += filterAttrs(dp.Attributes(), schema.PointAllowed)
					dropped += dp.Exemplars().Len()
					dp.Exemplars().RemoveIf(func(pmetric.Exemplar) bool { return true })
				}
				return dps.Len() == 0
			})
			return sm.Metrics().Len() == 0
		})
		return rm.ScopeMetrics().Len() == 0
	})
	return dropped
}
