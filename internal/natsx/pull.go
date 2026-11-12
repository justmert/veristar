package natsx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Handler processes one message. returning nil acks it, returning an error
// wrapped with Permanent moves it to the dead-letter stream and acks it, and
// any other error naks it with the next RetryBackoff delay.
type Handler func(ctx context.Context, msg jetstream.Msg) error

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks err as a failure that a retry cannot fix.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err was marked with Permanent.
func IsPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}

// RetryDelay returns the nak delay for a message delivered numDelivered
// times: the matching RetryBackoff step, then the last step for every later
// attempt.
func RetryDelay(numDelivered uint64) time.Duration {
	i := int(min(numDelivered, uint64(len(RetryBackoff)))) - 1 //nolint:gosec // bounded by len(RetryBackoff)
	return RetryBackoff[max(i, 0)]
}

// PullConfig configures PullLoop.
type PullConfig struct {
	// Batch is the most messages fetched at once.
	Batch int
	// MaxWait bounds one fetch, so BeforeBatch runs at least this often.
	MaxWait time.Duration
	// BeforeBatch runs before every fetch. an error stops the loop before
	// any further message is taken; the replayer uses it to confirm it still
	// holds its lock.
	BeforeBatch func(ctx context.Context) error
	// DeadLetter moves a permanently failed message to its dlq stream. it is
	// required, because a work-queue stream would otherwise keep retrying a
	// message that can never succeed until the stream's max age.
	DeadLetter func(ctx context.Context, msg jetstream.Msg, reason string) error
	Log        *slog.Logger
}

// fetchRetryWait is the pause after a failed fetch, for example while the
// client reconnects.
const fetchRetryWait = time.Second

// PullLoop fetches from cons and hands each message to h until ctx ends or
// BeforeBatch fails. messages are handled one at a time in stream order.
func PullLoop(ctx context.Context, cons jetstream.Consumer, cfg PullConfig, h Handler) error {
	if cfg.Batch < 1 || cfg.MaxWait <= 0 || cfg.DeadLetter == nil || cfg.Log == nil {
		return errors.New("natsx: PullLoop needs Batch, MaxWait, DeadLetter and Log")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if cfg.BeforeBatch != nil {
			if err := cfg.BeforeBatch(ctx); err != nil {
				return err
			}
		}
		batch, err := cons.Fetch(cfg.Batch, jetstream.FetchMaxWait(cfg.MaxWait))
		if err != nil {
			if errors.Is(err, jetstream.ErrConsumerDeleted) || errors.Is(err, jetstream.ErrConsumerNotFound) {
				return fmt.Errorf("natsx: consumer is gone, run platformctl nats apply: %w", err)
			}
			cfg.Log.Warn("nats fetch failed", "error", err.Error())
			if !sleep(ctx, fetchRetryWait) {
				return ctx.Err()
			}
			continue
		}
		for msg := range batch.Messages() {
			if ctx.Err() != nil {
				// hand the rest back at once instead of after the ack wait
				_ = msg.Nak()
				continue
			}
			handle(ctx, cfg, msg, h)
		}
		if err := batch.Error(); err != nil && !errors.Is(err, context.Canceled) {
			if errors.Is(err, jetstream.ErrConsumerDeleted) || errors.Is(err, jetstream.ErrConsumerNotFound) {
				return fmt.Errorf("natsx: consumer is gone, run platformctl nats apply: %w", err)
			}
			cfg.Log.Warn("nats fetch ended with an error", "error", err.Error())
		}
	}
}

func handle(ctx context.Context, cfg PullConfig, msg jetstream.Msg, h Handler) {
	meta, err := msg.Metadata()
	if err != nil {
		// not a jetstream message; nothing can ack it
		cfg.Log.Error("message without jetstream metadata", "subject", msg.Subject(), "error", err.Error())
		return
	}
	herr := h(ctx, msg)
	switch {
	case herr == nil:
	case IsPermanent(herr):
		if err := cfg.DeadLetter(ctx, msg, herr.Error()); err != nil {
			cfg.Log.Error("dead letter failed, retrying later", "stream_seq", meta.Sequence.Stream, "error", err.Error())
			_ = msg.NakWithDelay(RetryDelay(meta.NumDelivered))
			return
		}
	default:
		_ = msg.NakWithDelay(RetryDelay(meta.NumDelivered))
		return
	}
	// a double ack waits for the server, so a message counts as done only
	// once the server has recorded the ack
	if err := msg.DoubleAck(ctx); err != nil {
		cfg.Log.Warn("ack not confirmed, message may be redelivered", "stream_seq", meta.Sequence.Stream, "error", err.Error())
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
