package kafka

import (
	"context"
	"fmt"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/outbound-shipping/internal/application/outbox"
)

// Writer is the subset of *kafkago.Writer the relay sink needs.
type Writer interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// RelaySink is the outbox relay's ports-facing Sink: it writes each drained
// outbox message to the topic the message itself names (the writer built by
// NewWriter is topic-less, so one sink serves the events and the analytics
// topics).
type RelaySink struct {
	writer Writer
}

// NewRelaySink constructs a RelaySink over brokers. It does not dial.
func NewRelaySink(brokers []string) *RelaySink {
	w := NewWriter(join(brokers))
	return &RelaySink{writer: w}
}

// NewRelaySinkWithWriter constructs a RelaySink over an explicit Writer (a
// fake in tests). The writer must NOT have a Topic set.
func NewRelaySinkWithWriter(w Writer) *RelaySink { return &RelaySink{writer: w} }

// Send writes msgs in one WriteMessages call. A message without a topic is
// rejected before anything is written.
func (s *RelaySink) Send(ctx context.Context, msgs ...outbox.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]kafkago.Message, len(msgs))
	for i, m := range msgs {
		if m.Topic == "" {
			return fmt.Errorf("kafka relay sink: message %d (%s) has no topic", i, m.EventType)
		}
		headers := make([]kafkago.Header, len(m.Headers))
		for j, h := range m.Headers {
			headers[j] = kafkago.Header{Key: h.Key, Value: []byte(h.Value)}
		}
		out[i] = kafkago.Message{Topic: m.Topic, Key: m.Key, Value: m.Value, Headers: headers}
	}
	if err := s.writer.WriteMessages(ctx, out...); err != nil {
		return fmt.Errorf("kafka relay sink: write %d message(s): %w", len(out), err)
	}
	return nil
}

// Close releases the underlying Kafka writer.
func (s *RelaySink) Close() error {
	if w, ok := s.writer.(*kafkago.Writer); ok {
		return w.Close()
	}
	return nil
}

// join renders brokers the way NewWriter wants (comma-separated).
func join(brokers []string) string {
	out := ""
	for i, b := range brokers {
		if i > 0 {
			out += ","
		}
		out += b
	}
	return out
}
