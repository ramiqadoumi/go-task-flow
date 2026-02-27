package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ramiqadoumi/go-task-flow/internal/handlers"
	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
	"github.com/ramiqadoumi/go-task-flow/internal/postgres"
	redisstore "github.com/ramiqadoumi/go-task-flow/internal/redis"
	"github.com/ramiqadoumi/go-task-flow/pkg/telemetry"
	"github.com/ramiqadoumi/go-task-flow/services/worker"
	"github.com/ramiqadoumi/go-task-flow/services/worker/config"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the worker",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().String("kafka-brokers", "localhost:9092", "comma-separated Kafka broker addresses")
	serveCmd.Flags().String("redis-addr", "localhost:6379", "Redis address (host:port)")
	serveCmd.Flags().String("postgres-dsn",
		"postgres://taskflow:taskflow@localhost:5432/taskflow?sslmode=disable",
		"PostgreSQL DSN")
	serveCmd.Flags().String("worker-type", "email", "task type this worker handles (e.g. email, webhook)")
	serveCmd.Flags().Int("max-retries", 3, "maximum retry attempts per task")
	serveCmd.Flags().Duration("task-timeout", 30*time.Second, "per-task execution timeout")
	serveCmd.Flags().String("smtp-host", "localhost", "SMTP server host")
	serveCmd.Flags().Int("smtp-port", 1025, "SMTP server port")
	serveCmd.Flags().String("smtp-from", "noreply@taskflow.dev", "SMTP sender address")
	serveCmd.Flags().String("smtp-username", "", "SMTP auth username")
	serveCmd.Flags().String("smtp-password", "", "SMTP auth password or app password")
	serveCmd.Flags().String("metrics-addr", ":9091", "Prometheus metrics server address")
	serveCmd.Flags().String("otel-endpoint", "", "OTLP HTTP endpoint for tracing (e.g. localhost:4318); empty disables tracing")

	bindFlag("kafka_brokers", serveCmd.Flags(), "kafka-brokers")
	bindFlag("redis_addr", serveCmd.Flags(), "redis-addr")
	bindFlag("postgres_dsn", serveCmd.Flags(), "postgres-dsn")
	bindFlag("worker_type", serveCmd.Flags(), "worker-type")
	bindFlag("max_retries", serveCmd.Flags(), "max-retries")
	bindFlag("task_timeout", serveCmd.Flags(), "task-timeout")
	bindFlag("smtp_host", serveCmd.Flags(), "smtp-host")
	bindFlag("smtp_port", serveCmd.Flags(), "smtp-port")
	bindFlag("smtp_from", serveCmd.Flags(), "smtp-from")
	bindFlag("smtp_username", serveCmd.Flags(), "smtp-username")
	bindFlag("smtp_password", serveCmd.Flags(), "smtp-password")
	bindFlag("metrics_addr", serveCmd.Flags(), "metrics-addr")
	bindFlag("otel_endpoint", serveCmd.Flags(), "otel-endpoint")
	_ = viper.BindEnv("otel_endpoint", "OTEL_EXPORTER_OTLP_ENDPOINT")
}

func runServe(_ *cobra.Command, _ []string) error {
	cfg := config.Load(viper.GetViper())
	workerID := fmt.Sprintf("%s-%s", cfg.WorkerType, uuid.New().String()[:8])

	logger := buildLogger(cfg.LogLevel, "worker").With(
		slog.String("worker_type", cfg.WorkerType),
		slog.String("worker_id", workerID),
	)

	shutdownTracer, err := telemetry.InitTracer(context.Background(), "worker-"+cfg.WorkerType, cfg.OTelEndpoint)
	if err != nil {
		return fmt.Errorf("tracer: %w", err)
	}
	defer shutdownTracer()

	brokers := strings.Split(cfg.KafkaBrokers, ",")
	topic := "tasks.worker." + cfg.WorkerType
	groupID := "worker-" + cfg.WorkerType + "-group"

	consumer := kafka.NewConsumer(brokers, topic, groupID, logger)
	defer func() { _ = consumer.Close() }()

	producer := kafka.NewProducer(brokers)
	defer func() { _ = producer.Close() }()

	redisClient := redisstore.NewClient(cfg.RedisAddr)
	defer func() { _ = redisClient.Close() }()
	store := redisstore.NewStateStore(redisClient)

	initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	pool, err := postgres.NewPool(initCtx, cfg.PostgresDSN)
	cancel()
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()
	repo := postgres.NewRepository(pool)

	registry := handlers.NewRegistry()
	registry.Register(handlers.NewEmailHandler(handlers.EmailConfig{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		From:     cfg.SMTPFrom,
		Username: cfg.SMTPUsername,
		Password: cfg.SMTPPassword,
	}))
	registry.Register(handlers.NewWebhookHandler())

	w := worker.NewWorker(
		workerID, consumer, producer, store, repo, registry,
		worker.WithLogger(logger),
		worker.WithRetries(cfg.MaxRetries),
		worker.WithTimeout(cfg.TaskTimeout),
		worker.WithWorkerType(cfg.WorkerType),
	)

	runCtx, runCancel := context.WithCancel(context.Background())
	telemetry.StartMetricsServer(runCtx, cfg.MetricsAddr, logger)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-quit
		logger.Info("shutting down, draining in-flight tasks...")
		runCancel()
	}()

	logger.Info("worker starting",
		slog.String("topic", topic),
		slog.Int("max_retries", cfg.MaxRetries),
		slog.Duration("task_timeout", cfg.TaskTimeout),
	)

	if err := w.Run(runCtx); err != nil {
		return fmt.Errorf("worker: %w", err)
	}

	w.Wait()
	logger.Info("stopped cleanly")
	return nil
}
