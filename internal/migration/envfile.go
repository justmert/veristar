package migration

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/config"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/umami"
)

// ParseEnv reads KEY=VALUE lines as the dev stack's .env file and sops
// dotenv files hold them: no quoting, no expansion, # starts a comment line.
// a repeated key is an error, because which value wins would be a guess.
func ParseEnv(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			return nil, fmt.Errorf("line %d is not KEY=VALUE", n)
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("line %d repeats %s", n, k)
		}
		out[k] = v
	}
	return out, sc.Err()
}

// ReadEnvFile parses an env file from disk.
func ReadEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // the operator names the file on the command line
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	m, err := ParseEnv(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Endpoint is how verify-migration reaches one environment. both databases
// are read as the admin login (PLATFORM_DB_ADMIN_USER), the umami database
// named by UMAMI_DB_NAME on the same server.
type Endpoint struct {
	Admin         pdb.AdminConfig
	UmamiDatabase string
	UmamiURL      config.UmamiAdmin
}

// LoadEndpoint reads PLATFORM_DB_HOST, PLATFORM_DB_PORT, PLATFORM_DB_NAME,
// PLATFORM_DB_SSLMODE, PLATFORM_DB_ADMIN_USER, PLATFORM_DB_ADMIN_PASSWORD,
// UMAMI_DB_NAME, UMAMI_URL and UMAMI_ADMIN_PASSWORD.
func LoadEndpoint(values map[string]string) (Endpoint, error) {
	e := config.FromMap(values)
	admin, aerr := pdb.LoadAdminConfig(e)
	name := e.String("UMAMI_DB_NAME")
	u, uerr := config.LoadUmamiAdmin(e)
	return Endpoint{Admin: admin, UmamiDatabase: name, UmamiURL: u}, errors.Join(aerr, uerr, e.Err())
}

// Open connects to both databases and builds the umami admin client.
// close releases the connections.
func Open(ctx context.Context, ep Endpoint, appName string) (side Side, closeFn func(), err error) {
	p, err := pdb.AdminConnect(ctx, ep.Admin, appName)
	if err != nil {
		return Side{}, nil, err
	}
	ucfg := ep.Admin
	ucfg.Server.Database = ep.UmamiDatabase
	u, err := pdb.AdminConnect(ctx, ucfg, appName)
	if err != nil {
		_ = p.Close(ctx)
		return Side{}, nil, err
	}
	closeFn = func() {
		_ = p.Close(context.WithoutCancel(ctx))
		_ = u.Close(context.WithoutCancel(ctx))
	}
	c, err := umami.New(ep.UmamiURL.URL, nil)
	if err == nil {
		var a *umami.Admin
		a, err = umami.NewAdmin(c, umami.SeededUsername, ep.UmamiURL.Password.Reveal())
		side = Side{Platform: p, Umami: u, Admin: a}
	}
	if err != nil {
		closeFn()
		return Side{}, nil, err
	}
	return side, closeFn, nil
}

// Versions are the schema versions of both databases. a migration between
// two releases with different migrations is refused before any copy.
type Versions struct {
	Platform int64    `json:"platform_goose_version"`
	Umami    []string `json:"umami_prisma_migrations"`
}

// ReadVersions reads the platform goose version and umami's finished prisma
// migrations.
func ReadVersions(ctx context.Context, side Side) (Versions, error) {
	var v Versions
	if err := side.Platform.QueryRow(ctx,
		`select coalesce(max(version_id), 0) from goose_db_version where is_applied`).Scan(&v.Platform); err != nil {
		return v, fmt.Errorf("platform goose version: %w", err)
	}
	rows, err := side.Umami.Query(ctx,
		`select migration_name from _prisma_migrations where finished_at is not null and rolled_back_at is null order by 1`)
	if err != nil {
		return v, fmt.Errorf("umami prisma migrations: %w", err)
	}
	v.Umami, err = pgx.CollectRows(rows, pgx.RowTo[string])
	return v, err
}
