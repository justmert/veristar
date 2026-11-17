package systemdunits

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func pemCert(t *testing.T, cn string, notAfter time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    notAfter.Add(-48 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// the internal certificates are renewed only when an operator runs
// playbooks/internal-ca.yml, so sa-tls-expiry (roles/observability
// tasks/node.yml) writes their notAfter for InternalCertificateExpiringSoon:
// the node certificate, and the earliest ca of a bundle that holds a retired
// ca beside the new one during a rotation.
func TestTLSExpiryWriter(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl is not installed")
	}
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "infra", "ansible", "roles", "observability", "tasks", "node.yml"))
	require.NoError(t, err)
	var tasks []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &tasks))
	var script string
	for _, task := range tasks {
		if c, ok := task["ansible.builtin.copy"].(map[string]any); ok && c["dest"] == "/usr/local/sbin/sa-tls-expiry" {
			script, _ = c["content"].(string)
		}
	}
	require.NotEmpty(t, script, "the sa-tls-expiry writer")

	dir := t.TempDir()
	tls := filepath.Join(dir, "tls")
	for _, d := range []string{"textfile", "tls/ca", "tls/root"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, d), 0o755))
	}
	nodeEnd := time.Date(2027, 11, 13, 18, 32, 0, 0, time.UTC)
	oldCA := time.Date(2028, 3, 1, 0, 0, 0, 0, time.UTC)
	newCA := time.Date(2036, 11, 10, 0, 0, 0, 0, time.UTC)
	require.NoError(t, os.WriteFile(filepath.Join(tls, "root", "node.crt"), pemCert(t, "node-1", nodeEnd), 0o644))
	bundle := append(pemCert(t, "new ca", newCA), pemCert(t, "old ca", oldCA)...)
	require.NoError(t, os.WriteFile(filepath.Join(tls, "ca", "ca.crt"), bundle, 0o644))

	script = strings.ReplaceAll(script, "{{ observability_conf_dir }}", dir)
	script = strings.ReplaceAll(script, "{{ sa_tls_dir }}", tls)
	require.NotContains(t, script, "{{")
	writer := filepath.Join(dir, "sa-tls-expiry")
	require.NoError(t, os.WriteFile(writer, []byte(script), 0o755))
	text, err := exec.Command("/bin/sh", writer).CombinedOutput()
	require.NoError(t, err, "%s", text)

	out, err := os.ReadFile(filepath.Join(dir, "textfile", "tls.prom"))
	require.NoError(t, err)
	var series []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(l, "#") {
			series = append(series, l)
		}
	}
	require.Equal(t, []string{
		fmt.Sprintf(`sa_tls_certificate_expiry_timestamp_seconds{cert="node"} %d`, nodeEnd.Unix()),
		fmt.Sprintf(`sa_tls_certificate_expiry_timestamp_seconds{cert="ca"} %d`, oldCA.Unix()),
	}, series)
	require.Contains(t, string(out), "# TYPE sa_tls_certificate_expiry_timestamp_seconds gauge")
	_, err = os.Stat(filepath.Join(dir, "textfile", "tls.prom.tmp"))
	require.True(t, os.IsNotExist(err), "no temporary file left")
}
