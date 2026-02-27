package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
	redisstore "github.com/ramiqadoumi/go-task-flow/internal/redis"
	"github.com/ramiqadoumi/go-task-flow/pkg/telemetry"
)

const (
	TopicPending = "tasks.pending"
	topicDLQ     = "tasks.dlq"
)

func workerTopic(taskType string) string {
	return "tasks.worker." + taskType
}

// Dispatcher consumes from tasks.pending and routes to per-type worker topics.
type Dispatcher struct {
	consumer kafka.Consumer
	producer kafka.Producer
	store    redisstore.StateStore
	limiter  redisstore.RateLimiter // nil = disabled
	logger   *slog.Logger
}

func NewDispatcher(
	consumer kafka.Consumer,
	producer kafka.Producer,
	store redisstore.StateStore,
	limiter redisstore.RateLimiter,
	logger *slog.Logger,
) *Dispatcher {
	return &Dispatcher{
		consumer: consumer,
		producer: producer,
		store:    store,
		limiter:  limiter,
		logger:   logger,
	}
}

// Run starts consuming. Blocks until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) error {
	return d.consumer.Subscribe(ctx, d.route)
}

func (d *Dispatcher) route(ctx context.Context, msg kafka.Message) error {
	ctx, span := otel.Tracer("dispatcher").Start(ctx, "dispatcher.route")
	defer span.End()

	var task domain.Task
	if err := json.Unmarshal(msg.Value, &task); err != nil {
		d.logger.Error("malformed message, sending to DLQ", slog.String("error", err.Error()))
		span.RecordError(err)
		span.SetStatus(codes.Error, "malformed message")
		telemetry.DispatcherDLQTotal.Inc()
		return d.toDLQ(ctx, msg.Value)
	}

	span.SetAttributes(
		attribute.String("task.id", task.ID),
		attribute.String("task.type", task.Type),
	)

	log := d.logger.With(
		slog.String("task_id", task.ID),
		slog.String("task_type", task.Type),
	)

	if task.Type == "" {
		log.Error("empty task type, sending to DLQ")
		span.SetStatus(codes.Error, "empty task type")
		telemetry.DispatcherDLQTotal.Inc()
		return d.toDLQ(ctx, msg.Value)
	}

	// Rate limiting — reject early if over the limit for this task type.
	if d.limiter != nil {
		allowed, err := d.limiter.Allow(ctx, task.Type)
		if err != nil {
			log.Error("rate limiter error", slog.String("error", err.Error()))
			// Allow on limiter failure to avoid dropping tasks due to Redis issues.
		} else if !allowed {
			log.Warn("rate limit exceeded, sending to DLQ")
			span.SetStatus(codes.Error, "rate limit exceeded")
			telemetry.DispatcherRateLimitedTotal.Inc()
			telemetry.DispatcherDLQTotal.Inc()
			return d.toDLQ(ctx, msg.Value)
		}
	}

	target := workerTopic(task.Type)
	if err := d.producer.Publish(ctx, target, task.ID, msg.Value); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "kafka publish failed")
		// Transient Kafka error — return error so offset is NOT committed.
		return fmt.Errorf("publish to %s: %w", target, err)
	}

	// Best-effort state update. A Redis failure here doesn't block routing.
	if err := d.store.SetStatus(ctx, task.ID, domain.StatusQueued); err != nil {
		log.Error("failed to set status QUEUED", slog.String("error", err.Error()))
	}

	telemetry.DispatcherTasksRouted.WithLabelValues(task.Type).Inc()
	log.Info("task routed", slog.String("topic", target))
	return nil
}

// toDLQ publishes a raw message to the dead-letter queue.
func (d *Dispatcher) toDLQ(ctx context.Context, payload []byte) error {
	if err := d.producer.Publish(ctx, topicDLQ, "", payload); err != nil {
		d.logger.Error("failed to publish to DLQ", slog.String("error", err.Error()))
		return err
	}
	return nil
}
