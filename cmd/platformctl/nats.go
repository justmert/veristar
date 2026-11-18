package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
)

// natsUsage lists the nats subcommands for the main usage text.
const natsUsage = `  nats apply        create or update every nats stream, bucket and consumer (NATS_URL, NATS_REPLICAS)
  nats check        report differences between the server and the topology, change nothing
  nats retire-legacy-web [--wait 10m]
                    after an upgrade to partitioned web events, once every gateway runs
                    the new release: waits until the replayer delivered every event of
                    the old single web subject, then removes that subject and its consumer
                    (docs/deploy/upgrade.md)
  nats auth-config  print the nats server authorization block for every service user;
                    reads a json object {"<user>": "<bcrypt hash>"} on stdin
`

// natsTimeout bounds one apply. a 3-replica stream create waits for a raft
// group to form, which takes seconds, not minutes.
const natsTimeout = time.Minute

// runNATS runs `platformctl nats <subcommand>`; args are the words after
// "nats". it connects as the platformctl user (NATS_USER, NATS_PASSWORD when
// the server requires auth).
func runNATS(ctx context.Context, args []string) error {
	if slices.Equal(args, []string{"auth-config"}) {
		return natsAuthConfig(os.Stdin, os.Stdout)
	}
	return runNATSWith(ctx, args, config.FromEnviron(), obs.NewLogger(os.Stderr, slog.LevelInfo))
}

func runNATSWith(ctx context.Context, args []string, env *config.Env, log *slog.Logger) error {
	if len(args) >= 1 && args[0] == "retire-legacy-web" {
		return runRetireLegacyWeb(ctx, args[1:], env, log)
	}
	if len(args) != 1 || (args[0] != "apply" && args[0] != "check") {
		return fmt.Errorf("usage: platformctl nats apply|check|retire-legacy-web, got %q", strings.Join(args, " "))
	}
	cfg, err := config.LoadNATS(env)
	if err != nil {
		return err
	}
	spec, err := natsx.Topology(cfg.Replicas)
	if err != nil {
		return err
	}
	nc, err := natsx.Connect(cfg, natsx.ServicePlatformctl, log)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, natsTimeout)
	defer cancel()

	var changes []natsx.Change
	if args[0] == "apply" {
		changes, err = natsx.Apply(ctx, js, spec)
	} else {
		changes, err = natsx.Diff(ctx, js, spec)
	}
	for _, c := range changes {
		log.Info("nats object", "kind", c.Kind, "name", c.Name, "action", string(c.Action), "fields", strings.Join(c.Fields, ","))
	}
	if err != nil {
		return err
	}
	if args[0] == "check" {
		for _, c := range changes {
			if c.Action != natsx.ActionUnchanged {
				return errors.New("nats topology differs from the spec, run platformctl nats apply")
			}
		}
	}
	log.Info("nats topology in place", "replicas", cfg.Replicas, "objects", len(changes))
	return nil
}

// retirePoll is how often retire-legacy-web checks the legacy subject while
// the replayer drains it.
const retirePoll = 2 * time.Second

func runRetireLegacyWeb(ctx context.Context, args []string, env *config.Env, log *slog.Logger) error {
	fs := flag.NewFlagSet("nats retire-legacy-web", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	wait := fs.Duration("wait", 10*time.Minute, "how long to wait for the replayer to deliver the last legacy events")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("nats retire-legacy-web: %w", err)
	}
	if fs.NArg() != 0 || *wait < 0 {
		return fmt.Errorf("usage: platformctl nats retire-legacy-web [--wait 10m], got %q", strings.Join(args, " "))
	}
	cfg, err := config.LoadNATS(env)
	if err != nil {
		return err
	}
	nc, err := natsx.Connect(cfg, natsx.ServicePlatformctl, log)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, *wait+natsTimeout)
	defer cancel()
	st, err := natsx.RetireLegacyWeb(ctx, js, *wait, retirePoll)
	if err != nil {
		return err
	}
	log.Info("legacy web subject retired", "subject", natsx.WebEventsLegacy, "consumer", natsx.ConsumerReplayer, "left", st.Messages)
	return nil
}

// bcryptHash is the shape nats-server recognises as a bcrypt password entry.
// plain passwords are refused so a server config never carries a usable
// credential, only its hash.
var bcryptHash = regexp.MustCompile(`^\$2[abxy]?\$\d\d\$[./A-Za-z0-9]{53}$`)

// natsAuthConfig renders the nats authorization block from
// the users in internal/natsx/topology.go and one bcrypt hash per user, so
// the deployed server config and the permissions the code was tested with
// cannot drift apart. the ansible edge role feeds it from the sops file.
func natsAuthConfig(stdin io.Reader, stdout io.Writer) error {
	var hashes map[string]string
	dec := json.NewDecoder(io.LimitReader(stdin, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&hashes); err != nil {
		return fmt.Errorf("read {\"<user>\": \"<bcrypt hash>\"} from stdin: %w", err)
	}
	for name, h := range hashes {
		if !bcryptHash.MatchString(h) {
			return fmt.Errorf("credential for nats user %s is not a bcrypt hash", name)
		}
	}
	out, err := natsx.RenderAuthorization(natsx.Users(), hashes)
	if err != nil {
		return err
	}
	_, err = stdout.Write(out)
	return err
}
