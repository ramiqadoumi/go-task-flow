package domain

import "fmt"

// TaskNotFoundError is returned when a task ID does not exist.
type TaskNotFoundError struct {
	TaskID string
}

func (e *TaskNotFoundError) Error() string {
	return fmt.Sprintf("task not found: %s", e.TaskID)
}

// RateLimitExceededError is returned when a task type exceeds its rate limit.
type RateLimitExceededError struct {
	TaskType string
	Limit    int
}

func (e *RateLimitExceededError) Error() string {
	return fmt.Sprintf("rate limit exceeded for task type %q: limit is %d", e.TaskType, e.Limit)
}

// InvalidTaskTypeError is returned when no handler is registered for a task type.
type InvalidTaskTypeError struct {
	TaskType string
}

func (e *InvalidTaskTypeError) Error() string {
	return fmt.Sprintf("no handler registered for task type %q", e.TaskType)
}

// TaskAlreadyProcessedError is returned when a task is re-delivered but already in a terminal state.
type TaskAlreadyProcessedError struct {
	TaskID string
	Status Status
}

func (e *TaskAlreadyProcessedError) Error() string {
	return fmt.Sprintf("task %s already processed with status %s", e.TaskID, e.Status)
}
