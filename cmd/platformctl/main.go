// Command platformctl runs operator tasks against the platform's dependencies.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/obs"
	"github.com/justmert/veristar/internal/umami"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: platformctl <command>

commands:
  umami bootstrap   replace the seeded umami admin password with UMAMI_ADMIN_PASSWORD
  umami rotate-admin
                    replace the umami admin password UMAMI_ADMIN_PASSWORD_PREVIOUS with
                    UMAMI_ADMIN_PASSWORD and prove the previous one is refused
` + dbUsage + natsUsage + dlqUsage + forwardUsage + probeUsage + scfUsage + verifyUsage + rotationUsage + reportUsage + `  version           print the build version
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	log := obs.NewLogger(stderr, slog.LevelInfo)
	switch {
	case len(args) == 1 && args[0] == "version":
		_, _ = fmt.Fprintln(stdout, buildVersion())
		return 0
	case len(args) == 2 && args[0] == "umami" && args[1] == "bootstrap":
		if err := umamiBootstrap(ctx, log); err != nil {
			log.Error("umami bootstrap failed", "error", err.Error())
			return 1
		}
		return 0
	case len(args) == 2 && args[0] == "umami" && args[1] == "rotate-admin":
		if err := umamiRotateAdmin(ctx, log); err != nil {
			log.Error("umami rotate-admin failed", "error", err.Error())
			return 1
		}
		return 0
	case len(args) >= 1 && args[0] == "db":
		if err := runDB(ctx, args[1:]); err != nil {
			log.Error("db command failed", "error", err.Error())
			return 1
		}
		return 0
	case len(args) >= 1 && args[0] == "nats":
		if err := runNATS(ctx, args[1:]); err != nil {
			log.Error("nats command failed", "error", err.Error())
			return 1
		}
		return 0
	case len(args) >= 1 && args[0] == "dlq":
		if err := runDLQ(ctx, args[1:]); err != nil {
			log.Error("dlq command failed", "error", err.Error())
			return 1
		}
		return 0
	case len(args) >= 1 && args[0] == "queue":
		if err := runForward(ctx, args[1:]); err != nil {
			log.Error("queue command failed", "error", err.Error())
			return 1
		}
		return 0
	case len(args) >= 1 && args[0] == "probe-tenant":
		if err := runProbeTenant(ctx, args[1:], config.FromEnviron(), stdout); err != nil {
			log.Error("probe-tenant command failed", "error", err.Error())
			return 1
		}
		return 0
	case len(args) >= 1 && args[0] == "scf":
		if err := runSCF(ctx, args[1:], config.FromEnviron(), stdout); err != nil {
			log.Error("scf command failed", "error", err.Error())
			return 1
		}
		return 0
	case len(args) >= 1 && args[0] == "verify-migration":
		err := runVerifyMigration(ctx, args[1:], stdout)
		if errors.Is(err, errDiffers) {
			return 1
		}
		if err != nil {
			log.Error("verify-migration failed", "error", err.Error())
			return 2
		}
		return 0
	case len(args) == 1 && args[0] == "rotation-check":
		err := runRotationCheck(ctx, os.Stdin, stdout)
		if errors.Is(err, errDiffers) {
			return 1
		}
		if err != nil {
			log.Error("rotation-check failed", "error", err.Error())
			return 2
		}
		return 0
	case len(args) >= 1 && args[0] == "report":
		err := runReport(ctx, args[1:], stdout)
		if errors.Is(err, errNotValid) {
			return 1
		}
		if err != nil {
			log.Error("report command failed", "error", err.Error())
			return 2
		}
		return 0
	case len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help"):
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
}

// buildVersion prefers the ldflags value and adds the vcs revision when the
// go toolchain recorded one.
func buildVersion() string {
	v := version
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				v += " " + s.Value
			}
		}
	}
	return v
}

// bootstrapTimeout covers a cold umami that is still applying migrations
// behind its heartbeat.
const bootstrapTimeout = 2 * time.Minute

func umamiBootstrap(ctx context.Context, log *slog.Logger) error {
	cfg, err := config.LoadUmamiAdmin(config.FromEnviron())
	if err != nil {
		return err
	}
	c, err := umami.New(cfg.URL, nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, bootstrapTimeout)
	defer cancel()

	outcome, err := umami.Bootstrap(ctx, c, cfg.Password.Reveal())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("umami at %s did not answer within %s: %w", cfg.URL.Redacted(), bootstrapTimeout, err)
		}
		return err
	}
	log.Info("umami admin password in place",
		"outcome", string(outcome),
		"umami_url", cfg.URL.Redacted(),
		"seeded_login", "rejected",
	)
	return nil
}

func umamiRotateAdmin(ctx context.Context, log *slog.Logger) error {
	env := config.FromEnviron()
	cfg, err := config.LoadUmamiAdmin(env)
	prev := env.Secret("UMAMI_ADMIN_PASSWORD_PREVIOUS", 1)
	if err := errors.Join(err, env.Err()); err != nil {
		return err
	}
	c, err := umami.New(cfg.URL, nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, bootstrapTimeout)
	defer cancel()
	outcome, err := umami.RotateAdmin(ctx, c, prev.Reveal(), cfg.Password.Reveal())
	if err != nil {
		return err
	}
	log.Info("umami admin password in place",
		"outcome", string(outcome),
		"umami_url", cfg.URL.Redacted(),
		"previous_login", "rejected",
	)
	return nil
}
