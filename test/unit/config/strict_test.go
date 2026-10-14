package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

func isolateEnv(t *testing.T, env map[string]string) {
	t.Helper()
	saved := os.Environ()
	os.Clearenv()
	t.Cleanup(func() {
		os.Clearenv()
		for _, kv := range saved {
			k, v, _ := strings.Cut(kv, "=")
			_ = os.Setenv(k, v)
		}
	})
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func devEnv(extra map[string]string) map[string]string {
	env := map[string]string{"APP_ENV": "dev", "DATABASE_URL": "postgres://localhost/db"}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func TestLoad_RefusesEveryMisreadValue(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"port with trailing text", "HTTP_PORT", "8080abc"},
		{"port that is not a number", "METRICS_PORT", "nine"},
		{"duration that does not parse", "VALKEY_DIAL_TIMEOUT", "soon"},
		{"plan cache without expiry", "CACHE_COMPILED_PLAN_TTL", "0"},
		{"idempotency window of zero", "IDEMPOTENCY_TTL", "0s"},
		{"membership client without a timeout", "MEMBERSHIP_CLIENT_TIMEOUT", "0"},
		{"negative valkey timeout", "VALKEY_READ_TIMEOUT", "-1s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateEnv(t, devEnv(map[string]string{tc.key: tc.value}))
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("Load() error = %v, want one naming %s", err, tc.key)
			}
		})
	}
}

func TestLoad_ReportsEveryProblemAtOnce(t *testing.T) {
	isolateEnv(t, map[string]string{"HTTP_PORT": "8080abc", "CACHE_COMPILED_PLAN_TTL": "zz"})
	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() succeeded")
	}
	for _, want := range []string{"APP_ENV", "DATABASE_URL", "HTTP_PORT", "CACHE_COMPILED_PLAN_TTL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestLoad_AppEnvIsRequired(t *testing.T) {
	isolateEnv(t, map[string]string{"DATABASE_URL": "postgres://localhost/db"})
	_, err := config.Load()
	if err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("Load() error = %v, want one naming APP_ENV", err)
	}
}

func TestLoad_ValkeyAddressIsRequiredOutsideDev(t *testing.T) {
	staging := map[string]string{
		"APP_ENV": "staging", "DATABASE_URL": "postgres://localhost/db", "INTERNAL_API_TOKEN": "t",
		"ORG_MEMBERSHIP_BASE_URL": "http://org", "EXECUTION_SERVICE_URL": "http://execution", "EXECUTION_SERVICE_TOKEN": "t",
		"OPENBAO_ADDR": "http://bao", "OPENBAO_TOKEN": "t",
	}
	isolateEnv(t, staging)
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "VALKEY_ADDR") {
		t.Fatalf("Load() error = %v, want one naming VALKEY_ADDR", err)
	}
	staging["VALKEY_ADDR"] = "valkey:6379"
	isolateEnv(t, staging)
	if _, err := config.Load(); err != nil {
		t.Fatalf("Load() with an address: %v", err)
	}
}

func TestLoad_DotEnvIsReadInDevOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/.env", []byte("HTTP_PORT=1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	isolateEnv(t, devEnv(nil))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPPort != 1234 {
		t.Errorf("dev HTTPPort = %d, want 1234 from .env", cfg.HTTPPort)
	}

	isolateEnv(t, map[string]string{
		"APP_ENV": "staging", "DATABASE_URL": "postgres://localhost/db", "INTERNAL_API_TOKEN": "t",
		"ORG_MEMBERSHIP_BASE_URL": "http://org", "EXECUTION_SERVICE_URL": "http://execution", "EXECUTION_SERVICE_TOKEN": "t",
		"OPENBAO_ADDR": "http://bao", "OPENBAO_TOKEN": "t", "VALKEY_ADDR": "valkey:6379",
	})
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPPort != 8080 {
		t.Errorf("staging HTTPPort = %d, want 8080: .env must not be read", cfg.HTTPPort)
	}
}
