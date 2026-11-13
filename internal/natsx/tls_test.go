package natsx_test

import (
	"crypto/tls"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
	"github.com/justmert/veristar/tests/fixtures/tlsca"
)

// the client part of the edge role's nats.conf (roles/edge): tls with the
// node certificate, tls first, so the server sends nothing before the
// handshake
const tlsServerConf = `
tls {
  cert_file: "/etc/nats/tls/node.crt"
  key_file: "/etc/nats/tls/node.key"
  ca_file: "/etc/nats/tls/ca.crt"
  handshake_first: true
  min_version: "1.3"
  timeout: 5
}
`

// TestConnectWithTLS connects through natsx to a real nats server set up as
// the edge nodes are, and proves a client that tries plain text, a client
// that trusts another ca, and a client that waits for the server's INFO line
// are all refused.
func TestConnectWithTLS(t *testing.T) {
	ca := tlsca.New(t, "sa-test-ca")
	node := ca.Issue(t, "sa-edge-1", "127.0.0.1", "localhost", "nats")
	srv := natsctr.StartSingle(t, natsctr.WithConfig(tlsServerConf),
		natsctr.WithFile("/etc/nats/tls/ca.crt", ca.CertPEM),
		natsctr.WithFile("/etc/nats/tls/node.crt", node.CertPEM),
		natsctr.WithFile("/etc/nats/tls/node.key", node.KeyPEM))
	caFile, _, _ := tlsca.Files(t, t.TempDir(), ca, node)
	log := obs.NewLogger(testWriter{t}, slog.LevelInfo)

	cfg, err := config.LoadNATS(config.FromMap(map[string]string{
		"NATS_URL": srv.URL, "NATS_REPLICAS": "1", "NATS_TLS_CA": caFile,
	}))
	require.NoError(t, err)
	nc, err := natsx.Connect(cfg, natsx.ServiceGateway, log)
	require.NoError(t, err)
	defer nc.Close()
	st, err := nc.TLSConnectionState()
	require.NoError(t, err, "the connection is not tls")
	require.Equal(t, uint16(tls.VersionTLS13), st.Version)
	require.NoError(t, nc.Publish("tls.check", []byte("x")))
	require.NoError(t, nc.FlushTimeout(5*time.Second))

	short := []nats.Option{nats.Timeout(3 * time.Second), nats.NoReconnect()}

	// plain text: the server expects a tls handshake first and never
	// answers a plain client with its INFO line
	_, err = nats.Connect(srv.URL, short...)
	require.Error(t, err, "a plain text client must be refused")

	// tls without handshake first waits for an INFO line that never comes
	tc, err := cfg.TLS.ClientConfig()
	require.NoError(t, err)
	_, err = nats.Connect(srv.URL, append(short, nats.Secure(tc))...)
	require.Error(t, err)

	// a client that trusts another ca refuses the server
	other := tlsca.New(t, "other-ca")
	otherCA, _, _ := tlsca.Files(t, t.TempDir(), other, other.Issue(t, "x", "x"))
	ocfg, err := config.LoadNATS(config.FromMap(map[string]string{
		"NATS_URL": srv.URL, "NATS_REPLICAS": "1", "NATS_TLS_CA": otherCA,
	}))
	require.NoError(t, err)
	_, err = natsx.Connect(ocfg, natsx.ServiceGateway, log)
	require.Error(t, err)
	require.Contains(t, err.Error(), "certificate signed by unknown authority")
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
