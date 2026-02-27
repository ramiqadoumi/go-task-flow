package handlers

import (
	"context"
	"sync"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
)

// Handler processes a task of a specific type.
type Handler interface {
	Handle(ctx context.Context, task *domain.Task) error
	TaskType() string
}

// Registry maps task types to their handlers.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

// Register adds a handler. Safe to call concurrently.
func (r *Registry) Register(h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[h.TaskType()] = h
}

// Get returns the handler for the given task type.
// Returns InvalidTaskTypeError if not registered.
func (r *Registry) Get(taskType string) (Handler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[taskType]
	if !ok {
		return nil, &domain.InvalidTaskTypeError{TaskType: taskType}
	}
	return h, nil
}
