package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
)

const (
	leaderKey     = "scheduler:leader"
	leaderTTL     = 30 * time.Second
	checkInterval = 15 * time.Second
	pendingTopic  = "tasks.pending"
)

// ScheduledJob mirrors the scheduled_jobs DB table.
type ScheduledJob struct {
	ID        string
	Name      string
	CronExpr  string
	TaskType  string
	Payload   json.RawMessage
	Enabled   bool
	LastRunAt *time.Time
	NextRunAt *time.Time
}

// Scheduler fires cron jobs with Redis leader election.
type Scheduler struct {
	pool       *pgxpool.Pool
	producer   kafka.Producer
	redis      *redis.Client
	instanceID string
	logger     *slog.Logger
}

func NewScheduler(
	pool *pgxpool.Pool,
	producer kafka.Producer,
	redisClient *redis.Client,
	instanceID string,
	logger *slog.Logger,
) *Scheduler {
	return &Scheduler{
		pool:       pool,
		producer:   producer,
		redis:      redisClient,
		instanceID: instanceID,
		logger:     logger,
	}
}

// Run is the main polling loop: tries to become leader, then processes due jobs.
// Blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	// Run once immediately before waiting for the first tick.
	s.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	if !s.acquireOrRenewLeadership(ctx) {
		return
	}
	if err := s.processDueJobs(ctx); err != nil {
		s.logger.Error("processDueJobs", slog.String("error", err.Error()))
	}
}

// acquireOrRenewLeadership attempts SETNX; returns true if this instance is the leader.
func (s *Scheduler) acquireOrRenewLeadership(ctx context.Context) bool {
	// Attempt to become leader.
	ok, err := s.redis.SetNX(ctx, leaderKey, s.instanceID, leaderTTL).Result()
	if err != nil {
		s.logger.Error("leader election SetNX", slog.String("error", err.Error()))
		return false
	}
	if ok {
		s.logger.Info("acquired scheduler leadership", slog.String("instance_id", s.instanceID))
		return true
	}

	// Already set — renew only if we own it (atomic Lua script avoids races).
	renewScript := redis.NewScript(`
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("pexpire", KEYS[1], ARGV[2])
		end
		return 0
	`)
	result, err := renewScript.Run(
		ctx, s.redis,
		[]string{leaderKey},
		s.instanceID,
		leaderTTL.Milliseconds(),
	).Int()
	if err != nil && !errors.Is(err, redis.Nil) {
		s.logger.Error("leader renewal", slog.String("error", err.Error()))
		return false
	}
	return result == 1
}

func (s *Scheduler) processDueJobs(ctx context.Context) error {
	jobs, err := s.loadDueJobs(ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := s.runJob(ctx, job); err != nil {
			s.logger.Error("runJob failed",
				slog.String("job", job.Name),
				slog.String("error", err.Error()),
			)
		}
	}
	return nil
}

func (s *Scheduler) loadDueJobs(ctx context.Context) ([]ScheduledJob, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, cron_expr, task_type, payload, enabled, last_run_at, next_run_at
		FROM scheduled_jobs
		WHERE enabled = TRUE AND (next_run_at IS NULL OR next_run_at <= NOW())
		ORDER BY next_run_at ASC NULLS FIRST
	`)
	if err != nil {
		return nil, fmt.Errorf("query scheduled_jobs: %w", err)
	}
	defer rows.Close()

	var jobs []ScheduledJob
	for rows.Next() {
		var j ScheduledJob
		if err := rows.Scan(
			&j.ID, &j.Name, &j.CronExpr, &j.TaskType,
			&j.Payload, &j.Enabled, &j.LastRunAt, &j.NextRunAt,
		); err != nil {
			return nil, fmt.Errorf("scan scheduled_job: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *Scheduler) runJob(ctx context.Context, job ScheduledJob) error {
	now := time.Now().UTC()
	taskID := uuid.New().String()

	task := &domain.Task{
		ID:        taskID,
		Type:      job.TaskType,
		Payload:   job.Payload,
		Status:    domain.StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}

	payload, _ := json.Marshal(task)
	if err := s.producer.Publish(ctx, pendingTopic, taskID, payload); err != nil {
		return fmt.Errorf("publish task for job %q: %w", job.Name, err)
	}

	// Parse cron expression to compute next_run_at.
	schedule, err := cron.ParseStandard(job.CronExpr)
	if err != nil {
		return fmt.Errorf("parse cron %q for job %q: %w", job.CronExpr, job.Name, err)
	}
	nextRun := schedule.Next(now)

	if _, err := s.pool.Exec(ctx, `
		UPDATE scheduled_jobs
		SET last_run_at = $1, next_run_at = $2
		WHERE id = $3
	`, now, nextRun, job.ID); err != nil {
		return fmt.Errorf("update scheduled_job %q: %w", job.Name, err)
	}

	s.logger.Info("scheduled job fired",
		slog.String("job", job.Name),
		slog.String("task_id", taskID),
		slog.String("task_type", job.TaskType),
		slog.Time("next_run", nextRun),
	)
	return nil
}
