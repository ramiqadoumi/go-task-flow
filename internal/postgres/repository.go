package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ramiqadoumi/go-task-flow/internal/domain"
)

// TaskRepository abstracts all database access for tasks.
type TaskRepository interface {
	Create(ctx context.Context, task *domain.Task) error
	UpdateStatus(ctx context.Context, id string, status domain.Status) error
	RecordExecution(ctx context.Context, exec *domain.TaskExecution) error
	GetByID(ctx context.Context, id string) (*domain.Task, error)
	ListByStatus(ctx context.Context, status domain.Status, limit int) ([]*domain.Task, error)
}

type repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pgxpool with the TaskRepository interface.
func NewRepository(pool *pgxpool.Pool) TaskRepository {
	return &repository{pool: pool}
}

// NewPool creates a pgxpool and verifies connectivity.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return pool, nil
}

func (r *repository) Create(ctx context.Context, task *domain.Task) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO tasks
			(id, type, payload, status, priority, attempts, max_retries, created_at, updated_at, scheduled_at)
		VALUES
			($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`,
		task.ID, task.Type, task.Payload, string(task.Status),
		task.Priority, task.Attempts, task.MaxRetries,
		task.CreatedAt, task.UpdatedAt, task.ScheduledAt,
	)
	if err != nil {
		return fmt.Errorf("create task %s: %w", task.ID, err)
	}
	return nil
}

func (r *repository) UpdateStatus(ctx context.Context, id string, status domain.Status) error {
	now := time.Now().UTC()
	var completedAt *time.Time
	if status.IsTerminal() {
		t := now
		completedAt = &t
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE tasks
		SET status = $1, updated_at = $2, completed_at = $3
		WHERE id = $4
	`, string(status), now, completedAt, id)
	if err != nil {
		return fmt.Errorf("update status for task %s: %w", id, err)
	}
	return nil
}

func (r *repository) RecordExecution(ctx context.Context, exec *domain.TaskExecution) error {
	if exec.ID == "" {
		exec.ID = uuid.New().String()
	}
	if exec.ExecutedAt.IsZero() {
		exec.ExecutedAt = time.Now().UTC()
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO task_executions
			(id, task_id, worker_id, attempt, status, duration_ms, error, executed_at)
		VALUES
			($1, $2, $3, $4, $5, $6, $7, $8)
	`,
		exec.ID, exec.TaskID, exec.WorkerID, exec.Attempt,
		string(exec.Status), exec.DurationMs, exec.Error, exec.ExecutedAt,
	)
	if err != nil {
		return fmt.Errorf("record execution for task %s: %w", exec.TaskID, err)
	}
	return nil
}

func (r *repository) GetByID(ctx context.Context, id string) (*domain.Task, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, type, payload, status, priority, attempts, max_retries,
		       created_at, updated_at, scheduled_at, completed_at
		FROM tasks
		WHERE id = $1
	`, id)

	return scanTask(row)
}

func (r *repository) ListByStatus(ctx context.Context, status domain.Status, limit int) ([]*domain.Task, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, type, payload, status, priority, attempts, max_retries,
		       created_at, updated_at, scheduled_at, completed_at
		FROM tasks
		WHERE status = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, string(status), limit)
	if err != nil {
		return nil, fmt.Errorf("list tasks by status %s: %w", status, err)
	}
	defer rows.Close()

	var tasks []*domain.Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// scanTask reads a task row from any pgx row type.
func scanTask(row interface {
	Scan(...any) error
}) (*domain.Task, error) {
	var task domain.Task
	var statusStr string
	err := row.Scan(
		&task.ID, &task.Type, &task.Payload, &statusStr,
		&task.Priority, &task.Attempts, &task.MaxRetries,
		&task.CreatedAt, &task.UpdatedAt, &task.ScheduledAt, &task.CompletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &domain.TaskNotFoundError{TaskID: "unknown"}
		}
		return nil, fmt.Errorf("scan task: %w", err)
	}
	task.Status = domain.Status(statusStr)
	return &task, nil
}
