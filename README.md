# GoTaskFlow

A production-grade **distributed task queue** in Go. Tasks are submitted via REST or gRPC, routed through Apache Kafka, executed by typed workers with automatic retries, and tracked in PostgreSQL with Redis as a hot-state cache.

## Features

- REST and gRPC APIs with server-streaming and bulk-submit support
- At-least-once Kafka delivery (offset committed only on handler success)
- Idempotency guard via Redis state check before each execution
- Exponential-backoff retries; exhausted tasks forwarded to a dead-letter queue
- Sliding-window rate limiter in the Dispatcher (per task type)
- Cron-based Scheduler with Redis leader election
- Prometheus metrics on every service

## Tech Stack

Go 1.22 · Kafka (`segmentio/kafka-go`) · Redis (`go-redis/v9`) · PostgreSQL (`pgx/v5`) · chi · gRPC · cobra + viper · Prometheus

## Quick Start

```bash
# 1. Start Kafka + MailHog (Redis and Postgres run on the host)
make docker-up

# 2. Set up Postgres and run migrations
POSTGRES_DSN="postgres://taskflow:taskflow@localhost:5432/taskflow?sslmode=disable" make migrate

# 3. Build all services
make build

# 4. Run (four terminals)
./bin/api-gateway serve
./bin/dispatcher serve
./bin/worker serve --worker-type email
./bin/worker serve --worker-type webhook --metrics-addr :9092

# 5. Submit a task
curl -s -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"type":"email","payload":{"to":"test@example.com","subject":"hi","body":"world"}}' | jq .
```

For the full setup walkthrough, see **[docs/getting-started.md](docs/getting-started.md)**.

## Documentation

| Doc | Description |
|-----|-------------|
| [docs/getting-started.md](docs/getting-started.md) | Step-by-step setup and first task walkthrough |
| [docs/architecture.md](docs/architecture.md) | Service design, data flow, Kafka topics, design decisions |
| [docs/api-reference.md](docs/api-reference.md) | REST and gRPC API reference with examples |
| [docs/configuration.md](docs/configuration.md) | All CLI flags and config file options per service |
| [docs/adding-a-handler.md](docs/adding-a-handler.md) | How to add a new task type end-to-end |
| [docs/testing.md](docs/testing.md) | Running tests, writing mocks, integration testing |
| [docs/observability.md](docs/observability.md) | Prometheus metrics, Grafana, PromQL examples, structured logs |

## Development

```bash
make build    # build all services into bin/
make test     # go test -race ./...
make lint     # golangci-lint run ./...
make proto    # regenerate protobuf (requires protoc)
make clean    # remove bin/ and coverage.out
```