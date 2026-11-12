package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/obs"
)

// natsUsage lists the nats subcommands for the main usage text.
const natsUsage = `  nats apply        create or update every nats stream, bucket and consumer (NATS_URL, NATS_REPLICAS)
  nats check        report differences between the server and the topology, change nothing
`

// natsTimeout bounds one apply. a 3-replica stream create waits for a raft
// group to form, which takes seconds, not minutes.
const natsTimeout = time.Minute

// runNATS runs `platformctl nats <subcommand>`; args are the words after
// "nats". it connects as the platformctl user (NATS_USER, NATS_PASSWORD when
// the server requires auth).
func runNATS(ctx context.Context, args []string) error {
	return runNATSWith(ctx, args, config.FromEnviron(), obs.NewLogger(os.Stderr, slog.LevelInfo))
}

func runNATSWith(ctx context.Context, args []string, env *config.Env, log *slog.Logger) error {
	if len(args) != 1 || (args[0] != "apply" && args[0] != "check") {
		return fmt.Errorf("usage: platformctl nats apply|check, got %q", strings.Join(args, " "))
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
