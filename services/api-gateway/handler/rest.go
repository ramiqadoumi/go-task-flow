package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
	"github.com/ramiqadoumi/go-task-flow/internal/postgres"
	redisstore "github.com/ramiqadoumi/go-task-flow/internal/redis"
	"github.com/ramiqadoumi/go-task-flow/pkg/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const pendingTopic = "tasks.pending"

// REST handles HTTP requests for the API Gateway.
type REST struct {
	producer kafka.Producer
	store    redisstore.StateStore
	repo     postgres.TaskRepository
	logger   *slog.Logger
}

// NewREST creates a new REST handler.
func NewREST(producer kafka.Producer, store redisstore.StateStore, repo postgres.TaskRepository, logger *slog.Logger) *REST {
	return &REST{producer: producer, store: store, repo: repo, logger: logger}
}

// SubmitTaskRequest is the JSON body for POST /api/v1/tasks.
type SubmitTaskRequest struct {
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	Priority    int             `json:"priority"`
	ScheduledAt *time.Time      `json:"scheduled_at,omitempty"`
}

// SubmitTaskResponse is the 202 response body.
type SubmitTaskResponse struct {
	TaskID    string    `json:"task_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// TaskStatusResponse is the GET /tasks/{id} response body.
type TaskStatusResponse struct {
	TaskID      string     `json:"task_id"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	Priority    int        `json:"priority"`
	Attempts    int        `json:"attempts"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	DurationMs  int64      `json:"duration_ms,omitempty"`
}

// SubmitTask handles POST /api/v1/tasks.
func (h *REST) SubmitTask(w http.ResponseWriter, r *http.Request) {
	ctx, span := otel.Tracer("api-gateway").Start(r.Context(), "api_gateway.submit_task")
	defer span.End()
	r = r.WithContext(ctx)

	var req SubmitTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if strings.TrimSpace(req.Type) == "" {
		writeError(w, http.StatusBadRequest, "field 'type' is required")
		return
	}
	if len(req.Payload) == 0 || string(req.Payload) == "null" {
		writeError(w, http.StatusBadRequest, "field 'payload' is required")
		return
	}

	taskID := uuid.New().String()
	now := time.Now().UTC()

	span.SetAttributes(
		attribute.String("task.id", taskID),
		attribute.String("task.type", req.Type),
	)

	task := &domain.Task{
		ID:          taskID,
		Type:        req.Type,
		Payload:     req.Payload,
		Status:      domain.StatusPending,
		Priority:    req.Priority,
		MaxRetries:  3, // TODO: make configurable
		CreatedAt:   now,
		UpdatedAt:   now,
		ScheduledAt: req.ScheduledAt,
	}

	// Persist to Redis (fast state reads).
	if err := h.store.SetTaskMeta(ctx, task); err != nil {
		h.logger.Error("failed to set task meta", slog.String("task_id", taskID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to create task")
		return
	}
	if err := h.store.SetStatus(ctx, taskID, domain.StatusPending); err != nil {
		h.logger.Error("failed to set task status", slog.String("task_id", taskID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to create task")
		return
	}

	// Persist to PostgreSQL (audit trail).
	if err := h.repo.Create(ctx, task); err != nil {
		h.logger.Error("failed to persist task", slog.String("task_id", taskID), slog.String("error", err.Error()))
		// Non-fatal: Kafka+Redis are the primary flow. Log and continue.
	}

	// Publish to Kafka — task_id as message key ensures per-task ordering.
	payload, err := json.Marshal(task)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to serialize task")
		return
	}
	
	if err := h.producer.Publish(ctx, pendingTopic, taskID, payload); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "kafka publish failed")
		h.logger.Error("failed to publish task", slog.String("task_id", taskID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to enqueue task")
		return
	}

	telemetry.APITasksSubmitted.WithLabelValues(req.Type).Inc()
	h.logger.Info("task submitted",
		slog.String("task_id", taskID),
		slog.String("type", req.Type),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(SubmitTaskResponse{
		TaskID:    taskID,
		Status:    string(domain.StatusPending),
		CreatedAt: now,
	})
}

// GetTaskStatus handles GET /api/v1/tasks/{id}.
func (h *REST) GetTaskStatus(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "task ID is required")
		return
	}

	ctx := r.Context()

	// Fast path: Redis.
	task, err := h.store.GetTaskMeta(ctx, taskID)
	if err != nil {
		var notFound *domain.TaskNotFoundError
		if !errors.As(err, &notFound) {
			h.logger.Error("redis error", slog.String("task_id", taskID), slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, "failed to retrieve task")
			return
		}

		// Slow path: PostgreSQL fallback (Redis TTL expired or cache miss).
		task, err = h.repo.GetByID(ctx, taskID)
		if err != nil {
			if errors.As(err, &notFound) {
				writeError(w, http.StatusNotFound, "task not found")
				return
			}
			h.logger.Error("postgres error", slog.String("task_id", taskID), slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, "failed to retrieve task")
			return
		}
	}

	// Always read the live status from Redis (worker may have updated it).
	if status, err := h.store.GetStatus(ctx, taskID); err == nil {
		task.Status = status
	}

	resp := TaskStatusResponse{
		TaskID:      task.ID,
		Type:        task.Type,
		Status:      string(task.Status),
		Priority:    task.Priority,
		Attempts:    task.Attempts,
		CreatedAt:   task.CreatedAt,
		CompletedAt: task.CompletedAt,
	}
	if task.CompletedAt != nil {
		resp.DurationMs = task.CompletedAt.Sub(task.CreatedAt).Milliseconds()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Healthz handles GET /healthz.
func (h *REST) Healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// Readyz handles GET /readyz — checks Redis connectivity.
func (h *REST) Readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if _, err := h.store.GetStatus(ctx, "__readyz__"); err != nil {
		var notFound *domain.TaskNotFoundError
		if !errors.As(err, &notFound) {
			writeError(w, http.StatusServiceUnavailable, "redis not ready")
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ready"}`))
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
