package config

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv       string
	BuildVersion string

	HTTPPort int
	GRPCPort int

	// OTel — consumed by platform-gincommon.InitTracingFromEnv()
	OTELServiceName          string
	OTELExporterEndpoint     string
	OTELExporterInsecure     bool
	OTELTracesSamplerRatio   float64

	DatabaseURL            string
	PGMaxConns             int32
	PGMinConns             int32
	PGSlowQueryThresholdMS int

	ValkeyAddr     string
	ValkeyPassword string

	AWSUseStub   bool
	AWSRegion    string
	SNSTopicARN  string
	SQSQueueURL  string

	OutboxPollInterval time.Duration
	OutboxBatchSize    int

	OrgMembershipBaseURL   string
	ExecutionServiceAddr   string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv:       getEnvOrDefault("APP_ENV", "dev"),
		BuildVersion: getEnvOrDefault("BUILD_VERSION", "dev"),

		HTTPPort: getEnvIntOrDefault("HTTP_PORT", 8080),
		GRPCPort: getEnvIntOrDefault("GRPC_PORT", 9090),

		OTELServiceName:        getEnvOrDefault("OTEL_SERVICE_NAME", "workflow-definition-svc"),
		OTELExporterEndpoint:   getEnvOrDefault("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		OTELExporterInsecure:   getEnvBoolOrDefault("OTEL_EXPORTER_OTLP_INSECURE", true),
		OTELTracesSamplerRatio: getEnvFloat64OrDefault("OTEL_TRACES_SAMPLER_RATIO", 1.0),

		DatabaseURL:            getEnvOrDefault("DATABASE_URL", ""),
		PGMaxConns:             int32(getEnvIntOrDefault("PG_MAX_CONNS", 10)),
		PGMinConns:             int32(getEnvIntOrDefault("PG_MIN_CONNS", 2)),
		PGSlowQueryThresholdMS: getEnvIntOrDefault("PG_SLOW_QUERY_THRESHOLD_MS", 200),

		ValkeyAddr:     getEnvOrDefault("VALKEY_ADDR", "localhost:6379"),
		ValkeyPassword: getEnvOrDefault("VALKEY_PASSWORD", ""),

		AWSUseStub:  getEnvBoolOrDefault("AWS_USE_STUB", true),
		AWSRegion:   getEnvOrDefault("AWS_REGION", "us-east-1"),
		SNSTopicARN: getEnvOrDefault("SNS_TOPIC_ARN", ""),
		SQSQueueURL: getEnvOrDefault("SQS_QUEUE_URL", ""),

		OutboxPollInterval: getEnvDurationOrDefault("OUTBOX_POLL_INTERVAL", 500*time.Millisecond),
		OutboxBatchSize:    getEnvIntOrDefault("OUTBOX_BATCH_SIZE", 50),

		OrgMembershipBaseURL: getEnvOrDefault("ORG_MEMBERSHIP_BASE_URL", ""),
		ExecutionServiceAddr: getEnvOrDefault("EXECUTION_SERVICE_ADDR", ""),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if !c.AWSUseStub {
		if c.SNSTopicARN == "" {
			return fmt.Errorf("SNS_TOPIC_ARN is required when AWS_USE_STUB=false")
		}
		if c.SQSQueueURL == "" {
			return fmt.Errorf("SQS_QUEUE_URL is required when AWS_USE_STUB=false")
		}
	}
	return nil
}


func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvIntOrDefault(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var i int
	if _, err := fmt.Sscanf(v, "%d", &i); err != nil {
		return fallback
	}
	return i
}

func getEnvBoolOrDefault(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v == "true" || v == "1"
}

func getEnvFloat64OrDefault(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var f float64
	if _, err := fmt.Sscanf(v, "%f", &f); err != nil {
		return fallback
	}
	return f
}

func getEnvDurationOrDefault(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
