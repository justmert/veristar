package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/justmert/stellar-analytics/db"
	"github.com/justmert/stellar-analytics/internal/config"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/obs"
)

// dbUsage lists the db subcommands for the main usage text.
const dbUsage = `  db migrate        apply platform database migrations and check row-level security coverage
  db roles          set login passwords of the application roles from PLATFORM_DB_<ROLE>_PASSWORD
`

// runDB runs `platformctl db <subcommand>`; args are the words after "db".
//
//	db migrate  needs PLATFORM_DB_HOST, PLATFORM_DB_PORT, PLATFORM_DB_NAME,
//	            PLATFORM_DB_SSLMODE, PLATFORM_DB_ADMIN_USER, PLATFORM_DB_ADMIN_PASSWORD
//	db roles    needs the same plus PLATFORM_DB_CP_SYSTEM_PASSWORD,
//	            PLATFORM_DB_CP_TENANT_PASSWORD, PLATFORM_DB_CP_OPERATOR_PASSWORD,
//	            PLATFORM_DB_METRICS_WRITER_PASSWORD, PLATFORM_DB_JOBS_PASSWORD
//
// migrate creates the roles without passwords, so run migrate before roles.
// both are idempotent.
func runDB(ctx context.Context, args []string) error {
	return runDBWith(ctx, args, config.FromEnviron(), obs.NewLogger(os.Stderr, slog.LevelInfo))
}

func runDBWith(ctx context.Context, args []string, env *config.Env, log *slog.Logger) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: platformctl db migrate|roles, got %q", strings.Join(args, " "))
	}
	switch args[0] {
	case "migrate":
		cfg, err := pdb.LoadAdminConfig(env)
		if err != nil {
			return err
		}
		res, err := pdb.Migrate(ctx, cfg, db.Migrations(), log)
		if err != nil {
			return err
		}
		log.Info("platform database migrated",
			"database", cfg.Server.Database,
			"applied", len(res.Applied),
			"version", res.Version,
			"rls_coverage", "ok",
		)
		return nil
	case "roles":
		cfg, err := pdb.LoadRolesConfig(env)
		if err != nil {
			return err
		}
		if err := pdb.SetRolePasswords(ctx, cfg); err != nil {
			return err
		}
		names := make([]string, 0, len(cfg.Roles))
		for _, l := range cfg.Roles {
			names = append(names, l.User)
		}
		log.Info("application role logins set and verified",
			"database", cfg.Server.Database,
			"roles", strings.Join(names, ","),
		)
		return nil
	default:
		return fmt.Errorf("usage: platformctl db migrate|roles, unknown subcommand %q", args[0])
	}
}
