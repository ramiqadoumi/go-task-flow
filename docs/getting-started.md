# Getting Started

## Prerequisites

| Tool | Version | Notes |
|------|---------|-------|
| Go | 1.22+ | |
| Docker & Docker Compose | any recent | for Kafka / MailHog |
| PostgreSQL | 14+ | runs on host (`localhost:5432`) |
| Redis | 6+ | runs on host (`localhost:6379`) |

## Step 1 — Clone and download modules

```bash
git clone https://github.com/ramiqadoumi/go-task-flow.git
cd go-task-flow
go mod download
```

## Step 2 — Start infrastructure (Kafka + MailHog)

Redis and PostgreSQL are expected to run on the host. The Docker Compose file starts only Kafka, ZooKeeper, and MailHog:

```bash
make docker-up
```

Wait until Kafka is healthy (usually 20–30 s):

```bash
docker compose ps
```

**MailHog** (catches outgoing emails locally): http://localhost:8025

## Step 3 — Set up PostgreSQL

Run once as a superuser:

```sql
CREATE ROLE taskflow WITH LOGIN PASSWORD 'taskflow';
CREATE DATABASE taskflow OWNER taskflow;
GRANT ALL ON SCHEMA public TO taskflow;
```

## Step 4 — Run database migrations

```bash
POSTGRES_DSN="postgres://taskflow:taskflow@localhost:5432/taskflow?sslmode=disable" \
  make migrate
```

This creates the `tasks`, `task_executions`, and `scheduled_jobs` tables.

## Step 5 — Pre-create Kafka topics

Kafka auto-creates topics on first publish, but this causes a brief `leader not available` error. Pre-create them to avoid it:

```bash
KAFKA=$(docker ps --filter name=kafka --format '{{.Names}}' | head -1)

for topic in tasks.pending tasks.worker.email tasks.worker.webhook tasks.dlq; do
  docker exec $KAFKA kafka-topics \
    --bootstrap-server localhost:9092 --create \
    --topic $topic --partitions 6 --replication-factor 1 \
    --if-not-exists
done
```

## Step 6 — Build all services

```bash
make build
# Produces: bin/api-gateway  bin/dispatcher  bin/worker  bin/scheduler
```

## Step 7 — Run the services

Open four terminals (or run them in the background):

```bash
# Terminal 1 — API Gateway
./bin/api-gateway serve

# Terminal 2 — Dispatcher
./bin/dispatcher serve

# Terminal 3 — Email worker
./bin/worker serve --worker-type email

# Terminal 4 — Webhook worker
./bin/worker serve --worker-type webhook --metrics-addr :9092
```

## Step 8 — Submit your first task

```bash
curl -s -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "type": "email",
    "payload": {
      "to": "test@example.com",
      "subject": "Hello from GoTaskFlow",
      "body": "It works!"
    }
  }' | jq .
```

Expected response:
```json
{
  "task_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "PENDING",
  "created_at": "2025-01-01T12:00:00Z"
}
```

## Step 9 — Check the task status

```bash
curl -s http://localhost:8080/api/v1/tasks/<task_id> | jq .
```

The status should progress: `PENDING → QUEUED → RUNNING → DONE`

Check MailHog at http://localhost:8025 to see the delivered email.

## Using Config Files (Optional)

Instead of flags, generate a default config and edit it:

```bash
# Write defaults to ~/.go-task-flow/api-gateway.yaml
./bin/api-gateway init

# Edit as needed, then serve (config is auto-loaded from that path)
./bin/api-gateway serve

# Or point to an explicit path
./bin/api-gateway serve --config /path/to/api-gateway.yaml
```

See [configuration.md](configuration.md) for all available options.

## Stopping Everything

```bash
# Stop Docker services
make docker-down

# Kill local service processes with Ctrl+C in each terminal
# They will drain in-flight tasks before exiting
```
