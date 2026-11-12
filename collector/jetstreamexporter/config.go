package jetstreamexporter

import (
	"errors"
	"time"

	"github.com/justmert/stellar-analytics/collector/internal/natsconn"
)

// Config is the jetstream exporter configuration. the stream and subject
// prefix are fixed (API_METRICS, api.metrics.<tenant>), so only the
// connection and the acknowledgement timeout are configurable.
type Config struct {
	NATS natsconn.Config `mapstructure:"nats"`
	// PublishTimeout bounds the wait for one acknowledgement. a request that
	// runs out of it is answered with Unavailable (503), so the client
	// retries. required.
	PublishTimeout time.Duration `mapstructure:"publish_timeout"`
}

// Validate checks the connection and requires a positive timeout.
func (c *Config) Validate() error {
	var errs []error
	if err := c.NATS.Validate(); err != nil {
		errs = append(errs, err)
	}
	if c.PublishTimeout <= 0 {
		errs = append(errs, errors.New("publish_timeout must be positive"))
	}
	return errors.Join(errs...)
}
