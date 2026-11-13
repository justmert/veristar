package config

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/justmert/veristar/tests/fixtures/tlsca"
)

func tlsFiles(t *testing.T) (ca, cert, key string) {
	t.Helper()
	a := tlsca.New(t, "test-ca")
	return tlsca.Files(t, t.TempDir(), a, a.Issue(t, "node", "127.0.0.1", "localhost"))
}

func TestLoadNATSWithTLS(t *testing.T) {
	ca, cert, key := tlsFiles(t)
	c, err := LoadNATS(FromMap(map[string]string{
		"NATS_URL": "tls://10.0.0.1:4222,nats://10.0.0.2:4222", "NATS_REPLICAS": "3",
		"NATS_TLS_CA": ca, "NATS_TLS_CERT": cert, "NATS_TLS_KEY": key,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.TLS == nil || c.TLS.CAFile != ca || c.TLS.CertFile != cert {
		t.Fatalf("tls files not loaded: %+v", c.TLS)
	}
	tc, err := c.TLS.ClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	if tc.MinVersion != tls.VersionTLS13 || len(tc.Certificates) != 1 || tc.RootCAs == nil || tc.InsecureSkipVerify {
		t.Fatalf("unexpected client config %+v", tc)
	}

	// a ca alone is enough: the servers ask no client certificate of nats
	// clients, which authenticate with their user and password
	c, err = LoadNATS(FromMap(map[string]string{"NATS_URL": "nats://a:4222", "NATS_REPLICAS": "1", "NATS_TLS_CA": ca}))
	if err != nil || c.TLS == nil {
		t.Fatalf("ca only: %v", err)
	}
	if tc, _ := c.TLS.ClientConfig(); len(tc.Certificates) != 0 {
		t.Fatal("no client certificate was configured")
	}

	// without any key, nats runs without tls (the dev server only)
	c, err = LoadNATS(FromMap(map[string]string{"NATS_URL": "nats://a:4222", "NATS_REPLICAS": "1"}))
	if err != nil || c.TLS != nil {
		t.Fatalf("no tls expected: %+v %v", c.TLS, err)
	}
}

func TestLoadNATSRefusesBrokenTLS(t *testing.T) {
	ca, cert, key := tlsFiles(t)
	notPEM := filepath.Join(t.TempDir(), "x.crt")
	if err := os.WriteFile(notPEM, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := tlsca.New(t, "other")
	_, otherCert, _ := tlsca.Files(t, t.TempDir(), other, other.Issue(t, "x", "x"))
	base := map[string]string{"NATS_URL": "nats://a:4222", "NATS_REPLICAS": "1"}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	for name, tc := range map[string]struct {
		env  map[string]string
		key  string
		text string
	}{
		"tls url without ca":     {with("NATS_URL", "tls://a:4222"), "NATS_TLS_CA", "tls://"},
		"cert without ca":        {with("NATS_TLS_CERT", cert, "NATS_TLS_KEY", key), "NATS_TLS_CA", "required"},
		"cert without key":       {with("NATS_TLS_CA", ca, "NATS_TLS_CERT", cert), "NATS_TLS_KEY", "required"},
		"key without cert":       {with("NATS_TLS_CA", ca, "NATS_TLS_KEY", key), "NATS_TLS_CERT", "required"},
		"missing ca file":        {with("NATS_TLS_CA", filepath.Join(t.TempDir(), "none.crt")), "NATS_TLS_CA", "cannot be read"},
		"ca file is not pem":     {with("NATS_TLS_CA", notPEM), "NATS_TLS_CA", "no pem certificate"},
		"cert and key mismatch":  {with("NATS_TLS_CA", ca, "NATS_TLS_CERT", otherCert, "NATS_TLS_KEY", key), "NATS_TLS_CERT", "not a usable"},
		"cert file is not a pem": {with("NATS_TLS_CA", ca, "NATS_TLS_CERT", notPEM, "NATS_TLS_KEY", key), "NATS_TLS_CERT", "not a usable"},
	} {
		_, err := LoadNATS(FromMap(tc.env))
		if err == nil || !strings.Contains(err.Error(), tc.key) || !strings.Contains(err.Error(), tc.text) {
			t.Errorf("%s: expected an error naming %s with %q, got %v", name, tc.key, tc.text, err)
		}
	}
}

func TestLoadPostgresTLS(t *testing.T) {
	ca, cert, key := tlsFiles(t)
	e := FromMap(map[string]string{"PLATFORM_DB_SSLROOTCERT": ca, "PLATFORM_DB_SSLCERT": cert, "PLATFORM_DB_SSLKEY": key})
	got := LoadPostgresTLS(e, "verify-full")
	if err := e.Err(); err != nil || got == nil {
		t.Fatalf("verify-full with a ca: %v", err)
	}
	if p := got.PGParams(); len(p) != 3 || p[0] != [2]string{"sslrootcert", ca} || p[1] != [2]string{"sslcert", cert} || p[2] != [2]string{"sslkey", key} {
		t.Fatalf("unexpected params %v", p)
	}
	if (*TLSFiles)(nil).PGParams() != nil {
		t.Fatal("no params expected without tls files")
	}

	// a configured ca never runs with a mode that skips it or the name check
	for _, mode := range []string{"disable", "require", "verify-ca"} {
		e := FromMap(map[string]string{"PLATFORM_DB_SSLROOTCERT": ca})
		LoadPostgresTLS(e, mode)
		if err := e.Err(); err == nil || !strings.Contains(err.Error(), "PLATFORM_DB_SSLMODE must be verify-full") {
			t.Errorf("%s with a ca: expected a refusal, got %v", mode, err)
		}
	}
	// verifying modes name their trust anchor explicitly
	for _, mode := range []string{"verify-ca", "verify-full"} {
		e := FromMap(map[string]string{})
		LoadPostgresTLS(e, mode)
		if err := e.Err(); err == nil || !strings.Contains(err.Error(), "PLATFORM_DB_SSLROOTCERT is required") {
			t.Errorf("%s without a ca: expected a refusal, got %v", mode, err)
		}
	}
	// disable without files stays the dev stack's setting
	e = FromMap(map[string]string{})
	if LoadPostgresTLS(e, "disable") != nil || e.Err() != nil {
		t.Fatal("disable without files must load")
	}
}
