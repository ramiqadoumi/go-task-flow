# Architecture

## Overview

GoTaskFlow is a distributed task queue built around four independent services that communicate through Apache Kafka. Each service has a single responsibility and can be scaled independently.

```
  Client (HTTP / gRPC)
          │
          ▼
  ┌───────────────┐   Redis (meta/state write)
  │  API Gateway  │   Postgres (audit write)
  │ :8080 / :9090 │
  └───────┬───────┘
          │ publishes
          ▼
  ┌──────────────────┐
  │  tasks.pending   │  Kafka topic
  └────────┬─────────┘
           │ consumes
           ▼
  ┌──────────────┐   Redis (state: PENDING → QUEUED)
  │  Dispatcher  │   Rate limiter (sliding window)
  └──────┬───────┘
         │ routes by task.type
         ├──────────────────────┬─────────────────────┐
         ▼                      ▼                      ▼
  tasks.worker.email   tasks.worker.webhook      tasks.dlq
         │                      │
         ▼                      ▼
  ┌────────────┐       ┌────────────────┐
  │   Worker   │       │     Worker     │
  │  (email)   │       │   (webhook)    │
  └─────┬──────┘       └──────┬─────────┘
        │                     │
        └──────────┬──────────┘
                   │ Redis (state update) + Postgres (status + execution log)
```

## Services

### API Gateway (`services/api-gateway`)

The single entry point for clients. It:
1. Validates the incoming request (type + payload required)
2. Generates a UUID task ID
3. Writes task metadata to **Redis** (`task:meta:<id>`) and status to **Redis** (`task:state:<id>`)
4. Persists the task row to **PostgreSQL** (non-fatal if this fails)
5. Publishes the serialised task to Kafka topic `tasks.pending` (task_id as message key)
6. Returns `202 Accepted` with the task ID

Status reads go Redis-first (hot path), fall back to Postgres if the key has expired.

Exposes both REST (chi router, port 8080) and gRPC (port 9090) on the same dependencies. gRPC reflection is enabled for `grpcurl` introspection.

### Dispatcher (`services/dispatcher`)

Consumes `tasks.pending` in a dedicated consumer group. For each message it:
1. Deserialises the task; sends malformed messages straight to `tasks.dlq`
2. Checks the rate limiter (Redis sorted-set sliding window, per task type); rejects over-limit tasks to DLQ
3. Publishes to `tasks.worker.<type>` (the target topic)
4. Best-effort: updates Redis state to `QUEUED`

A non-nil return from the route function means the Kafka offset is **not** committed — the message will be redelivered. This is used only for transient Kafka publish failures.

### Worker (`services/worker`)

Consumes a single `tasks.worker.<type>` topic. For each message it:
1. Deserialises the task
2. Checks Redis for idempotency — if the task is already in a terminal state, ACKs and skips
3. Sets status to `RUNNING` in Redis (if this fails, returns an error to hold the offset)
4. Executes the handler via `pkg/retry` with exponential backoff (`BaseDelay × attempt²`)
5. On success: writes `DONE` to Redis + Postgres, records a `task_executions` row
6. On exhausted retries: writes `DEAD` to Redis + Postgres, publishes raw bytes to `tasks.dlq`

Worker is configured with functional options: `WithRetries`, `WithTimeout`, `WithLogger`, `WithWorkerType`.

Each task runs with `context.WithTimeout(context.Background(), timeout)` — deliberately **not** the consumer context — so a SIGTERM drains in-flight tasks gracefully rather than cancelling them mid-execution.

### Scheduler (`services/scheduler`)

Fires cron-based tasks. Only one instance runs at a time via **Redis leader election** (`SET NX` with a 30s TTL, renewed every 15s with an atomic Lua script).

Every 15 seconds, the leader:
1. Queries `scheduled_jobs WHERE enabled AND next_run_at <= NOW()`
2. For each due job, publishes a new task to `tasks.pending`
3. Parses the cron expression with `robfig/cron` to compute `next_run_at`
4. Updates `last_run_at` and `next_run_at` in Postgres

## Data Stores

### Redis — hot state

| Key pattern | Content | TTL |
|-------------|---------|-----|
| `task:state:<id>` | Status string (e.g. `RUNNING`) | 24 h |
| `task:meta:<id>` | Full task JSON | 24 h |
| `task:result:<id>` | Handler result bytes | 1 h |
| `ratelimit:<type>` | Sorted set of nanosecond timestamps | `window × 2` |
| `scheduler:leader` | Instance ID of the current leader | 30 s |

### PostgreSQL — source of truth

| Table | Purpose |
|-------|---------|
| `tasks` | One row per task (UUID PK, status, payload, audit timestamps) |
| `task_executions` | One row per attempt (worker_id, duration_ms, error) |
| `scheduled_jobs` | Cron job registry (expr, task_type, payload, next_run_at) |

## Task Lifecycle

```
PENDING → QUEUED → RUNNING → DONE
                          ↘
                        RETRYING → … → DEAD → tasks.dlq
```

| Status | Set by | Trigger |
|--------|--------|---------|
| `PENDING` | API Gateway | Task accepted |
| `QUEUED` | Dispatcher | Routed to worker topic |
| `RUNNING` | Worker | Handler started |
| `RETRYING` | Worker | Attempt failed, retry scheduled |
| `DONE` | Worker | Handler returned nil |
| `DEAD` | Worker | All retries exhausted |

`IsTerminal()` returns true for `DONE`, `DEAD`, and `FAILED`.

## Kafka Topic Design

| Topic | Key | Partitions | Purpose |
|-------|-----|-----------|---------|
| `tasks.pending` | `task_id` | 6 | All newly submitted tasks |
| `tasks.worker.<type>` | `task_id` | 6 | Per-type worker queue |
| `tasks.dlq` | — | 1 | Dead-letter queue |

Message key = `task_id` ensures all messages for the same task land in the same partition, providing per-task ordering guarantees.

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| Offset committed only after handler returns `nil` | At-least-once delivery — failures are retried, not lost |
| Redis for hot state reads | Sub-millisecond reads; avoids Postgres load on every status poll |
| Rate limiter in Dispatcher | Single enforcement point; workers stay stateless |
| `context.Background()` for task execution | SIGTERM drains gracefully; doesn't cancel in-flight handlers |
| Task key = `task_id` | Per-task partition ordering |
| Redis SETNX for scheduler | Lightweight leader election with automatic TTL-based failover |
