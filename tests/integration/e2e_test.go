//go:build integration

// Package integration contains end-to-end integration tests that require
// real infrastructure (Kafka, Redis, PostgreSQL) provided by testcontainers-go.
//
// Run with: go test -tags=integration -v ./tests/integration/
package integration

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
	"github.com/ramiqadoumi/go-task-flow/internal/postgres"
	redisstore "github.com/ramiqadoumi/go-task-flow/internal/redis"
)

// TestE2E_FullTaskLifecycle exercises the complete task pipeline against real
// infrastructure, simulating the roles of API Gateway, Dispatcher, and Worker
// using inline logic.
//
// Flow: API-submit → Kafka publish → Dispatcher route → Kafka consume →
//
//	Worker process → Redis DONE + Postgres execution recorded.
func TestE2E_FullTaskLifecycle(t *testing.T) {
	ctx := context.Background()

	// ── Infrastructure setup ─────────────────────────────────────────────────
	redisClient := redis.NewClient(&redis.Options{Addr: testRedisAddr})
	t.Cleanup(func() {
		redisClient.FlushDB(ctx) //nolint:errcheck
		redisClient.Close()      //nolint:errcheck
	})

	pool, err := pgxpool.New(ctx, testPostgresDSN)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Exec(ctx, "TRUNCATE task_executions, tasks CASCADE") //nolint:errcheck
		pool.Close()
	})

	store := redisstore.NewStateStore(redisClient)
	repo := postgres.NewRepository(pool)

	producer := kafka.NewProducer(testKafkaBrokers)
	t.Cleanup(func() { producer.Close() }) //nolint:errcheck

	// Use unique topics to avoid interference with kafka_test.go tests.
	pendingTopic := uniqueTopic("e2e-pending")
	workerTopic := uniqueTopic("e2e-worker")
	createTopic(t, pendingTopic)
	createTopic(t, workerTopic)

	// ── Step 1: API Gateway — create task, set initial state, publish ────────
	taskID := uuid.New().String()
	task := &domain.Task{
		ID:         taskID,
		Type:       "email",
		Payload:    []byte(`{"to":"e2e@test.com","subject":"E2E Test"}`),
		Status:     domain.StatusPending,
		MaxRetries: 2,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	require.NoError(t, repo.Create(ctx, task))
	require.NoError(t, store.SetStatus(ctx, taskID, domain.StatusPending))

	raw, err := json.Marshal(task)
	require.NoError(t, err)
	require.NoError(t, producer.Publish(ctx, pendingTopic, taskID, raw))

	// ── Step 2: Dispatcher — consume tasks.pending, route to worker topic ────
	dispConsumer := kafka.NewConsumer(testKafkaBrokers, pendingTopic, "e2e-disp", slog.Default())
	t.Cleanup(func() { dispConsumer.Close() }) //nolint:errcheck

	dispCtx, dispCancel := context.WithTimeout(ctx, 30*time.Second)
	defer dispCancel()

	dispDone := make(chan struct{})
	go func() {
		defer close(dispDone)
		dispConsumer.Subscribe(dispCtx, func(_ context.Context, msg kafka.Message) error { //nolint:errcheck
			if err := producer.Publish(dispCtx, workerTopic, taskID, msg.Value); err != nil {
				return err // non-nil → offset not committed → retry
			}
			store.SetStatus(dispCtx, taskID, domain.StatusQueued) //nolint:errcheck
			dispCancel()
			return nil
		})
	}()
	<-dispDone

	status, err := store.GetStatus(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusQueued, status, "dispatcher should set status to QUEUED")

	// ── Step 3: Worker — consume worker topic, process, record execution ─────
	workerConsumer := kafka.NewConsumer(testKafkaBrokers, workerTopic, "e2e-worker", slog.Default())
	t.Cleanup(func() { workerConsumer.Close() }) //nolint:errcheck

	workerCtx, workerCancel := context.WithTimeout(ctx, 30*time.Second)
	defer workerCancel()

	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		workerConsumer.Subscribe(workerCtx, func(_ context.Context, msg kafka.Message) error { //nolint:errcheck
			var t domain.Task
			if err := json.Unmarshal(msg.Value, &t); err != nil {
				return nil // discard malformed
			}

			store.SetStatus(workerCtx, t.ID, domain.StatusRunning) //nolint:errcheck

			// Simulate successful handler execution.
			now := time.Now().UTC()
			exec := &domain.TaskExecution{
				TaskID:     t.ID,
				WorkerID:   "e2e-worker-1",
				Attempt:    1,
				Status:     domain.StatusDone,
				DurationMs: 10,
				ExecutedAt: now,
			}
			repo.RecordExecution(workerCtx, exec)                        //nolint:errcheck
			repo.UpdateStatus(workerCtx, t.ID, domain.StatusDone)        //nolint:errcheck
			store.SetStatus(workerCtx, t.ID, domain.StatusDone)          //nolint:errcheck

			workerCancel()
			return nil
		})
	}()
	<-workerDone

	// ── Assertions ────────────────────────────────────────────────────────────
	finalStatus, err := store.GetStatus(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDone, finalStatus, "Redis should show DONE")

	finalTask, err := repo.GetByID(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDone, finalTask.Status, "Postgres should show DONE")
	assert.NotNil(t, finalTask.CompletedAt, "completed_at should be set")
}
