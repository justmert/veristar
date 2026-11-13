//go:build consolee2e

package consolee2e

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/justmert/veristar/tests/fixtures/tlsca"
)

// the alertmanager of the operator alerts page (contract 11). the image is
// the one the observability role pins, read from its defaults so the two
// never drift apart. like production it serves https with a certificate of
// an internal ca and asks every client for one; the control plane presents
// its own. the specs post alerts to its real v2 api through the loopback
// control api (AppsControl), as prometheus does.
var alertmanagerImageLine = regexp.MustCompile(`(?m)^\s*alertmanager:\s*(prom/alertmanager:\S+@sha256:[0-9a-f]{64})\s*$`)

const (
	alertmanagerAlias = "alertmanager"
	// where the control plane container finds its client certificate
	amClientDir = "/etc/sa-alertmanager-tls"
)

// production's web config (roles/observability/templates/web-config.yml.j2)
const amWebConfig = `tls_server_config:
  cert_file: /etc/sa-tls/node.crt
  key_file: /etc/sa-tls/node.key
  client_ca_file: /etc/sa-tls/ca.crt
  client_auth_type: RequireAndVerifyClientCert
  min_version: TLS13
`

// no push channel: the deployment this stack stands for has an operator input unset,
// so alerts are seen in the console only. a receiver without configurations
// is alertmanager's documented way to deliver nowhere.
const amConfig = `route:
  receiver: console-only
receivers:
  - name: console-only
`

type amStack struct {
	name string
	// the published port, and a client holding the control plane's
	// certificate for the relay and the readiness checks
	addr   string
	client *http.Client
	// the control plane's ca, certificate and key, mounted at amClientDir
	clientDir string
}

func alertmanagerImage(t testing.TB) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(), "infra", "ansible", "roles", "observability", "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := alertmanagerImageLine.FindSubmatch(b)
	if m == nil {
		t.Fatal("the observability role defaults pin no alertmanager image")
	}
	return string(m[1])
}

// writeFiles writes files readable by the containers' own users (nobody in
// the alertmanager image, nonroot in the control plane's): test material of
// a throwaway ca, removed with the run
func writeFiles(t testing.TB, files map[string][]byte) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sa-ce-am-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// StartAlertmanager runs one alertmanager member; StartControlPlane points
// the control plane at it.
func (s *Stack) StartAlertmanager(t testing.TB) {
	t.Helper()
	ca := tlsca.New(t, "sa-ce-internal-ca")
	server := ca.Issue(t, alertmanagerAlias, alertmanagerAlias, "127.0.0.1")
	client := ca.Issue(t, "controlplane", "controlplane")

	tlsDir := writeFiles(t, map[string][]byte{
		"ca.crt": ca.CertPEM, "node.crt": server.CertPEM, "node.key": server.KeyPEM,
	})
	confDir := writeFiles(t, map[string][]byte{"alertmanager.yml": []byte(amConfig), "web-config.yml": []byte(amWebConfig)})
	clientDir := writeFiles(t, map[string][]byte{
		"ca.crt": ca.CertPEM, "node.crt": client.CertPEM, "node.key": client.KeyPEM,
	})

	port := freePort(t)
	s.AM = &amStack{name: "sa-ce-alertmanager-" + s.ID, addr: "127.0.0.1:" + port, clientDir: clientDir}
	s.run(t, s.AM.name, []string{
		"--network-alias", alertmanagerAlias, "-p", s.AM.addr + ":9093",
		"-v", confDir + ":/etc/alertmanager:ro", "-v", tlsDir + ":/etc/sa-tls:ro",
	}, alertmanagerImage(t),
		"--config.file=/etc/alertmanager/alertmanager.yml", "--storage.path=/alertmanager",
		"--web.listen-address=0.0.0.0:9093", "--web.config.file=/etc/alertmanager/web-config.yml",
		// one member: no cluster
		"--cluster.listen-address=")

	pair, err := tls.X509KeyPair(client.CertPEM, client.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.CertPEM)
	s.AM.client = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs: roots, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13,
		}},
	}
	if err := s.waitAlertmanager(time.Minute); err != nil {
		t.Fatal(err)
	}
}

// alertmanagerEnv is the control plane's side: the member's address on the
// stack network and the client certificate, as roles/core sets them.
func (s *Stack) alertmanagerEnv() map[string]string {
	return map[string]string{
		"ALERTMANAGER_URLS":     "https://" + alertmanagerAlias + ":9093",
		"ALERTMANAGER_TLS_CA":   amClientDir + "/ca.crt",
		"ALERTMANAGER_TLS_CERT": amClientDir + "/node.crt",
		"ALERTMANAGER_TLS_KEY":  amClientDir + "/node.key",
	}
}

func (s *Stack) waitAlertmanager(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		resp, err := s.AM.client.Get("https://" + s.AM.addr + "/-/ready")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			err = fmt.Errorf("/-/ready answered %d", resp.StatusCode)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("alertmanager not ready: %w", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// postAlerts hands a postableAlerts body to alertmanager's v2 api with the
// client certificate, the way prometheus delivers alerts.
func (s *Stack) postAlerts(body []byte) error {
	resp, err := s.AM.client.Post("https://"+s.AM.addr+"/api/v2/alerts", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("alertmanager answered %d: %s", resp.StatusCode, b)
	}
	return nil
}

// stopAlertmanager and startAlertmanager let a spec see the page while no
// member answers. a restarted member has lost its alerts: the specs post
// again what they need.
func (s *Stack) stopAlertmanager() error {
	_, err := docker("stop", "-t", "5", s.AM.name)
	return err
}

func (s *Stack) startAlertmanager() error {
	if _, err := docker("start", s.AM.name); err != nil {
		return err
	}
	return s.waitAlertmanager(time.Minute)
}

// alertmanagerRoutes are the AppsControl routes of the alerts spec.
func (s *Stack) alertmanagerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /alertmanager/alerts", func(w http.ResponseWriter, req *http.Request) {
		b, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.postAlerts(b); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /alertmanager/stop", func(w http.ResponseWriter, _ *http.Request) {
		if err := s.stopAlertmanager(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /alertmanager/start", func(w http.ResponseWriter, _ *http.Request) {
		if err := s.startAlertmanager(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
