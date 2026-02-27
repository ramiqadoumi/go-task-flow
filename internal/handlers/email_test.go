package handlers_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	"github.com/ramiqadoumi/go-task-flow/internal/handlers"
)

func TestEmailHandler_TaskType(t *testing.T) {
	h := handlers.NewEmailHandler(handlers.EmailConfig{Host: "localhost", Port: 1025, From: "from@test.com"})
	assert.Equal(t, "email", h.TaskType())
}

func TestEmailHandler_Handle_InvalidJSON(t *testing.T) {
	h := handlers.NewEmailHandler(handlers.EmailConfig{Host: "localhost", Port: 1025})
	task := &domain.Task{Payload: []byte("not-json")}

	err := h.Handle(context.Background(), task)
	require.Error(t, err, "should fail on invalid JSON payload")
}

func TestEmailHandler_Handle_MissingTo(t *testing.T) {
	h := handlers.NewEmailHandler(handlers.EmailConfig{Host: "localhost", Port: 1025})
	task := &domain.Task{Payload: []byte(`{"subject":"hi","body":"world"}`)}

	err := h.Handle(context.Background(), task)
	require.Error(t, err, "should fail when 'to' field is missing")
	assert.Contains(t, err.Error(), "to")
}

func TestEmailHandler_Handle_CancelledContext(t *testing.T) {
	h := handlers.NewEmailHandler(handlers.EmailConfig{Host: "localhost", Port: 1025})
	task := &domain.Task{Payload: []byte(`{"to":"x@y.com","subject":"hi","body":"world"}`)}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling Handle

	err := h.Handle(ctx, task)
	require.Error(t, err, "cancelled context should result in an error")
}
