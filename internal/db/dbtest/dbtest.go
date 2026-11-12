// Package dbtest starts a real postgresql server for tests, from the same
// image digest and init script as the dev stack (deploy/compose/dev.yml).
// only test files import it.
package dbtest

import (
	"bufio"
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

// Start runs the container. call Terminate when done.
func Start(ctx context.Context) (*Postgres, error) {
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

	c, err := testcontainers.Run(ctx, image,
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
	)
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

// Superuser connects to a database as the postgres superuser.
func (p *Postgres) Superuser(ctx context.Context, database string) (*pgx.Conn, error) {
	return pgx.Connect(ctx, fmt.Sprintf("host=%s port=%d dbname=%s user=postgres password=%s sslmode=disable",
		p.Host, p.Port, database, p.Env["PLATFORM_DB_ADMIN_PASSWORD"]))
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
