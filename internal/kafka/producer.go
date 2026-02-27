package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
)

// Producer publishes messages to a Kafka topic.
type Producer interface {
	Publish(ctx context.Context, topic, key string, value []byte) error
	Close() error
}

type producer struct {
	writer *kafka.Writer
}

// NewProducer creates a Kafka producer connected to the given brokers.
func NewProducer(brokers []string) Producer {
	w := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Balancer:     &kafka.Hash{}, // route by key → deterministic partition
		RequiredAcks: kafka.RequireOne,
		MaxAttempts:  3,
		WriteTimeout: 10 * time.Second,
		ReadTimeout:  10 * time.Second,
		// Auto-create topics if they don't exist
		AllowAutoTopicCreation: true,
	}
	return &producer{writer: w}
}

func (p *producer) Publish(ctx context.Context, topic, key string, value []byte) error {
	// Inject the active trace context into message headers so downstream
	// consumers can extract and continue the trace.
	headers := make(HeaderCarrier, 0)
	otel.GetTextMapPropagator().Inject(ctx, &headers)

	err := p.writer.WriteMessages(ctx, kafka.Message{
		Topic:   topic,
		Key:     []byte(key),
		Value:   value,
		Headers: []kafka.Header(headers),
		Time:    time.Now(),
	})
	if err != nil {
		return fmt.Errorf("kafka publish to %s: %w", topic, err)
	}
	return nil
}

func (p *producer) Close() error {
	return p.writer.Close()
}
