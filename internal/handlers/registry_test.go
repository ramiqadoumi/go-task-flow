package handlers_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	"github.com/ramiqadoumi/go-task-flow/internal/handlers"
)

// stub is a minimal Handler implementation for registry tests.
type stub struct{ taskType string }

func (s *stub) TaskType() string                                   { return s.taskType }
func (s *stub) Handle(_ context.Context, _ *domain.Task) error     { return nil }

func TestRegistry_Get_KnownType(t *testing.T) {
	reg := handlers.NewRegistry()
	reg.Register(&stub{taskType: "email"})

	h, err := reg.Get("email")
	require.NoError(t, err)
	assert.Equal(t, "email", h.TaskType())
}

func TestRegistry_Get_UnknownType(t *testing.T) {
	reg := handlers.NewRegistry()

	_, err := reg.Get("sms")
	require.Error(t, err)

	var invalidType *domain.InvalidTaskTypeError
	assert.True(t, errors.As(err, &invalidType),
		"expected InvalidTaskTypeError, got %T", err)
	assert.Equal(t, "sms", invalidType.TaskType)
}

func TestRegistry_Register_Overwrites(t *testing.T) {
	reg := handlers.NewRegistry()
	reg.Register(&stub{taskType: "email"})
	reg.Register(&stub{taskType: "email"}) // second registration — should replace

	h, err := reg.Get("email")
	require.NoError(t, err)
	assert.Equal(t, "email", h.TaskType())
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	reg := handlers.NewRegistry()
	reg.Register(&stub{taskType: "email"})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); reg.Register(&stub{taskType: "webhook"}) }()
		go func() { defer wg.Done(); _, _ = reg.Get("email") }()
	}
	wg.Wait()
}
