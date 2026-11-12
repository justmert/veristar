package natsx

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/nats-io/nats.go/jetstream"
)

// Action is what Apply did, or what Diff found to do, for one object.
type Action string

const (
	ActionCreate    Action = "create"
	ActionUpdate    Action = "update"
	ActionUnchanged Action = "unchanged"
)

// Change describes one object of the topology.
type Change struct {
	Kind   string // stream, bucket or consumer
	Name   string
	Action Action
	// Fields names the settings that differ from the spec, for an update.
	Fields []string
}

func (c Change) String() string {
	if len(c.Fields) == 0 {
		return fmt.Sprintf("%s %s: %s", c.Kind, c.Name, c.Action)
	}
	return fmt.Sprintf("%s %s: %s %v", c.Kind, c.Name, c.Action, c.Fields)
}

// Apply creates every missing object and updates every object whose settings
// differ from the spec. objects that already match are not touched, so a
// second run reports every object as unchanged.
func Apply(ctx context.Context, js jetstream.JetStream, spec Spec) ([]Change, error) {
	return reconcile(ctx, js, spec, true)
}

// Diff reports what Apply would do without changing anything.
func Diff(ctx context.Context, js jetstream.JetStream, spec Spec) ([]Change, error) {
	return reconcile(ctx, js, spec, false)
}

func reconcile(ctx context.Context, js jetstream.JetStream, spec Spec, write bool) ([]Change, error) {
	var out []Change
	for _, want := range spec.Streams {
		c, err := reconcileStream(ctx, js, want, write)
		if err != nil {
			return out, err
		}
		out = append(out, c)
	}
	for _, want := range spec.Buckets {
		c, err := reconcileBucket(ctx, js, want, write)
		if err != nil {
			return out, err
		}
		out = append(out, c)
	}
	for _, want := range spec.Consumers {
		c, err := reconcileConsumer(ctx, js, want, write)
		if err != nil {
			return out, err
		}
		out = append(out, c)
	}
	return out, nil
}

func reconcileStream(ctx context.Context, js jetstream.JetStream, want jetstream.StreamConfig, write bool) (Change, error) {
	c := Change{Kind: "stream", Name: want.Name}
	s, err := js.Stream(ctx, want.Name)
	switch {
	case errors.Is(err, jetstream.ErrStreamNotFound):
		c.Action = ActionCreate
		if write {
			if _, err := js.CreateStream(ctx, want); err != nil {
				return c, fmt.Errorf("natsx: create stream %s: %w", want.Name, err)
			}
		}
		return c, nil
	case err != nil:
		return c, fmt.Errorf("natsx: read stream %s: %w", want.Name, err)
	}
	c.Fields = StreamDiff(want, s.CachedInfo().Config)
	if len(c.Fields) == 0 {
		c.Action = ActionUnchanged
		return c, nil
	}
	c.Action = ActionUpdate
	if write {
		if _, err := js.UpdateStream(ctx, want); err != nil {
			return c, fmt.Errorf("natsx: update stream %s %v: %w", want.Name, c.Fields, err)
		}
	}
	return c, nil
}

func reconcileBucket(ctx context.Context, js jetstream.JetStream, want jetstream.KeyValueConfig, write bool) (Change, error) {
	c := Change{Kind: "bucket", Name: want.Bucket}
	kv, err := js.KeyValue(ctx, want.Bucket)
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		c.Action = ActionCreate
		if write {
			if _, err := js.CreateKeyValue(ctx, want); err != nil {
				return c, fmt.Errorf("natsx: create bucket %s: %w", want.Bucket, err)
			}
		}
		return c, nil
	case err != nil:
		return c, fmt.Errorf("natsx: read bucket %s: %w", want.Bucket, err)
	}
	st, err := kv.Status(ctx)
	if err != nil {
		return c, fmt.Errorf("natsx: read bucket %s: %w", want.Bucket, err)
	}
	c.Fields = BucketDiff(want, st.Config())
	if len(c.Fields) == 0 {
		c.Action = ActionUnchanged
		return c, nil
	}
	c.Action = ActionUpdate
	if write {
		if _, err := js.UpdateKeyValue(ctx, want); err != nil {
			return c, fmt.Errorf("natsx: update bucket %s %v: %w", want.Bucket, c.Fields, err)
		}
	}
	return c, nil
}

func reconcileConsumer(ctx context.Context, js jetstream.JetStream, want ConsumerSpec, write bool) (Change, error) {
	c := Change{Kind: "consumer", Name: want.Stream + "/" + want.Config.Durable}
	cons, err := js.Consumer(ctx, want.Stream, want.Config.Durable)
	switch {
	case errors.Is(err, jetstream.ErrConsumerNotFound):
		c.Action = ActionCreate
		if write {
			if _, err := js.CreateConsumer(ctx, want.Stream, want.Config); err != nil {
				return c, fmt.Errorf("natsx: create consumer %s: %w", c.Name, err)
			}
		}
		return c, nil
	case errors.Is(err, jetstream.ErrStreamNotFound) && !write:
		// a dry run against an empty server: the stream would be created first
		c.Action = ActionCreate
		return c, nil
	case err != nil:
		return c, fmt.Errorf("natsx: read consumer %s: %w", c.Name, err)
	}
	c.Fields = ConsumerDiff(want.Config, cons.CachedInfo().Config)
	if len(c.Fields) == 0 {
		c.Action = ActionUnchanged
		return c, nil
	}
	c.Action = ActionUpdate
	if write {
		if _, err := js.UpdateConsumer(ctx, want.Stream, want.Config); err != nil {
			return c, fmt.Errorf("natsx: update consumer %s %v: %w", c.Name, c.Fields, err)
		}
	}
	return c, nil
}

type diff struct{ fields []string }

func (d *diff) check(name string, equal bool) {
	if !equal {
		d.fields = append(d.fields, name)
	}
}

// unlimited maps the "not set" zero value to the -1 the server stores.
func unlimited[T int64 | int32](v T) T {
	if v == 0 {
		return -1
	}
	return v
}

// StreamDiff names the settings the spec declares that differ in have. it
// compares only declared settings, because the server fills in defaults for
// the rest.
func StreamDiff(want, have jetstream.StreamConfig) []string {
	var d diff
	d.check("description", want.Description == have.Description)
	d.check("subjects", slices.Equal(want.Subjects, have.Subjects))
	d.check("retention", want.Retention == have.Retention)
	d.check("storage", want.Storage == have.Storage)
	d.check("replicas", want.Replicas == have.Replicas)
	d.check("max_age", want.MaxAge == have.MaxAge)
	d.check("duplicate_window", want.Duplicates == have.Duplicates)
	d.check("discard", want.Discard == have.Discard)
	d.check("max_msgs", unlimited(want.MaxMsgs) == unlimited(have.MaxMsgs))
	d.check("max_bytes", unlimited(want.MaxBytes) == unlimited(have.MaxBytes))
	d.check("max_msg_size", unlimited(want.MaxMsgSize) == unlimited(have.MaxMsgSize))
	return d.fields
}

// BucketDiff names the bucket settings that differ.
func BucketDiff(want, have jetstream.KeyValueConfig) []string {
	history := func(h uint8) uint8 {
		if h == 0 {
			return 1
		}
		return h
	}
	var d diff
	d.check("description", want.Description == have.Description)
	d.check("history", history(want.History) == history(have.History))
	d.check("ttl", want.TTL == have.TTL)
	d.check("storage", want.Storage == have.Storage)
	d.check("replicas", max(want.Replicas, 1) == max(have.Replicas, 1))
	d.check("limit_marker_ttl", want.LimitMarkerTTL == have.LimitMarkerTTL)
	d.check("max_bytes", unlimited(want.MaxBytes) == unlimited(have.MaxBytes))
	d.check("max_value_size", unlimited(want.MaxValueSize) == unlimited(have.MaxValueSize))
	return d.fields
}

// ConsumerDiff names the consumer settings that differ.
func ConsumerDiff(want, have jetstream.ConsumerConfig) []string {
	var d diff
	d.check("description", want.Description == have.Description)
	d.check("deliver_policy", want.DeliverPolicy == have.DeliverPolicy)
	d.check("ack_policy", want.AckPolicy == have.AckPolicy)
	d.check("ack_wait", want.AckWait == have.AckWait)
	d.check("max_deliver", want.MaxDeliver == have.MaxDeliver)
	d.check("backoff", slices.Equal(want.BackOff, have.BackOff))
	d.check("filter_subject", want.FilterSubject == have.FilterSubject)
	return d.fields
}
