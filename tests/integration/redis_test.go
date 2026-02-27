//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	redisstore "github.com/ramiqadoumi/go-task-flow/internal/redis"
)

// newRedisClient returns a client connected to the test container and flushes
// the database on test cleanup so tests don't interfere with each other.
func newRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: testRedisAddr})
	t.Cleanup(func() {
		client.FlushDB(context.Background()) //nolint:errcheck
		client.Close()                       //nolint:errcheck
	})
	return client
}

func TestRedis_SetGetStatus_RoundTrip(t *testing.T) {
	store := redisstore.NewStateStore(newRedisClient(t))
	ctx := context.Background()

	require.NoError(t, store.SetStatus(ctx, "task-1", domain.StatusRunning))

	got, err := store.GetStatus(ctx, "task-1")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusRunning, got)
}

func TestRedis_GetStatus_NotFound(t *testing.T) {
	store := redisstore.NewStateStore(newRedisClient(t))

	_, err := store.GetStatus(context.Background(), "does-not-exist")
	require.Error(t, err)

	var notFound *domain.TaskNotFoundError
	require.ErrorAs(t, err, &notFound)
	assert.Equal(t, "does-not-exist", notFound.TaskID)
}

func TestRedis_SetGetMeta_RoundTrip(t *testing.T) {
	store := redisstore.NewStateStore(newRedisClient(t))
	ctx := context.Background()

	task := &domain.Task{
		ID:     "task-meta-1",
		Type:   "email",
		Status: domain.StatusPending,
	}
	require.NoError(t, store.SetTaskMeta(ctx, task))

	got, err := store.GetTaskMeta(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, task.ID, got.ID)
	assert.Equal(t, task.Type, got.Type)
	assert.Equal(t, task.Status, got.Status)
}

func TestRedis_StatusTransitions(t *testing.T) {
	store := redisstore.NewStateStore(newRedisClient(t))
	ctx := context.Background()

	transitions := []domain.Status{
		domain.StatusPending,
		domain.StatusQueued,
		domain.StatusRunning,
		domain.StatusRetrying,
		domain.StatusDone,
	}
	for _, status := range transitions {
		require.NoError(t, store.SetStatus(ctx, "task-fsm", status))
		got, err := store.GetStatus(ctx, "task-fsm")
		require.NoError(t, err)
		assert.Equal(t, status, got, "status should be %s", status)
	}
}

// ── Rate limiter ─────────────────────────────────────────────────────────────

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	limiter := redisstore.NewRateLimiter(newRedisClient(t), 5, time.Second)
	ctx := context.Background()

	for i := range 5 {
		ok, err := limiter.Allow(ctx, "within-limit")
		require.NoError(t, err)
		assert.True(t, ok, "request %d should be allowed", i+1)
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	limiter := redisstore.NewRateLimiter(newRedisClient(t), 3, time.Second)
	ctx := context.Background()

	for range 3 {
		ok, err := limiter.Allow(ctx, "over-limit")
		require.NoError(t, err)
		require.True(t, ok)
	}

	ok, err := limiter.Allow(ctx, "over-limit")
	require.NoError(t, err)
	assert.False(t, ok, "4th request should be rate-limited")
}

func TestRateLimiter_WindowExpiry(t *testing.T) {
	// Use a short window so the test doesn't take too long.
	window := 200 * time.Millisecond
	limiter := redisstore.NewRateLimiter(newRedisClient(t), 2, window)
	ctx := context.Background()

	// Fill the window.
	for range 2 {
		ok, err := limiter.Allow(ctx, "expiry-key")
		require.NoError(t, err)
		require.True(t, ok)
	}

	// Third request in the same window should be blocked.
	ok, err := limiter.Allow(ctx, "expiry-key")
	require.NoError(t, err)
	assert.False(t, ok, "should be blocked within window")

	// After the window expires, the limit resets.
	time.Sleep(window + 50*time.Millisecond)

	ok, err = limiter.Allow(ctx, "expiry-key")
	require.NoError(t, err)
	assert.True(t, ok, "should be allowed after window expires")
}

func TestRateLimiter_IndependentKeys(t *testing.T) {
	limiter := redisstore.NewRateLimiter(newRedisClient(t), 1, time.Second)
	ctx := context.Background()

	// Exhaust limit for key A.
	ok, err := limiter.Allow(ctx, "key-a")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = limiter.Allow(ctx, "key-a")
	require.NoError(t, err)
	assert.False(t, ok, "key-a should be limited")

	// key-b has its own independent window.
	ok, err = limiter.Allow(ctx, "key-b")
	require.NoError(t, err)
	assert.True(t, ok, "key-b should be independent of key-a")
}
