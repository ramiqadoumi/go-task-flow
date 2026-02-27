//go:build integration

package integration

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tcKafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcPostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcRedis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	testRedisAddr    string
	testPostgresDSN  string
	testKafkaBrokers []string
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	// ── Redis ────────────────────────────────────────────────────────────────
	redisCtr, err := tcRedis.Run(ctx, "redis:7-alpine")
	if err != nil {
		log.Fatalf("start redis container: %v", err)
	}
	defer redisCtr.Terminate(ctx) //nolint:errcheck

	redisConnStr, err := redisCtr.ConnectionString(ctx)
	if err != nil {
		log.Fatalf("redis connection string: %v", err)
	}
	// ConnectionString returns "redis://host:port" — strip the scheme for go-redis Addr.
	testRedisAddr = strings.TrimPrefix(redisConnStr, "redis://")

	// ── PostgreSQL ───────────────────────────────────────────────────────────
	pgCtr, err := tcPostgres.Run(ctx, "postgres:15-alpine",
		tcPostgres.WithDatabase("taskflow"),
		tcPostgres.WithUsername("taskflow"),
		tcPostgres.WithPassword("taskflow"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}
	defer pgCtr.Terminate(ctx) //nolint:errcheck

	pgDSN, err := pgCtr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("postgres connection string: %v", err)
	}
	testPostgresDSN = pgDSN

	if err := runMigrations(ctx, pgDSN); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	// ── Kafka ────────────────────────────────────────────────────────────────
	kafkaCtr, err := tcKafka.Run(ctx, "confluentinc/confluent-local:7.7.1",
		testcontainers.WithWaitStrategy(
			wait.ForLog("Kafka Server started").
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		log.Fatalf("start kafka container: %v", err)
	}
	defer kafkaCtr.Terminate(ctx) //nolint:errcheck

	brokers, err := kafkaCtr.Brokers(ctx)
	if err != nil {
		log.Fatalf("kafka brokers: %v", err)
	}
	testKafkaBrokers = brokers

	return m.Run()
}

// createTopic explicitly creates a Kafka topic before use.
// Relying solely on AllowAutoTopicCreation in the producer is not reliable —
// the first publish can race against topic creation and return UNKNOWN_TOPIC_OR_PARTITION.
func createTopic(t *testing.T, topic string) {
	t.Helper()
	conn, err := kafkago.DialContext(context.Background(), "tcp", testKafkaBrokers[0])
	if err != nil {
		t.Fatalf("kafka dial for topic creation: %v", err)
	}
	defer conn.Close()

	err = conn.CreateTopics(kafkago.TopicConfig{
		Topic:             topic,
		NumPartitions:     1,
		ReplicationFactor: 1,
	})
	if err != nil {
		t.Fatalf("create topic %q: %v", topic, err)
	}
}

// runMigrations applies both SQL migration files to the test database.
func runMigrations(ctx context.Context, dsn string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("pgxpool.New: %w", err)
	}
	defer pool.Close()

	files := []string{
		"../../internal/postgres/migrations/001_create_tasks.sql",
		"../../internal/postgres/migrations/002_create_executions.sql",
	}
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read %s: %w", f, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("exec %s: %w", f, err)
		}
	}
	return nil
}
