package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	"github.com/ramiqadoumi/go-task-flow/internal/handlers"
	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
	"github.com/ramiqadoumi/go-task-flow/internal/postgres"
	redisstore "github.com/ramiqadoumi/go-task-flow/internal/redis"
	"github.com/ramiqadoumi/go-task-flow/pkg/retry"
	"github.com/ramiqadoumi/go-task-flow/pkg/telemetry"
)

const topicDLQ = "tasks.dlq"

// Worker consumes tasks from a Kafka topic and executes them.
type Worker struct {
	consumer   kafka.Consumer
	producer   kafka.Producer
	store      redisstore.StateStore
	repo       postgres.TaskRepository
	registry   *handlers.Registry
	workerID   string
	workerType string
	maxRetries int
	timeout    time.Duration
	baseDelay  time.Duration
	logger     *slog.Logger

	wg       sync.WaitGroup
	inFlight atomic.Int64
}

// Option configures a Worker.
type Option func(*Worker)

func WithRetries(n int) Option            { return func(w *Worker) { w.maxRetries = n } }
func WithTimeout(d time.Duration) Option  { return func(w *Worker) { w.timeout = d } }
func WithLogger(l *slog.Logger) Option    { return func(w *Worker) { w.logger = l } }
func WithWorkerType(t string) Option      { return func(w *Worker) { w.workerType = t } }
func WithBaseDelay(d time.Duration) Option { return func(w *Worker) { w.baseDelay = d } }

// NewWorker constructs a Worker with the given dependencies and options.
func NewWorker(
	workerID string,
	consumer kafka.Consumer,
	producer kafka.Producer,
	store redisstore.StateStore,
	repo postgres.TaskRepository,
	registry *handlers.Registry,
	opts ...Option,
) *Worker {
	w := &Worker{
		workerID:   workerID,
		consumer:   consumer,
		producer:   producer,
		store:      store,
		repo:       repo,
		registry:   registry,
		maxRetries: 3,
		timeout:    30 * time.Second,
		baseDelay:  time.Second,
		logger:     slog.Default(),
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Run starts consuming and processing messages. Blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	return w.consumer.Subscribe(ctx, w.processMessage)
}

// Wait blocks until all in-flight tasks finish. Call after Run returns.
func (w *Worker) Wait() { w.wg.Wait() }

// processMessage is the Kafka HandlerFunc — called for each message.
// Always returns nil so the offset is committed (failures go to DLQ).
func (w *Worker) processMessage(consumerCtx context.Context, msg kafka.Message) error {
	var task domain.Task
	if err := json.Unmarshal(msg.Value, &task); err != nil {
		w.logger.Error("malformed task message, discarding",
			slog.String("error", err.Error()),
			slog.String("raw", string(msg.Value)),
		)
		return nil
	}

	// Start a child span parented to the trace context extracted from Kafka headers.
	ctx, span := otel.Tracer("worker").Start(consumerCtx, "worker.process_task")
	defer span.End()
	span.SetAttributes(
		attribute.String("task.id", task.ID),
		attribute.String("task.type", task.Type),
		attribute.String("worker.id", w.workerID),
	)

	log := w.logger.With(
		slog.String("task_id", task.ID),
		slog.String("task_type", task.Type),
		slog.String("worker_id", w.workerID),
	)

	// Idempotency: if already in a terminal state, skip.
	if s, err := w.store.GetStatus(ctx, task.ID); err == nil && s.IsTerminal() {
		alreadyProcessed := &domain.TaskAlreadyProcessedError{TaskID: task.ID, Status: s}
		log.Info("task already terminal, skipping", slog.String("error", alreadyProcessed.Error()))
		span.RecordError(alreadyProcessed)
		return nil
	}

	h, err := w.registry.Get(task.Type)
	if err != nil {
		log.Error("no handler for task type", slog.String("error", err.Error()))
		span.RecordError(err)
		span.SetStatus(codes.Error, "no handler registered")
		_ = w.store.SetStatus(ctx, task.ID, domain.StatusDead)
		_ = w.producer.Publish(ctx, topicDLQ, task.ID, msg.Value)
		telemetry.WorkerDLQTotal.WithLabelValues(w.workerType).Inc()
		return nil
	}

	// QUEUED → RUNNING
	if err := w.store.SetStatus(ctx, task.ID, domain.StatusRunning); err != nil {
		log.Error("failed to set RUNNING status", slog.String("error", err.Error()))
		return fmt.Errorf("set running status: %w", err)
	}

	w.wg.Add(1)
	w.inFlight.Add(1)
	telemetry.WorkerTasksInFlight.WithLabelValues(w.workerType).Inc()
	defer func() {
		telemetry.WorkerTasksInFlight.WithLabelValues(w.workerType).Dec()
		w.inFlight.Add(-1)
		w.wg.Done()
	}()

	start := time.Now()
	attempts := 0

	execErr := retry.Do(ctx, retry.Config{
		MaxAttempts: w.maxRetries + 1,
		BaseDelay:   w.baseDelay,
		OnRetry: func(attempt int, retryErr error) {
			attempts = attempt
			telemetry.WorkerRetriesTotal.WithLabelValues(w.workerType).Inc()
			log.Warn("attempt failed, retrying",
				slog.Int("attempt", attempt),
				slog.String("error", retryErr.Error()),
			)
			_ = w.store.SetStatus(ctx, task.ID, domain.StatusRetrying)
		},
	}, func() error {
		attempts++
		// Attach span to a fresh context so the handler timeout is independent
		// of consumer shutdown, but handler child spans are still parented here.
		execCtx, cancel := context.WithTimeout(
			trace.ContextWithSpan(context.Background(), span),
			w.timeout,
		)
		defer cancel()
		return h.Handle(execCtx, &task)
	})

	task.Attempts = attempts
	durationSec := time.Since(start).Seconds()
	durationMs := int64(durationSec * 1000)

	telemetry.WorkerTaskDurationSeconds.WithLabelValues(w.workerType).Observe(durationSec)

	if execErr == nil {
		log.Info("task completed",
			slog.Int64("duration_ms", durationMs),
			slog.Int("attempts", attempts),
		)
		w.finishSuccess(ctx, &task, durationMs)
	} else {
		log.Error("task dead after all retries",
			slog.Int("attempts", attempts),
			slog.String("error", execErr.Error()),
			slog.Int64("duration_ms", durationMs),
		)
		span.RecordError(execErr)
		span.SetStatus(codes.Error, "task exhausted all retries")
		w.finishFailure(ctx, &task, execErr, durationMs, msg.Value)
	}

	return nil
}

func (w *Worker) finishSuccess(ctx context.Context, task *domain.Task, durationMs int64) {
	now := time.Now().UTC()
	task.CompletedAt = &now
	task.Status = domain.StatusDone
	task.UpdatedAt = now

	_ = w.store.SetStatus(ctx, task.ID, domain.StatusDone)
	_ = w.store.SetTaskMeta(ctx, task)

	exec := &domain.TaskExecution{
		TaskID:     task.ID,
		WorkerID:   w.workerID,
		Attempt:    task.Attempts,
		Status:     domain.StatusDone,
		DurationMs: durationMs,
		ExecutedAt: now,
	}
	if err := w.repo.RecordExecution(ctx, exec); err != nil {
		w.logger.Error("failed to record execution", slog.String("task_id", task.ID), slog.String("error", err.Error()))
	}
	if err := w.repo.UpdateStatus(ctx, task.ID, domain.StatusDone); err != nil {
		w.logger.Error("failed to update DB status", slog.String("task_id", task.ID), slog.String("error", err.Error()))
	}

	telemetry.WorkerTasksProcessed.WithLabelValues(w.workerType, "done").Inc()
}

func (w *Worker) finishFailure(ctx context.Context, task *domain.Task, taskErr error, durationMs int64, raw []byte) {
	now := time.Now().UTC()
	task.Status = domain.StatusDead
	task.UpdatedAt = now

	_ = w.store.SetStatus(ctx, task.ID, domain.StatusDead)
	_ = w.store.SetTaskMeta(ctx, task)

	exec := &domain.TaskExecution{
		TaskID:     task.ID,
		WorkerID:   w.workerID,
		Attempt:    task.Attempts,
		Status:     domain.StatusDead,
		DurationMs: durationMs,
		Error:      taskErr.Error(),
		ExecutedAt: now,
	}
	if err := w.repo.RecordExecution(ctx, exec); err != nil {
		w.logger.Error("failed to record execution", slog.String("task_id", task.ID), slog.String("error", err.Error()))
	}
	if err := w.repo.UpdateStatus(ctx, task.ID, domain.StatusDead); err != nil {
		w.logger.Error("failed to update DB status", slog.String("task_id", task.ID), slog.String("error", err.Error()))
	}

	if err := w.producer.Publish(ctx, topicDLQ, task.ID, raw); err != nil {
		w.logger.Error("failed to publish to DLQ", slog.String("task_id", task.ID), slog.String("error", err.Error()))
	}

	telemetry.WorkerTasksProcessed.WithLabelValues(w.workerType, "dead").Inc()
	telemetry.WorkerDLQTotal.WithLabelValues(w.workerType).Inc()
}
