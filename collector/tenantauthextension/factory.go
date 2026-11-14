// Package tenantauthextension is the tenantauth server authenticator of the
// scf-otelcol distribution (docs/reference/collector.md). it maps an otlp
// bearer token to the tenant that owns it through an in-memory snapshot of
// the registry_otlp kv bucket, and fails closed while no snapshot exists.
package tenantauthextension

import (
	"context"
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
)

// Type is the component type used in otelcol.yaml.
var Type = component.MustNewType("tenantauth")

// NewFactory returns the tenantauth extension factory.
func NewFactory() extension.Factory {
	return extension.NewFactory(Type, func() component.Config { return &Config{} }, create, component.StabilityLevelBeta)
}

func create(_ context.Context, set extension.Settings, cfg component.Config) (extension.Extension, error) {
	c, ok := cfg.(*Config)
	if !ok {
		return nil, fmt.Errorf("tenantauth: unexpected config type %T", cfg)
	}
	return newAuthenticator(c, set), nil
}
