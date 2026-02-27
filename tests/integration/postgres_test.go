//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	"github.com/ramiqadoumi/go-task-flow/internal/postgres"
)

// newRepo creates a repository connected to the test Postgres container
// and truncates the tables on cleanup.
func newRepo(t *testing.T) postgres.TaskRepository {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testPostgresDSN)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Exec(ctx, "TRUNCATE task_executions, tasks CASCADE") //nolint:errcheck
		pool.Close()
	})
	return postgres.NewRepository(pool)
}

func makeTask(taskType string) *domain.Task {
	now := time.Now().UTC()
	return &domain.Task{
		ID:         uuid.New().String(),
		Type:       taskType,
		Payload:    []byte(`{"test":true}`),
		Status:     domain.StatusPending,
		MaxRetries: 3,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func TestPostgres_Create_GetByID(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	task := makeTask("email")
	require.NoError(t, repo.Create(ctx, task))

	got, err := repo.GetByID(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, task.ID, got.ID)
	assert.Equal(t, "email", got.Type)
	assert.Equal(t, domain.StatusPending, got.Status)
}

func TestPostgres_GetByID_NotFound(t *testing.T) {
	repo := newRepo(t)

	_, err := repo.GetByID(context.Background(), uuid.New().String())
	require.Error(t, err)

	var notFound *domain.TaskNotFoundError
	require.ErrorAs(t, err, &notFound)
}

func TestPostgres_UpdateStatus_SetsCompletedAt(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	task := makeTask("webhook")
	require.NoError(t, repo.Create(ctx, task))
	require.NoError(t, repo.UpdateStatus(ctx, task.ID, domain.StatusDone))

	got, err := repo.GetByID(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDone, got.Status)
	assert.NotNil(t, got.CompletedAt, "completed_at should be set for terminal status")
}

func TestPostgres_RecordExecution(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	task := makeTask("email")
	require.NoError(t, repo.Create(ctx, task))

	exec := &domain.TaskExecution{
		TaskID:     task.ID,
		WorkerID:   "worker-test-1",
		Attempt:    1,
		Status:     domain.StatusDone,
		DurationMs: 42,
		ExecutedAt: time.Now().UTC(),
	}
	require.NoError(t, repo.RecordExecution(ctx, exec))
	assert.NotEmpty(t, exec.ID, "RecordExecution should populate the ID field")
}

func TestPostgres_ListByStatus(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	// Insert 3 PENDING tasks.
	for i := range 3 {
		task := makeTask(fmt.Sprintf("email-%d", i))
		require.NoError(t, repo.Create(ctx, task))
	}

	// Insert 1 DONE task.
	doneTask := makeTask("webhook")
	require.NoError(t, repo.Create(ctx, doneTask))
	require.NoError(t, repo.UpdateStatus(ctx, doneTask.ID, domain.StatusDone))

	pending, err := repo.ListByStatus(ctx, domain.StatusPending, 10)
	require.NoError(t, err)
	assert.Len(t, pending, 3)

	done, err := repo.ListByStatus(ctx, domain.StatusDone, 10)
	require.NoError(t, err)
	require.Len(t, done, 1)
	assert.Equal(t, doneTask.ID, done[0].ID)
}
