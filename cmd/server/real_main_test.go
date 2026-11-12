package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func isolateServerEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range []string{
		"APP_ENV", "DATABASE_URL", "MIGRATION_DATABASE_URL", "SYSTEM_DATABASE_URL", "LOG_FILE",
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_SERVICE_NAME", "HTTP_PORT", "METRICS_PORT",
	} {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func TestRealMain_ABadConfigurationExitsWithCode1AndNamesTheProblem(t *testing.T) {
	isolateServerEnv(t, nil)
	var stderr bytes.Buffer

	code := realMain([]string{"workflow-definition-service"}, &stderr)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "config error: ")
	assert.Contains(t, stderr.String(), "APP_ENV is required")
}

func TestRealMain_MigrateAgainstAnUnreachableDatabaseExitsWithCode1(t *testing.T) {
	isolateServerEnv(t, map[string]string{
		"APP_ENV": "dev", "DATABASE_URL": "postgres://u:pw@127.0.0.1:1/wf?sslmode=disable",
		"LOG_FILE": t.TempDir() + "/app.log",
	})
	var stderr bytes.Buffer

	code := realMain([]string{"workflow-definition-service", "migrate"}, &stderr)

	require.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "migrate error: ")
	assert.NotContains(t, stderr.String(), "pw", "the database password stays out of the output")
}
