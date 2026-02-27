package dispatcher

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
	redisstore "github.com/ramiqadoumi/go-task-flow/internal/redis"
)

// ── mocks ────────────────────────────────────────────────────────────────────

type publishedMsg struct {
	topic string
	key   string
	value []byte
}

type fakeProducer struct {
	msgs []publishedMsg
	err  error
}

func (p *fakeProducer) Publish(_ context.Context, topic, key string, value []byte) error {
	if p.err != nil {
		return p.err
	}
	p.msgs = append(p.msgs, publishedMsg{topic, key, value})
	return nil
}
func (p *fakeProducer) Close() error { return nil }

type fakeStore struct {
	states map[string]domain.Status
	setErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{states: make(map[string]domain.Status)}
}

func (s *fakeStore) SetStatus(_ context.Context, id string, st domain.Status) error {
	if s.setErr != nil {
		return s.setErr
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

type fakeRateLimiter struct {
	allow bool
	limit int
}

func (r *fakeRateLimiter) Allow(_ context.Context, _ string) (bool, error) {
	return r.allow, nil
}
func (r *fakeRateLimiter) Limit() int { return r.limit }

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestDispatcher(producer *fakeProducer, store redisstore.StateStore, limiter redisstore.RateLimiter) *Dispatcher {
	return NewDispatcher(nil, producer, store, limiter, slog.Default())
}

func taskMessage(t *testing.T, taskType string) kafka.Message {
	t.Helper()
	task := domain.Task{ID: "test-id", Type: taskType}
	raw, err := json.Marshal(task)
	require.NoError(t, err)
	return kafka.Message{Value: raw}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestDispatcher_Route_Email(t *testing.T) {
	prod := &fakeProducer{}
	store := newFakeStore()
	d := newTestDispatcher(prod, store, nil)

	err := d.route(context.Background(), taskMessage(t, "email"))
	require.NoError(t, err)

	require.Len(t, prod.msgs, 1)
	assert.Equal(t, "tasks.worker.email", prod.msgs[0].topic)
	assert.Equal(t, "test-id", prod.msgs[0].key)
}

func TestDispatcher_Route_Webhook(t *testing.T) {
	prod := &fakeProducer{}
	d := newTestDispatcher(prod, newFakeStore(), nil)

	err := d.route(context.Background(), taskMessage(t, "webhook"))
	require.NoError(t, err)

	require.Len(t, prod.msgs, 1)
	assert.Equal(t, "tasks.worker.webhook", prod.msgs[0].topic)
}

func TestDispatcher_Route_UnknownType_GoesToDLQ(t *testing.T) {
	prod := &fakeProducer{}
	d := newTestDispatcher(prod, newFakeStore(), nil)

	// "sms" has no registered worker, but dispatcher routes it anyway to tasks.worker.sms
	// since routing is generic. Let's test with an empty type → DLQ.
	raw, _ := json.Marshal(domain.Task{ID: "x", Type: ""})
	err := d.route(context.Background(), kafka.Message{Value: raw})
	require.NoError(t, err) // DLQ publish succeeded

	require.Len(t, prod.msgs, 1)
	assert.Equal(t, topicDLQ, prod.msgs[0].topic)
}

func TestDispatcher_Route_MalformedJSON_GoesToDLQ(t *testing.T) {
	prod := &fakeProducer{}
	d := newTestDispatcher(prod, newFakeStore(), nil)

	err := d.route(context.Background(), kafka.Message{Value: []byte("not-json")})
	require.NoError(t, err)

	require.Len(t, prod.msgs, 1)
	assert.Equal(t, topicDLQ, prod.msgs[0].topic)
}

func TestDispatcher_Route_SetsStatusQueued(t *testing.T) {
	prod := &fakeProducer{}
	store := newFakeStore()
	d := newTestDispatcher(prod, store, nil)

	err := d.route(context.Background(), taskMessage(t, "email"))
	require.NoError(t, err)

	assert.Equal(t, domain.StatusQueued, store.states["test-id"])
}

func TestDispatcher_RateLimited_GoesToDLQ(t *testing.T) {
	prod := &fakeProducer{}
	limiter := &fakeRateLimiter{allow: false}
	d := newTestDispatcher(prod, newFakeStore(), limiter)

	err := d.route(context.Background(), taskMessage(t, "email"))
	require.NoError(t, err)

	require.Len(t, prod.msgs, 1)
	assert.Equal(t, topicDLQ, prod.msgs[0].topic)
}

func TestDispatcher_RateLimiter_Allowed_Routes(t *testing.T) {
	prod := &fakeProducer{}
	limiter := &fakeRateLimiter{allow: true}
	d := newTestDispatcher(prod, newFakeStore(), limiter)

	err := d.route(context.Background(), taskMessage(t, "email"))
	require.NoError(t, err)

	require.Len(t, prod.msgs, 1)
	assert.Equal(t, "tasks.worker.email", prod.msgs[0].topic)
}

func TestDispatcher_TransientKafkaError_ReturnsError(t *testing.T) {
	prod := &fakeProducer{err: assert.AnError}
	d := newTestDispatcher(prod, newFakeStore(), nil)

	err := d.route(context.Background(), taskMessage(t, "email"))
	require.Error(t, err, "transient Kafka error should not commit offset")
}
