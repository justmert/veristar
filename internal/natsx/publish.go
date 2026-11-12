package natsx

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// ErrNoMsgID is returned by PublishDedup when the message id is empty.
var ErrNoMsgID = errors.New("natsx: publish without a message id cannot be deduplicated")

// PublishDedup publishes data to subject with msgID as Nats-Msg-Id and waits
// for the stream's acknowledgement. the expected stream is checked by the
// server, so a subject that maps to the wrong stream fails instead of being
// stored somewhere unexpected. a repeated publish of the same id inside the
// duplicate window is acknowledged with Duplicate set and stored once.
func PublishDedup(ctx context.Context, js jetstream.JetStream, stream, subject, msgID string, data []byte, header nats.Header) (*jetstream.PubAck, error) {
	if msgID == "" {
		return nil, ErrNoMsgID
	}
	m := &nats.Msg{Subject: subject, Data: data, Header: header}
	ack, err := js.PublishMsg(ctx, m, jetstream.WithMsgID(msgID), jetstream.WithExpectStream(stream))
	if err != nil {
		return nil, fmt.Errorf("natsx: publish to %s: %w", subject, err)
	}
	return ack, nil
}

// dead-letter headers. neutral names, because the dlq is read by operators
// and erasure, not by browsers, but the same naming rule applies everywhere.
const (
	HeaderDLQReason    = "Sa-Dlq-Reason"
	HeaderDLQSubject   = "Sa-Dlq-Subject"
	HeaderDLQStream    = "Sa-Dlq-Stream"
	HeaderDLQStreamSeq = "Sa-Dlq-Stream-Seq"
)

// maxReasonLen bounds the reason header, so a long upstream error body cannot
// grow every dlq message.
const maxReasonLen = 500

// DeadLetter copies msg to dlqSubject with the reason and origin in headers.
// the copy keeps the original Nats-Msg-Id, so moving the same message twice
// (a crash between this publish and the ack) stores it once.
func DeadLetter(ctx context.Context, js jetstream.JetStream, dlqStream, dlqSubject string, msg jetstream.Msg, reason string) error {
	meta, err := msg.Metadata()
	if err != nil {
		return fmt.Errorf("natsx: dead letter: %w", err)
	}
	h := nats.Header{}
	for k, v := range msg.Headers() {
		h[k] = append([]string(nil), v...)
	}
	if len(reason) > maxReasonLen {
		reason = reason[:maxReasonLen]
	}
	h.Set(HeaderDLQReason, reason)
	h.Set(HeaderDLQSubject, msg.Subject())
	h.Set(HeaderDLQStream, meta.Stream)
	h.Set(HeaderDLQStreamSeq, strconv.FormatUint(meta.Sequence.Stream, 10))
	id := h.Get(jetstream.MsgIDHeader)
	if id == "" {
		// a message published without an id still gets a stable one
		id = meta.Stream + "." + strconv.FormatUint(meta.Sequence.Stream, 10)
	}
	h.Del(jetstream.MsgIDHeader)
	_, err = PublishDedup(ctx, js, dlqStream, dlqSubject, id, msg.Data(), h)
	return err
}
