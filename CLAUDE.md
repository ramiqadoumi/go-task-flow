# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build all four service binaries into bin/
make build

# Build a single service
go build -o bin/api-gateway ./cmd/api-gateway/

# Run all tests
make test                        # go test -race ./...

# Run a single test
go test -run TestName ./internal/redis/

# Lint (requires golangci-lint)
make lint

# Run DB migrations (via Makefile or directly)
POSTGRES_DSN="postgres://taskflow:taskflow@localhost:5432/taskflow?sslmode=disable" make migrate
# or via binary:
./bin/api-gateway migrate --postgres-dsn "postgres://taskflow:taskflow@localhost:5432/taskflow?sslmode=disable"

# Regenerate protobuf (requires protoc + protoc-gen-go + protoc-gen-go-grpc)
make proto

# Start infrastructure (Kafka, ZooKeeper, MailHog — NOT Redis/Postgres, those run on the host)
make docker-up
```

## Architecture

Four independent services communicate through Kafka:

```
Client → API Gateway (:8080/:9090) → tasks.pending → Dispatcher → tasks.worker.<type> → Worker
                                                                 ↘ tasks.dlq (on error/rate-limit)
```

- **`services/api-gateway`** — REST (chi) + gRPC server. Writes task metadata to Redis and Postgres, then publishes to `tasks.pending`. gRPC reflection is enabled.
- **`services/dispatcher`** — Consumes `tasks.pending`, applies a Redis sliding-window rate limiter (per task type), routes to `tasks.worker.<type>`, updates Redis state to QUEUED.
- **`services/worker`** — Consumes its own `tasks.worker.<type>` topic. Checks Redis for idempotency (skips terminal tasks), runs the handler with exponential-backoff retry, writes final state back to Redis + Postgres. Failed tasks go to `tasks.dlq`.
- **`services/scheduler`** — Redis SETNX leader election every 15s. Queries `scheduled_jobs` for due entries, publishes tasks to `tasks.pending`, updates `next_run_at` using `robfig/cron` to parse expressions.

## Key Packages

- **`internal/domain`** — `Task`, `TaskExecution`, `Status` type with `IsTerminal()`. These types flow through every layer.
- **`internal/kafka`** — Thin wrappers: `Producer` (publish) and `Consumer` (subscribe with manual offset commit). Offset is committed **only after** the handler returns `nil` — do not change this.
- **`internal/redis`** — `StateStore` interface (hot state: `task:state:<id>`, `task:meta:<id>`). `RateLimiter` interface (sorted-set sliding window).
- **`internal/postgres`** — `TaskRepository` interface backed by `pgxpool`. Source of truth.
- **`internal/handlers`** — `Handler` interface (`Handle(ctx, *Task) error` + `TaskType() string`) and `Registry`. Add new task types by implementing the interface and registering in `services/worker/cli/serve.go`.
- **`pkg/retry`** — `retry.Do(ctx, config, fn)` with `BaseDelay × attempt²` backoff.
- **`pkg/telemetry`** — All Prometheus metric variables (package-level `promauto` vars) + `StartMetricsServer`.

## Patterns to Follow

**Worker functional options** — Worker is configured with `WithRetries`, `WithTimeout`, `WithLogger`, `WithWorkerType`. New config fields go through this pattern.

**CLI config priority** — All services use cobra + viper: `CLI flag > config file > default`. No `.env` file support — use a config file (`./bin/{svc} init` to generate). New flags must call `bindFlag(viperKey, flagSet, flagName)` after `serveCmd.Flags().String(...)` in `services/{svc}/cli/serve.go`.

**Interface-driven dependencies** — `kafka.Producer`, `kafka.Consumer`, `redisstore.StateStore`, `postgres.TaskRepository`, `handlers.Handler` are all interfaces. Pass them as interfaces, not concrete types.

**Error handling in workers** — `processMessage` always returns `nil` (offset is committed). Unrecoverable errors go to DLQ. Only return an error when you want the offset NOT committed (e.g., transient Redis failure before processing starts).

## Infrastructure

- **Kafka/ZooKeeper/MailHog** run in Docker (`docker-compose.yml`).
- **Redis (`:6379`) and PostgreSQL (`:5432`)** run on the host — the Docker services for them are commented out.
- Kafka topic message key = `task_id` (ensures per-task partition ordering).
- Pre-create topics before first run to avoid `leader not available` errors.

## Metrics Ports (default)

| Service | Port |
|---------|------|
| Dispatcher | `:9094` |
| Worker (email) | `:9091` |
| Scheduler | `:9093` |
| API Gateway | `:9095` |



