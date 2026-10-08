package config_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

func TestLoad_Success(t *testing.T) {
	envBackup := make(map[string]string)
	keysToBackup := []string{
		"DATABASE_URL", "APP_ENV", "BUILD_VERSION", "HTTP_PORT",
		"OTEL_SERVICE_NAME", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_INSECURE",
		"OTEL_TRACES_SAMPLER_RATIO", "PG_MAX_CONNS", "PG_MIN_CONNS",
		"PG_SLOW_QUERY_THRESHOLD_MS", "VALKEY_ADDR", "VALKEY_PASSWORD",
		"ORG_MEMBERSHIP_BASE_URL", "EXECUTION_SERVICE_URL", "EXECUTION_SERVICE_TOKEN",
		"INTERNAL_API_TOKEN",
	}
	for _, k := range keysToBackup {
		envBackup[k] = os.Getenv(k)
	}
	defer func() {
		for k, v := range envBackup {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}()

	_ = os.Setenv("DATABASE_URL", "postgres://localhost:5432/test")
	_ = os.Setenv("APP_ENV", "production")
	_ = os.Setenv("BUILD_VERSION", "v1.2.3")
	_ = os.Setenv("HTTP_PORT", "9000")
	_ = os.Setenv("OTEL_SERVICE_NAME", "custom-service")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "otel:4317")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "false")
	_ = os.Setenv("OTEL_TRACES_SAMPLER_RATIO", "0.5")
	_ = os.Setenv("PG_MAX_CONNS", "20")
	_ = os.Setenv("PG_MIN_CONNS", "5")
	_ = os.Setenv("PG_SLOW_QUERY_THRESHOLD_MS", "500")
	_ = os.Setenv("VALKEY_ADDR", "valkey:6379")
	_ = os.Setenv("VALKEY_PASSWORD", "secret")
	_ = os.Setenv("ORG_MEMBERSHIP_BASE_URL", "http://org")
	_ = os.Setenv("EXECUTION_SERVICE_URL", "http://execution:8080")
	_ = os.Setenv("EXECUTION_SERVICE_TOKEN", "execution-token")
	_ = os.Setenv("INTERNAL_API_TOKEN", "definition-token")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://localhost:5432/test" {
		t.Errorf("DatabaseURL = %q, want postgres://localhost:5432/test", cfg.DatabaseURL)
	}
	if cfg.AppEnv != "production" {
		t.Errorf("AppEnv = %q, want production", cfg.AppEnv)
	}
	if cfg.BuildVersion != "v1.2.3" {
		t.Errorf("BuildVersion = %q, want v1.2.3", cfg.BuildVersion)
	}
	if cfg.HTTPPort != 9000 {
		t.Errorf("HTTPPort = %d, want 9000", cfg.HTTPPort)
	}
	if cfg.OTELServiceName != "custom-service" {
		t.Errorf("OTELServiceName = %q, want custom-service", cfg.OTELServiceName)
	}
	if cfg.OTELExporterEndpoint != "otel:4317" {
		t.Errorf("OTELExporterEndpoint = %q, want otel:4317", cfg.OTELExporterEndpoint)
	}
	if cfg.OTELExporterInsecure {
		t.Errorf("OTELExporterInsecure = %t, want false", cfg.OTELExporterInsecure)
	}
	if cfg.OTELTracesSamplerRatio != 0.5 {
		t.Errorf("OTELTracesSamplerRatio = %f, want 0.5", cfg.OTELTracesSamplerRatio)
	}
	if cfg.PGMaxConns != 20 {
		t.Errorf("PGMaxConns = %d, want 20", cfg.PGMaxConns)
	}
	if cfg.PGMinConns != 5 {
		t.Errorf("PGMinConns = %d, want 5", cfg.PGMinConns)
	}
	if cfg.PGSlowQueryThresholdMS != 500 {
		t.Errorf("PGSlowQueryThresholdMS = %d, want 500", cfg.PGSlowQueryThresholdMS)
	}
	if cfg.ValkeyAddr != "valkey:6379" {
		t.Errorf("ValkeyAddr = %q, want valkey:6379", cfg.ValkeyAddr)
	}
	if cfg.ValkeyPassword != "secret" {
		t.Errorf("ValkeyPassword = %q, want secret", cfg.ValkeyPassword)
	}
	if cfg.OrgMembershipBaseURL != "http://org" {
		t.Errorf("OrgMembershipBaseURL = %q, want http://org", cfg.OrgMembershipBaseURL)
	}
	if cfg.ExecutionServiceURL != "http://execution:8080" || cfg.ExecutionServiceToken != "execution-token" {
		t.Errorf("ExecutionServiceURL, ExecutionServiceToken = %q, %q", cfg.ExecutionServiceURL, cfg.ExecutionServiceToken)
	}
}

func TestLoad_ValidationFailures(t *testing.T) {
	keysToBackup := []string{
		"DATABASE_URL", "APP_ENV",
		"ORG_MEMBERSHIP_BASE_URL", "EXECUTION_SERVICE_URL", "EXECUTION_SERVICE_TOKEN",
		"INTERNAL_API_TOKEN", "SYSTEM_DATABASE_URL",
		"HTTP_PORT", "PG_MIN_CONNS", "PG_MAX_CONNS",
		"OTEL_TRACES_SAMPLER_RATIO", "EXECUTION_CLIENT_TIMEOUT",
	}
	envBackup := make(map[string]string)
	for _, k := range keysToBackup {
		envBackup[k] = os.Getenv(k)
	}
	defer func() {
		for k, v := range envBackup {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}()

	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		// wantErrVar is the variable the error must name, so a case cannot
		// pass on another check's failure.
		wantErrVar string
	}{
		{
			name:    "missing database url",
			env:     map[string]string{"DATABASE_URL": ""},
			wantErr: true,
		},
		{
			name: "valid in non-dev with every required setting",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost", "APP_ENV": "production",
				"INTERNAL_API_TOKEN":      "t",
				"ORG_MEMBERSHIP_BASE_URL": "http://org", "EXECUTION_SERVICE_URL": "http://execution:8080", "EXECUTION_SERVICE_TOKEN": "t",
			},
			wantErr: false,
		},
		{
			name: "missing org membership url in non-dev",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost",
				"APP_ENV":      "production", "INTERNAL_API_TOKEN": "t",
				"ORG_MEMBERSHIP_BASE_URL": "", "EXECUTION_SERVICE_URL": "http://execution:8080", "EXECUTION_SERVICE_TOKEN": "t",
			},
			wantErr:    true,
			wantErrVar: "ORG_MEMBERSHIP_BASE_URL",
		},
		{
			name: "missing execution url in non-dev",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost",
				"APP_ENV":      "production", "INTERNAL_API_TOKEN": "t",
				"ORG_MEMBERSHIP_BASE_URL": "http://org", "EXECUTION_SERVICE_URL": "", "EXECUTION_SERVICE_TOKEN": "t",
			},
			wantErr:    true,
			wantErrVar: "EXECUTION_SERVICE_URL",
		},
		{
			name: "missing execution token in non-dev",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost",
				"APP_ENV":      "production", "INTERNAL_API_TOKEN": "t",
				"ORG_MEMBERSHIP_BASE_URL": "http://org", "EXECUTION_SERVICE_URL": "http://execution:8080", "EXECUTION_SERVICE_TOKEN": "",
			},
			wantErr:    true,
			wantErrVar: "EXECUTION_SERVICE_TOKEN",
		},
		{
			name: "valid in dev without org/execution",
			env: map[string]string{
				"DATABASE_URL":            "postgres://localhost",
				"APP_ENV":                 "dev",
				"ORG_MEMBERSHIP_BASE_URL": "", "EXECUTION_SERVICE_URL": "",
			},
			wantErr: false,
		},
		{
			name: "missing internal api token outside dev",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost", "APP_ENV": "staging",
				"INTERNAL_API_TOKEN": "",
			},
			wantErr:    true,
			wantErrVar: "INTERNAL_API_TOKEN",
		},
		{
			name: "missing internal api token in prod",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost", "APP_ENV": "prod",
				"INTERNAL_API_TOKEN": "",
			},
			wantErr:    true,
			wantErrVar: "INTERNAL_API_TOKEN",
		},
		{
			name: "valid in prod with internal api token set",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost", "APP_ENV": "prod",
				"INTERNAL_API_TOKEN": "secret-token", "SYSTEM_DATABASE_URL": "postgres://system-host/db",
				"ORG_MEMBERSHIP_BASE_URL": "http://org", "EXECUTION_SERVICE_URL": "http://execution:8080", "EXECUTION_SERVICE_TOKEN": "t",
			},
			wantErr: false,
		},
		{
			name: "missing system database url in prod",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost", "APP_ENV": "prod",
				"INTERNAL_API_TOKEN": "secret-token", "SYSTEM_DATABASE_URL": "",
			},
			wantErr: true,
		},
		{
			name:    "http port below range",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost", "HTTP_PORT": "0"},
			wantErr: true,
		},
		{
			name:    "http port above range",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost", "HTTP_PORT": "65536"},
			wantErr: true,
		},
		{
			name:    "pg max conns zero",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost", "PG_MAX_CONNS": "0", "PG_MIN_CONNS": "0"},
			wantErr: true,
		},
		{
			name:    "pg min conns negative",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost", "PG_MIN_CONNS": "-1", "PG_MAX_CONNS": "10"},
			wantErr: true,
		},
		{
			name:    "pg min conns exceeds max conns",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost", "PG_MIN_CONNS": "20", "PG_MAX_CONNS": "10"},
			wantErr: true,
		},
		{
			name:    "otel sampler ratio above 1",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost", "OTEL_TRACES_SAMPLER_RATIO": "1.5"},
			wantErr: true,
		},
		{
			name:    "otel sampler ratio below 0",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost", "OTEL_TRACES_SAMPLER_RATIO": "-0.1"},
			wantErr: true,
		},
		{
			name:    "execution client timeout zero",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost", "EXECUTION_CLIENT_TIMEOUT": "0s"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range keysToBackup {
				_ = os.Unsetenv(k)
			}
			for k, v := range tt.env {
				_ = os.Setenv(k, v)
			}
			_, err := config.Load()
			if (err != nil) != tt.wantErr {
				t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrVar != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrVar)) {
				t.Errorf("Load() error = %v, want one naming %s", err, tt.wantErrVar)
			}
		})
	}
}

func TestMigrationDSN(t *testing.T) {
	const dbURL = "postgres://localhost:5432/test"
	const migURL = "postgres://localhost:5432/migration"

	tests := []struct {
		name         string
		databaseURL  string
		migrationURL string
		wantDSN      string
	}{
		{
			name:         "MIGRATION_DATABASE_URL set — returns migration URL",
			databaseURL:  dbURL,
			migrationURL: migURL,
			wantDSN:      migURL,
		},
		{
			name:         "MIGRATION_DATABASE_URL empty — falls back to DATABASE_URL",
			databaseURL:  dbURL,
			migrationURL: "",
			wantDSN:      dbURL,
		},
	}

	keysToBackup := []string{"DATABASE_URL", "MIGRATION_DATABASE_URL"}
	envBackup := make(map[string]string)
	for _, k := range keysToBackup {
		envBackup[k] = os.Getenv(k)
	}
	defer func() {
		for k, v := range envBackup {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Setenv("DATABASE_URL", tt.databaseURL)
			if tt.migrationURL == "" {
				_ = os.Unsetenv("MIGRATION_DATABASE_URL")
			} else {
				_ = os.Setenv("MIGRATION_DATABASE_URL", tt.migrationURL)
			}
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if got := cfg.MigrationDSN(); got != tt.wantDSN {
				t.Errorf("MigrationDSN() = %q, want %q", got, tt.wantDSN)
			}
		})
	}
}

func TestLoad_InvalidValues_FallBackToDefaults(t *testing.T) {
	keysToBackup := []string{"HTTP_PORT", "OTEL_EXPORTER_OTLP_INSECURE", "OTEL_TRACES_SAMPLER_RATIO", "EXECUTION_CLIENT_TIMEOUT"}
	envBackup := make(map[string]string)
	for _, k := range keysToBackup {
		envBackup[k] = os.Getenv(k)
	}
	defer func() {
		for k, v := range envBackup {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}()

	_ = os.Setenv("DATABASE_URL", "postgres://localhost")
	_ = os.Setenv("HTTP_PORT", "not-an-int")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "not-a-bool")
	_ = os.Setenv("OTEL_TRACES_SAMPLER_RATIO", "not-a-float")
	_ = os.Setenv("EXECUTION_CLIENT_TIMEOUT", "not-a-duration")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.HTTPPort != 8080 {
		t.Errorf("HTTPPort fallback = %d, want 8080", cfg.HTTPPort)
	}
	if !cfg.OTELExporterInsecure {
		t.Errorf("OTELExporterInsecure fallback = %t, want true", cfg.OTELExporterInsecure)
	}
	if cfg.OTELTracesSamplerRatio != 1.0 {
		t.Errorf("OTELTracesSamplerRatio fallback = %f, want 1.0", cfg.OTELTracesSamplerRatio)
	}
	if cfg.ExecutionClientTimeout != 5*time.Second {
		t.Errorf("ExecutionClientTimeout fallback = %v, want 5s", cfg.ExecutionClientTimeout)
	}
}

// SystemDSN backs scope=global catalog writes. Production requires its own
// DSN (Load rejects a prod config without one); everywhere else it falls
// back to DATABASE_URL so a local run needs one connection string, not two.
func TestSystemDSN(t *testing.T) {
	tests := []struct {
		name     string
		system   string
		database string
		want     string
	}{
		{"falls back to DATABASE_URL when unset", "", "postgres://app", "postgres://app"},
		{"uses SYSTEM_DATABASE_URL when set", "postgres://system", "postgres://app", "postgres://system"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", tt.database)
			t.Setenv("SYSTEM_DATABASE_URL", tt.system)
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if got := cfg.SystemDSN(); got != tt.want {
				t.Errorf("SystemDSN() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The connector-alias allowlist is read as CSV. Blank entries are dropped so
// a trailing comma cannot smuggle an empty host into the list — an empty
// entry that reached ValidateBaseURLHost would be skipped there too, but the
// list is also what operators read back, so it must be clean at the source.
func TestLoad_ConnectorAliasAllowedHosts(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"unset yields no hosts, which fails closed downstream", "", nil},
		{"single host", "api.internal", []string{"api.internal"}},
		{"trims surrounding whitespace", " api.internal , .svc.cluster.local ", []string{"api.internal", ".svc.cluster.local"}},
		{"drops blank entries from a trailing comma", "api.internal,,", []string{"api.internal"}},
		{"a list of only separators yields no hosts", " , , ", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://localhost")
			t.Setenv("CONNECTOR_ALIAS_ALLOWED_HOSTS", tt.raw)
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if len(cfg.ConnectorAliasAllowedHosts) != len(tt.want) {
				t.Fatalf("ConnectorAliasAllowedHosts = %#v, want %#v", cfg.ConnectorAliasAllowedHosts, tt.want)
			}
			for i, host := range tt.want {
				if cfg.ConnectorAliasAllowedHosts[i] != host {
					t.Errorf("host[%d] = %q, want %q", i, cfg.ConnectorAliasAllowedHosts[i], host)
				}
			}
		})
	}
}
