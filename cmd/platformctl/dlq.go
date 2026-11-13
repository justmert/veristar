package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
)

// dlqUsage lists the dlq subcommands for the main usage text.
const dlqUsage = `  dlq list|replay|purge --queue web|api [--all] [--from SEQ] [--to SEQ] [--key ID] [--reason TEXT] [--yes]
                    inspect, re-publish or delete dead-lettered messages of WEB_DLQ or
                    API_DLQ (docs/runbooks/queue-backlog-and-dlq.md). list never prints
                    payloads; replay puts each message back on its original subject and
                    deletes it from the dlq after the stream acknowledged it; purge needs --yes
`

// HeaderDLQReplay names the dlq message a replayed message came from, so a
// message that is dead-lettered again can be traced to its earlier copy.
const HeaderDLQReplay = "Sa-Dlq-Replay"

// replaySuffix separates the original message id from the replay part of a
// replayed message's id.
const replaySuffix = ".replay."

// dlqTimeout bounds one dlq command. the dlq streams are small (messages
// that failed permanently), and each message is one request.
const dlqTimeout = 10 * time.Minute

// dlqQueue pairs a dlq stream with the work-queue stream its messages came
// from.
type dlqQueue struct {
	stream, subject             string
	targetStream, targetSubject string
}

var dlqQueues = map[string]dlqQueue{
	"web": {natsx.StreamWebDLQ, natsx.SubjectWebDLQ, natsx.StreamWebEvents, natsx.SubjectWebEvents},
	"api": {natsx.StreamAPIDLQ, natsx.SubjectAPIDLQ, natsx.StreamAPIMetrics, natsx.SubjectAPIMetrics},
}

// dlqSelect is which messages a command acts on. a destructive command needs
// an explicit selection, so a forgotten flag never means "everything".
type dlqSelect struct {
	all      bool
	from, to uint64
	key      string
	reason   string
}

func (s dlqSelect) explicit() bool {
	return s.all || s.from > 0 || s.to > 0 || s.key != "" || s.reason != ""
}

func (s dlqSelect) match(m *jetstream.RawStreamMsg) bool {
	if s.reason != "" && !strings.Contains(m.Header.Get(natsx.HeaderDLQReason), s.reason) {
		return false
	}
	return true
}

func runDLQ(ctx context.Context, args []string) error {
	return runDLQWith(ctx, args, config.FromEnviron(), os.Stdout, obs.NewLogger(os.Stderr, slog.LevelInfo))
}

func runDLQWith(ctx context.Context, args []string, env *config.Env, stdout io.Writer, log *slog.Logger) error {
	if len(args) == 0 || (args[0] != "list" && args[0] != "replay" && args[0] != "purge") {
		return errors.New("usage: platformctl dlq list|replay|purge --queue web|api [selection]")
	}
	cmd := args[0]
	fs := flag.NewFlagSet("dlq "+cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	queue := fs.String("queue", "", "web (WEB_DLQ) or api (API_DLQ)")
	var sel dlqSelect
	fs.BoolVar(&sel.all, "all", false, "every message")
	fs.Uint64Var(&sel.from, "from", 0, "first dlq sequence, inclusive")
	fs.Uint64Var(&sel.to, "to", 0, "last dlq sequence, inclusive")
	fs.StringVar(&sel.key, "key", "", "site id (web) or tenant id (api), the last subject token")
	fs.StringVar(&sel.reason, "reason", "", "only messages whose reason header contains this text")
	yes := fs.Bool("yes", false, "confirm purge")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("dlq %s: %w", cmd, err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("dlq %s: unexpected arguments %q", cmd, fs.Args())
	}
	q, ok := dlqQueues[*queue]
	if !ok {
		return fmt.Errorf("dlq %s: --queue must be web or api, got %q", cmd, *queue)
	}
	if sel.from > 0 && sel.to > 0 && sel.from > sel.to {
		return fmt.Errorf("dlq %s: --from %d is after --to %d", cmd, sel.from, sel.to)
	}
	if strings.ContainsAny(sel.key, ".*> ") {
		return fmt.Errorf("dlq %s: --key must be one subject token, got %q", cmd, sel.key)
	}
	if cmd != "list" && !sel.explicit() {
		return fmt.Errorf("dlq %s: select messages with --all, --from, --to, --key or --reason", cmd)
	}
	if cmd == "purge" && !*yes {
		return errors.New("dlq purge deletes messages for good: add --yes to confirm")
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
	ctx, cancel := context.WithTimeout(ctx, dlqTimeout)
	defer cancel()
	dlq, err := js.Stream(ctx, q.stream)
	if err != nil {
		return fmt.Errorf("dlq: bind %s: %w", q.stream, err)
	}

	switch cmd {
	case "list":
		return dlqList(ctx, dlq, q, sel, stdout)
	case "replay":
		return dlqReplay(ctx, js, dlq, q, sel, stdout)
	default:
		return dlqPurge(ctx, dlq, q, sel, stdout)
	}
}

// dlqEach calls fn for every selected message, in sequence order. it stops
// at the stream's last sequence when it started, so a message that a replay
// sends back to the dlq (the fault is still there) is not taken again in the
// same run.
func dlqEach(ctx context.Context, dlq jetstream.Stream, q dlqQueue, sel dlqSelect, fn func(*jetstream.RawStreamMsg) error) error {
	info, err := dlq.Info(ctx)
	if err != nil {
		return fmt.Errorf("dlq: %s info: %w", q.stream, err)
	}
	last := info.State.LastSeq
	if sel.to > 0 && sel.to < last {
		last = sel.to
	}
	filter := q.subject + ".*"
	if sel.key != "" {
		filter = natsx.Subject(q.subject, sel.key)
	}
	for seq := max(sel.from, 1); seq <= last; {
		m, err := dlq.GetMsg(ctx, seq, jetstream.WithGetMsgSubject(filter))
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("dlq: read %s: %w", q.stream, err)
		}
		if m.Sequence > last {
			return nil
		}
		if sel.match(m) {
			if err := fn(m); err != nil {
				return err
			}
		}
		clear(m.Data)
		seq = m.Sequence + 1
	}
	return nil
}

func dlqList(ctx context.Context, dlq jetstream.Stream, q dlqQueue, sel dlqSelect, stdout io.Writer) error {
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SEQ\tTIME\tKEY\tORIGIN\tBYTES\tREASON")
	n := 0
	err := dlqEach(ctx, dlq, q, sel, func(m *jetstream.RawStreamMsg) error {
		n++
		origin := m.Header.Get(natsx.HeaderDLQStream) + ":" + m.Header.Get(natsx.HeaderDLQStreamSeq) + " " + m.Header.Get(natsx.HeaderDLQSubject)
		// a reason is an upstream error text; tabs and newlines would break
		// the columns
		reason := strings.Join(strings.Fields(m.Header.Get(natsx.HeaderDLQReason)), " ")
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s\n", m.Sequence, m.Time.UTC().Format(time.RFC3339), lastToken(m.Subject), origin, len(m.Data), reason)
		return nil
	})
	if ferr := tw.Flush(); err == nil {
		err = ferr
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%d message(s) in %s\n", n, q.stream)
	return err
}

func dlqReplay(ctx context.Context, js jetstream.JetStream, dlq jetstream.Stream, q dlqQueue, sel dlqSelect, stdout io.Writer) error {
	var replayed, duplicates, skipped int
	err := dlqEach(ctx, dlq, q, sel, func(m *jetstream.RawStreamMsg) error {
		subject := m.Header.Get(natsx.HeaderDLQSubject)
		// only a concrete subject of the original stream is accepted, so a
		// damaged header cannot route a message anywhere else
		if !strings.HasPrefix(subject, q.targetSubject+".") || strings.ContainsAny(subject, "*> ") ||
			strings.Count(subject, ".") != strings.Count(q.targetSubject, ".")+1 {
			skipped++
			_, _ = fmt.Fprintf(stdout, "skip %d: original subject %q is not on %s\n", m.Sequence, subject, q.targetStream)
			return nil
		}
		h := nats.Header{}
		for k, v := range m.Header {
			h[k] = append([]string(nil), v...)
		}
		for _, k := range []string{natsx.HeaderDLQReason, natsx.HeaderDLQSubject, natsx.HeaderDLQStream, natsx.HeaderDLQStreamSeq, jetstream.MsgIDHeader} {
			h.Del(k)
		}
		h.Set(HeaderDLQReplay, q.stream+"."+strconv.FormatUint(m.Sequence, 10))
		ack, err := natsx.PublishDedup(ctx, js, q.targetStream, subject, replayID(m.Header.Get(jetstream.MsgIDHeader), q.stream, m.Sequence), m.Data, h)
		if err != nil {
			return fmt.Errorf("dlq replay %d: %w (replayed %d before it)", m.Sequence, err, replayed)
		}
		// a duplicate ack means an earlier run published this very dlq
		// message and stopped before deleting it, so deleting now is right
		if ack.Duplicate {
			duplicates++
		}
		if err := secureDelete(ctx, dlq, m.Sequence); err != nil {
			return fmt.Errorf("dlq replay %d: published, but %w", m.Sequence, err)
		}
		replayed++
		return nil
	})
	_, _ = fmt.Fprintf(stdout, "replayed %d message(s) from %s to %s (%d already there from an earlier run), skipped %d\n",
		replayed, q.stream, q.targetStream, duplicates, skipped)
	if err == nil && skipped > 0 {
		err = fmt.Errorf("dlq replay: %d message(s) skipped and kept", skipped)
	}
	return err
}

func dlqPurge(ctx context.Context, dlq jetstream.Stream, q dlqQueue, sel dlqSelect, stdout io.Writer) error {
	var n int
	err := dlqEach(ctx, dlq, q, sel, func(m *jetstream.RawStreamMsg) error {
		if err := secureDelete(ctx, dlq, m.Sequence); err != nil {
			return err
		}
		n++
		return nil
	})
	_, _ = fmt.Fprintf(stdout, "deleted %d message(s) from %s\n", n, q.stream)
	return err
}

// secureDelete overwrites the stored bytes like erasure does, because a dlq
// message is a full event. a message that expired meanwhile is gone too.
func secureDelete(ctx context.Context, dlq jetstream.Stream, seq uint64) error {
	err := dlq.SecureDeleteMsg(ctx, seq)
	if err == nil {
		return nil
	}
	if _, gerr := dlq.GetMsg(ctx, seq); errors.Is(gerr, jetstream.ErrMsgNotFound) {
		return nil
	}
	return fmt.Errorf("delete dlq message %d: %w", seq, err)
}

// replayID is the message id of a replayed message: the original id plus
// the dlq stream and sequence. a rerun after a crash between publish and
// delete repeats the same id and is deduplicated, while the original
// message's id, still inside the duplicate window when it failed fast, does
// not swallow the replay. an id from an earlier replay keeps only its
// original part, so ids do not grow with every round.
func replayID(orig, stream string, seq uint64) string {
	if i := strings.Index(orig, replaySuffix); i >= 0 {
		orig = orig[:i]
	}
	if orig == "" {
		orig = "dlq"
	}
	return orig + replaySuffix + stream + "." + strconv.FormatUint(seq, 10)
}

func lastToken(subject string) string {
	return subject[strings.LastIndexByte(subject, '.')+1:]
}
