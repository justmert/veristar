// Package tenantguardprocessor is the tenantguard processor of the
// scf-otelcol distribution (docs/reference/collector.md). it is the first processor after
// memory_limiter in every tenant pipeline and cannot be skipped: it refuses
// data without tenantauth credentials, enforces per-tenant token buckets,
// stamps the tenant from the credential, reduces resources to an allowlist,
// keeps only http server duration metrics with allowlisted attributes and
// normalized routes, keeps only SERVER spans with an http method, and
// discards logs.
package tenantguardprocessor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processorhelper"
)

// Type is the component type used in otelcol.yaml.
var Type = component.MustNewType("tenantguard")

// NewFactory returns the tenantguard processor factory.
func NewFactory() processor.Factory {
	return processor.NewFactory(Type, func() component.Config { return &Config{} },
		processor.WithMetrics(createMetrics, component.StabilityLevelBeta),
		processor.WithTraces(createTraces, component.StabilityLevelBeta),
		processor.WithLogs(createLogs, component.StabilityLevelBeta),
	)
}

// the processor rewrites data in place
var caps = processorhelper.WithCapabilities(consumer.Capabilities{MutatesData: true})

// limiters are shared by the metrics, traces and logs instances of one
// configured processor, so a tenant's budget covers every signal instead of
// tripling with the number of pipelines.
var (
	limitersMu sync.Mutex
	limiters   = map[component.ID]*guard{}
)

func sharedGuard(set processor.Settings, cfg *Config) *guard {
	limitersMu.Lock()
	defer limitersMu.Unlock()
	g, ok := limiters[set.ID]
	if !ok || g.lim.cfg != cfg {
		g = newGuard(cfg, set.Logger, time.Now)
		limiters[set.ID] = g
	}
	return g
}

func config(cfg component.Config) (*Config, error) {
	c, ok := cfg.(*Config)
	if !ok {
		return nil, fmt.Errorf("tenantguard: unexpected config type %T", cfg)
	}
	return c, nil
}

func createMetrics(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Metrics) (processor.Metrics, error) {
	c, err := config(cfg)
	if err != nil {
		return nil, err
	}
	g := sharedGuard(set, c)
	return processorhelper.NewMetrics(ctx, set, cfg, next, g.processMetrics, caps)
}

func createTraces(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Traces) (processor.Traces, error) {
	c, err := config(cfg)
	if err != nil {
		return nil, err
	}
	g := sharedGuard(set, c)
	return processorhelper.NewTraces(ctx, set, cfg, next, g.processTraces, caps)
}

func createLogs(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Logs) (processor.Logs, error) {
	c, err := config(cfg)
	if err != nil {
		return nil, err
	}
	g := sharedGuard(set, c)
	return processorhelper.NewLogs(ctx, set, cfg, next, g.processLogs, caps)
}
