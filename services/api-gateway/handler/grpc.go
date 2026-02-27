package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
	"github.com/ramiqadoumi/go-task-flow/internal/postgres"
	redisstore "github.com/ramiqadoumi/go-task-flow/internal/redis"
	taskv1 "github.com/ramiqadoumi/go-task-flow/proto/task/v1"
)

// GRPC implements taskv1.TaskServiceServer.
type GRPC struct {
	taskv1.UnimplementedTaskServiceServer
	producer kafka.Producer
	store    redisstore.StateStore
	repo     postgres.TaskRepository
	logger   *slog.Logger
}

// NewGRPC creates a new gRPC handler sharing the same dependencies as REST.
func NewGRPC(
	producer kafka.Producer,
	store redisstore.StateStore,
	repo postgres.TaskRepository,
	logger *slog.Logger,
) *GRPC {
	return &GRPC{producer: producer, store: store, repo: repo, logger: logger}
}

func (g *GRPC) SubmitTask(ctx context.Context, req *taskv1.SubmitTaskRequest) (*taskv1.SubmitTaskResponse, error) {
	if req.Type == "" {
		return nil, status.Error(codes.InvalidArgument, "field 'type' is required")
	}
	if len(req.Payload) == 0 {
		return nil, status.Error(codes.InvalidArgument, "field 'payload' is required")
	}

	taskID := uuid.New().String()
	now := time.Now().UTC()

	var scheduledAt *time.Time
	if req.ScheduledAt != 0 {
		t := time.Unix(req.ScheduledAt, 0).UTC()
		scheduledAt = &t
	}

	task := &domain.Task{
		ID:          taskID,
		Type:        req.Type,
		Payload:     json.RawMessage(req.Payload),
		Status:      domain.StatusPending,
		Priority:    int(req.Priority),
		MaxRetries:  3,
		CreatedAt:   now,
		UpdatedAt:   now,
		ScheduledAt: scheduledAt,
	}

	if err := g.store.SetTaskMeta(ctx, task); err != nil {
		g.logger.Error("grpc SubmitTask: set meta", slog.String("error", err.Error()))
		return nil, status.Error(codes.Internal, "failed to create task")
	}
	if err := g.store.SetStatus(ctx, taskID, domain.StatusPending); err != nil {
		g.logger.Error("grpc SubmitTask: set status", slog.String("error", err.Error()))
		return nil, status.Error(codes.Internal, "failed to create task")
	}
	if err := g.repo.Create(ctx, task); err != nil {
		// Non-fatal: Kafka + Redis are the primary flow.
		g.logger.Error("grpc SubmitTask: persist", slog.String("error", err.Error()))
	}

	payload, _ := json.Marshal(task)
	if err := g.producer.Publish(ctx, pendingTopic, taskID, payload); err != nil {
		g.logger.Error("grpc SubmitTask: publish", slog.String("error", err.Error()))
		return nil, status.Error(codes.Internal, "failed to enqueue task")
	}

	g.logger.Info("grpc: task submitted",
		slog.String("task_id", taskID),
		slog.String("type", req.Type),
	)

	return &taskv1.SubmitTaskResponse{
		TaskId:    taskID,
		Status:    string(domain.StatusPending),
		CreatedAt: now.Unix(),
	}, nil
}

func (g *GRPC) GetTaskStatus(ctx context.Context, req *taskv1.GetTaskStatusRequest) (*taskv1.TaskStatusResponse, error) {
	if req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id is required")
	}

	task, err := g.store.GetTaskMeta(ctx, req.TaskId)
	if err != nil {
		var notFound *domain.TaskNotFoundError
		if !errors.As(err, &notFound) {
			return nil, status.Error(codes.Internal, "failed to retrieve task")
		}
		task, err = g.repo.GetByID(ctx, req.TaskId)
		if err != nil {
			if errors.As(err, &notFound) {
				return nil, status.Error(codes.NotFound, "task not found")
			}
			return nil, status.Error(codes.Internal, "failed to retrieve task")
		}
	}

	// TODO: Check why we get status
	if s, err := g.store.GetStatus(ctx, req.TaskId); err == nil {
		task.Status = s
	}

	resp := &taskv1.TaskStatusResponse{
		TaskId:    task.ID,
		Type:      task.Type,
		Status:    string(task.Status),
		Attempts:  int32(task.Attempts),
		CreatedAt: task.CreatedAt.Unix(),
	}
	if task.CompletedAt != nil {
		resp.CompletedAt = task.CompletedAt.Unix()
		resp.DurationMs = task.CompletedAt.Sub(task.CreatedAt).Milliseconds()
	}
	return resp, nil
}

// StreamTaskUpdates polls every 500 ms and streams status until a terminal state is reached.
func (g *GRPC) StreamTaskUpdates(
	req *taskv1.GetTaskStatusRequest,
	stream grpc.ServerStreamingServer[taskv1.TaskStatusResponse],
) error {
	if req.TaskId == "" {
		return status.Error(codes.InvalidArgument, "task_id is required")
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
			resp, err := g.GetTaskStatus(stream.Context(), &taskv1.GetTaskStatusRequest{TaskId: req.TaskId})
			if err != nil {
				return err
			}
			if err := stream.Send(resp); err != nil {
				return err
			}
			if domain.Status(resp.Status).IsTerminal() {
				return nil
			}
		}
	}
}

// BulkSubmitTasks accepts a client stream and submits each task independently.
func (g *GRPC) BulkSubmitTasks(
	stream grpc.ClientStreamingServer[taskv1.SubmitTaskRequest, taskv1.BulkSubmitResponse],
) error {
	var submitted, failed int32
	var taskIDs []string

	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return status.Errorf(codes.Internal, "stream recv: %v", err)
		}

		resp, submitErr := g.SubmitTask(stream.Context(), req)
		if submitErr != nil {
			failed++
			continue
		}
		submitted++
		taskIDs = append(taskIDs, resp.TaskId)
	}

	return stream.SendAndClose(&taskv1.BulkSubmitResponse{
		Submitted: submitted,
		Failed:    failed,
		TaskIds:   taskIDs,
	})
}
