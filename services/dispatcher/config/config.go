package config

import "github.com/spf13/viper"

// Config holds typed configuration for the dispatcher service.
type Config struct {
	LogLevel     string
	KafkaBrokers string
	RedisAddr    string
	RateLimit    int
	MetricsAddr  string
	OTelEndpoint string
}

// Load reads all values from the given viper instance.
func Load(v *viper.Viper) Config {
	return Config{
		LogLevel:     v.GetString("log_level"),
		KafkaBrokers: v.GetString("kafka_brokers"),
		RedisAddr:    v.GetString("redis_addr"),
		RateLimit:    v.GetInt("rate_limit"),
		MetricsAddr:  v.GetString("metrics_addr"),
		OTelEndpoint: v.GetString("otel_endpoint"),
	}
}
