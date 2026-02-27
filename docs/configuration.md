# Configuration

All services share the same configuration priority chain:

```
CLI flag  >  config file  >  built-in default
```

## Config Files

Each service searches for `config.yaml` in these locations (in order):

1. Current working directory (`./config.yaml`)
2. `$HOME/.go-task-flow/config.yaml`
3. `/etc/go-task-flow/config.yaml`

Override the path with `--config /path/to/file.yaml`.

Use the `init` subcommand to generate a default config in one step:

```bash
# Write to ~/.go-task-flow/<service>.yaml
./bin/api-gateway init
./bin/dispatcher  init
./bin/worker      init
./bin/scheduler   init

# Or write to an explicit path
./bin/api-gateway init --config /etc/go-task-flow/api-gateway.yaml

# Use --force to overwrite an existing file
./bin/api-gateway init --force
```

Reference copies also live in `configs/` if you prefer to copy manually.

## Environment Variables

Viper maps flag names to env vars automatically by uppercasing and replacing `-` with `_`.

| Flag | Env var |
|------|---------|
| `--kafka-brokers` | `KAFKA_BROKERS` |
| `--redis-addr` | `REDIS_ADDR` |
| `--postgres-dsn` | `POSTGRES_DSN` |
| `--log-level` | `LOG_LEVEL` |
| `--worker-type` | `WORKER_TYPE` |

---

## Subcommands

Every binary now has explicit subcommands:

| Subcommand | Available on | Purpose |
|------------|-------------|---------|
| `serve` | all | Start the service (all service flags live here) |
| `init` | all | Write a default config file |
| `version` | all | Print version, commit, build time, Go version |
| `migrate` | api-gateway, scheduler | Apply PostgreSQL schema migrations |

The `--config` and `--log-level` flags are **global** (available to every subcommand).

---

## API Gateway

```
./bin/api-gateway serve [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--http-port` | `8080` | REST server port |
| `--grpc-port` | `9090` | gRPC server port |
| `--metrics-addr` | `:9095` | Prometheus `/metrics` address |
| `--kafka-brokers` | `localhost:9092` | Comma-separated broker list |
| `--redis-addr` | `localhost:6379` | Redis `host:port` |
| `--postgres-dsn` | `postgres://taskflow:taskflow@localhost:5432/taskflow?sslmode=disable` | PostgreSQL DSN |
| `--jwt-secret` | `changeme` | JWT signing secret |
| `--log-level` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `--config` | — | Explicit config file path |

---

## Dispatcher

```
./bin/dispatcher serve [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--kafka-brokers` | `localhost:9092` | Broker list |
| `--redis-addr` | `localhost:6379` | Redis address |
| `--rate-limit` | `100` | Max tasks per second per task type (`0` = disabled) |
| `--metrics-addr` | `:9090` | Prometheus address |
| `--log-level` | `info` | Log level |
| `--config` | — | Config file path |

---

## Worker

```
./bin/worker serve [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--worker-type` | `email` | Task type this instance handles (also sets the Kafka topic) |
| `--max-retries` | `3` | Maximum retry attempts before DLQ |
| `--task-timeout` | `30s` | Per-attempt execution timeout (Go duration string) |
| `--kafka-brokers` | `localhost:9092` | Broker list |
| `--redis-addr` | `localhost:6379` | Redis address |
| `--postgres-dsn` | `postgres://...` | PostgreSQL DSN |
| `--smtp-host` | `localhost` | SMTP host (MailHog locally) |
| `--smtp-port` | `1025` | SMTP port |
| `--smtp-from` | `noreply@taskflow.dev` | Sender address |
| `--metrics-addr` | `:9091` | Prometheus address |
| `--log-level` | `info` | Log level |
| `--config` | — | Config file path |

> Running two worker instances of the same type? Give them different `--metrics-addr` values (e.g. `:9091` and `:9092`).

---

## Scheduler

```
./bin/scheduler serve [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--kafka-brokers` | `localhost:9092` | Broker list |
| `--redis-addr` | `localhost:6379` | Redis address |
| `--postgres-dsn` | `postgres://...` | PostgreSQL DSN |
| `--metrics-addr` | `:9093` | Prometheus address |
| `--log-level` | `info` | Log level |
| `--config` | — | Config file path |

---

## Example: `configs/worker.example.yaml`

```yaml
kafka_brokers: "localhost:9092"
redis_addr:    "localhost:6379"
postgres_dsn:  "postgres://taskflow:taskflow@localhost:5432/taskflow?sslmode=disable"
log_level:     "info"

worker_type:  "email"
max_retries:  3
task_timeout: "30s"
metrics_addr: ":9091"

smtp_host: "localhost"
smtp_port: 1025
smtp_from: "noreply@taskflow.dev"
# --- Gmail ---
# smtp_host:     "smtp.gmail.com"
# smtp_port:     587
# smtp_from:     "your@gmail.com"
# smtp_username: "your@gmail.com"
# smtp_password: "abcdefghijklmnop"
```
