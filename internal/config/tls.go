package config

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// TLSFiles names the files a client uses for one link of the private
// network: the internal ca that signs the server's certificate, and an
// optional client certificate and key for servers that ask for one. the
// files are checked when the configuration loads, so a missing or broken
// file stops the service at start instead of at its first connection.
type TLSFiles struct {
	CAFile   string
	CertFile string
	KeyFile  string

	roots *x509.CertPool
	cert  *tls.Certificate
}

// loadTLSFiles reads the three keys. it returns nil when none is set, which
// means the link runs without tls; the caller decides whether that is
// allowed. a certificate without its key (or the reverse), or a client
// certificate without a ca, is refused rather than half applied.
func loadTLSFiles(e *Env, caKey, certKey, keyKey string) *TLSFiles {
	ca, hasCA := e.raw(caKey)
	cert, hasCert := e.raw(certKey)
	key, hasKey := e.raw(keyKey)
	if !hasCA && !hasCert && !hasKey {
		return nil
	}
	t := &TLSFiles{CAFile: ca, CertFile: cert, KeyFile: key}
	switch {
	case !hasCA:
		e.fail(caKey, fmt.Sprintf("is required when %s or %s is set", certKey, keyKey))
		return t
	case hasCert && !hasKey:
		e.fail(keyKey, fmt.Sprintf("is required when %s is set", certKey))
		return t
	case hasKey && !hasCert:
		e.fail(certKey, fmt.Sprintf("is required when %s is set", keyKey))
		return t
	}
	pem, err := os.ReadFile(ca) //nolint:gosec // the path is the operator's own configuration
	if err != nil {
		e.fail(caKey, "names a file that cannot be read: "+pathError(err))
		return t
	}
	t.roots = x509.NewCertPool()
	if !t.roots.AppendCertsFromPEM(pem) {
		e.fail(caKey, "holds no pem certificate")
		return t
	}
	if hasCert {
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			e.fail(certKey, "and "+keyKey+" are not a usable certificate and key: "+pathError(err))
			return t
		}
		t.cert = &pair
	}
	return t
}

// pathError keeps the reason of a file error without repeating the path,
// which the key name already identifies.
func pathError(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// ClientConfig returns the tls settings of a client: only the internal ca
// is trusted (never the system roots), the server name is checked by the
// caller's library against the address it dials, and tls 1.3 is the
// minimum, which every server of the private network speaks.
func (t *TLSFiles) ClientConfig() (*tls.Config, error) {
	if t == nil || t.roots == nil {
		return nil, errors.New("config: tls files were not loaded")
	}
	c := &tls.Config{RootCAs: t.roots, MinVersion: tls.VersionTLS13}
	if t.cert != nil {
		c.Certificates = []tls.Certificate{*t.cert}
	}
	return c, nil
}

// LoadPostgresTLS reads PLATFORM_DB_SSLROOTCERT, PLATFORM_DB_SSLCERT and
// PLATFORM_DB_SSLKEY and checks them against sslmode, the value of
// PLATFORM_DB_SSLMODE:
//   - a ca makes verify-full mandatory. disable would ignore it and require
//     would encrypt without checking whom it talks to, so a configured ca
//     with either of them is refused, never quietly weakened.
//   - verify-ca and verify-full need the ca, so the trust anchor is always
//     the internal ca named here and never the system roots by omission.
func LoadPostgresTLS(e *Env, sslmode string) *TLSFiles {
	t := loadTLSFiles(e, "PLATFORM_DB_SSLROOTCERT", "PLATFORM_DB_SSLCERT", "PLATFORM_DB_SSLKEY")
	switch {
	case t != nil && sslmode != "" && sslmode != "verify-full":
		e.fail("PLATFORM_DB_SSLMODE", "must be verify-full when PLATFORM_DB_SSLROOTCERT is set")
	case t == nil && (sslmode == "verify-ca" || sslmode == "verify-full"):
		e.fail("PLATFORM_DB_SSLROOTCERT", "is required when PLATFORM_DB_SSLMODE is "+sslmode)
	}
	return t
}

// PGParams returns the libpq keywords of the files, for a keyword/value
// connection string; nil when no tls files are configured.
func (t *TLSFiles) PGParams() [][2]string {
	if t == nil {
		return nil
	}
	out := [][2]string{{"sslrootcert", t.CAFile}}
	if t.CertFile != "" {
		out = append(out, [2]string{"sslcert", t.CertFile}, [2]string{"sslkey", t.KeyFile})
	}
	return out
}
