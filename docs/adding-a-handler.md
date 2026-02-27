# Adding a New Task Handler

This guide walks through adding a new task type end-to-end. The example adds an `sms` handler.

---

## 1. Implement the Handler interface

Create `internal/handlers/sms.go`:

```go
package handlers

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/ramiqadoumi/go-task-flow/internal/domain"
)

type smsPayload struct {
    To   string `json:"to"`
    Body string `json:"body"`
}

type smsHandler struct{}

func NewSMSHandler() Handler {
    return &smsHandler{}
}

func (h *smsHandler) TaskType() string { return "sms" }

func (h *smsHandler) Handle(ctx context.Context, task *domain.Task) error {
    var p smsPayload
    if err := json.Unmarshal(task.Payload, &p); err != nil {
        return fmt.Errorf("invalid sms payload: %w", err)
    }
    if p.To == "" || p.Body == "" {
        return fmt.Errorf("sms: 'to' and 'body' are required")
    }

    // TODO: call your SMS provider SDK here
    // Use ctx for timeout/cancellation awareness.

    return nil
}
```

Rules:
- Return a non-nil error to trigger a retry.
- Return nil only when the side effect is durably complete.
- Use `ctx` for any I/O — it carries a per-attempt timeout.

---

## 2. Register the Handler in the Worker

In `services/worker/cli/serve.go`, add one line inside `runServe` where handlers are registered:

```go
registry.Register(handlers.NewEmailHandler(handlers.EmailConfig{...}))
registry.Register(handlers.NewWebhookHandler())
registry.Register(handlers.NewSMSHandler())   // ← add this
```

---

## 3. Pre-create the Kafka Topic

The Dispatcher routes to `tasks.worker.<type>`, so the topic must exist:

```bash
docker exec -it <kafka-container> kafka-topics \
  --bootstrap-server localhost:9092 --create \
  --topic tasks.worker.sms --partitions 6 --replication-factor 1
```

---

## 4. Run an SMS Worker

```bash
./bin/worker serve --worker-type sms --metrics-addr :9094
```

The worker subscribes to `tasks.worker.sms` and the consumer group is set to `worker-sms-group` automatically.

---

## 5. Submit an SMS Task

```bash
curl -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "type": "sms",
    "payload": { "to": "+1234567890", "body": "Your code is 42" }
  }'
```

---

## Handler Contract

| Concern | Behaviour |
|---------|-----------|
| Return `nil` | Task marked `DONE`, offset committed |
| Return any error | Retry attempted (up to `--max-retries`) |
| Exceed `--task-timeout` | Context cancelled; current attempt errors, retry scheduled |
| All retries exhausted | Task marked `DEAD`, raw message forwarded to `tasks.dlq` |
| Task already terminal in Redis | Message ACKed and skipped (idempotency guard) |

---

## Adding Handler-Specific Config

If the handler needs configuration (API keys, endpoints), follow the same pattern as `EmailConfig`:

1. Define a config struct: `type SMSConfig struct { APIKey, From string }`
2. Accept it in the constructor: `func NewSMSHandler(cfg SMSConfig) Handler`
3. Add flags in `services/worker/cli/serve.go` (inside the `init` func):
   ```go
   serveCmd.Flags().String("sms-api-key", "", "SMS provider API key")
   bindFlag("sms_api_key", serveCmd.Flags(), "sms-api-key")
   ```
4. Pass the config when registering:
   ```go
   registry.Register(handlers.NewSMSHandler(handlers.SMSConfig{
       APIKey: viper.GetString("sms_api_key"),
   }))
   ```
