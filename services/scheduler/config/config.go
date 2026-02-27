package config

import "github.com/spf13/viper"

// Config holds typed configuration for the scheduler service.
type Config struct {
	LogLevel     string
	KafkaBrokers string
	RedisAddr    string
	PostgresDSN  string
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
		MetricsAddr:  v.GetString("metrics_addr"),
		OTelEndpoint: v.GetString("otel_endpoint"),
	}
}
