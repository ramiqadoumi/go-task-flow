package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	"github.com/ramiqadoumi/go-task-flow/internal/handlers"
	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
	"github.com/ramiqadoumi/go-task-flow/internal/postgres"
)

// ── mocks ────────────────────────────────────────────────────────────────────

type fakeProducer struct {
	topics []string
	err    error
}

func (p *fakeProducer) Publish(_ context.Context, topic, _ string, _ []byte) error {
	if p.err != nil {
		return p.err
	}
	p.topics = append(p.topics, topic)
	return nil
}
func (p *fakeProducer) Close() error { return nil }

type fakeStore struct {
	states    map[string]domain.Status
	setErrFor map[domain.Status]error // return error when setting this specific status
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		states:    make(map[string]domain.Status),
		setErrFor: make(map[domain.Status]error),
	}
}

func (s *fakeStore) SetStatus(_ context.Context, id string, st domain.Status) error {
	if err, ok := s.setErrFor[st]; ok {
		return err
	}
	s.states[id] = st
	return nil
}
func (s *fakeStore) GetStatus(_ context.Context, id string) (domain.Status, error) {
	st, ok := s.states[id]
	if !ok {
		return "", &domain.TaskNotFoundError{TaskID: id}
	}
	return st, nil
}
func (s *fakeStore) SetTaskMeta(_ context.Context, _ *domain.Task) error { return nil }
func (s *fakeStore) GetTaskMeta(_ context.Context, id string) (*domain.Task, error) {
	return nil, &domain.TaskNotFoundError{TaskID: id}
}
func (s *fakeStore) SetResult(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}
func (s *fakeStore) GetResult(_ context.Context, id string) ([]byte, error) {
	return nil, &domain.TaskNotFoundError{TaskID: id}
}

type fakeRepo struct {
	executions []*domain.TaskExecution
	statuses   map[string]domain.Status
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{statuses: make(map[string]domain.Status)}
}

func (r *fakeRepo) Create(_ context.Context, _ *domain.Task) error { return nil }
func (r *fakeRepo) UpdateStatus(_ context.Context, id string, status domain.Status) error {
	r.statuses[id] = status
	return nil
}
func (r *fakeRepo) RecordExecution(_ context.Context, exec *domain.TaskExecution) error {
	r.executions = append(r.executions, exec)
	return nil
}
func (r *fakeRepo) GetByID(_ context.Context, _ string) (*domain.Task, error) { return nil, nil }
func (r *fakeRepo) ListByStatus(_ context.Context, _ domain.Status, _ int) ([]*domain.Task, error) {
	return nil, nil
}

// Ensure fakeRepo satisfies the interface at compile time.
var _ postgres.TaskRepository = (*fakeRepo)(nil)

type fakeHandler struct {
	taskType string
	callsErr []error // errors to return per call; nil entry = success
	calls    int
}

func (h *fakeHandler) TaskType() string { return h.taskType }
func (h *fakeHandler) Handle(_ context.Context, _ *domain.Task) error {
	var err error
	if h.calls < len(h.callsErr) {
		err = h.callsErr[h.calls]
	}
	h.calls++
	return err
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestWorker(producer *fakeProducer, store *fakeStore, repo *fakeRepo, reg *handlers.Registry) *Worker {
	return NewWorker("test-worker", nil, producer, store, repo, reg,
		WithLogger(slog.Default()),
		WithRetries(3),
		WithBaseDelay(time.Millisecond),
	)
}

func taskMsg(t *testing.T, id, taskType string) kafka.Message {
	t.Helper()
	raw, err := json.Marshal(domain.Task{ID: id, Type: taskType})
	require.NoError(t, err)
	return kafka.Message{Value: raw}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestWorker_SuccessPath_StatusDone(t *testing.T) {
	store := newFakeStore()
	repo := newFakeRepo()
	prod := &fakeProducer{}

	reg := handlers.NewRegistry()
	reg.Register(&fakeHandler{taskType: "email", callsErr: []error{nil}})

	w := newTestWorker(prod, store, repo, reg)
	err := w.processMessage(context.Background(), taskMsg(t, "task-1", "email"))
	require.NoError(t, err)

	assert.Equal(t, domain.StatusDone, store.states["task-1"], "Redis state should be DONE")
	assert.Equal(t, domain.StatusDone, repo.statuses["task-1"], "Postgres status should be DONE")
	require.Len(t, repo.executions, 1)
	assert.Equal(t, domain.StatusDone, repo.executions[0].Status)
	assert.Empty(t, prod.topics, "no DLQ publish on success")
}

func TestWorker_RetryPath_StatusDead(t *testing.T) {
	store := newFakeStore()
	repo := newFakeRepo()
	prod := &fakeProducer{}

	sentinel := errors.New("handler error")
	reg := handlers.NewRegistry()
	// Always fails — handler error returned every call.
	reg.Register(&fakeHandler{
		taskType: "email",
		callsErr: []error{sentinel, sentinel, sentinel, sentinel},
	})

	w := newTestWorker(prod, store, repo, reg)
	err := w.processMessage(context.Background(), taskMsg(t, "task-2", "email"))
	require.NoError(t, err) // processMessage always returns nil

	assert.Equal(t, domain.StatusDead, store.states["task-2"])
	assert.Equal(t, domain.StatusDead, repo.statuses["task-2"])
	require.Len(t, repo.executions, 1)
	assert.Equal(t, domain.StatusDead, repo.executions[0].Status)
	assert.Contains(t, prod.topics, topicDLQ, "exhausted task must go to DLQ")
}

func TestWorker_Idempotency_AlreadyDone(t *testing.T) {
	store := newFakeStore()
	store.states["task-3"] = domain.StatusDone // pre-seeded terminal state

	repo := newFakeRepo()
	prod := &fakeProducer{}

	h := &fakeHandler{taskType: "email"}
	reg := handlers.NewRegistry()
	reg.Register(h)

	w := newTestWorker(prod, store, repo, reg)
	err := w.processMessage(context.Background(), taskMsg(t, "task-3", "email"))
	require.NoError(t, err)

	assert.Equal(t, 0, h.calls, "handler must not be called for already-terminal task")
	assert.Empty(t, repo.executions)
}

func TestWorker_MalformedJSON_Discarded(t *testing.T) {
	w := newTestWorker(&fakeProducer{}, newFakeStore(), newFakeRepo(), handlers.NewRegistry())
	err := w.processMessage(context.Background(), kafka.Message{Value: []byte("not-json")})
	require.NoError(t, err, "malformed message must be silently discarded")
}

func TestWorker_UnknownHandlerType_StatusDead(t *testing.T) {
	store := newFakeStore()
	prod := &fakeProducer{}

	// Registry has no "sms" handler.
	w := newTestWorker(prod, store, newFakeRepo(), handlers.NewRegistry())
	err := w.processMessage(context.Background(), taskMsg(t, "task-4", "sms"))
	require.NoError(t, err)

	assert.Equal(t, domain.StatusDead, store.states["task-4"])
	assert.Contains(t, prod.topics, topicDLQ)
}

func TestWorker_SetRunningFails_OffsetNotCommitted(t *testing.T) {
	store := newFakeStore()
	// Simulate a Redis failure specifically when transitioning to RUNNING.
	store.setErrFor[domain.StatusRunning] = errors.New("redis unavailable")

	reg := handlers.NewRegistry()
	reg.Register(&fakeHandler{taskType: "email"})

	w := newTestWorker(&fakeProducer{}, store, newFakeRepo(), reg)
	err := w.processMessage(context.Background(), taskMsg(t, "task-6", "email"))

	// processMessage must return a non-nil error so the Kafka offset is NOT committed.
	require.Error(t, err, "offset must not be committed when SetStatus(RUNNING) fails")
	assert.NotEqual(t, domain.StatusRunning, store.states["task-6"])
}

func TestWorker_SucceedsOnSecondAttempt(t *testing.T) {
	store := newFakeStore()
	repo := newFakeRepo()
	prod := &fakeProducer{}

	reg := handlers.NewRegistry()
	reg.Register(&fakeHandler{
		taskType: "email",
		callsErr: []error{errors.New("transient"), nil}, // fail once then succeed
	})

	w := newTestWorker(prod, store, repo, reg)
	err := w.processMessage(context.Background(), taskMsg(t, "task-5", "email"))
	require.NoError(t, err)

	assert.Equal(t, domain.StatusDone, store.states["task-5"])
	assert.Empty(t, prod.topics, "no DLQ when task eventually succeeds")
}
