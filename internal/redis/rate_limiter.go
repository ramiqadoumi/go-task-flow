package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimiter allows or denies requests using a sliding-window count in Redis.
type RateLimiter interface {
	Allow(ctx context.Context, key string) (bool, error)
	Limit() int
}

type slidingWindowLimiter struct {
	client *redis.Client
	limit  int
	window time.Duration
}

// NewRateLimiter returns a Redis-backed sliding-window rate limiter.
// limit is the maximum number of events allowed per window for a given key.
func NewRateLimiter(client *redis.Client, limit int, window time.Duration) RateLimiter {
	return &slidingWindowLimiter{client: client, limit: limit, window: window}
}

func (r *slidingWindowLimiter) Limit() int { return r.limit }

// Allow returns true when the request is within the allowed rate, false when
// it should be rejected.  It uses a Redis sorted set as a timestamp ring buffer.
func (r *slidingWindowLimiter) Allow(ctx context.Context, key string) (bool, error) {
	now := time.Now().UnixNano()
	windowStart := now - r.window.Nanoseconds()
	rkey := "ratelimit:" + key

	pipe := r.client.TxPipeline()
	// Evict timestamps that fell outside the window.
	pipe.ZRemRangeByScore(ctx, rkey, "0", strconv.FormatInt(windowStart, 10))
	// Record this event with the current nanosecond timestamp as both score and member.
	pipe.ZAdd(ctx, rkey, redis.Z{Score: float64(now), Member: strconv.FormatInt(now, 10)})
	// Count events still in the window.
	countCmd := pipe.ZCard(ctx, rkey)
	// Keep the key alive for at least one more window.
	pipe.Expire(ctx, rkey, r.window*2)

	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("rate limiter pipeline for %q: %w", key, err)
	}

	return countCmd.Val() <= int64(r.limit), nil
}
