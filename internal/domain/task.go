package domain

import "time"

// Status represents the states a task can be in.
type Status string

const (
	StatusPending  Status = "PENDING"
	StatusQueued   Status = "QUEUED"
	StatusRunning  Status = "RUNNING"
	StatusDone     Status = "DONE"
	StatusFailed   Status = "FAILED"
	StatusRetrying Status = "RETRYING"
	StatusDead     Status = "DEAD"
)

// IsTerminal returns true if no further state transitions are possible.
func (s Status) IsTerminal() bool {
	return s == StatusDone || s == StatusDead || s == StatusFailed
}

// Task is the core domain entity representing a unit of background work.
type Task struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Payload     []byte     `json:"payload"`
	Status      Status     `json:"status"`
	Priority    int        `json:"priority"`
	Attempts    int        `json:"attempts"`
	MaxRetries  int        `json:"max_retries"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// TaskExecution records a single execution attempt of a task.
type TaskExecution struct {
	ID         string    `json:"id"`
	TaskID     string    `json:"task_id"`
	WorkerID   string    `json:"worker_id"`
	Attempt    int       `json:"attempt"`
	Status     Status    `json:"status"`
	DurationMs int64     `json:"duration_ms"`
	Error      string    `json:"error,omitempty"`
	ExecutedAt time.Time `json:"executed_at"`
}
