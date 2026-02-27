# Testing

## Running Tests

```bash
# Run all unit tests with race detector
make test
# equivalent to: go test -race ./...

# Run integration tests (requires Docker — spins up Redis, Postgres, Kafka containers)
make test-integration
# equivalent to: go test -tags=integration -race -v -timeout=120s ./tests/integration/...

# Run tests for a specific package
go test -race ./internal/domain/
go test -race ./pkg/retry/
go test -race ./internal/handlers/
go test -race ./services/dispatcher/
go test -race ./services/worker/

# Run a single test by name
go test -run TestDo_RetriesOnTransientError ./pkg/retry/
go test -run TestDispatcher_RateLimited_GoesToDLQ ./services/dispatcher/
go test -run TestWorker_SuccessPath_StatusDone ./services/worker/

# Run with verbose output
go test -v -race ./internal/...

# Run with coverage
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Benchmarks

```bash
make bench
# equivalent to: go test -bench=. -benchmem -count=3 ./internal/...
```

## Test Structure

Tests live alongside their package source files (standard Go convention):

```
internal/
  domain/
    task.go
    task_test.go        ← Status constants, IsTerminal
    errors.go
    errors_test.go      ← Error message format tests
  handlers/
    handler.go
    email.go
    email_test.go       ← Payload validation, cancelled context
    webhook.go
    webhook_test.go     ← httptest server, custom headers
    registry_test.go    ← Concurrent access, unknown type error
pkg/
  retry/
    retry.go
    retry_test.go       ← Attempt counts, context cancellation, OnRetry hook
services/
  dispatcher/
    dispatcher.go
    dispatcher_test.go  ← Routing, DLQ paths, rate limiting (package main)
  worker/
    worker.go
    worker_test.go      ← Success/retry/dead paths, idempotency (package main)
```

## Unit Testing Patterns

### Interface-based mocks (no framework)

All dependencies are behind interfaces (`StateStore`, `TaskRepository`, `Producer`, `Consumer`), making them trivially mockable:

```go
type fakeStore struct {
    states map[string]domain.Status
}

func newFakeStore() *fakeStore { return &fakeStore{states: make(map[string]domain.Status)} }

func (s *fakeStore) SetStatus(_ context.Context, id string, st domain.Status) error {
    s.states[id] = st
    return nil
}
func (s *fakeStore) GetStatus(_ context.Context, id string) (domain.Status, error) {
    st, ok := s.states[id]
    if !ok {
        return "", &domain.TaskNotFoundError{TaskID: id}
    }
    return st, nil
}
// ... implement remaining interface methods as no-ops
```

### Testing the retry package

```go
func TestDo_ReturnsErrorAfterMaxAttempts(t *testing.T) {
    calls := 0
    err := retry.Do(context.Background(), retry.Config{
        MaxAttempts: 3,
        BaseDelay:   time.Millisecond, // keep tests fast
    }, func() error {
        calls++
        return errors.New("permanent error")
    })

    require.Error(t, err)
    assert.Equal(t, 3, calls)
}
```

### Testing the rate limiter

The `RateLimiter` depends on a real Redis client. Use `testcontainers-go` for integration tests, or a locally running Redis for development:

```go
func TestSlidingWindowLimiter(t *testing.T) {
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer client.FlushAll(context.Background())

    limiter := redisstore.NewRateLimiter(client, 3, time.Second)

    for i := 0; i < 3; i++ {
        ok, err := limiter.Allow(context.Background(), "test-key")
        require.NoError(t, err)
        require.True(t, ok, "request %d should be allowed", i+1)
    }

    // 4th request in the same second should be rejected
    ok, err := limiter.Allow(context.Background(), "test-key")
    require.NoError(t, err)
    require.False(t, ok, "4th request should be rate-limited")
}
```

## Integration Testing

Integration tests live in `tests/integration/` and use `testcontainers-go` to spin up real Redis, Postgres, and Kafka containers automatically. No local infrastructure is needed.

```
tests/integration/
  setup_test.go      ← TestMain: starts 3 containers, runs migrations
  redis_test.go      ← StateStore + RateLimiter against real Redis
  postgres_test.go   ← TaskRepository CRUD against real Postgres
  kafka_test.go      ← Producer/Consumer round-trip + offset-commit guarantee
  e2e_test.go        ← Full pipeline: API-submit → Dispatch → Worker → DONE
```

All files carry the `//go:build integration` tag and are excluded from `make test`.

Run with:

```bash
make test-integration
# go test -tags=integration -race -v -timeout=120s ./tests/integration/...
```

`TestMain` in `setup_test.go` starts all containers once, runs migrations on the Postgres container, and tears everything down after the suite completes. Individual tests clean up their own rows/keys via `t.Cleanup`.

## Testing the Worker

The worker's `processMessage` method is the seam for unit tests — it takes a `kafka.Message` directly, so no real Kafka is needed. Use `WithBaseDelay(time.Millisecond)` to avoid the default 1-second retry wait:

```go
func TestWorker_SuccessPath_StatusDone(t *testing.T) {
    store := newFakeStore()
    repo  := newFakeRepo()
    reg   := handlers.NewRegistry()
    reg.Register(&fakeHandler{taskType: "email", callsErr: []error{nil}})

    w := NewWorker("test-worker", nil, &fakeProducer{}, store, repo, reg,
        WithRetries(3),
        WithBaseDelay(time.Millisecond),
    )

    raw, _ := json.Marshal(domain.Task{ID: "task-1", Type: "email"})
    err := w.processMessage(context.Background(), kafka.Message{Value: raw})
    require.NoError(t, err)

    assert.Equal(t, domain.StatusDone, store.states["task-1"])
}
```

## What to Test

| Layer | Implemented | Focus |
|-------|-------------|-------|
| `pkg/retry` | ✓ | Attempt counts, backoff timing, context cancellation, OnRetry hook |
| `internal/domain` | ✓ | Status constants, `IsTerminal`, error message format |
| `internal/handlers` | ✓ | Payload validation, context cancellation, HTTP responses (webhook) |
| `services/dispatcher` | ✓ | Routing, DLQ on malformed/empty type, rate-limit rejection, Kafka errors |
| `services/worker` | ✓ | Idempotency guard, retry → dead path, unknown handler type, success path |
| `internal/redis` | ✓ (integration) | State transitions, rate limiter window + expiry, independent keys |
| `internal/postgres` | ✓ (integration) | CRUD, `completed_at` on terminal status, `ListByStatus` filter |
| `tests/integration` | ✓ | Kafka round-trip, offset-commit guarantee, full E2E lifecycle |
