package config

import (
	"time"

	"github.com/spf13/viper"
)

// Config holds typed configuration for the worker service.
type Config struct {
	LogLevel     string
	KafkaBrokers string
	RedisAddr    string
	PostgresDSN  string
	WorkerType   string
	MaxRetries   int
	TaskTimeout  time.Duration
	SMTPHost     string
	SMTPPort     int
	SMTPFrom     string
	SMTPUsername string
	SMTPPassword string
	MetricsAddr  string
	OTelEndpoint string
}

// Load reads all values from the given viper instance.
func Load(v *viper.Viper) Config {
	return Config{
		LogLevel:     v.GetString("log_level"),
		KafkaBrokers: v.GetString("kafka_brokers"),
		RedisAddr:    v.GetString("redis_addr"),
		PostgresDSN:  v.GetString("postgres_dsn"),
		WorkerType:   v.GetString("worker_type"),
		MaxRetries:   v.GetInt("max_retries"),
		TaskTimeout:  v.GetDuration("task_timeout"),
		SMTPHost:     v.GetString("smtp_host"),
		SMTPPort:     v.GetInt("smtp_port"),
		SMTPFrom:     v.GetString("smtp_from"),
		SMTPUsername: v.GetString("smtp_username"),
		SMTPPassword: v.GetString("smtp_password"),
		MetricsAddr:  v.GetString("metrics_addr"),
		OTelEndpoint: v.GetString("otel_endpoint"),
	}
}
