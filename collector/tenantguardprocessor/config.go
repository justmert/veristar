package tenantguardprocessor

import (
	"errors"
	"time"
)

// Config holds the per-tenant limits. every value is required: there are no
// built-in defaults, so the limits in force are always the ones written in
// otelcol.yaml.
type Config struct {
	// RequestsPerSecond and RequestsBurst bound export requests per tenant,
	// across every signal.
	RequestsPerSecond float64 `mapstructure:"requests_per_second"`
	RequestsBurst     int     `mapstructure:"requests_burst"`
	// ItemsPerSecond and ItemsBurst bound data points, spans and log records
	// per tenant, counted as they arrive, before filtering.
	ItemsPerSecond float64 `mapstructure:"items_per_second"`
	ItemsBurst     int     `mapstructure:"items_burst"`
	// IdleBucketTTL is how long the buckets of a tenant without traffic are
	// kept. a tenant that comes back after that starts with full buckets.
	IdleBucketTTL time.Duration `mapstructure:"idle_bucket_ttl"`
}

// Validate requires every limit to be positive.
func (c *Config) Validate() error {
	var errs []error
	if c.RequestsPerSecond <= 0 {
		errs = append(errs, errors.New("requests_per_second must be positive"))
	}
	if c.RequestsBurst < 1 {
		errs = append(errs, errors.New("requests_burst must be at least 1"))
	}
	if c.ItemsPerSecond <= 0 {
		errs = append(errs, errors.New("items_per_second must be positive"))
	}
	if c.ItemsBurst < 1 {
		errs = append(errs, errors.New("items_burst must be at least 1"))
	}
	if c.IdleBucketTTL <= 0 {
		errs = append(errs, errors.New("idle_bucket_ttl must be positive"))
	}
	return errors.Join(errs...)
}
