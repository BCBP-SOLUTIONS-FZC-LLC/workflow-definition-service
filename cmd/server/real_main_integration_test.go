//go:build integration

package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRealMain_StartUpAgainstAnUnreachableDatabaseExitsWithCode1(t *testing.T) {
	isolateServerEnv(t, map[string]string{
		"APP_ENV": "test", "OTEL_SERVICE_NAME": "workflow-definition-service", "DATABASE_URL": "postgres://u:pw@127.0.0.1:1/wf?sslmode=disable",
		"LOG_FILE": t.TempDir() + "/app.log", "OTEL_EXPORTER_OTLP_ENDPOINT": "none",
		"VALKEY_ADDR":             "127.0.0.1:1",
		"ORG_MEMBERSHIP_BASE_URL": "http://org", "CATALOG_ADMIN_BASE_URL": "http://catalog",
		"EXECUTION_SERVICE_URL": "http://execution",
		"OPENBAO_ADDR":          "http://bao", "OPENBAO_TOKEN": "t",
	})
	var stderr bytes.Buffer

	code := realMain([]string{"workflow-definition-service"}, &stderr)

	require.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "init error: ")
	assert.NotContains(t, stderr.String(), "pw", "the database password stays out of the output")
}
