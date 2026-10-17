package config

import (
	"errors"
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

	OTELServiceName string

	DatabaseURL          string
	MigrationDatabaseURL string
	DatabaseFallbackURL  string
	SystemDatabaseURL    string

	ValkeyAddr           string
	ValkeyPassword       string
	ValkeyDialTimeout    time.Duration
	ValkeyReadTimeout    time.Duration
	ValkeyWriteTimeout   time.Duration
	CacheCompiledPlanTTL time.Duration
	IdempotencyTTL       time.Duration
	ShutdownDrainDelay   time.Duration

	ShutdownGrace            time.Duration
	ShutdownPoolDrainTimeout time.Duration

	InternalAPIToken string

	OrgMembershipBaseURL    string
	MembershipClientTimeout time.Duration
	CatalogAdminBaseURL     string
	CatalogClientTimeout    time.Duration
	PlanTableTTL            time.Duration
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

type reader struct{ errs []error }

func (r *reader) fail(format string, args ...any) {
	r.errs = append(r.errs, fmt.Errorf(format, args...))
}

func (r *reader) str(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (r *reader) integer(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		r.fail("%s must be an integer, got %q", key, v)
		return fallback
	}
	return i
}

func (r *reader) duration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		r.fail("%s must be a duration such as 30s, got %q", key, v)
		return fallback
	}
	return d
}

func (r *reader) csv(key string) []string {
	var out []string
	for _, part := range strings.Split(os.Getenv(key), ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func devLike(env string) bool { return env == "dev" || env == "local" }

func loadDotEnv() {
	env := os.Getenv("APP_ENV")
	if env == "" {
		file, err := godotenv.Read()
		if err != nil {
			return
		}
		env = file["APP_ENV"]
	}
	if devLike(env) {
		_ = godotenv.Load()
	}
}

func Load() (*Config, error) {
	loadDotEnv()

	r := &reader{}
	cfg := &Config{
		AppEnv:       r.str("APP_ENV", ""),
		BuildVersion: r.str("BUILD_VERSION", "dev"),

		HTTPPort:    r.integer("HTTP_PORT", 8080),
		MetricsPort: r.integer("METRICS_PORT", 9091),

		OTELServiceName: r.str("OTEL_SERVICE_NAME", "workflow-definition-service"),

		DatabaseURL:          r.str("DATABASE_URL", ""),
		MigrationDatabaseURL: r.str("MIGRATION_DATABASE_URL", ""),
		DatabaseFallbackURL:  r.str("DATABASE_FALLBACK_URL", ""),
		SystemDatabaseURL:    r.str("SYSTEM_DATABASE_URL", ""),

		ValkeyAddr:           r.str("VALKEY_ADDR", ""),
		ValkeyPassword:       r.str("VALKEY_PASSWORD", ""),
		ValkeyDialTimeout:    r.duration("VALKEY_DIAL_TIMEOUT", 2*time.Second),
		ValkeyReadTimeout:    r.duration("VALKEY_READ_TIMEOUT", 1*time.Second),
		ValkeyWriteTimeout:   r.duration("VALKEY_WRITE_TIMEOUT", 1*time.Second),
		CacheCompiledPlanTTL: r.duration("CACHE_COMPILED_PLAN_TTL", time.Hour),
		IdempotencyTTL:       r.duration("IDEMPOTENCY_TTL", 24*time.Hour),
		ShutdownDrainDelay:   r.duration("SHUTDOWN_DRAIN_DELAY", 5*time.Second),

		ShutdownGrace:            r.duration("SHUTDOWN_GRACE", 30*time.Second),
		ShutdownPoolDrainTimeout: r.duration("SHUTDOWN_POOL_DRAIN_TIMEOUT", 10*time.Second),

		InternalAPIToken: r.str("INTERNAL_API_TOKEN", ""),

		OrgMembershipBaseURL:    r.str("ORG_MEMBERSHIP_BASE_URL", ""),
		MembershipClientTimeout: r.duration("MEMBERSHIP_CLIENT_TIMEOUT", 10*time.Second),
		CatalogAdminBaseURL:     r.str("CATALOG_ADMIN_BASE_URL", ""),
		CatalogClientTimeout:    r.duration("CATALOG_CLIENT_TIMEOUT", 5*time.Second),
		PlanTableTTL:            r.duration("PLAN_TABLE_TTL", 10*time.Minute),
		ExecutionServiceURL:     r.str("EXECUTION_SERVICE_URL", ""),
		ExecutionServiceToken:   r.str("EXECUTION_SERVICE_TOKEN", ""),
		ExecutionClientTimeout:  r.duration("EXECUTION_CLIENT_TIMEOUT", 5*time.Second),

		OpenBaoAddr:          r.str("OPENBAO_ADDR", ""),
		OpenBaoToken:         r.str("OPENBAO_TOKEN", ""),
		OpenBaoMount:         r.str("OPENBAO_MOUNT", "secret"),
		SecretsClientTimeout: r.duration("SECRETS_CLIENT_TIMEOUT", 5*time.Second),

		RLSViolationExportInterval: r.duration("RLS_VIOLATION_EXPORT_INTERVAL", 30*time.Second),
		RLSViolationRetention:      r.duration("RLS_VIOLATION_RETENTION", 7*24*time.Hour),

		ConnectorAliasAllowedHosts: r.csv("CONNECTOR_ALIAS_ALLOWED_HOSTS"),
	}
	if cfg.ValkeyAddr == "" && cfg.AppEnv == "dev" {
		cfg.ValkeyAddr = "localhost:6379"
	}

	if err := errors.Join(append(r.errs, cfg.validate()...)...); err != nil {
		return nil, err
	}
	return cfg, nil
}

var appEnvs = []string{"local", "dev", "test", "staging", "prod"}

func (c *Config) validate() []error {
	var errs []error
	need := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}
	nonDev := c.AppEnv != "" && c.AppEnv != "dev"

	need(c.AppEnv != "", "APP_ENV is required")
	need(c.AppEnv == "" || slices.Contains(appEnvs, c.AppEnv), "APP_ENV must be one of %s", strings.Join(appEnvs, ", "))
	need(c.DatabaseURL != "", "DATABASE_URL is required")
	need(c.HTTPPort >= 1 && c.HTTPPort <= 65535, "HTTP_PORT must be in [1, 65535]")
	need(c.MetricsPort >= 1 && c.MetricsPort <= 65535, "METRICS_PORT must be in [1, 65535]")
	need(c.ValkeyAddr != "" || !nonDev, "VALKEY_ADDR is required in non-dev environments")
	need(c.ValkeyDialTimeout > 0, "VALKEY_DIAL_TIMEOUT must be > 0")
	need(c.ValkeyReadTimeout > 0, "VALKEY_READ_TIMEOUT must be > 0")
	need(c.ValkeyWriteTimeout > 0, "VALKEY_WRITE_TIMEOUT must be > 0")
	need(c.CacheCompiledPlanTTL > 0, "CACHE_COMPILED_PLAN_TTL must be > 0")
	need(c.IdempotencyTTL > 0, "IDEMPOTENCY_TTL must be > 0")
	need(c.MembershipClientTimeout > 0, "MEMBERSHIP_CLIENT_TIMEOUT must be > 0")
	need(c.CatalogClientTimeout > 0, "CATALOG_CLIENT_TIMEOUT must be > 0")
	need(c.PlanTableTTL > 0, "PLAN_TABLE_TTL must be > 0")
	need(c.ExecutionClientTimeout > 0, "EXECUTION_CLIENT_TIMEOUT must be > 0")
	need(c.ShutdownDrainDelay >= 0, "SHUTDOWN_DRAIN_DELAY must not be negative")
	need(c.ShutdownGrace > 0, "SHUTDOWN_GRACE must be > 0")
	need(c.ShutdownPoolDrainTimeout > 0, "SHUTDOWN_POOL_DRAIN_TIMEOUT must be > 0")
	need(c.SecretsClientTimeout > 0, "SECRETS_CLIENT_TIMEOUT must be > 0")
	need(c.RLSViolationExportInterval > 0, "RLS_VIOLATION_EXPORT_INTERVAL must be > 0")
	need(c.RLSViolationRetention > 0, "RLS_VIOLATION_RETENTION must be > 0")
	need(!nonDev || c.InternalAPIToken != "", "INTERNAL_API_TOKEN is required in non-dev environments")
	need(c.AppEnv != "prod" || c.SystemDatabaseURL != "", "SYSTEM_DATABASE_URL is required in prod environment")
	if nonDev {
		need(c.OrgMembershipBaseURL != "", "ORG_MEMBERSHIP_BASE_URL is required in non-dev environments")
		need(c.CatalogAdminBaseURL != "", "CATALOG_ADMIN_BASE_URL is required in non-dev environments")
		need(c.ExecutionServiceURL != "", "EXECUTION_SERVICE_URL is required in non-dev environments")
		need(c.ExecutionServiceToken != "", "EXECUTION_SERVICE_TOKEN is required in non-dev environments")
		need(c.OpenBaoAddr != "", "OPENBAO_ADDR is required in non-dev environments")
		need(c.OpenBaoToken != "", "OPENBAO_TOKEN is required in non-dev environments")
	}
	return errs
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
