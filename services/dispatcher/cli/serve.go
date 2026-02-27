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

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
	redisstore "github.com/ramiqadoumi/go-task-flow/internal/redis"
	"github.com/ramiqadoumi/go-task-flow/pkg/telemetry"
	"github.com/ramiqadoumi/go-task-flow/services/dispatcher"
	"github.com/ramiqadoumi/go-task-flow/services/dispatcher/config"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the dispatcher",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().String("kafka-brokers", "localhost:9092", "comma-separated Kafka broker addresses")
	serveCmd.Flags().String("redis-addr", "localhost:6379", "Redis address (host:port)")
	serveCmd.Flags().Int("rate-limit", 100, "max tasks per second per task type (0 = disabled)")
	serveCmd.Flags().String("metrics-addr", ":9094", "Prometheus metrics server address")
	serveCmd.Flags().String("otel-endpoint", "", "OTLP HTTP endpoint for tracing (e.g. localhost:4318); empty disables tracing")

	bindFlag("kafka_brokers", serveCmd.Flags(), "kafka-brokers")
	bindFlag("redis_addr", serveCmd.Flags(), "redis-addr")
	bindFlag("rate_limit", serveCmd.Flags(), "rate-limit")
	bindFlag("metrics_addr", serveCmd.Flags(), "metrics-addr")
	bindFlag("otel_endpoint", serveCmd.Flags(), "otel-endpoint")
	_ = viper.BindEnv("otel_endpoint", "OTEL_EXPORTER_OTLP_ENDPOINT")
}

func runServe(_ *cobra.Command, _ []string) error {
	cfg := config.Load(viper.GetViper())
	logger := buildLogger(cfg.LogLevel, "dispatcher")

	shutdownTracer, err := telemetry.InitTracer(context.Background(), "dispatcher", cfg.OTelEndpoint)
	if err != nil {
		return fmt.Errorf("tracer: %w", err)
	}
	defer shutdownTracer()

	brokers := strings.Split(cfg.KafkaBrokers, ",")

	consumer := kafka.NewConsumer(brokers, dispatcher.TopicPending, "dispatcher-group", logger)
	defer func() { _ = consumer.Close() }()

	producer := kafka.NewProducer(brokers)
	defer func() { _ = producer.Close() }()

	redisClient := redisstore.NewClient(cfg.RedisAddr)
	defer func() { _ = redisClient.Close() }()
	store := redisstore.NewStateStore(redisClient)

	var limiter redisstore.RateLimiter
	if cfg.RateLimit > 0 {
		limiter = redisstore.NewRateLimiter(redisClient, cfg.RateLimit, time.Second)
		logger.Info("rate limiter enabled", slog.Int("limit_per_second", cfg.RateLimit))
	}

	d := dispatcher.NewDispatcher(consumer, producer, store, limiter, logger)

	ctx, cancel := context.WithCancel(context.Background())
	telemetry.StartMetricsServer(ctx, cfg.MetricsAddr, logger)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-quit
		logger.Info("shutting down...")
		cancel()
	}()

	logger.Info("dispatcher starting", slog.String("topic", dispatcher.TopicPending))
	if err := d.Run(ctx); err != nil {
		return fmt.Errorf("dispatcher: %w", err)
	}
	logger.Info("stopped")
	return nil
}
