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
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
	"github.com/ramiqadoumi/go-task-flow/internal/postgres"
	"github.com/ramiqadoumi/go-task-flow/pkg/telemetry"
	"github.com/ramiqadoumi/go-task-flow/services/scheduler"
	"github.com/ramiqadoumi/go-task-flow/services/scheduler/config"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the scheduler",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().String("kafka-brokers", "localhost:9092", "comma-separated Kafka broker addresses")
	serveCmd.Flags().String("redis-addr", "localhost:6379", "Redis address (host:port)")
	serveCmd.Flags().String("metrics-addr", ":9093", "Prometheus metrics server address")
	serveCmd.Flags().String("otel-endpoint", "", "OTLP HTTP endpoint for tracing (e.g. localhost:4318); empty disables tracing")

	bindFlag("kafka_brokers", serveCmd.Flags(), "kafka-brokers")
	bindFlag("redis_addr", serveCmd.Flags(), "redis-addr")
	bindFlag("metrics_addr", serveCmd.Flags(), "metrics-addr")
	bindFlag("otel_endpoint", serveCmd.Flags(), "otel-endpoint")
	_ = viper.BindEnv("otel_endpoint", "OTEL_EXPORTER_OTLP_ENDPOINT")
}

func runServe(_ *cobra.Command, _ []string) error {
	cfg := config.Load(viper.GetViper())
	logger := buildLogger(cfg.LogLevel, "scheduler")
	instanceID := "scheduler-" + uuid.New().String()[:8]

	shutdownTracer, err := telemetry.InitTracer(context.Background(), "scheduler", cfg.OTelEndpoint)
	if err != nil {
		return fmt.Errorf("tracer: %w", err)
	}
	defer shutdownTracer()

	brokers := strings.Split(cfg.KafkaBrokers, ",")
	producer := kafka.NewProducer(brokers)
	defer producer.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  1 * time.Second,
		WriteTimeout: 1 * time.Second,
	})
	defer redisClient.Close()

	initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	pool, err := postgres.NewPool(initCtx, cfg.PostgresDSN)
	cancel()
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

	runCtx, runCancel := context.WithCancel(context.Background())
	telemetry.StartMetricsServer(runCtx, cfg.MetricsAddr, logger)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-quit
		logger.Info("shutting down...")
		runCancel()
	}()

	sched := scheduler.NewScheduler(pool, producer, redisClient, instanceID, logger)
	logger.Info("scheduler starting",
		slog.String("instance_id", instanceID),
		slog.Duration("check_interval", 15*time.Second),
	)
	sched.Run(runCtx)
	logger.Info("stopped")
	return nil
}
