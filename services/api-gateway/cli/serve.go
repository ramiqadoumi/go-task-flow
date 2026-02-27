package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/ramiqadoumi/go-task-flow/internal/kafka"
	"github.com/ramiqadoumi/go-task-flow/internal/postgres"
	redisstore "github.com/ramiqadoumi/go-task-flow/internal/redis"
	"github.com/ramiqadoumi/go-task-flow/pkg/telemetry"
	taskv1 "github.com/ramiqadoumi/go-task-flow/proto/task/v1"
	"github.com/ramiqadoumi/go-task-flow/services/api-gateway/config"
	"github.com/ramiqadoumi/go-task-flow/services/api-gateway/handler"
	"github.com/ramiqadoumi/go-task-flow/services/api-gateway/middleware"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the REST and gRPC servers",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().String("http-port", "8080", "HTTP server port")
	serveCmd.Flags().String("grpc-port", "9090", "gRPC server port")
	serveCmd.Flags().String("metrics-addr", ":9095", "Prometheus metrics server address")
	serveCmd.Flags().String("kafka-brokers", "localhost:9092", "comma-separated Kafka broker addresses")
	serveCmd.Flags().String("redis-addr", "localhost:6379", "Redis address (host:port)")
	serveCmd.Flags().String("jwt-secret", "changeme", "JWT signing secret")
	serveCmd.Flags().String("otel-endpoint", "", "OTLP HTTP endpoint for tracing (e.g. localhost:4318); empty disables tracing")

	bindFlag("http_port", serveCmd.Flags(), "http-port")
	bindFlag("grpc_port", serveCmd.Flags(), "grpc-port")
	bindFlag("metrics_addr", serveCmd.Flags(), "metrics-addr")
	bindFlag("kafka_brokers", serveCmd.Flags(), "kafka-brokers")
	bindFlag("redis_addr", serveCmd.Flags(), "redis-addr")
	bindFlag("jwt_secret", serveCmd.Flags(), "jwt-secret")
	bindFlag("otel_endpoint", serveCmd.Flags(), "otel-endpoint")
	_ = viper.BindEnv("otel_endpoint", "OTEL_EXPORTER_OTLP_ENDPOINT")
}

func runServe(_ *cobra.Command, _ []string) error {
	cfg := config.Load(viper.GetViper())
	logger := buildLogger(cfg.LogLevel, "api-gateway")

	shutdownTracer, err := telemetry.InitTracer(context.Background(), "api-gateway", cfg.OTelEndpoint)
	if err != nil {
		return fmt.Errorf("tracer: %w", err)
	}
	defer shutdownTracer()

	brokers := strings.Split(cfg.KafkaBrokers, ",")
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

	restHandler := handler.NewREST(producer, store, repo, logger)
	grpcHandler := handler.NewGRPC(producer, store, repo, logger)

	// ── HTTP server ───────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(chimw.Recoverer)
	r.Use(middleware.RequestLogger(logger))
	r.Use(middleware.MaxBodySize(1 << 20)) // 1MB limit
	r.Get("/healthz", restHandler.Healthz)
	r.Get("/readyz", restHandler.Readyz)
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/tasks", restHandler.SubmitTask)
		r.Get("/tasks/{id}", restHandler.GetTaskStatus)
	})

	httpSrv := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ── gRPC server ───────────────────────────────────────────────────────────
	grpcSrv := grpc.NewServer()
	taskv1.RegisterTaskServiceServer(grpcSrv, grpcHandler)
	reflection.Register(grpcSrv)

	grpcLis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		return fmt.Errorf("grpc listen: %w", err)
	}

	// ── signal handling ───────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	// ── Prometheus metrics ────────────────────────────────────────────────────
	telemetry.StartMetricsServer(runCtx, cfg.MetricsAddr, logger)

	go func() {
		logger.Info("api-gateway HTTP starting", slog.String("addr", httpSrv.Addr))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	go func() {
		logger.Info("api-gateway gRPC starting", slog.String("addr", grpcLis.Addr().String()))
		if err := grpcSrv.Serve(grpcLis); err != nil {
			logger.Error("gRPC server error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	<-quit
	logger.Info("shutting down...")
	runCancel()

	grpcSrv.GracefulStop()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		logger.Error("HTTP shutdown error", slog.String("error", err.Error()))
	}
	logger.Info("stopped")
	return nil
}
