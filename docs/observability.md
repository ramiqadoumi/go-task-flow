# Observability

## Prometheus Metrics

Each service exposes a `/metrics` endpoint on a configurable address.

| Service | Default address |
|---------|-----------------|
| Dispatcher | `:9090` |
| Worker | `:9091` |
| Scheduler | `:9093` |
| API Gateway | `:9095` |

Override with `--metrics-addr` on any service.

### Metric Reference

#### API Gateway

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `taskflow_api_tasks_submitted_total` | Counter | `type` | Tasks accepted by the REST/gRPC API |

#### Worker

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `taskflow_worker_tasks_processed_total` | Counter | `worker_type`, `status` | Completed tasks (`status` = `done` or `dead`) |
| `taskflow_worker_tasks_inflight` | Gauge | `worker_type` | Tasks currently executing |
| `taskflow_worker_task_duration_seconds` | Histogram | `worker_type` | End-to-end execution time |
| `taskflow_worker_retries_total` | Counter | `worker_type` | Retry attempts |
| `taskflow_worker_dlq_total` | Counter | `worker_type` | Tasks forwarded to DLQ |

#### Dispatcher

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `taskflow_dispatcher_tasks_routed_total` | Counter | `task_type` | Successfully routed tasks |
| `taskflow_dispatcher_dlq_total` | Counter | — | Tasks sent to DLQ (malformed or rate-limited) |
| `taskflow_dispatcher_rate_limited_total` | Counter | — | Tasks rejected by the rate limiter |

---

## Prometheus + Grafana Stack

```bash
docker compose -f docker-compose.observability.yml up -d
```

| Service | URL |
|---------|-----|
| Prometheus | http://localhost:9091 |
| Grafana | http://localhost:3000 (admin / admin) |

The `prometheus.yml` in the project root contains scrape configuration. Add or adjust targets there.

### Sample prometheus.yml scrape config

```yaml
scrape_configs:
  - job_name: taskflow-dispatcher
    static_configs:
      - targets: ["host.docker.internal:9090"]

  - job_name: taskflow-worker
    static_configs:
      - targets: ["host.docker.internal:9091"]

  - job_name: taskflow-scheduler
    static_configs:
      - targets: ["host.docker.internal:9093"]

  - job_name: taskflow-api-gateway
    static_configs:
      - targets: ["host.docker.internal:9095"]
```

---

## Useful Prometheus Queries

**Task throughput (submitted/min)**
```promql
rate(taskflow_api_tasks_submitted_total[1m]) * 60
```

**Success rate by worker type**
```promql
rate(taskflow_worker_tasks_processed_total{status="done"}[5m])
/
rate(taskflow_worker_tasks_processed_total[5m])
```

**P99 task execution latency**
```promql
histogram_quantile(0.99,
  rate(taskflow_worker_task_duration_seconds_bucket[5m])
)
```

**Current in-flight tasks**
```promql
taskflow_worker_tasks_inflight
```

**DLQ send rate**
```promql
rate(taskflow_dispatcher_dlq_total[5m])
+ rate(taskflow_worker_dlq_total[5m])
```

**Rate-limited requests**
```promql
rate(taskflow_dispatcher_rate_limited_total[1m]) * 60
```

---

## Health & Readiness Probes

Every service exposes `/healthz` and `/readyz` endpoints on its metrics port:

| Service | Port | Endpoint |
|---------|------|----------|
| API Gateway | `:8080` | `/healthz` always 200; `/readyz` checks Redis (returns 503 if unreachable) |
| Dispatcher | `:9090` | `/healthz` and `/readyz` always 200 (via metrics server) |
| Worker | `:9091` | `/healthz` and `/readyz` always 200 (via metrics server) |
| Scheduler | `:9093` | `/healthz` and `/readyz` always 200 (via metrics server) |

The API Gateway's `/readyz` is the only probe that actively checks a dependency (Redis). The other services use the metrics port's probe which simply confirms the process is alive.

These endpoints are used by the Kubernetes liveness and readiness probes defined in `k8s/`.

---

## Structured Logging

All services emit JSON logs to stdout via `log/slog`:

```json
{
  "time": "2025-01-01T12:00:00Z",
  "level": "INFO",
  "msg": "task completed",
  "service": "worker",
  "worker_type": "email",
  "worker_id": "email-a1b2c3d4",
  "task_id": "550e8400-...",
  "duration_ms": 142,
  "attempts": 1
}
```

Set `--log-level debug` for verbose output including retry attempts and Redis operations.

Pipe to `jq` for local development:

```bash
./bin/worker serve --worker-type email 2>&1 | jq '.'
```
