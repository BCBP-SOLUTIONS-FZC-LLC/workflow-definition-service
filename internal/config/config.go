package config

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv       string
	BuildVersion string

	HTTPPort    int
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
	ShutdownDrainDelay   time.Duration

	InternalAPIToken string

	OrgMembershipBaseURL    string
	MembershipClientTimeout time.Duration
	ExecutionServiceURL     string
	ExecutionServiceToken   string
	ExecutionClientTimeout  time.Duration

	OpenBaoAddr          string
	OpenBaoToken         string
	OpenBaoMount         string
	SecretsClientTimeout time.Duration

	RLSViolationExportInterval time.Duration
	RLSViolationRetention      time.Duration

	ConnectorAliasAllowedHosts []string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv:       getEnvOrDefault("APP_ENV", "dev"),
		BuildVersion: getEnvOrDefault("BUILD_VERSION", "dev"),

		HTTPPort:    getEnvIntOrDefault("HTTP_PORT", 8080),
		MetricsPort: getEnvIntOrDefault("METRICS_PORT", 9091),

		OTELServiceName:        getEnvOrDefault("OTEL_SERVICE_NAME", "workflow-definition-service"),
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
		ShutdownDrainDelay:   getEnvDurationOrDefault("SHUTDOWN_DRAIN_DELAY", 5*time.Second),

		InternalAPIToken: getEnvOrDefault("INTERNAL_API_TOKEN", ""),

		OrgMembershipBaseURL:    getEnvOrDefault("ORG_MEMBERSHIP_BASE_URL", ""),
		MembershipClientTimeout: getEnvDurationOrDefault("MEMBERSHIP_CLIENT_TIMEOUT", 10*time.Second),
		ExecutionServiceURL:     getEnvOrDefault("EXECUTION_SERVICE_URL", ""),
		ExecutionServiceToken:   getEnvOrDefault("EXECUTION_SERVICE_TOKEN", ""),
		ExecutionClientTimeout:  getEnvDurationOrDefault("EXECUTION_CLIENT_TIMEOUT", 5*time.Second),

		OpenBaoAddr:          getEnvOrDefault("OPENBAO_ADDR", ""),
		OpenBaoToken:         getEnvOrDefault("OPENBAO_TOKEN", ""),
		OpenBaoMount:         getEnvOrDefault("OPENBAO_MOUNT", "secret"),
		SecretsClientTimeout: getEnvDurationOrDefault("SECRETS_CLIENT_TIMEOUT", 5*time.Second),

		RLSViolationExportInterval: getEnvDurationOrDefault("RLS_VIOLATION_EXPORT_INTERVAL", 30*time.Second),
		RLSViolationRetention:      getEnvDurationOrDefault("RLS_VIOLATION_RETENTION", 7*24*time.Hour),

		ConnectorAliasAllowedHosts: getEnvCSV("CONNECTOR_ALIAS_ALLOWED_HOSTS"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

var appEnvs = []string{"local", "dev", "test", "staging", "prod"}

func (c *Config) validate() error {
	if !slices.Contains(appEnvs, c.AppEnv) {
		return fmt.Errorf("APP_ENV must be one of %s", strings.Join(appEnvs, ", "))
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		return fmt.Errorf("HTTP_PORT must be in [1, 65535]")
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
	if c.ExecutionClientTimeout <= 0 {
		return fmt.Errorf("EXECUTION_CLIENT_TIMEOUT must be > 0")
	}
	if c.ShutdownDrainDelay < 0 {
		return fmt.Errorf("SHUTDOWN_DRAIN_DELAY must not be negative")
	}
	if c.SecretsClientTimeout <= 0 {
		return fmt.Errorf("SECRETS_CLIENT_TIMEOUT must be > 0")
	}
	if c.RLSViolationExportInterval <= 0 {
		return fmt.Errorf("RLS_VIOLATION_EXPORT_INTERVAL must be > 0")
	}
	if c.RLSViolationRetention <= 0 {
		return fmt.Errorf("RLS_VIOLATION_RETENTION must be > 0")
	}
	if c.AppEnv != "dev" && c.InternalAPIToken == "" {
		return fmt.Errorf("INTERNAL_API_TOKEN is required in non-dev environments")
	}
	if c.AppEnv == "prod" && c.SystemDatabaseURL == "" {
		return fmt.Errorf("SYSTEM_DATABASE_URL is required in prod environment")
	}
	if c.AppEnv != "dev" {
		if c.OrgMembershipBaseURL == "" {
			return fmt.Errorf("ORG_MEMBERSHIP_BASE_URL is required in non-dev environments")
		}
		if c.ExecutionServiceURL == "" {
			return fmt.Errorf("EXECUTION_SERVICE_URL is required in non-dev environments")
		}
		if c.ExecutionServiceToken == "" {
			return fmt.Errorf("EXECUTION_SERVICE_TOKEN is required in non-dev environments")
		}
		if c.OpenBaoAddr == "" {
			return fmt.Errorf("OPENBAO_ADDR is required in non-dev environments")
		}
		if c.OpenBaoToken == "" {
			return fmt.Errorf("OPENBAO_TOKEN is required in non-dev environments")
		}
	}
	return nil
}

func (c *Config) MigrationDSN() string {
	if c.MigrationDatabaseURL != "" {
		return c.MigrationDatabaseURL
	}
	return c.DatabaseURL
}

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

func getEnvCSV(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
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
