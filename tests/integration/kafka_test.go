//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
)

// uniqueTopic returns a topic name unique to this test run to avoid
// cross-test interference on a shared Kafka broker.
func uniqueTopic(base string) string {
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
}

func TestKafka_ProducerConsumer_RoundTrip(t *testing.T) {
	topic := uniqueTopic("test-roundtrip")
	producer := kafka.NewProducer(testKafkaBrokers)
	t.Cleanup(func() { producer.Close() }) //nolint:errcheck

	ctx := context.Background()
	payload := []byte(`{"id":"abc","type":"email"}`)
	require.NoError(t, producer.Publish(ctx, topic, "key-1", payload))

	consumer := kafka.NewConsumer(testKafkaBrokers, topic, "group-roundtrip", slog.Default())
	t.Cleanup(func() { consumer.Close() }) //nolint:errcheck

	received := make(chan []byte, 1)
	consumerCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	go func() {
		consumer.Subscribe(consumerCtx, func(_ context.Context, m kafka.Message) error { //nolint:errcheck
			received <- m.Value
			cancel() // stop after first message
			return nil
		})
	}()

	select {
	case got := <-received:
		assert.Equal(t, payload, got)
	case <-consumerCtx.Done():
		t.Fatal("timed out waiting for Kafka message")
	}
}

// TestKafka_Consumer_OffsetNotCommittedOnError verifies the at-least-once
// delivery guarantee: when a handler returns an error the offset is not
// committed, and a new consumer in the same group receives the message again.
func TestKafka_Consumer_OffsetNotCommittedOnError(t *testing.T) {
	topic := uniqueTopic("test-no-commit")
	groupID := fmt.Sprintf("group-no-commit-%d", time.Now().UnixNano())

	producer := kafka.NewProducer(testKafkaBrokers)
	t.Cleanup(func() { producer.Close() }) //nolint:errcheck

	ctx := context.Background()
	payload := []byte(`{"test":"redelivery"}`)
	require.NoError(t, producer.Publish(ctx, topic, "key-1", payload))

	// Consumer 1: returns error → offset NOT committed.
	consumer1 := kafka.NewConsumer(testKafkaBrokers, topic, groupID, slog.Default())
	ctx1, cancel1 := context.WithTimeout(ctx, 30*time.Second)

	seen := make(chan struct{}, 1)
	go func() {
		consumer1.Subscribe(ctx1, func(_ context.Context, _ kafka.Message) error { //nolint:errcheck
			seen <- struct{}{}
			cancel1()
			return errors.New("intentional failure — do not commit offset")
		})
	}()

	select {
	case <-seen:
	case <-ctx1.Done():
		t.Fatal("consumer1 timed out waiting for message")
	}

	// Give the consumer time to finish its error-handling path before closing.
	time.Sleep(300 * time.Millisecond)
	consumer1.Close() //nolint:errcheck

	// Consumer 2 (same group): should receive the same uncommitted message.
	consumer2 := kafka.NewConsumer(testKafkaBrokers, topic, groupID, slog.Default())
	t.Cleanup(func() { consumer2.Close() }) //nolint:errcheck

	redelivered := make(chan []byte, 1)
	ctx2, cancel2 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel2()

	go func() {
		consumer2.Subscribe(ctx2, func(_ context.Context, m kafka.Message) error { //nolint:errcheck
			redelivered <- m.Value
			cancel2()
			return nil
		})
	}()

	select {
	case got := <-redelivered:
		assert.Equal(t, payload, got, "message should be redelivered after non-commit")
	case <-ctx2.Done():
		t.Fatal("message was NOT redelivered — offset may have been committed unexpectedly")
	}
}
