package natsx_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/natsx"
)

// TestOptionsLiveness pins the client liveness settings that the production
// node loss drill of 2026-11-14 proved (docs/reference/drills/2026-11-13-azure.md,
// NATS route liveness): a ping every 5 s with 3 outstanding notices a
// powered off server within 20 s, and the configured server order is kept,
// so a converged node's services stay on their own node's server. with
// nats.go's defaults the drill saw 5.5 minutes of failed publishes.
func TestOptionsLiveness(t *testing.T) {
	cfg := config.NATS{URLs: []string{"tls://10.10.1.11:4222", "tls://10.10.2.12:4222", "tls://10.10.3.13:4222"}}
	for _, service := range []string{"gateway", "collector", "replayer", "controlplane"} {
		t.Run(service, func(t *testing.T) {
			opts, err := natsx.Options(cfg, service, slog.New(slog.DiscardHandler))
			require.NoError(t, err)
			o := nats.GetDefaultOptions()
			for _, opt := range opts {
				require.NoError(t, opt(&o))
			}
			require.Equal(t, 5*time.Second, o.PingInterval, "client ping interval")
			require.Equal(t, 3, o.MaxPingsOut, "client pings outstanding")
			require.True(t, o.NoRandomize, "the client must keep the configured server order")
			require.Equal(t, -1, o.MaxReconnect, "the client must reconnect forever")
			require.Equal(t, time.Second, o.ReconnectWait)
		})
	}
}
