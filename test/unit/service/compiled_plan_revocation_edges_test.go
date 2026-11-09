package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

type planEdgeWarnLog struct {
	testkit.FakeLogger
	warnings []string
}

func (l *planEdgeWarnLog) Warn(msg string, _ map[string]any) { l.warnings = append(l.warnings, msg) }

func TestCompiledPlan_ArchivedVersion_ReturnedButNeverCached(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		isValid bool
	}{
		{name: "archived and valid", isValid: true},
		{name: "archived and invalid", isValid: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tenantID, versionID := uuid.New(), uuid.New()
			plan := `{"schema_version":1}`
			versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
				v := publishedPlanVersion(versionID, uuid.New(), plan)
				v.Status = domain.VersionStatusArchived
				v.IsValid = tc.isValid
				return v, nil
			}}
			cache := newPlanCache()
			svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour})

			got, err := svc.Get(context.Background(), tenantID, versionID)

			require.NoError(t, err)
			assert.Equal(t, domain.VersionStatusArchived, got.Status)
			require.NotNil(t, got.PlanJSON)
			assert.Equal(t, plan, *got.PlanJSON)
			assert.Zero(t, cache.sets, "an archived version is never written to the cache")
			assert.Empty(t, cache.store)
			assert.Equal(t, 1, versions.reads, "no recheck read follows a version that was not cached")
		})
	}
}

func TestCompiledPlan_RecheckReadFails_DropsEntryAndStillServes(t *testing.T) {
	t.Parallel()

	tenantID, versionID := uuid.New(), uuid.New()
	plan := `{"schema_version":1}`
	versions := &planVersions{}
	versions.get = func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		if versions.reads > 1 {
			return nil, errors.New("connection reset")
		}
		return publishedPlanVersion(versionID, uuid.New(), plan), nil
	}
	cache := newPlanCache()
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour})

	got, err := svc.Get(context.Background(), tenantID, versionID)

	require.NoError(t, err)
	require.NotNil(t, got.PlanJSON)
	assert.Equal(t, plan, *got.PlanJSON)
	assert.Equal(t, 1, cache.sets)
	assert.Equal(t, 2, versions.reads)
	assert.NotContains(t, cache.store, domain.CompiledPlanCacheKey(tenantID, versionID),
		"an entry that cannot be rechecked is removed")
}

func TestCompiledPlan_EvictionFails_StillServesAndWarns(t *testing.T) {
	t.Parallel()

	tenantID, versionID := uuid.New(), uuid.New()
	plan := `{"schema_version":1}`
	versions := &planVersions{}
	versions.get = func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		v := publishedPlanVersion(versionID, uuid.New(), plan)
		v.IsValid = versions.reads <= 1
		return v, nil
	}
	cache := newPlanCache()
	cache.delErr = errors.New("valkey down")
	log := &planEdgeWarnLog{}
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour, Log: log})

	got, err := svc.Get(context.Background(), tenantID, versionID)

	require.NoError(t, err)
	require.NotNil(t, got.PlanJSON)
	assert.Equal(t, plan, *got.PlanJSON)
	assert.Equal(t, []string{"compiled-plan cache eviction failed"}, log.warnings)
}

func TestCompiledPlan_CachedEntryOfLaterRevokedVersion_ServedUntilExpiry(t *testing.T) {
	t.Parallel()

	tenantID, versionID := uuid.New(), uuid.New()
	revoked := false
	versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		v := publishedPlanVersion(versionID, uuid.New(), `{"schema_version":1}`)
		v.IsValid = !revoked
		return v, nil
	}}
	cache := newPlanCache()
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour})

	_, err := svc.Get(context.Background(), tenantID, versionID)
	require.NoError(t, err)
	readsAfterPopulate := versions.reads
	revoked = true

	got, err := svc.Get(context.Background(), tenantID, versionID)

	require.NoError(t, err)
	assert.True(t, got.IsValid, "the hit serves the entry cached before the revocation")
	assert.Equal(t, domain.VersionStatusPublished, got.Status)
	assert.Equal(t, readsAfterPopulate, versions.reads, "a cache hit does not read the database")
}
