// Package dbtest starts a real postgresql server for tests, from the same
// image digest and init script as the dev stack (deploy/compose/dev.yml).
// only test files import it.
package dbtest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/justmert/stellar-analytics/internal/config"
	pdb "github.com/justmert/stellar-analytics/internal/db"
)

// Postgres is a running server with the umami and platform databases that
// deploy/compose/postgres-init.sh creates.
type Postgres struct {
	Host string
	Port int
	// Env holds every PLATFORM_DB_* key, as platformctl would read it, for
	// the platform database: the postgres superuser as admin, and random
	// passwords for the application roles.
	Env       map[string]string
	container *testcontainers.DockerContainer
}

// ServerTLS makes the server speak tls only, as the core role configures
// patroni (roles/core): ssl on, tls 1.3 at least, and a pg_hba that admits
// hostssl lines only, so a plain text login is refused by the server. the
// pem values are the server's certificate and key; CAFile is the ca on the
// test's side, which Env then names with sslmode verify-full.
type ServerTLS struct {
	CertPEM, KeyPEM []byte
	CAFile          string
}

// Option customises the server.
type Option func(*options)

type options struct{ tls *ServerTLS }

// WithTLS starts the server with tls only.
func WithTLS(t ServerTLS) Option { return func(o *options) { o.tls = &t } }

// hostsslOnly runs after initdb: it replaces the image's pg_hba (one
// "host all all all" line) with hostssl lines, so the server itself refuses
// every connection that did not negotiate tls
const hostsslOnly = `#!/bin/sh
set -eu
cat >"$PGDATA/pg_hba.conf" <<'HBA'
local all all trust
hostssl all all all scram-sha-256
HBA
`

// tlsEntrypoint gives the key to the postgres user, which postgresql
// requires of a key file, then hands over to the image's entrypoint
const tlsEntrypoint = `set -eu
install -d -o postgres -g postgres -m 0700 /var/lib/postgresql/tls
install -o postgres -g postgres -m 0600 /tls-src/server.key /var/lib/postgresql/tls/server.key
install -o postgres -g postgres -m 0644 /tls-src/server.crt /var/lib/postgresql/tls/server.crt
exec docker-entrypoint.sh postgres -c ssl=on -c ssl_min_protocol_version=TLSv1.3 \
  -c ssl_cert_file=/var/lib/postgresql/tls/server.crt -c ssl_key_file=/var/lib/postgresql/tls/server.key`

// Start runs the container. call Terminate when done.
func Start(ctx context.Context, opts ...Option) (*Postgres, error) {
	var o options
	for _, f := range opts {
		f(&o)
	}
	root, err := repoRoot()
	if err != nil {
		return nil, err
	}
	image, err := devImage(filepath.Join(root, "deploy", "compose", "dev.yml"))
	if err != nil {
		return nil, err
	}
	superPW, err := randHex(24)
	if err != nil {
		return nil, err
	}
	umamiPW, err := randHex(24)
	if err != nil {
		return nil, err
	}

	custom := []testcontainers.ContainerCustomizer{}
	if o.tls != nil {
		custom = append(custom,
			testcontainers.WithFiles(
				testcontainers.ContainerFile{Reader: strings.NewReader(hostsslOnly), ContainerFilePath: "/docker-entrypoint-initdb.d/99-hostssl.sh", FileMode: 0o755},
				testcontainers.ContainerFile{Reader: bytes.NewReader(o.tls.CertPEM), ContainerFilePath: "/tls-src/server.crt", FileMode: 0o644},
				testcontainers.ContainerFile{Reader: bytes.NewReader(o.tls.KeyPEM), ContainerFilePath: "/tls-src/server.key", FileMode: 0o600},
			),
			testcontainers.WithEntrypoint("sh", "-c", tlsEntrypoint),
		)
	}
	c, err := testcontainers.Run(ctx, image, append([]testcontainers.ContainerCustomizer{
		testcontainers.WithEnv(map[string]string{
			"POSTGRES_USER":     "postgres",
			"POSTGRES_PASSWORD": superPW,
			"UMAMI_DB_PASSWORD": umamiPW,
			"TZ":                "UTC",
		}),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath:      filepath.Join(root, "deploy", "compose", "postgres-init.sh"),
			ContainerFilePath: "/docker-entrypoint-initdb.d/10-init.sh",
			FileMode:          0o755,
		}),
		testcontainers.WithExposedPorts("5432/tcp"),
		// the entrypoint starts a temporary server for the init scripts and
		// then the real one, so the ready line appears twice
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(2*time.Minute),
			wait.ForListeningPort("5432/tcp"),
		),
	}, custom...)...)
	if err != nil {
		if c != nil {
			_ = c.Terminate(context.WithoutCancel(ctx))
		}
		return nil, fmt.Errorf("dbtest: start postgres: %w", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(context.WithoutCancel(ctx))
		return nil, err
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		_ = c.Terminate(context.WithoutCancel(ctx))
		return nil, err
	}
	p := &Postgres{Host: host, Port: int(port.Num()), container: c}
	p.Env = map[string]string{
		"PLATFORM_DB_HOST":           host,
		"PLATFORM_DB_PORT":           fmt.Sprint(p.Port),
		"PLATFORM_DB_NAME":           "platform",
		"PLATFORM_DB_SSLMODE":        "disable",
		"PLATFORM_DB_ADMIN_USER":     "postgres",
		"PLATFORM_DB_ADMIN_PASSWORD": superPW,
	}
	if o.tls != nil {
		p.Env["PLATFORM_DB_SSLMODE"] = "verify-full"
		p.Env["PLATFORM_DB_SSLROOTCERT"] = o.tls.CAFile
	}
	for _, r := range pdb.AppRoles {
		pw, err := randHex(24)
		if err != nil {
			_ = c.Terminate(context.WithoutCancel(ctx))
			return nil, err
		}
		p.Env[r.PasswordKey()] = pw
	}
	return p, nil
}

// Terminate stops and removes the container.
func (p *Postgres) Terminate(ctx context.Context) error {
	return p.container.Terminate(ctx)
}

// EnvFor returns Env with PLATFORM_DB_NAME replaced.
func (p *Postgres) EnvFor(database string) map[string]string {
	out := make(map[string]string, len(p.Env))
	for k, v := range p.Env {
		out[k] = v
	}
	out["PLATFORM_DB_NAME"] = database
	return out
}

// Config returns a config.Env over EnvFor(database).
func (p *Postgres) Config(database string) *config.Env {
	return config.FromMap(p.EnvFor(database))
}

// Superuser connects to a database as the postgres superuser, with the
// server's tls settings when it has them.
func (p *Postgres) Superuser(ctx context.Context, database string) (*pgx.Conn, error) {
	s := fmt.Sprintf("host=%s port=%d dbname=%s user=postgres password=%s sslmode=%s",
		p.Host, p.Port, database, p.Env["PLATFORM_DB_ADMIN_PASSWORD"], p.Env["PLATFORM_DB_SSLMODE"])
	if ca := p.Env["PLATFORM_DB_SSLROOTCERT"]; ca != "" {
		s += " sslrootcert=" + ca
	}
	return pgx.Connect(ctx, s)
}

// CreateDatabase creates an empty database the way postgres-init.sh creates
// platform, so a test can migrate it from scratch.
func (p *Postgres) CreateDatabase(ctx context.Context, name string) error {
	c, err := p.Superuser(ctx, "postgres")
	if err != nil {
		return err
	}
	defer func() { _ = c.Close(context.WithoutCancel(ctx)) }()
	id := pgx.Identifier{name}.Sanitize()
	if _, err := c.Exec(ctx, "create database "+id); err != nil {
		return err
	}
	_, err = c.Exec(ctx, "revoke all on database "+id+" from public")
	return err
}

// repoRoot walks up from the working directory to the directory with go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("dbtest: go.mod not found above the working directory")
		}
		dir = parent
	}
}

// devImage reads the postgres image reference from the dev compose file, so
// tests can never drift from the pinned digest the dev stack uses.
func devImage(composeFile string) (string, error) {
	f, err := os.Open(composeFile) //nolint:gosec // fixed path inside the repository
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if ref, ok := strings.CutPrefix(line, "image: postgres:"); ok {
			if !strings.Contains(ref, "@sha256:") {
				return "", fmt.Errorf("dbtest: postgres image in %s is not pinned by digest", composeFile)
			}
			return "postgres:" + ref, nil
		}
	}
	if err := s.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("dbtest: no postgres image in %s", composeFile)
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
