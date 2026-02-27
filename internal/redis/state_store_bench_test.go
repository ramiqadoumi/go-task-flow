package redis

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// newBenchClient returns a Redis client connected to localhost:6379.
// Benchmarks are skipped if Redis is not reachable.
func newBenchClient(b *testing.B) *redis.Client {
	b.Helper()
	c := redis.NewClient(&redis.Options{
		Addr:         "localhost:6379",
		DialTimeout:  1 * time.Second,
		ReadTimeout:  500 * time.Millisecond,
		WriteTimeout: 500 * time.Millisecond,
	})
	if err := c.Ping(context.Background()).Err(); err != nil {
		b.Skipf("Redis not available at localhost:6379: %v", err)
	}
	b.Cleanup(func() { _ = c.Close() })
	return c
}

// BenchmarkStateStore_SetStatus measures a single SET with TTL.
func BenchmarkStateStore_SetStatus(b *testing.B) {
	store := NewStateStore(newBenchClient(b))
	ctx := context.Background()
	const taskID = "bench-task-set"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := store.SetStatus(ctx, taskID, "RUNNING"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStateStore_GetStatus measures a single GET.
func BenchmarkStateStore_GetStatus(b *testing.B) {
	client := newBenchClient(b)
	store := NewStateStore(client)
	ctx := context.Background()
	const taskID = "bench-task-get"

	// Pre-seed so every GET hits a real value.
	if err := store.SetStatus(ctx, taskID, "RUNNING"); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.GetStatus(ctx, taskID); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStateStore_SetStatus_Parallel stresses concurrent writes.
func BenchmarkStateStore_SetStatus_Parallel(b *testing.B) {
	store := NewStateStore(newBenchClient(b))
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := store.SetStatus(ctx, "bench-parallel", "DONE"); err != nil {
				b.Fatal(err)
			}
		}
	})
}
