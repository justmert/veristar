package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
)

// forwardUsage lists the queue forward subcommand for the main usage text.
const forwardUsage = `  queue forward [--queues web,api,stats] [--until-empty] [--idle 2m] [--batch 100]
                    moves the queued messages of this environment's WEB_EVENTS,
                    API_METRICS and EDGE_STATS to another environment's streams
                    (TARGET_NATS_URL, TARGET_NATS_USER, TARGET_NATS_PASSWORD and the
                    TARGET_NATS_TLS_* files) during a
                    handover cutover (docs/handover/plan.md section 2.3). it binds the
                    consumers of the stopped replayer, metricswriter and jobs and acks each
                    message only after the target stream acknowledged it
`

// forwardQueue is one work-queue stream and the durable consumers whose
// service is stopped while its messages are forwarded. web has one
// consumer per partition and, while an upgrade drains it, the legacy one;
// a consumer listed as optional may be missing.
type forwardQueue struct {
	stream    string
	consumers []string
	optional  string
}

var forwardQueues = map[string]forwardQueue{
	"web":   {stream: natsx.StreamWebEvents, consumers: natsx.ReplayerConsumers(), optional: natsx.ConsumerReplayer},
	"api":   {stream: natsx.StreamAPIMetrics, consumers: []string{natsx.ConsumerMetricsWriter}},
	"stats": {stream: natsx.StreamEdgeStats, consumers: []string{natsx.ConsumerJobs}},
}

// forwardSource is one bound consumer of a queue.
type forwardSource struct {
	queue, name string
	cons        jetstream.Consumer
}

// forwardIdlePoll is the pause after a round that found every queue empty.
const forwardIdlePoll = 500 * time.Millisecond

// errConsumerBusy means a service still pulls from a consumer the forwarder
// would bind: the two would split the queue between two environments.
var errConsumerBusy = errors.New("queue forward: a client is still pulling")

func runForward(ctx context.Context, args []string) error {
	target := map[string]string{}
	for _, k := range []string{"NATS_URL", "NATS_USER", "NATS_PASSWORD", "NATS_TLS_CA", "NATS_TLS_CERT", "NATS_TLS_KEY"} {
		if v, ok := os.LookupEnv("TARGET_" + k); ok {
			target[k] = v
		}
	}
	return runForwardWith(ctx, args, config.FromEnviron(), config.FromMap(target), os.Stdout, obs.NewLogger(os.Stderr, slog.LevelInfo))
}

// forwardTotals counts the messages moved per queue.
type forwardTotals map[string]int

func (t forwardTotals) String() string {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, t[k]))
	}
	return strings.Join(parts, " ")
}

func runForwardWith(ctx context.Context, args []string, srcEnv, dstEnv *config.Env, stdout io.Writer, log *slog.Logger) error {
	if len(args) == 0 || args[0] != "forward" {
		return errors.New("usage: platformctl queue forward [--queues web,api,stats] [--until-empty] [--idle 2m]")
	}
	fs := flag.NewFlagSet("queue forward", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	queues := fs.String("queues", "web,api,stats", "which work queues to move")
	untilEmpty := fs.Bool("until-empty", false, "stop once every queue stayed empty for --idle")
	idle := fs.Duration("idle", 2*time.Minute, "how long the queues must stay empty before --until-empty stops")
	batch := fs.Int("batch", 100, "messages per fetch")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("queue forward: %w", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("queue forward: unexpected arguments %q", fs.Args())
	}
	if *batch < 1 || *batch > 1000 {
		return fmt.Errorf("queue forward: --batch must be 1 to 1000, got %d", *batch)
	}
	var selected []string
	for q := range strings.SplitSeq(*queues, ",") {
		if _, ok := forwardQueues[q]; !ok {
			return fmt.Errorf("queue forward: unknown queue %q (web, api, stats)", q)
		}
		selected = append(selected, q)
	}

	srcCfg, err := config.LoadNATS(srcEnv)
	if err != nil {
		return err
	}
	dstCfg, err := config.LoadNATS(dstEnv)
	if err != nil {
		return fmt.Errorf("queue forward: target (TARGET_NATS_*): %w", err)
	}
	if len(dstCfg.URLs) == 0 {
		return errors.New("queue forward: set TARGET_NATS_URL, TARGET_NATS_USER and TARGET_NATS_PASSWORD")
	}
	// the same servers on both sides would ack messages into their own
	// stream, which is a loop and not a move
	for _, u := range dstCfg.URLs {
		if slices.Contains(srcCfg.URLs, u) {
			return fmt.Errorf("queue forward: target %s is also a source server", u)
		}
	}

	srcNC, err := natsx.Connect(srcCfg, natsx.ServicePlatformctl, log)
	if err != nil {
		return err
	}
	defer srcNC.Close()
	dstNC, err := natsx.Connect(dstCfg, natsx.ServicePlatformctl, log)
	if err != nil {
		return fmt.Errorf("queue forward: target: %w", err)
	}
	defer dstNC.Close()
	src, err := jetstream.New(srcNC)
	if err != nil {
		return err
	}
	dst, err := jetstream.New(dstNC)
	if err != nil {
		return err
	}

	var sources []forwardSource
	for _, q := range selected {
		fq := forwardQueues[q]
		for _, name := range fq.consumers {
			c, err := src.Consumer(ctx, fq.stream, name)
			if name == fq.optional && errors.Is(err, jetstream.ErrConsumerNotFound) {
				continue
			}
			if err != nil {
				return fmt.Errorf("queue forward: bind %s/%s: %w", fq.stream, name, err)
			}
			info, err := c.Info(ctx)
			if err != nil {
				return fmt.Errorf("queue forward: %s/%s info: %w", fq.stream, name, err)
			}
			// a waiting pull request means the service still runs
			if info.NumWaiting > 0 {
				return fmt.Errorf("%w on %s/%s; stop the %s service in the source environment first", errConsumerBusy, fq.stream, name, fq.consumers[0])
			}
			sources = append(sources, forwardSource{queue: q, name: name, cons: c})
		}
		if _, err := dst.Stream(ctx, fq.stream); err != nil {
			return fmt.Errorf("queue forward: target stream %s: %w", fq.stream, err)
		}
	}

	totals := forwardTotals{}
	lastMove := time.Now()
	for {
		moved := 0
		for _, fs := range sources {
			n, err := forwardBatch(ctx, fs.cons, dst, forwardQueues[fs.queue].stream, *batch)
			totals[fs.queue] += n
			moved += n
			if err != nil {
				_, _ = fmt.Fprintf(stdout, "forwarded %s\n", totals)
				return err
			}
		}
		if moved > 0 {
			lastMove = time.Now()
			log.Info("queue forward progress", "totals", totals.String())
			continue
		}
		if ctx.Err() != nil {
			_, _ = fmt.Fprintf(stdout, "forwarded %s (stopped)\n", totals)
			return nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(forwardIdlePoll):
		}
		if *untilEmpty && time.Since(lastMove) >= *idle {
			empty, err := forwardEmpty(ctx, sources)
			if err != nil {
				return err
			}
			if empty {
				_, _ = fmt.Fprintf(stdout, "forwarded %s; every queue empty for %s\n", totals, *idle)
				return nil
			}
		}
	}
}

// forwardBatch moves up to n messages of one consumer. each message is
// published to the target with its headers and body unchanged, so the
// target stream deduplicates on the same Nats-Msg-Id the source used and a
// message forwarded twice (a crash between the target's ack and the source
// ack) is stored once. the subject is unchanged too, except that a web event
// queued on the legacy subject goes to its partition, which the target
// stream always has. the source message is acked only after the
// target acknowledged it; on a failure it is handed back at once.
func forwardBatch(ctx context.Context, c jetstream.Consumer, dst jetstream.JetStream, stream string, n int) (int, error) {
	// no long poll: web alone has one consumer per partition, and a second
	// of waiting on each empty one would hold back the others
	b, err := c.FetchNoWait(n)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil
		}
		return 0, fmt.Errorf("queue forward: fetch %s: %w", stream, err)
	}
	moved := 0
	for m := range b.Messages() {
		msg := &nats.Msg{Subject: forwardSubject(stream, m.Subject(), m.Data()), Header: m.Headers(), Data: m.Data()}
		if _, err := dst.PublishMsg(ctx, msg, jetstream.WithExpectStream(stream)); err != nil {
			_ = m.Nak()
			return moved, fmt.Errorf("queue forward: publish to target %s: %w", stream, err)
		}
		if err := m.DoubleAck(ctx); err != nil {
			return moved, fmt.Errorf("queue forward: ack %s in the source (the target holds it; a rerun deduplicates): %w", stream, err)
		}
		moved++
	}
	if err := b.Error(); err != nil && !errors.Is(err, nats.ErrTimeout) && ctx.Err() == nil {
		return moved, fmt.Errorf("queue forward: fetch %s: %w", stream, err)
	}
	return moved, nil
}

// forwardSubject is the target subject of a forwarded message.
func forwardSubject(stream, subject string, data []byte) string {
	if stream != natsx.StreamWebEvents || strings.Count(subject, ".") != strings.Count(natsx.SubjectWebEvents, ".")+1 {
		return subject
	}
	if ev, err := events.DecodeWebEvent(data); err == nil && ev.LegacySubject() == subject {
		return ev.Subject()
	}
	return subject
}

// forwardEmpty reports whether no consumer has a message left to deliver
// or one delivered and not yet acked.
func forwardEmpty(ctx context.Context, sources []forwardSource) (bool, error) {
	for _, fs := range sources {
		info, err := fs.cons.Info(ctx)
		if err != nil {
			return false, fmt.Errorf("queue forward: %s info: %w", fs.name, err)
		}
		if info.NumPending > 0 || info.NumAckPending > 0 {
			return false, nil
		}
	}
	return true, nil
}
