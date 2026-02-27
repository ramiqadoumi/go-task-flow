package kafka

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
)

// Message wraps a Kafka message with the fields services need.
type Message struct {
	Topic   string
	Key     []byte
	Value   []byte
	Offset  int64
	Headers []kafka.Header
}

// HandlerFunc processes a single Kafka message.
// Return nil to commit the offset. Return an error to skip committing (message will be re-delivered).
type HandlerFunc func(ctx context.Context, msg Message) error

// Consumer reads messages from a Kafka topic.
type Consumer interface {
	Subscribe(ctx context.Context, handler HandlerFunc) error
	Close() error
}

type consumer struct {
	reader *kafka.Reader
	logger *slog.Logger
}

// NewConsumer creates a Kafka consumer for the given topic and consumer group.
func NewConsumer(brokers []string, topic, groupID string, logger *slog.Logger) Consumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        brokers,
		Topic:          topic,
		GroupID:        groupID,
		MinBytes:       1,
		MaxBytes:       10e6, // 10 MB
		MaxWait:        500 * time.Millisecond,
		CommitInterval: 0, // manual commit only
		StartOffset:    kafka.FirstOffset,
	})
	return &consumer{reader: r, logger: logger}
}

// Subscribe reads messages in a loop until ctx is cancelled.
// Offsets are committed only after the handler returns nil (at-least-once delivery).
func (c *consumer) Subscribe(ctx context.Context, handler HandlerFunc) error {
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // normal shutdown
			}
			return fmt.Errorf("kafka fetch: %w", err)
		}

		msg := Message{
			Topic:   m.Topic,
			Key:     m.Key,
			Value:   m.Value,
			Offset:  m.Offset,
			Headers: m.Headers,
		}

		// Extract any trace context injected by the producer into the message
		// headers, creating a child span context for this consumption.
		carrier := HeaderCarrier(m.Headers)
		msgCtx := otel.GetTextMapPropagator().Extract(ctx, &carrier)

		if err := handler(msgCtx, msg); err != nil {
			// Log and continue — do NOT commit. Kafka will re-deliver on restart.
			c.logger.Error("message handler failed, skipping commit",
				slog.String("topic", m.Topic),
				slog.Int64("offset", m.Offset),
				slog.String("error", err.Error()),
			)
			continue
		}

		// Commit only on handler success.
		if err := c.reader.CommitMessages(ctx, m); err != nil {
			c.logger.Error("failed to commit kafka offset",
				slog.String("topic", m.Topic),
				slog.Int64("offset", m.Offset),
				slog.String("error", err.Error()),
			)
		}
	}
}

func (c *consumer) Close() error {
	return c.reader.Close()
}
