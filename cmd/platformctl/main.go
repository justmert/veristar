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

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/obs"
	"github.com/justmert/stellar-analytics/internal/umami"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: platformctl <command>

commands:
  umami bootstrap   replace the seeded umami admin password with UMAMI_ADMIN_PASSWORD
` + dbUsage + natsUsage + probeUsage + `  version           print the build version
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
	case len(args) >= 1 && args[0] == "probe-tenant":
		if err := runProbeTenant(ctx, args[1:], config.FromEnviron(), stdout); err != nil {
			log.Error("probe-tenant command failed", "error", err.Error())
			return 1
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
