package natsx

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// ErrLegacyNotEmpty means the legacy web subject still holds events, so it
// cannot be retired yet.
var ErrLegacyNotEmpty = errors.New("natsx: the legacy web subject still holds events")

// LegacyWebState is what is left of the single web subject of earlier
// releases.
type LegacyWebState struct {
	// Subject is true while WEB_EVENTS still lists the legacy subject.
	Subject bool
	// Consumer is true while the legacy replayer consumer exists.
	Consumer bool
	// Messages is how many legacy events the stream holds, delivered or not.
	Messages uint64
}

// Retired reports whether nothing of the legacy subject is left.
func (s LegacyWebState) Retired() bool { return !s.Subject && !s.Consumer && s.Messages == 0 }

// ReadLegacyWeb reads the legacy part of WEB_EVENTS.
func ReadLegacyWeb(ctx context.Context, js jetstream.JetStream) (LegacyWebState, error) {
	var st LegacyWebState
	s, err := js.Stream(ctx, StreamWebEvents)
	if err != nil {
		return st, fmt.Errorf("natsx: bind %s: %w", StreamWebEvents, err)
	}
	info, err := s.Info(ctx, jetstream.WithSubjectFilter(WebEventsLegacy))
	if err != nil {
		return st, fmt.Errorf("natsx: %s info: %w", StreamWebEvents, err)
	}
	st.Subject = slices.Contains(info.Config.Subjects, WebEventsLegacy)
	for _, n := range info.State.Subjects {
		st.Messages += n
	}
	_, err = js.Consumer(ctx, StreamWebEvents, ConsumerReplayer)
	switch {
	case err == nil:
		st.Consumer = true
	case errors.Is(err, jetstream.ErrConsumerNotFound):
	default:
		return st, fmt.Errorf("natsx: read consumer %s: %w", ConsumerReplayer, err)
	}
	return st, nil
}

// RetireLegacyWeb removes the legacy web subject and its consumer once every
// legacy event is delivered. it waits up to wait for the replayer to drain
// them and fails with ErrLegacyNotEmpty when they are still there.
//
// the subject goes first, so no gateway of the earlier release can queue
// another event on it; an event that landed between the last check and the
// removal puts the subject back, so the replayer can still deliver it, and
// the run waits again. the consumer goes last, after the stream holds no
// legacy event.
func RetireLegacyWeb(ctx context.Context, js jetstream.JetStream, wait time.Duration, poll time.Duration) (LegacyWebState, error) {
	deadline := time.Now().Add(wait)
	for {
		st, err := ReadLegacyWeb(ctx, js)
		if err != nil || st.Retired() {
			return st, err
		}
		if st.Messages == 0 {
			if st.Subject {
				if err := setLegacySubject(ctx, js, false); err != nil {
					return st, err
				}
				after, err := ReadLegacyWeb(ctx, js)
				if err != nil {
					return after, err
				}
				if after.Messages > 0 {
					// an event of an earlier gateway arrived just before the
					// removal; give it back its subject and wait for it
					if err := setLegacySubject(ctx, js, true); err != nil {
						return after, err
					}
					continue
				}
			}
			if st.Consumer {
				if err := js.DeleteConsumer(ctx, StreamWebEvents, ConsumerReplayer); err != nil && !errors.Is(err, jetstream.ErrConsumerNotFound) {
					return st, fmt.Errorf("natsx: delete consumer %s: %w", ConsumerReplayer, err)
				}
			}
			continue
		}
		if !time.Now().Before(deadline) {
			return st, fmt.Errorf("%w: %d event(s) wait for the replayer", ErrLegacyNotEmpty, st.Messages)
		}
		t := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return st, ctx.Err()
		case <-t.C:
		}
	}
}

// setLegacySubject adds or removes the legacy subject on WEB_EVENTS.
func setLegacySubject(ctx context.Context, js jetstream.JetStream, on bool) error {
	s, err := js.Stream(ctx, StreamWebEvents)
	if err != nil {
		return fmt.Errorf("natsx: bind %s: %w", StreamWebEvents, err)
	}
	cfg := s.CachedInfo().Config
	has := slices.Contains(cfg.Subjects, WebEventsLegacy)
	switch {
	case on && !has:
		cfg.Subjects = append([]string{WebEventsLegacy}, cfg.Subjects...)
	case !on && has:
		cfg.Subjects = slices.DeleteFunc(slices.Clone(cfg.Subjects), func(s string) bool { return s == WebEventsLegacy })
	default:
		return nil
	}
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		return fmt.Errorf("natsx: update %s subjects: %w", StreamWebEvents, err)
	}
	return nil
}
