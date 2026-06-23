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
	OTELServiceName        string
	OTELExporterEndpoint   string
	OTELExporterInsecure   bool
	OTELTracesSamplerRatio float64

	DatabaseURL            string
	PGMaxConns             int32
	PGMinConns             int32
	PGSlowQueryThresholdMS int
	PGBouncerMode          bool
	// MigrationDatabaseURL overrides DatabaseURL for the migrate subcommand.
	// Set to a direct Postgres DSN when DATABASE_URL points to PgBouncer —
	// golang-migrate uses session advisory locks that PgBouncer transaction
	// pooling drops. Falls back to DatabaseURL when unset.
	MigrationDatabaseURL string
	// DatabaseFallbackURL is tried at startup if the primary pool (DATABASE_URL)
	// fails to connect. Use a direct Postgres DSN as a safety net when PgBouncer
	// may not be ready. Startup-only — not used for runtime reconnection.
	DatabaseFallbackURL string

	ValkeyAddr        string
	ValkeyPassword    string
	ValkeyDialTimeout time.Duration
	ValkeyReadTimeout time.Duration
	// CacheCompiledPlanTTL bounds the gRPC GetCompiledWorkflow compiled-plan cache
	// (key wf:plan:<tenant>:<version>); entries are also deleted on state change.
	CacheCompiledPlanTTL time.Duration

	AWSUseStub       bool
	AWSRegion        string
	SNSTopicARN      string
	AWSEndpointURL   string
	GlueRegistryName string
	GlueRegistryARN  string

	OutboxPollInterval time.Duration
	OutboxBatchSize    int

	// InternalAPIToken, when set, is required as the x-internal-token header on
	// the internal route group (POST /internal/events). Empty disables the check
	// (local/dev); NetworkPolicy/mesh remains the primary control.
	InternalAPIToken string

	OrgMembershipBaseURL   string
	ExecutionServiceAddr   string
	ExecutionClientTimeout time.Duration
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
		PGBouncerMode:          getEnvBoolOrDefault("PG_BOUNCER_MODE", false),
		MigrationDatabaseURL:   getEnvOrDefault("MIGRATION_DATABASE_URL", ""),
		DatabaseFallbackURL:    getEnvOrDefault("DATABASE_FALLBACK_URL", ""),

		ValkeyAddr:           getEnvOrDefault("VALKEY_ADDR", "localhost:6379"),
		ValkeyPassword:       getEnvOrDefault("VALKEY_PASSWORD", ""),
		ValkeyDialTimeout:    getEnvDurationOrDefault("VALKEY_DIAL_TIMEOUT", 2*time.Second),
		ValkeyReadTimeout:    getEnvDurationOrDefault("VALKEY_READ_TIMEOUT", 1*time.Second),
		CacheCompiledPlanTTL: getEnvDurationOrDefault("CACHE_COMPILED_PLAN_TTL", time.Hour),

		AWSUseStub:       getEnvBoolOrDefault("AWS_USE_STUB", true),
		AWSRegion:        getEnvOrDefault("AWS_REGION", "us-east-1"),
		SNSTopicARN:      getEnvOrDefault("SNS_TOPIC_ARN", ""),
		AWSEndpointURL:   getEnvOrDefault("AWS_ENDPOINT_URL", ""),
		GlueRegistryName: getEnvOrDefault("GLUE_REGISTRY_NAME", ""),
		GlueRegistryARN:  getEnvOrDefault("GLUE_REGISTRY_ARN", ""),

		OutboxPollInterval: getEnvDurationOrDefault("OUTBOX_POLL_INTERVAL", 500*time.Millisecond),
		OutboxBatchSize:    getEnvIntOrDefault("OUTBOX_BATCH_SIZE", 50),

		InternalAPIToken: getEnvOrDefault("INTERNAL_API_TOKEN", ""),

		OrgMembershipBaseURL:   getEnvOrDefault("ORG_MEMBERSHIP_BASE_URL", ""),
		ExecutionServiceAddr:   getEnvOrDefault("EXECUTION_SERVICE_ADDR", ""),
		ExecutionClientTimeout: getEnvDurationOrDefault("EXECUTION_CLIENT_TIMEOUT", 5*time.Second),
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
		if c.GlueRegistryName == "" {
			return fmt.Errorf("GLUE_REGISTRY_NAME is required when AWS_USE_STUB=false")
		}
		if c.GlueRegistryARN == "" {
			return fmt.Errorf("GLUE_REGISTRY_ARN is required when AWS_USE_STUB=false")
		}
		if c.AppEnv != "dev" {
			if c.OrgMembershipBaseURL == "" {
				return fmt.Errorf("ORG_MEMBERSHIP_BASE_URL is required in non-dev environments when AWS_USE_STUB=false")
			}
			if c.ExecutionServiceAddr == "" {
				return fmt.Errorf("EXECUTION_SERVICE_ADDR is required in non-dev environments when AWS_USE_STUB=false")
			}
		}
	}
	return nil
}

// MigrationDSN returns the DSN to use for schema migrations. It prefers
// MIGRATION_DATABASE_URL so migrations can bypass PgBouncer (advisory locks
// require a direct Postgres connection), falling back to DATABASE_URL.
func (c *Config) MigrationDSN() string {
	if c.MigrationDatabaseURL != "" {
		return c.MigrationDatabaseURL
	}
	return c.DatabaseURL
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
