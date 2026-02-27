package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/ramiqadoumi/go-task-flow/internal/domain"
)

const (
	stateTTL  = 24 * time.Hour
	resultTTL = time.Hour
)

func stateKey(taskID string) string  { return "task:state:" + taskID }
func metaKey(taskID string) string   { return "task:meta:" + taskID }
func resultKey(taskID string) string { return "task:result:" + taskID }

// StateStore manages real-time task state in Redis.
type StateStore interface {
	SetStatus(ctx context.Context, taskID string, status domain.Status) error
	GetStatus(ctx context.Context, taskID string) (domain.Status, error)
	SetTaskMeta(ctx context.Context, task *domain.Task) error
	GetTaskMeta(ctx context.Context, taskID string) (*domain.Task, error)
	SetResult(ctx context.Context, taskID string, result []byte, ttl time.Duration) error
	GetResult(ctx context.Context, taskID string) ([]byte, error)
}

type stateStore struct {
	client *redis.Client
}

// NewStateStore creates a Redis-backed StateStore.
func NewStateStore(client *redis.Client) StateStore {
	return &stateStore{client: client}
}

// NewClient creates and returns a new Redis client.
func NewClient(addr string) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  1 * time.Second,
		WriteTimeout: 1 * time.Second,
		PoolSize:     10,
	})
}

func (s *stateStore) SetStatus(ctx context.Context, taskID string, status domain.Status) error {
	err := s.client.Set(ctx, stateKey(taskID), string(status), stateTTL).Err()
	if err != nil {
		return fmt.Errorf("redis set status for %s: %w", taskID, err)
	}
	return nil
}

func (s *stateStore) GetStatus(ctx context.Context, taskID string) (domain.Status, error) {
	val, err := s.client.Get(ctx, stateKey(taskID)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", &domain.TaskNotFoundError{TaskID: taskID}
		}
		return "", fmt.Errorf("redis get status for %s: %w", taskID, err)
	}
	return domain.Status(val), nil
}

func (s *stateStore) SetTaskMeta(ctx context.Context, task *domain.Task) error {
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal task meta: %w", err)
	}
	err = s.client.Set(ctx, metaKey(task.ID), data, stateTTL).Err()
	if err != nil {
		return fmt.Errorf("redis set meta for %s: %w", task.ID, err)
	}
	return nil
}

func (s *stateStore) GetTaskMeta(ctx context.Context, taskID string) (*domain.Task, error) {
	data, err := s.client.Get(ctx, metaKey(taskID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, &domain.TaskNotFoundError{TaskID: taskID}
		}
		return nil, fmt.Errorf("redis get meta for %s: %w", taskID, err)
	}
	var task domain.Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("unmarshal task meta: %w", err)
	}
	return &task, nil
}

func (s *stateStore) SetResult(ctx context.Context, taskID string, result []byte, ttl time.Duration) error {
	if ttl == 0 {
		ttl = resultTTL
	}
	err := s.client.Set(ctx, resultKey(taskID), result, ttl).Err()
	if err != nil {
		return fmt.Errorf("redis set result for %s: %w", taskID, err)
	}
	return nil
}

func (s *stateStore) GetResult(ctx context.Context, taskID string) ([]byte, error) {
	data, err := s.client.Get(ctx, resultKey(taskID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, &domain.TaskNotFoundError{TaskID: taskID}
		}
		return nil, fmt.Errorf("redis get result for %s: %w", taskID, err)
	}
	return data, nil
}
