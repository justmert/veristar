package tenantauthextension

import (
	"errors"
	"time"

	"github.com/justmert/veristar/collector/internal/natsconn"
)

// Config is the tenantauth configuration. the registry bucket is fixed
// (registry_otlp), so only the connection and the startup bound are
// configurable.
type Config struct {
	NATS natsconn.Config `mapstructure:"nats"`
	// SnapshotTimeout bounds how long Start waits for the first full
	// snapshot of registry_otlp. required.
	SnapshotTimeout time.Duration `mapstructure:"snapshot_timeout"`
}

// Validate checks the nats settings and the timeout.
func (c *Config) Validate() error {
	var errs []error
	if err := c.NATS.Validate(); err != nil {
		errs = append(errs, err)
	}
	if c.SnapshotTimeout <= 0 {
		errs = append(errs, errors.New("snapshot_timeout must be positive"))
	}
	return errors.Join(errs...)
}
