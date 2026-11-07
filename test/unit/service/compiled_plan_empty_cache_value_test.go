package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

// coreSvcAEmptyCache answers every read with an empty value and no error, the
// way a cache reports a key it does not hold.
type coreSvcAEmptyCache struct {
	port.CacheStore
	setKeys []string
	delKeys []string
}

func (*coreSvcAEmptyCache) Get(context.Context, string) (string, error) { return "", nil }

func (c *coreSvcAEmptyCache) Set(_ context.Context, key, _ string, _ time.Duration) error {
	c.setKeys = append(c.setKeys, key)
	return nil
}

func (c *coreSvcAEmptyCache) Del(_ context.Context, keys ...string) error {
	c.delKeys = append(c.delKeys, keys...)
	return nil
}

func TestCompiledPlan_CacheReturnsEmptyValueWithoutError_IsAMissAndReadsTheDatabase(t *testing.T) {
	t.Parallel()

	tenantID, versionID, workflowID := uuid.New(), uuid.New(), uuid.New()
	plan := `{"schema_version":1,"main_plan":"from-the-database"}`
	versions := &planVersions{get: func(gotTenant, gotVersion uuid.UUID) (*domain.WorkflowVersion, error) {
		require.Equal(t, tenantID, gotTenant)
		require.Equal(t, versionID, gotVersion)
		return publishedPlanVersion(versionID, workflowID, plan), nil
	}}
	cache := &coreSvcAEmptyCache{}
	log := &coreSvcALog{}
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour, Log: log})

	got, err := svc.Get(context.Background(), tenantID, versionID)

	require.NoError(t, err)
	assert.Equal(t, workflowID, got.WorkflowID)
	assert.Equal(t, versionID, got.VersionID)
	require.NotNil(t, got.PlanJSON)
	assert.Equal(t, plan, *got.PlanJSON, "the plan comes from the database read that follows the miss")
	assert.Equal(t, []string{domain.CompiledPlanCacheKey(tenantID, versionID)}, cache.setKeys, "the database result is written back under the same key")
	assert.Empty(t, cache.delKeys)
	assert.Empty(t, log.warns, "an empty value is a plain miss, not a read failure or a decode failure")
}
