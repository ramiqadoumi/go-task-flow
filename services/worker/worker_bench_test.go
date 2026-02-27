package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	"github.com/ramiqadoumi/go-task-flow/internal/handlers"
	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
)

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// BenchmarkWorker_ProcessTask measures the overhead of processMessage with a
// no-op handler — i.e., the worker engine itself, excluding real I/O.
func BenchmarkWorker_ProcessTask(b *testing.B) {
	reg := handlers.NewRegistry()
	reg.Register(&fakeHandler{taskType: "email"}) // always returns nil

	store := newFakeStore()
	repo := newFakeRepo()
	prod := &fakeProducer{}

	w := NewWorker("bench-worker", nil, prod, store, repo, reg,
		WithLogger(discardLogger),
		WithRetries(3),
		WithBaseDelay(time.Millisecond),
		WithWorkerType("email"),
	)

	task := domain.Task{ID: "bench-task", Type: "email"}
	raw, err := json.Marshal(task)
	if err != nil {
		b.Fatal(err)
	}
	msg := kafka.Message{Value: raw}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Re-seed a fresh state so idempotency guard doesn't short-circuit.
		store.states["bench-task"] = domain.StatusQueued
		_ = w.processMessage(ctx, msg)
	}
}

// BenchmarkWorker_ProcessTask_Parallel measures throughput under concurrent load.
func BenchmarkWorker_ProcessTask_Parallel(b *testing.B) {
	reg := handlers.NewRegistry()
	reg.Register(&fakeHandler{taskType: "email"})

	b.RunParallel(func(pb *testing.PB) {
		store := newFakeStore()
		repo := newFakeRepo()
		prod := &fakeProducer{}

		w := NewWorker("bench-worker", nil, prod, store, repo, reg,
			WithLogger(discardLogger),
			WithRetries(3),
			WithBaseDelay(time.Millisecond),
			WithWorkerType("email"),
		)

		task := domain.Task{ID: "bench-task", Type: "email"}
		raw, _ := json.Marshal(task)
		msg := kafka.Message{Value: raw}
		ctx := context.Background()

		for pb.Next() {
			store.states["bench-task"] = domain.StatusQueued
			_ = w.processMessage(ctx, msg)
		}
	})
}
