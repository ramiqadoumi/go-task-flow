package retry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramiqadoumi/go-task-flow/pkg/retry"
)

func TestDo_SucceedsOnFirstAttempt(t *testing.T) {
	calls := 0
	err := retry.Do(context.Background(), retry.Config{MaxAttempts: 3, BaseDelay: time.Millisecond}, func() error {
		calls++
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "fn should be called exactly once on immediate success")
}

func TestDo_RetriesOnTransientError(t *testing.T) {
	calls := 0
	err := retry.Do(context.Background(), retry.Config{MaxAttempts: 3, BaseDelay: time.Millisecond}, func() error {
		calls++
		if calls < 2 {
			return errors.New("transient error")
		}
		return nil // succeeds on 2nd attempt
	})
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "fn should be called twice: fail then succeed")
}

func TestDo_ReturnsErrorAfterMaxAttempts(t *testing.T) {
	calls := 0
	sentinel := errors.New("permanent error")
	err := retry.Do(context.Background(), retry.Config{MaxAttempts: 3, BaseDelay: time.Millisecond}, func() error {
		calls++
		return sentinel
	})
	require.Error(t, err)
	assert.Equal(t, sentinel, err)
	assert.Equal(t, 3, calls, "fn should be called exactly MaxAttempts times")
}

func TestDo_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	err := retry.Do(ctx, retry.Config{MaxAttempts: 10, BaseDelay: 50 * time.Millisecond}, func() error {
		return errors.New("always fails")
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded),
		"expected DeadlineExceeded, got: %v", err)
}

func TestDo_OnRetry_CalledWithCorrectAttempt(t *testing.T) {
	var retryAttempts []int
	_ = retry.Do(context.Background(), retry.Config{
		MaxAttempts: 4,
		BaseDelay:   time.Millisecond,
		OnRetry: func(attempt int, _ error) {
			retryAttempts = append(retryAttempts, attempt)
		},
	}, func() error {
		return errors.New("fail")
	})

	// OnRetry is called after attempts 1, 2, 3 (not after the last attempt).
	assert.Equal(t, []int{1, 2, 3}, retryAttempts)
}

func TestDo_ZeroMaxAttempts_DefaultsToOne(t *testing.T) {
	calls := 0
	err := retry.Do(context.Background(), retry.Config{MaxAttempts: 0, BaseDelay: time.Millisecond}, func() error {
		calls++
		return errors.New("fail")
	})
	require.Error(t, err)
	assert.Equal(t, 1, calls, "MaxAttempts=0 should default to 1 attempt")
}
