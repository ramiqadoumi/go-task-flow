# GoTaskFlow — Features & Verification Guide

All commands assume services are running locally (see `docs/getting-started.md`).

---

## 1. REST API

Submit tasks and query status over HTTP.

**Submit a task**
```bash
curl -s -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"type":"email","payload":{"to":"test@example.com","subject":"Hello","body":"World"}}' | jq .
# → 202 {"task_id":"<uuid>","status":"PENDING","created_at":"..."}
```

**Get task status**
```bash
curl -s http://localhost:8080/api/v1/tasks/<task_id> | jq .
# → {"task_id":"...","type":"email","status":"DONE","attempts":1,"duration_ms":142}
```

**Health probes**
```bash
curl -s http://localhost:8080/healthz   # → {"status":"ok"}
curl -s http://localhost:8080/readyz    # → {"status":"ready"}  (checks Redis)
```

---

## 2. gRPC API

Full gRPC service alongside REST, with reflection enabled.

**Prerequisites**
```bash
# Install grpcurl
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
```

**Introspect the service**
```bash
grpcurl -plaintext localhost:9090 list
# → task.v1.TaskService

grpcurl -plaintext localhost:9090 describe task.v1.TaskService
```

**SubmitTask RPC**
```bash
grpcurl -plaintext -d '{
  "type": "email",
  "payload": "eyJ0byI6InRlc3RAZXhhbXBsZS5jb20iLCJzdWJqZWN0IjoiZ1JQQyIsImJvZHkiOiJpdCB3b3JrcyJ9"
}' localhost:9090 task.v1.TaskService/SubmitTask
# payload is base64({"to":"test@example.com","subject":"gRPC","body":"it works"})
```

**GetTaskStatus RPC**
```bash
grpcurl -plaintext -d '{"task_id":"<uuid>"}' \
  localhost:9090 task.v1.TaskService/GetTaskStatus
```

**StreamTaskUpdates — server-streaming**
```bash
grpcurl -plaintext -d '{"task_id":"<uuid>"}' \
  localhost:9090 task.v1.TaskService/StreamTaskUpdates
# streams status updates every 500ms until DONE or DEAD
```

**BulkSubmitTasks — client-streaming**
```bash
grpcurl -plaintext -d @ localhost:9090 task.v1.TaskService/BulkSubmitTasks <<EOF
{"type":"email","payload":"eyJ0byI6ImExQGUuY29tIiwic3ViamVjdCI6IkExIiwiYm9keSI6IiJ9"}
{"type":"email","payload":"eyJ0byI6ImEyQGUuY29tIiwic3ViamVjdCI6IkEyIiwiYm9keSI6IiJ9"}
EOF
# → {"submitted":2,"failed":0,"task_ids":["<id1>","<id2>"]}
```

---

## 3. Task State Machine

Every task flows through a defined set of states.

```
PENDING → QUEUED → RUNNING → DONE
                           ↘ RETRYING → RUNNING → ...
                                               ↘ DEAD → DLQ
```

**Verify the full transition**
```bash
ID=$(curl -s -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"type":"email","payload":{"to":"t@e.com","subject":"s","body":"b"}}' \
  | jq -r '.task_id')

# Check Redis directly at each stage
redis-cli GET "task:state:$ID"

# Or poll the API
watch -n1 "curl -s http://localhost:8080/api/v1/tasks/$ID | jq '{status,attempts,duration_ms}'"
```

---

## 4. At-Least-Once Kafka Delivery

Kafka offsets are committed **only after** the handler returns success. A crash mid-processing re-delivers the message on restart.

**Verify**
```bash
# 1. Start a worker, submit a task
# 2. Kill the worker mid-processing (Ctrl+C during RUNNING)
# 3. Restart the worker — it will re-consume and process the same task
# 4. Check that the task reaches DONE (not lost)

# Confirm offset behaviour in Kafka
docker exec -it $(docker-compose ps -q kafka) \
  kafka-consumer-groups --bootstrap-server localhost:9092 \
  --describe --group worker-email-group
```

---

## 5. Idempotency Guard

If a task already reached a terminal state (`DONE` or `DEAD`), a re-delivered Kafka message is silently skipped — the handler is never called twice.

**Verify**
```bash
# 1. Submit a task and let it complete (status = DONE)
# 2. Manually re-publish the same task JSON to the worker topic
docker exec -it $(docker-compose ps -q kafka) \
  kafka-console-producer --bootstrap-server localhost:9092 \
  --topic tasks.worker.email --property "parse.key=true" --property "key.separator=:"

# Paste: <task_id>:<task_json>   (same task_id that is already DONE)
# 3. Worker logs will show "task already terminal, skipping" — no duplicate execution
```

---

## 6. Exponential-Backoff Retries

A failing handler is retried up to 3 times with delays of `1s → 4s → 9s`. After all retries the task is marked `DEAD` and forwarded to the DLQ.

**Verify with a webhook pointing to a bad URL**
```bash
curl -s -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"type":"webhook","payload":{"url":"http://localhost:19999/nope","method":"POST"}}' | jq .

# Watch worker logs — you will see:
# "attempt failed, retrying" attempt=1
# "attempt failed, retrying" attempt=2
# "attempt failed, retrying" attempt=3
# "task dead after all retries"
```

---

## 7. Dead-Letter Queue (DLQ)

Tasks that exhaust retries, have no registered handler, or are malformed land in `tasks.dlq`.

**Watch the DLQ in real time**
```bash
docker exec -it $(docker-compose ps -q kafka) \
  kafka-console-consumer --bootstrap-server localhost:9092 \
  --topic tasks.dlq --from-beginning
```

---

## 8. Sliding-Window Rate Limiter

The dispatcher allows a maximum of N tasks per second per task type. Over-limit tasks are forwarded to the DLQ instead of the worker topic.

**Verify**
```bash
# Default limit is 100/sec. Lower it to 2/sec for testing:
./bin/dispatcher serve --rate-limit 2 --metrics-addr :9094

# Flood with 10 tasks quickly
for i in $(seq 1 10); do
  curl -s -X POST http://localhost:8080/api/v1/tasks \
    -H "Content-Type: application/json" \
    -d '{"type":"email","payload":{"to":"t@e.com","subject":"flood","body":""}}' &
done
wait

# Dispatcher logs will show "rate limit exceeded, sending to DLQ"
# DLQ consumer will show the rejected tasks
```

---

## 9. Cron Scheduler with Leader Election

The scheduler polls `scheduled_jobs` and fires tasks on schedule. Multiple instances compete for a Redis lock — only one fires at a time.

**Insert a scheduled job**
```bash
psql "postgres://taskflow:taskflow@localhost:5432/taskflow?sslmode=disable" <<SQL
INSERT INTO scheduled_jobs (name, task_type, payload, cron_expr, next_run_at)
VALUES (
  'test-heartbeat',
  'webhook',
  '{"url":"http://localhost:8080/healthz","method":"GET"}',
  '* * * * *',
  NOW()
);
SQL
```

**Watch it fire**
```bash
# Scheduler logs will show "fired scheduled job" every minute
./bin/scheduler serve

# Verify the task appeared in the API
curl -s http://localhost:8080/api/v1/tasks/<returned_id> | jq .
```

**Test leader election**
```bash
# Run two scheduler instances simultaneously
./bin/scheduler serve &
./bin/scheduler serve --metrics-addr :9096 &

# Only one will log "acquired leader lock"
# Kill the leader — the standby takes over within 30s
```

---

## 10. Graceful Shutdown

Services finish in-flight work before exiting on `SIGTERM`/`SIGINT`.

**Verify**
```bash
# Submit a webhook task with a slow target (use a sleep endpoint)
# While it's RUNNING, send SIGTERM to the worker
kill -SIGTERM $(pgrep -f "worker --worker-type")

# Worker logs show:
# "shutting down, draining in-flight tasks..."
# task completes normally
# "stopped cleanly"
# Process exits with code 0
```

---

## 11. Email Handler

Sends email via SMTP. MailHog catches all outgoing mail in dev.

**Submit an email task**
```bash
curl -s -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "type": "email",
    "payload": {
      "to": "user@example.com",
      "subject": "Test from GoTaskFlow",
      "body": "It works!"
    }
  }' | jq .
```

**Check MailHog UI**

Open **http://localhost:8025** — the email appears within 1-2 seconds.

---

## 12. Webhook Handler

Makes an outbound HTTP call. Returns an error if the response status is >= 400.

**Submit a webhook task**
```bash
# Use a public echo service, or run nc locally
nc -lk 8099 &   # listens and echoes

curl -s -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "type": "webhook",
    "payload": {
      "url": "http://localhost:8099",
      "method": "POST",
      "headers": {"X-Source": "taskflow"},
      "body": "{\"event\":\"test\"}"
    }
  }' | jq .
```

---

## 13. Prometheus Metrics

Every service exposes `/metrics` in Prometheus text format.

**Verify scrape endpoints are alive**
```bash
curl -s http://localhost:9095/metrics | grep taskflow_api      # api-gateway
curl -s http://localhost:9094/metrics | grep taskflow_dispatcher  # dispatcher
curl -s http://localhost:9091/metrics | grep taskflow_worker   # worker-email
curl -s http://localhost:9092/metrics | grep taskflow_worker   # worker-webhook
curl -s http://localhost:9093/metrics | grep taskflow          # scheduler
```

**Key metrics to check after submitting tasks**
```bash
# Tasks submitted counter
curl -s http://localhost:9095/metrics | grep tasks_submitted_total

# Tasks processed
curl -s http://localhost:9091/metrics | grep tasks_processed_total

# In-flight gauge (should be 0 when idle)
curl -s http://localhost:9091/metrics | grep tasks_inflight

# DLQ counter (should be 0 on happy path)
curl -s http://localhost:9091/metrics | grep dlq_total
```

---

## 14. Prometheus + Grafana Stack

Full metrics scraping and dashboards.

**Start the observability stack**
```bash
docker-compose -f docker-compose.observability.yml up -d
```

**Prometheus UI** — http://localhost:9090

Verify all targets are `UP`:
- Navigate to **Status → Targets**
- All 5 jobs (`api-gateway`, `dispatcher`, `worker-email`, `worker-webhook`, `scheduler`) should show `UP`

Run a PromQL query:
```promql
rate(taskflow_api_tasks_submitted_total[5m])
```

**Grafana dashboard** — http://localhost:3000 (admin / admin)

- The **GoTaskFlow** dashboard loads automatically under the `GoTaskFlow` folder
- Submit a batch of tasks and watch panels update within 15 seconds:
```bash
for i in $(seq 1 20); do
  curl -s -X POST http://localhost:8080/api/v1/tasks \
    -H "Content-Type: application/json" \
    -d '{"type":"email","payload":{"to":"t@e.com","subject":"bench","body":""}}' > /dev/null
done
```

Panels to observe:
| Panel | What to see |
|-------|-------------|
| Tasks Submitted Rate | Spike to ~1 op/s |
| Tasks Processed Rate | Matching spike, status=done |
| Processing Duration p99 | Latency distribution |
| Tasks In-Flight | Brief non-zero value during processing |
| Retry Rate | 0 on happy path |
| DLQ Rate | 0 on happy path |
| Rate Limited Rate | 0 unless rate-limit is hit |

---

## 15. OpenTelemetry Traces (Jaeger)

Distributed traces span API Gateway → Dispatcher → Worker → Handler in a single trace.

**Start Jaeger**
```bash
docker-compose -f docker-compose.observability.yml up -d jaeger
```

**Configure services to export traces**
```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4318

./bin/api-gateway serve &
./bin/dispatcher serve --metrics-addr :9094 &
./bin/worker serve --worker-type email &
```

**Submit a task and find the trace**
```bash
curl -s -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"type":"email","payload":{"to":"t@e.com","subject":"trace","body":"test"}}' | jq .
```

**Jaeger UI** — http://localhost:16686

- Select service: `api-gateway`
- Click **Find Traces**
- Open the trace — it shows the full span chain:
  ```
  api-gateway: api_gateway.submit_task
    └─ dispatcher: dispatcher.route
         └─ worker: worker.process_task
              └─ worker: handler.email
  ```
- Each span shows duration, attributes (`task.id`, `task.type`, `email.to`), and any errors

---

## 16. Structured JSON Logging

All services emit JSON logs compatible with any log aggregator.

**Verify format**
```bash
./bin/api-gateway serve 2>&1 | head -5 | jq .
# → {"time":"...","level":"INFO","msg":"api-gateway HTTP starting","service":"api-gateway","addr":":8080"}
```

**Filter by level**
```bash
./bin/worker serve --worker-type email 2>&1 | jq 'select(.level == "ERROR")'
```

**Filter by task**
```bash
./bin/worker serve --worker-type email 2>&1 | jq 'select(.task_id == "<uuid>")'
```

---

## 17. PostgreSQL Audit Trail

Every task and every execution attempt is persisted.

```bash
PSQL="psql postgres://taskflow:taskflow@localhost:5432/taskflow?sslmode=disable"

# All tasks
$PSQL -c "SELECT id, type, status, attempts, created_at FROM tasks ORDER BY created_at DESC LIMIT 10;"

# All execution records (one row per attempt)
$PSQL -c "SELECT task_id, worker_id, attempt, status, duration_ms, error FROM task_executions ORDER BY executed_at DESC LIMIT 10;"

# Failed tasks with their errors
$PSQL -c "SELECT t.id, t.type, e.attempt, e.error FROM tasks t JOIN task_executions e ON t.id = e.task_id WHERE t.status = 'DEAD';"
```

---

## 18. Redis Hot-State Cache

Task state and metadata are served from Redis for low-latency reads. Falls back to PostgreSQL if the key has expired.

```bash
# Check task state key directly
redis-cli GET "task:state:<task_id>"         # → "DONE"

# Check task metadata
redis-cli GET "task:meta:<task_id>" | jq .   # → full task JSON

# Simulate cache expiry and verify PostgreSQL fallback
redis-cli DEL "task:state:<task_id>" "task:meta:<task_id>"
curl -s http://localhost:8080/api/v1/tasks/<task_id> | jq .
# → still returns correct data (served from Postgres)
```

---

## Quick Feature Checklist

| Feature | Verify command | Expected result |
|---------|---------------|-----------------|
| REST submit | `curl POST /api/v1/tasks` | `202` with task_id |
| REST status | `curl GET /api/v1/tasks/:id` | status transitions to DONE |
| gRPC submit | `grpcurl SubmitTask` | task_id returned |
| gRPC stream | `grpcurl StreamTaskUpdates` | multiple status frames |
| Email delivery | MailHog http://localhost:8025 | email appears |
| Webhook delivery | netcat listener | HTTP POST received |
| Retries | Bad webhook URL | 3 retries in logs |
| DLQ | Bad webhook URL | message in tasks.dlq |
| Rate limiter | `--rate-limit 2` + flood | "rate limit exceeded" in logs |
| Idempotency | Re-publish DONE task | "already terminal, skipping" |
| Graceful shutdown | SIGTERM during RUNNING | task completes, clean exit |
| Scheduler | Insert scheduled_job row | task fired on schedule |
| Prometheus | `curl :9095/metrics` | taskflow_* metrics present |
| Grafana | http://localhost:3000 | GoTaskFlow dashboard loads |
| Jaeger traces | http://localhost:16686 | end-to-end span chain |
| JSON logs | pipe to `jq` | valid JSON every line |
| Postgres audit | `SELECT * FROM tasks` | rows present |
| Redis fallback | `DEL task:state:<id>` | API still returns data |
