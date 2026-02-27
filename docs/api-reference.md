# API Reference

GoTaskFlow exposes both a REST and a gRPC API through the API Gateway service.

---

## REST API

Base URL: `http://localhost:8080`

### Submit a Task

```
POST /api/v1/tasks
Content-Type: application/json
```

**Request body**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | yes | Handler type: `email` or `webhook` |
| `payload` | object | yes | Handler-specific data (see below) |
| `priority` | int | no | Higher = more important (informational, default 0) |
| `scheduled_at` | RFC3339 | no | Defer execution until this time |

**Email payload**
```json
{
  "type": "email",
  "payload": {
    "to": "user@example.com",
    "subject": "Hello",
    "body": "World"
  }
}
```

**Webhook payload**
```json
{
  "type": "webhook",
  "payload": {
    "url": "https://example.com/hook",
    "method": "POST",
    "headers": { "X-Secret": "abc" },
    "body": "{\"event\":\"order.created\"}"
  }
}
```

**Response `202 Accepted`**
```json
{
  "task_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "PENDING",
  "created_at": "2025-01-01T12:00:00Z"
}
```

**Error responses**

| Code | Condition |
|------|-----------|
| `400` | Missing `type` or `payload` |
| `500` | Redis/Kafka error |

---

### Get Task Status

```
GET /api/v1/tasks/{id}
```

**Response `200 OK`**
```json
{
  "task_id": "550e8400-e29b-41d4-a716-446655440000",
  "type": "email",
  "status": "DONE",
  "priority": 0,
  "attempts": 1,
  "created_at": "2025-01-01T12:00:00Z",
  "completed_at": "2025-01-01T12:00:02Z",
  "duration_ms": 2150
}
```

`completed_at` and `duration_ms` are omitted when the task is not yet terminal.

**Status values:** `PENDING` → `QUEUED` → `RUNNING` → `DONE` / `DEAD`

**Error responses**

| Code | Condition |
|------|-----------|
| `404` | Task ID not found |
| `500` | Storage error |

---

### Health Probes

```
GET /healthz   → 200 {"status":"ok"}
GET /readyz    → 200 {"status":"ready"}   (checks Redis connectivity)
               → 503 {"error":"redis not ready"}
```

---

## gRPC API

**Endpoint:** `localhost:9090`
**Reflection:** enabled (use `grpcurl` without a proto file)

Proto source: [`proto/task/v1/task.proto`](../proto/task/v1/task.proto)

---

### SubmitTask

```protobuf
rpc SubmitTask(SubmitTaskRequest) returns (SubmitTaskResponse)
```

```bash
grpcurl -plaintext -d '{
  "type": "email",
  "payload": "<base64-encoded JSON>"
}' localhost:9090 task.v1.TaskService/SubmitTask
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Task handler type |
| `payload` | bytes | Handler payload (JSON bytes) |
| `priority` | int32 | Priority (default 0) |
| `scheduled_at` | int64 | Unix timestamp, 0 = immediate |

Response fields: `task_id`, `status`, `created_at` (Unix timestamp).

---

### GetTaskStatus

```protobuf
rpc GetTaskStatus(GetTaskStatusRequest) returns (TaskStatusResponse)
```

```bash
grpcurl -plaintext -d '{"task_id":"<uuid>"}' \
  localhost:9090 task.v1.TaskService/GetTaskStatus
```

---

### StreamTaskUpdates

Server-streaming RPC. Sends a `TaskStatusResponse` every 500 ms until the task reaches a terminal state (`DONE` or `DEAD`).

```bash
grpcurl -plaintext -d '{"task_id":"<uuid>"}' \
  localhost:9090 task.v1.TaskService/StreamTaskUpdates
```

---

### BulkSubmitTasks

Client-streaming RPC. Send multiple `SubmitTaskRequest` messages; receive a single `BulkSubmitResponse` with counts and the list of accepted task IDs.

```bash
grpcurl -plaintext \
  -d @ localhost:9090 task.v1.TaskService/BulkSubmitTasks <<EOF
{"type":"email","payload":"<base64>"}
{"type":"webhook","payload":"<base64>"}
EOF
```

Response: `{ "submitted": 2, "failed": 0, "task_ids": ["<id1>", "<id2>"] }`

---

## Introspection

```bash
# List all services
grpcurl -plaintext localhost:9090 list

# Describe the TaskService
grpcurl -plaintext localhost:9090 describe task.v1.TaskService

# Show message schema
grpcurl -plaintext localhost:9090 describe task.v1.SubmitTaskRequest
```
