package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

func TestLoad_TextValueMadeOnlyOfSpaces_IsTreatedAsUnset(t *testing.T) {
	isolateEnv(t, devEnv(map[string]string{
		"BUILD_VERSION":     "   ",
		"OTEL_SERVICE_NAME": "\t",
		"OPENBAO_MOUNT":     "   ",
		"VALKEY_ADDR":       " ",
	}))

	cfg, err := config.Load()

	require.NoError(t, err)
	assert.Equal(t, "dev", cfg.BuildVersion)
	assert.Equal(t, "workflow-definition-service", cfg.OTELServiceName)
	assert.Equal(t, "secret", cfg.OpenBaoMount)
	assert.Equal(t, "localhost:6379", cfg.ValkeyAddr)
}

func TestLoad_TextValueWithSurroundingSpacesAndContent_IsTrimmed(t *testing.T) {
	isolateEnv(t, devEnv(map[string]string{"OPENBAO_MOUNT": " kv-zzq "}))

	cfg, err := config.Load()

	require.NoError(t, err)
	assert.Equal(t, "kv-zzq", cfg.OpenBaoMount)
}
