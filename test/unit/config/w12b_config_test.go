package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

func w12bNonDevEnv(appEnv string, extra map[string]string) map[string]string {
	env := map[string]string{
		"APP_ENV": appEnv, "DATABASE_URL": "postgres://localhost/db", "VALKEY_ADDR": "valkey:6379",
		"ORG_MEMBERSHIP_BASE_URL": "http://org", "CATALOG_ADMIN_BASE_URL": "http://catalog",
		"EXECUTION_SERVICE_URL": "http://execution",
		"OPENBAO_ADDR":          "http://bao", "OPENBAO_TOKEN": "t",
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func TestLoad_DevWithOnlyDatabaseURL_UsesEveryDefault(t *testing.T) {
	isolateEnv(t, devEnv(nil))

	cfg, err := config.Load()

	require.NoError(t, err)
	want := config.Config{
		AppEnv:                     "dev",
		BuildVersion:               "dev",
		HTTPPort:                   8080,
		MetricsPort:                9091,
		OTELServiceName:            "workflow-definition-service",
		DatabaseURL:                "postgres://localhost/db",
		ValkeyAddr:                 "localhost:6379",
		ValkeyDialTimeout:          2 * time.Second,
		ValkeyReadTimeout:          time.Second,
		ValkeyWriteTimeout:         time.Second,
		CacheCompiledPlanTTL:       time.Hour,
		IdempotencyTTL:             24 * time.Hour,
		ShutdownDrainDelay:         5 * time.Second,
		ShutdownGrace:              30 * time.Second,
		ShutdownPoolDrainTimeout:   10 * time.Second,
		MembershipClientTimeout:    10 * time.Second,
		CatalogClientTimeout:       5 * time.Second,
		PlanTableTTL:               10 * time.Minute,
		EntitlementsClientTimeout:  5 * time.Second,
		EntitlementsCacheTTL:       time.Minute,
		EntitlementsFreshnessBound: 10 * time.Minute,
		TenantPurgeEnabled:         true,
		TenantCredentialRetention:  30 * 24 * time.Hour,
		TenantArtifactRetention:    7 * 365 * 24 * time.Hour,
		TenantPurgeInterval:        24 * time.Hour,
		TenantPurgeBatch:           100,
		ExecutionClientTimeout:     5 * time.Second,
		OpenBaoMount:               "secret",
		SecretsClientTimeout:       5 * time.Second,
		RLSViolationExportInterval: 30 * time.Second,
		RLSViolationRetention:      7 * 24 * time.Hour,
	}
	assert.Equal(t, want, *cfg)
}

func TestLoad_StagingWithEverySettingSupplied_FillsEveryField(t *testing.T) {
	isolateEnv(t, map[string]string{
		"APP_ENV":                       "staging",
		"BUILD_VERSION":                 "v9.8.7",
		"HTTP_PORT":                     "9000",
		"METRICS_PORT":                  "9100",
		"OTEL_SERVICE_NAME":             "zzq-service",
		"DATABASE_URL":                  "postgres://app/db",
		"MIGRATION_DATABASE_URL":        "postgres://migrate/db",
		"DATABASE_FALLBACK_URL":         "postgres://fallback/db",
		"SYSTEM_DATABASE_URL":           "postgres://system/db",
		"VALKEY_ADDR":                   "valkey:7000",
		"VALKEY_PASSWORD":               "valkey-secret",
		"VALKEY_DIAL_TIMEOUT":           "3s",
		"VALKEY_READ_TIMEOUT":           "4s",
		"VALKEY_WRITE_TIMEOUT":          "5s",
		"CACHE_COMPILED_PLAN_TTL":       "2h",
		"IDEMPOTENCY_TTL":               "36h",
		"SHUTDOWN_DRAIN_DELAY":          "6s",
		"SHUTDOWN_GRACE":                "21s",
		"SHUTDOWN_POOL_DRAIN_TIMEOUT":   "7s",
		"ORG_MEMBERSHIP_BASE_URL":       "http://membership.zzq",
		"MEMBERSHIP_CLIENT_TIMEOUT":     "11s",
		"CATALOG_ADMIN_BASE_URL":        "http://catalog.zzq",
		"CATALOG_CLIENT_TIMEOUT":        "12s",
		"PLAN_TABLE_TTL":                "13m",
		"ENTITLEMENTS_BASE_URL":         "http://entitlements.zzq",
		"ENTITLEMENTS_CLIENT_TIMEOUT":   "17s",
		"ENTITLEMENTS_CACHE_TTL":        "18s",
		"ENTITLEMENTS_FRESHNESS_BOUND":  "19m",
		"TENANT_PURGE_ENABLED":          "false",
		"TENANT_CREDENTIAL_RETENTION":   "100h",
		"TENANT_ARTIFACT_RETENTION":     "200h",
		"TENANT_PURGE_INTERVAL":         "3h",
		"TENANT_PURGE_BATCH":            "7",
		"EXECUTION_SERVICE_URL":         "http://execution.zzq",
		"EXECUTION_CLIENT_TIMEOUT":      "14s",
		"OPENBAO_ADDR":                  "http://bao.zzq:8200",
		"OPENBAO_TOKEN":                 "bao-token",
		"OPENBAO_MOUNT":                 "kv-zzq",
		"SECRETS_CLIENT_TIMEOUT":        "15s",
		"RLS_VIOLATION_EXPORT_INTERVAL": "16s",
		"RLS_VIOLATION_RETENTION":       "300h",
		"CONNECTOR_ALIAS_ALLOWED_HOSTS": "a.zzq, .svc.zzq",
	})

	cfg, err := config.Load()

	require.NoError(t, err)
	want := config.Config{
		AppEnv:                     "staging",
		BuildVersion:               "v9.8.7",
		HTTPPort:                   9000,
		MetricsPort:                9100,
		OTELServiceName:            "zzq-service",
		DatabaseURL:                "postgres://app/db",
		MigrationDatabaseURL:       "postgres://migrate/db",
		DatabaseFallbackURL:        "postgres://fallback/db",
		SystemDatabaseURL:          "postgres://system/db",
		ValkeyAddr:                 "valkey:7000",
		ValkeyPassword:             "valkey-secret",
		ValkeyDialTimeout:          3 * time.Second,
		ValkeyReadTimeout:          4 * time.Second,
		ValkeyWriteTimeout:         5 * time.Second,
		CacheCompiledPlanTTL:       2 * time.Hour,
		IdempotencyTTL:             36 * time.Hour,
		ShutdownDrainDelay:         6 * time.Second,
		ShutdownGrace:              21 * time.Second,
		ShutdownPoolDrainTimeout:   7 * time.Second,
		OrgMembershipBaseURL:       "http://membership.zzq",
		MembershipClientTimeout:    11 * time.Second,
		CatalogAdminBaseURL:        "http://catalog.zzq",
		CatalogClientTimeout:       12 * time.Second,
		PlanTableTTL:               13 * time.Minute,
		EntitlementsBaseURL:        "http://entitlements.zzq",
		EntitlementsClientTimeout:  17 * time.Second,
		EntitlementsCacheTTL:       18 * time.Second,
		EntitlementsFreshnessBound: 19 * time.Minute,
		TenantPurgeEnabled:         false,
		TenantCredentialRetention:  100 * time.Hour,
		TenantArtifactRetention:    200 * time.Hour,
		TenantPurgeInterval:        3 * time.Hour,
		TenantPurgeBatch:           7,
		ExecutionServiceURL:        "http://execution.zzq",
		ExecutionClientTimeout:     14 * time.Second,
		OpenBaoAddr:                "http://bao.zzq:8200",
		OpenBaoToken:               "bao-token",
		OpenBaoMount:               "kv-zzq",
		SecretsClientTimeout:       15 * time.Second,
		RLSViolationExportInterval: 16 * time.Second,
		RLSViolationRetention:      300 * time.Hour,
		ConnectorAliasAllowedHosts: []string{"a.zzq", ".svc.zzq"},
	}
	assert.Equal(t, want, *cfg)
}

func TestLoad_ValkeyAddress_DefaultsOnlyInDev(t *testing.T) {
	t.Run("dev without an address defaults to localhost", func(t *testing.T) {
		isolateEnv(t, devEnv(nil))
		cfg, err := config.Load()
		require.NoError(t, err)
		assert.Equal(t, "localhost:6379", cfg.ValkeyAddr)
	})

	t.Run("dev with an explicit address keeps it", func(t *testing.T) {
		isolateEnv(t, devEnv(map[string]string{"VALKEY_ADDR": "valkey.zzq:7001"}))
		cfg, err := config.Load()
		require.NoError(t, err)
		assert.Equal(t, "valkey.zzq:7001", cfg.ValkeyAddr)
	})

	t.Run("test without an address gets no default and is refused", func(t *testing.T) {
		env := w12bNonDevEnv("test", nil)
		delete(env, "VALKEY_ADDR")
		isolateEnv(t, env)
		cfg, err := config.Load()
		assert.Nil(t, cfg)
		require.Error(t, err)
		assert.EqualError(t, err, "VALKEY_ADDR is required in non-dev environments")
	})

	t.Run("test with an address passes and keeps it", func(t *testing.T) {
		isolateEnv(t, w12bNonDevEnv("test", map[string]string{"VALKEY_ADDR": "valkey.zzq:7002"}))
		cfg, err := config.Load()
		require.NoError(t, err)
		assert.Equal(t, "valkey.zzq:7002", cfg.ValkeyAddr)
	})
}

func TestLoad_ProdSystemDatabaseURL_IsRequiredAndNamed(t *testing.T) {
	t.Run("missing is the only error", func(t *testing.T) {
		isolateEnv(t, w12bNonDevEnv("prod", nil))
		cfg, err := config.Load()
		assert.Nil(t, cfg)
		assert.EqualError(t, err, "SYSTEM_DATABASE_URL is required in prod environment")
	})

	t.Run("set passes and is kept", func(t *testing.T) {
		isolateEnv(t, w12bNonDevEnv("prod", map[string]string{"SYSTEM_DATABASE_URL": "postgres://system/db"}))
		cfg, err := config.Load()
		require.NoError(t, err)
		assert.Equal(t, "postgres://system/db", cfg.SystemDatabaseURL)
	})

	t.Run("staging does not need it", func(t *testing.T) {
		isolateEnv(t, w12bNonDevEnv("staging", nil))
		cfg, err := config.Load()
		require.NoError(t, err)
		assert.Empty(t, cfg.SystemDatabaseURL)
	})
}

func TestLoad_TenantPurgeEnabled_ParsesBooleanSpellings(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"1", true}, {"t", true}, {"T", true}, {"TRUE", true}, {"true", true}, {"True", true},
		{"0", false}, {"f", false}, {"F", false}, {"FALSE", false}, {"false", false}, {"False", false},
	} {
		t.Run("accepts "+tc.value, func(t *testing.T) {
			isolateEnv(t, devEnv(map[string]string{"TENANT_PURGE_ENABLED": tc.value}))
			cfg, err := config.Load()
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.TenantPurgeEnabled)
		})
	}

	for _, value := range []string{"maybe", "yes", "on", "no", "off", "tRuE", "2"} {
		t.Run("refuses "+value, func(t *testing.T) {
			isolateEnv(t, devEnv(map[string]string{"TENANT_PURGE_ENABLED": value}))
			cfg, err := config.Load()
			assert.Nil(t, cfg)
			assert.EqualError(t, err, "TENANT_PURGE_ENABLED must be true or false, got \""+value+"\"")
		})
	}
}

func TestLoad_ValkeyDialTimeout_NeedsAUnit(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"30s", 30 * time.Second},
		{"1m30s", 90 * time.Second},
		{"1h", time.Hour},
		{"250ms", 250 * time.Millisecond},
	} {
		t.Run("accepts "+tc.value, func(t *testing.T) {
			isolateEnv(t, devEnv(map[string]string{"VALKEY_DIAL_TIMEOUT": tc.value}))
			cfg, err := config.Load()
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.ValkeyDialTimeout)
		})
	}

	for _, value := range []string{"30", "abc", "30 s", "1m 30s"} {
		t.Run("refuses "+value, func(t *testing.T) {
			isolateEnv(t, devEnv(map[string]string{"VALKEY_DIAL_TIMEOUT": value}))
			cfg, err := config.Load()
			assert.Nil(t, cfg)
			assert.EqualError(t, err, "VALKEY_DIAL_TIMEOUT must be a duration such as 30s, got \""+value+"\"")
		})
	}
}

func TestLoad_ListenPorts_MustBeInRange(t *testing.T) {
	for _, key := range []string{"HTTP_PORT", "METRICS_PORT"} {
		for _, value := range []string{"0", "-1", "65536"} {
			t.Run(key+" refuses "+value, func(t *testing.T) {
				isolateEnv(t, devEnv(map[string]string{key: value}))
				cfg, err := config.Load()
				assert.Nil(t, cfg)
				assert.EqualError(t, err, key+" must be in [1, 65535]")
			})
		}

		for _, tc := range []struct {
			value string
			want  int
		}{{"1", 1}, {"65535", 65535}} {
			t.Run(key+" accepts "+tc.value, func(t *testing.T) {
				isolateEnv(t, devEnv(map[string]string{key: tc.value}))
				cfg, err := config.Load()
				require.NoError(t, err)
				if key == "HTTP_PORT" {
					assert.Equal(t, tc.want, cfg.HTTPPort)
					assert.Equal(t, 9091, cfg.MetricsPort)
				} else {
					assert.Equal(t, tc.want, cfg.MetricsPort)
					assert.Equal(t, 8080, cfg.HTTPPort)
				}
			})
		}
	}
}

func TestLoad_PositiveOnlySettings_RefuseZeroAndNegative(t *testing.T) {
	durationKeys := []string{
		"VALKEY_DIAL_TIMEOUT", "VALKEY_READ_TIMEOUT", "VALKEY_WRITE_TIMEOUT",
		"CACHE_COMPILED_PLAN_TTL", "IDEMPOTENCY_TTL",
		"MEMBERSHIP_CLIENT_TIMEOUT", "CATALOG_CLIENT_TIMEOUT", "PLAN_TABLE_TTL",
		"ENTITLEMENTS_CLIENT_TIMEOUT", "ENTITLEMENTS_CACHE_TTL",
		"TENANT_CREDENTIAL_RETENTION", "TENANT_ARTIFACT_RETENTION", "TENANT_PURGE_INTERVAL",
		"EXECUTION_CLIENT_TIMEOUT", "SHUTDOWN_GRACE", "SHUTDOWN_POOL_DRAIN_TIMEOUT",
		"SECRETS_CLIENT_TIMEOUT", "RLS_VIOLATION_EXPORT_INTERVAL", "RLS_VIOLATION_RETENTION",
	}
	require.Len(t, durationKeys, 19)

	for _, key := range durationKeys {
		for _, value := range []string{"0s", "-1s"} {
			t.Run(key+" refuses "+value, func(t *testing.T) {
				isolateEnv(t, devEnv(map[string]string{key: value}))
				cfg, err := config.Load()
				assert.Nil(t, cfg)
				assert.EqualError(t, err, key+" must be > 0")
			})
		}
		t.Run(key+" accepts one nanosecond", func(t *testing.T) {
			isolateEnv(t, devEnv(map[string]string{key: "1ns"}))
			cfg, err := config.Load()
			require.NoError(t, err)
			assert.NotNil(t, cfg)
		})
	}

	for _, value := range []string{"0", "-5"} {
		t.Run("TENANT_PURGE_BATCH refuses "+value, func(t *testing.T) {
			isolateEnv(t, devEnv(map[string]string{"TENANT_PURGE_BATCH": value}))
			cfg, err := config.Load()
			assert.Nil(t, cfg)
			assert.EqualError(t, err, "TENANT_PURGE_BATCH must be > 0")
		})
	}
	t.Run("TENANT_PURGE_BATCH accepts 1", func(t *testing.T) {
		isolateEnv(t, devEnv(map[string]string{"TENANT_PURGE_BATCH": "1"}))
		cfg, err := config.Load()
		require.NoError(t, err)
		assert.Equal(t, 1, cfg.TenantPurgeBatch)
	})
}

func TestLoad_ShutdownDrainDelay_ZeroIsAllowedAndNegativeIsRefused(t *testing.T) {
	t.Run("zero skips the delay", func(t *testing.T) {
		isolateEnv(t, devEnv(map[string]string{"SHUTDOWN_DRAIN_DELAY": "0s"}))
		cfg, err := config.Load()
		require.NoError(t, err)
		assert.Equal(t, time.Duration(0), cfg.ShutdownDrainDelay)
	})

	t.Run("negative is refused", func(t *testing.T) {
		isolateEnv(t, devEnv(map[string]string{"SHUTDOWN_DRAIN_DELAY": "-1s"}))
		cfg, err := config.Load()
		assert.Nil(t, cfg)
		assert.EqualError(t, err, "SHUTDOWN_DRAIN_DELAY must not be negative")
	})
}

func TestLoad_ParseAndValidationFailures_ReturnOneJoinedErrorAndNoConfig(t *testing.T) {
	isolateEnv(t, map[string]string{
		"HTTP_PORT": "8080abc", "CACHE_COMPILED_PLAN_TTL": "zz", "METRICS_PORT": "0",
	})

	cfg, err := config.Load()

	assert.Nil(t, cfg)
	require.Error(t, err)
	lines := strings.Split(err.Error(), "\n")
	assert.ElementsMatch(t, []string{
		"HTTP_PORT must be an integer, got \"8080abc\"",
		"CACHE_COMPILED_PLAN_TTL must be a duration such as 30s, got \"zz\"",
		"APP_ENV is required",
		"DATABASE_URL is required",
		"METRICS_PORT must be in [1, 65535]",
	}, lines)
}

func TestLoad_EmptyValue_IsTreatedAsUnset(t *testing.T) {
	isolateEnv(t, devEnv(map[string]string{
		"BUILD_VERSION":        "",
		"OTEL_SERVICE_NAME":    "",
		"OPENBAO_MOUNT":        "",
		"HTTP_PORT":            "",
		"TENANT_PURGE_BATCH":   "",
		"TENANT_PURGE_ENABLED": "",
		"VALKEY_DIAL_TIMEOUT":  "",
		"VALKEY_ADDR":          "",
	}))

	cfg, err := config.Load()

	require.NoError(t, err)
	assert.Equal(t, "dev", cfg.BuildVersion)
	assert.Equal(t, "workflow-definition-service", cfg.OTELServiceName)
	assert.Equal(t, "secret", cfg.OpenBaoMount)
	assert.Equal(t, 8080, cfg.HTTPPort)
	assert.Equal(t, 100, cfg.TenantPurgeBatch)
	assert.True(t, cfg.TenantPurgeEnabled)
	assert.Equal(t, 2*time.Second, cfg.ValkeyDialTimeout)
	assert.Equal(t, "localhost:6379", cfg.ValkeyAddr)
}

func TestLoad_WhitespaceOnlyValue_IsNotTreatedAsUnsetForNumbersBooleansAndDurations(t *testing.T) {
	for _, tc := range []struct{ key, want string }{
		{"HTTP_PORT", "HTTP_PORT must be an integer, got \" \""},
		{"TENANT_PURGE_BATCH", "TENANT_PURGE_BATCH must be an integer, got \" \""},
		{"TENANT_PURGE_ENABLED", "TENANT_PURGE_ENABLED must be true or false, got \" \""},
		{"VALKEY_DIAL_TIMEOUT", "VALKEY_DIAL_TIMEOUT must be a duration such as 30s, got \" \""},
	} {
		t.Run(tc.key, func(t *testing.T) {
			isolateEnv(t, devEnv(map[string]string{tc.key: " "}))
			cfg, err := config.Load()
			assert.Nil(t, cfg)
			assert.EqualError(t, err, tc.want)
		})
	}
}

func TestLoad_DotEnv_IsReadForLocalAndIgnoredForProd(t *testing.T) {
	nonDevFile := "DATABASE_URL=postgres://file/db\nHTTP_PORT=1234\nVALKEY_ADDR=valkey:6379\n" +
		"ORG_MEMBERSHIP_BASE_URL=http://org\nCATALOG_ADMIN_BASE_URL=http://catalog\n" +
		"EXECUTION_SERVICE_URL=http://execution\n" +
		"OPENBAO_ADDR=http://bao\nOPENBAO_TOKEN=t\nSYSTEM_DATABASE_URL=postgres://file/system\n"

	t.Run("local in the process loads the file", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(nonDevFile), 0o600))
		t.Chdir(dir)
		isolateEnv(t, map[string]string{"APP_ENV": "local"})

		cfg, err := config.Load()

		require.NoError(t, err)
		assert.Equal(t, "local", cfg.AppEnv)
		assert.Equal(t, 1234, cfg.HTTPPort)
		assert.Equal(t, "postgres://file/db", cfg.DatabaseURL)
	})

	t.Run("prod in the process ignores the file", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(nonDevFile), 0o600))
		t.Chdir(dir)
		isolateEnv(t, map[string]string{"APP_ENV": "prod"})

		cfg, err := config.Load()

		assert.Nil(t, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "DATABASE_URL is required")
		assert.Empty(t, os.Getenv("HTTP_PORT"), "no value from the file may be exported")
		assert.Empty(t, os.Getenv("DATABASE_URL"), "no value from the file may be exported")
	})

	t.Run("process values win over the file", func(t *testing.T) {
		dir := t.TempDir()
		file := "APP_ENV=prod\nHTTP_PORT=1234\nMETRICS_PORT=2222\nDATABASE_URL=postgres://file/db\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(file), 0o600))
		t.Chdir(dir)
		isolateEnv(t, map[string]string{
			"APP_ENV": "dev", "HTTP_PORT": "4321", "DATABASE_URL": "postgres://process/db",
		})

		cfg, err := config.Load()

		require.NoError(t, err)
		assert.Equal(t, "dev", cfg.AppEnv)
		assert.Equal(t, 4321, cfg.HTTPPort)
		assert.Equal(t, "postgres://process/db", cfg.DatabaseURL)
		assert.Equal(t, 2222, cfg.MetricsPort, "a value only in the file is still loaded")
	})
}
