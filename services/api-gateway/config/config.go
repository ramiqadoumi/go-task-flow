package config

import "github.com/spf13/viper"

// Config holds typed configuration for the api-gateway service.
type Config struct {
	LogLevel     string
	HTTPPort     string
	GRPCPort     string
	MetricsAddr  string
	KafkaBrokers string
	RedisAddr    string
	PostgresDSN  string
	JWTSecret    string
	OTelEndpoint string
}

// Load reads all values from the given viper instance.
func Load(v *viper.Viper) Config {
	return Config{
		LogLevel:     v.GetString("log_level"),
		HTTPPort:     v.GetString("http_port"),
		GRPCPort:     v.GetString("grpc_port"),
		MetricsAddr:  v.GetString("metrics_addr"),
		KafkaBrokers: v.GetString("kafka_brokers"),
		RedisAddr:    v.GetString("redis_addr"),
		PostgresDSN:  v.GetString("postgres_dsn"),
		JWTSecret:    v.GetString("jwt_secret"),
		OTelEndpoint: v.GetString("otel_endpoint"),
	}
}
