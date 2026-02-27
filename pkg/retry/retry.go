package retry

import (
	"context"
	"fmt"
	"time"
)

// Config controls retry behaviour.
type Config struct {
	// MaxAttempts is the total number of calls including the first attempt.
	MaxAttempts int
	// BaseDelay is the base for exponential backoff. Wait = BaseDelay * attempt².
	BaseDelay time.Duration
	// OnRetry is called after a failed attempt and before the next delay.
	// attempt is 1-indexed (1 = first attempt just failed).
	OnRetry func(attempt int, err error)
}

// Do calls fn up to cfg.MaxAttempts times.
//
// Wait schedule with BaseDelay=1s:
//   attempt 1 fails → wait 1s  (1² × 1s)
//   attempt 2 fails → wait 4s  (2² × 1s)
//   attempt 3 fails → wait 9s  (3² × 1s)
//
// Returns nil on first success, or the last error after all attempts.
func Do(ctx context.Context, cfg Config, fn func() error) error {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// Last attempt — no delay, just return the error.
		if attempt == cfg.MaxAttempts {
			break
		}

		if cfg.OnRetry != nil {
			cfg.OnRetry(attempt, lastErr)
		}

		delay := cfg.BaseDelay * time.Duration(attempt*attempt)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled after attempt %d: %w", attempt, ctx.Err())
		}
	}
	return lastErr
}
