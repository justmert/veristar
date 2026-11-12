package natsx

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/justmert/stellar-analytics/internal/config"
)

// reconnectWait is the pause between reconnect attempts to one server. the
// client cycles through every url in NATS_URL, so with three servers a
// surviving node is tried within a second.
const reconnectWait = time.Second

// Options returns the connection options every service uses: a connection
// named after the service, reconnects that never give up, a per-service reply
// inbox that the subject permissions are written against, the service's
// credentials when configured, and connection events in the log.
func Options(cfg config.NATS, service string, log *slog.Logger) ([]nats.Option, error) {
	if service == "" || strings.ContainsAny(service, ".*> \t") {
		return nil, fmt.Errorf("natsx: service name %q is not a single subject token", service)
	}
	opts := []nats.Option{
		nats.Name(service),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(reconnectWait),
		nats.CustomInboxPrefix(InboxPrefix(service)),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			// a nil error is our own Close, which the closed handler logs
			if err != nil {
				log.Warn("nats disconnected", "error", err.Error())
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Info("nats reconnected", "server", nc.ConnectedUrlRedacted())
		}),
		nats.ErrorHandler(func(_ *nats.Conn, sub *nats.Subscription, err error) {
			// permission violations arrive here asynchronously; they mean the
			// topology and the service disagree, so they are logged as errors
			if sub != nil {
				log.Error("nats async error", "subject", sub.Subject, "error", err.Error())
				return
			}
			log.Error("nats async error", "error", err.Error())
		}),
		nats.ClosedHandler(func(*nats.Conn) {
			log.Info("nats connection closed")
		}),
	}
	if cfg.User != "" {
		if cfg.User != service {
			// each service has its own user; running one service with another
			// one's credentials would give it the wrong permissions
			return nil, fmt.Errorf("natsx: NATS_USER is %q but this service is %q", cfg.User, service)
		}
		opts = append(opts, nats.UserInfo(cfg.User, cfg.Password.Reveal()))
	}
	return opts, nil
}

// Connect opens a connection for service. the first connect fails fast so a
// misconfigured service exits; after that the client reconnects forever.
func Connect(cfg config.NATS, service string, log *slog.Logger) (*nats.Conn, error) {
	if len(cfg.URLs) == 0 {
		return nil, errors.New("natsx: no server urls configured")
	}
	opts, err := Options(cfg, service, log)
	if err != nil {
		return nil, err
	}
	nc, err := nats.Connect(strings.Join(cfg.URLs, ","), opts...)
	if err != nil {
		return nil, fmt.Errorf("natsx: connect as %s: %w", service, err)
	}
	return nc, nil
}
