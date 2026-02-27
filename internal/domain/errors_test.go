package domain_test

import (
	"strings"
	"testing"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
)

func TestTaskNotFoundError(t *testing.T) {
	err := &domain.TaskNotFoundError{TaskID: "abc-123"}
	if !strings.Contains(err.Error(), "abc-123") {
		t.Errorf("error message should contain task ID, got: %q", err.Error())
	}
}

func TestRateLimitExceededError(t *testing.T) {
	err := &domain.RateLimitExceededError{TaskType: "email", Limit: 100}
	msg := err.Error()
	if !strings.Contains(msg, "email") {
		t.Errorf("error message should contain task type, got: %q", msg)
	}
	if !strings.Contains(msg, "100") {
		t.Errorf("error message should contain limit, got: %q", msg)
	}
}

func TestInvalidTaskTypeError(t *testing.T) {
	err := &domain.InvalidTaskTypeError{TaskType: "unknown-type"}
	if !strings.Contains(err.Error(), "unknown-type") {
		t.Errorf("error message should contain task type, got: %q", err.Error())
	}
}

func TestTaskAlreadyProcessedError(t *testing.T) {
	err := &domain.TaskAlreadyProcessedError{TaskID: "xyz-789", Status: domain.StatusDone}
	msg := err.Error()
	if !strings.Contains(msg, "xyz-789") {
		t.Errorf("error message should contain task ID, got: %q", msg)
	}
	if !strings.Contains(msg, "DONE") {
		t.Errorf("error message should contain status, got: %q", msg)
	}
}

func TestAllErrorTypesImplementError(t *testing.T) {
	// Compile-time interface checks via assignment to error variables.
	var _ error = &domain.TaskNotFoundError{}
	var _ error = &domain.RateLimitExceededError{}
	var _ error = &domain.InvalidTaskTypeError{}
	var _ error = &domain.TaskAlreadyProcessedError{}
}
