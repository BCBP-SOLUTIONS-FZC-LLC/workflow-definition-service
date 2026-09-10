package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// Config's fields are documented by env var in .env.example, not per-field
// here — that file is the single source of truth for defaults/fallback
// semantics, kept in sync with the getEnvOrDefault calls in Load() below.
type Config struct {
	AppEnv       string
	BuildVersion string

	HTTPPort    int
	GRPCPort    int
	MetricsPort int

	OTELServiceName        string
	OTELExporterEndpoint   string
	OTELExporterInsecure   bool
	OTELTracesSamplerRatio float64

	DatabaseURL            string
	PGMaxConns             int32
	PGMinConns             int32
	PGSlowQueryThresholdMS int
	PGBouncerMode          bool
	MigrationDatabaseURL   string
	DatabaseFallbackURL    string
	SystemDatabaseURL      string

	ValkeyAddr           string
	ValkeyPassword       string
	ValkeyDialTimeout    time.Duration
	ValkeyReadTimeout    time.Duration
	ValkeyWriteTimeout   time.Duration
	CacheCompiledPlanTTL time.Duration
	IdempotencyTTL       time.Duration

	AWSUseStub         bool
	AWSRegion          string
	SNSTopicARN        string
	AWSEndpointURL     string
	GlueRegistryName   string
	GlueSchemaCacheTTL time.Duration

	OutboxPollInterval time.Duration
	OutboxBatchSize    int

	InternalAPIToken string

	OrgMembershipBaseURL    string
	MembershipClientTimeout time.Duration
	ExecutionServiceAddr    string
	ExecutionClientTimeout  time.Duration

	OpenBaoAddr          string
	OpenBaoToken         string
	OpenBaoMount         string
	SecretsClientTimeout time.Duration
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv:       getEnvOrDefault("APP_ENV", "dev"),
		BuildVersion: getEnvOrDefault("BUILD_VERSION", "dev"),

		HTTPPort:    getEnvIntOrDefault("HTTP_PORT", 8080),
		GRPCPort:    getEnvIntOrDefault("GRPC_PORT", 9090),
		MetricsPort: getEnvIntOrDefault("METRICS_PORT", 9091),

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
		SystemDatabaseURL:      getEnvOrDefault("SYSTEM_DATABASE_URL", ""),

		ValkeyAddr:           getEnvOrDefault("VALKEY_ADDR", "localhost:6379"),
		ValkeyPassword:       getEnvOrDefault("VALKEY_PASSWORD", ""),
		ValkeyDialTimeout:    getEnvDurationOrDefault("VALKEY_DIAL_TIMEOUT", 2*time.Second),
		ValkeyReadTimeout:    getEnvDurationOrDefault("VALKEY_READ_TIMEOUT", 1*time.Second),
		ValkeyWriteTimeout:   getEnvDurationOrDefault("VALKEY_WRITE_TIMEOUT", 1*time.Second),
		CacheCompiledPlanTTL: getEnvDurationOrDefault("CACHE_COMPILED_PLAN_TTL", time.Hour),
		IdempotencyTTL:       getEnvDurationOrDefault("IDEMPOTENCY_TTL", 24*time.Hour),

		AWSUseStub:         getEnvBoolOrDefault("AWS_USE_STUB", true),
		AWSRegion:          getEnvOrDefault("AWS_REGION", "us-east-1"),
		SNSTopicARN:        getEnvOrDefault("SNS_TOPIC_ARN", ""),
		AWSEndpointURL:     getEnvOrDefault("AWS_ENDPOINT_URL", ""),
		GlueRegistryName:   getEnvOrDefault("GLUE_REGISTRY_NAME", ""),
		GlueSchemaCacheTTL: getEnvDurationOrDefault("GLUE_SCHEMA_CACHE_TTL", 5*time.Minute),

		OutboxPollInterval: getEnvDurationOrDefault("OUTBOX_POLL_INTERVAL", 500*time.Millisecond),
		OutboxBatchSize:    getEnvIntOrDefault("OUTBOX_BATCH_SIZE", 50),

		InternalAPIToken: getEnvOrDefault("INTERNAL_API_TOKEN", ""),

		OrgMembershipBaseURL:    getEnvOrDefault("ORG_MEMBERSHIP_BASE_URL", ""),
		MembershipClientTimeout: getEnvDurationOrDefault("MEMBERSHIP_CLIENT_TIMEOUT", 10*time.Second),
		ExecutionServiceAddr:    getEnvOrDefault("EXECUTION_SERVICE_ADDR", ""),
		ExecutionClientTimeout:  getEnvDurationOrDefault("EXECUTION_CLIENT_TIMEOUT", 5*time.Second),

		OpenBaoAddr:          getEnvOrDefault("OPENBAO_ADDR", ""),
		OpenBaoToken:         getEnvOrDefault("OPENBAO_TOKEN", ""),
		OpenBaoMount:         getEnvOrDefault("OPENBAO_MOUNT", "secret"),
		SecretsClientTimeout: getEnvDurationOrDefault("SECRETS_CLIENT_TIMEOUT", 5*time.Second),
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
	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		return fmt.Errorf("HTTP_PORT must be in [1, 65535]")
	}
	if c.GRPCPort < 1 || c.GRPCPort > 65535 {
		return fmt.Errorf("GRPC_PORT must be in [1, 65535]")
	}
	if c.MetricsPort < 1 || c.MetricsPort > 65535 {
		return fmt.Errorf("METRICS_PORT must be in [1, 65535]")
	}
	if c.PGMaxConns <= 0 {
		return fmt.Errorf("PG_MAX_CONNS must be > 0")
	}
	if c.PGMinConns < 0 {
		return fmt.Errorf("PG_MIN_CONNS must be >= 0")
	}
	if c.PGMinConns > c.PGMaxConns {
		return fmt.Errorf("PG_MIN_CONNS must be <= PG_MAX_CONNS")
	}
	if c.OTELTracesSamplerRatio < 0.0 || c.OTELTracesSamplerRatio > 1.0 {
		return fmt.Errorf("OTEL_TRACES_SAMPLER_RATIO must be in [0.0, 1.0]")
	}
	if c.OutboxBatchSize <= 0 {
		return fmt.Errorf("OUTBOX_BATCH_SIZE must be > 0")
	}
	if c.OutboxPollInterval <= 0 {
		return fmt.Errorf("OUTBOX_POLL_INTERVAL must be > 0")
	}
	if c.ExecutionClientTimeout <= 0 {
		return fmt.Errorf("EXECUTION_CLIENT_TIMEOUT must be > 0")
	}
	if c.SecretsClientTimeout <= 0 {
		return fmt.Errorf("SECRETS_CLIENT_TIMEOUT must be > 0")
	}
	if c.AppEnv != "dev" && c.OpenBaoAddr != "" && c.OpenBaoToken == "" {
		return fmt.Errorf("OPENBAO_TOKEN is required when OPENBAO_ADDR is set in non-dev environments")
	}
	if c.AppEnv == "prod" && c.InternalAPIToken == "" {
		return fmt.Errorf("INTERNAL_API_TOKEN is required in prod environment")
	}
	if c.AppEnv == "prod" && c.SystemDatabaseURL == "" {
		return fmt.Errorf("SYSTEM_DATABASE_URL is required in prod environment")
	}
	if !c.AWSUseStub {
		if c.SNSTopicARN == "" {
			return fmt.Errorf("SNS_TOPIC_ARN is required when AWS_USE_STUB=false")
		}
		if c.GlueRegistryName == "" {
			return fmt.Errorf("GLUE_REGISTRY_NAME is required when AWS_USE_STUB=false")
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

// SystemDSN returns the DSN to use for scope=global catalog writes, falling
// back to DatabaseURL when SYSTEM_DATABASE_URL is unset.
func (c *Config) SystemDSN() string {
	if c.SystemDatabaseURL != "" {
		return c.SystemDatabaseURL
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
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
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
