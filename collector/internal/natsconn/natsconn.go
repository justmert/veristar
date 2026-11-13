// Package natsconn is the nats connection setting shared by the collector's
// tenantauth extension and jetstream exporter. both connect as the
// collector's own nats user (internal/natsx), with the same reconnect and
// inbox behaviour as every other service.
package natsconn

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/collector/config/configopaque"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
)

// Config is the nats part of a component configuration.
type Config struct {
	// URL is a comma separated list of nats:// or tls:// urls, every server
	// of the cluster.
	URL string `mapstructure:"url"`
	// User and Password are the collector's nats credentials. both empty
	// only against the dev server, which runs without auth.
	User     string              `mapstructure:"user"`
	Password configopaque.String `mapstructure:"password"`
	// TLSCA, TLSCert and TLSKey are the files of NATS_TLS_CA, NATS_TLS_CERT
	// and NATS_TLS_KEY. all empty only against the dev server; with a ca
	// the collector never connects in plain text (natsx.Options).
	TLSCA   string `mapstructure:"tls_ca_file"`
	TLSCert string `mapstructure:"tls_cert_file"`
	TLSKey  string `mapstructure:"tls_key_file"`
}

// load runs the same checks as every service's NATS_* keys. the replica
// count is required by config.LoadNATS but only used by `platformctl nats
// apply`, never by a client, so the value here has no effect.
func (c Config) load() (config.NATS, error) {
	m := map[string]string{"NATS_URL": c.URL, "NATS_REPLICAS": "1"}
	if c.User != "" {
		m["NATS_USER"] = c.User
	}
	if c.Password != "" {
		m["NATS_PASSWORD"] = string(c.Password)
	}
	for k, v := range map[string]string{"NATS_TLS_CA": c.TLSCA, "NATS_TLS_CERT": c.TLSCert, "NATS_TLS_KEY": c.TLSKey} {
		if v != "" {
			m[k] = v
		}
	}
	return config.LoadNATS(config.FromMap(m))
}

// Validate checks the urls and the user and password pair.
func (c Config) Validate() error {
	if c.URL == "" {
		return errors.New("nats url is required")
	}
	_, err := c.load()
	return err
}

// Logger is the slog logger natsx reports connection events to, guarded so
// no credential reaches the log. the collector's own logger is zap; these
// lines go to the same stderr.
func Logger(component string) *slog.Logger {
	return obs.NewLogger(os.Stderr, slog.LevelInfo).With("component", component)
}

// Connect opens the collector's connection with the options every service
// uses (natsx.Options). the first connect fails fast, so a misconfigured
// collector does not start; afterwards it reconnects forever.
//
// the reconnect buffer is turned off: a publish while disconnected fails at
// once, the client gets 503 and retries, and nothing the client was told
// failed is flushed into the stream minutes later. a retry carries the same
// payload and therefore the same Nats-Msg-Id, so the case where jetstream
// stored a message but the acknowledgement was lost is deduplicated.
func (c Config) Connect(log *slog.Logger) (*nats.Conn, error) {
	cfg, err := c.load()
	if err != nil {
		return nil, err
	}
	opts, err := natsx.Options(cfg, natsx.ServiceCollector, log)
	if err != nil {
		return nil, err
	}
	opts = append(opts, nats.ReconnectBufSize(-1))
	nc, err := nats.Connect(strings.Join(cfg.URLs, ","), opts...)
	if err != nil {
		return nil, fmt.Errorf("natsconn: connect as %s: %w", natsx.ServiceCollector, err)
	}
	return nc, nil
}
