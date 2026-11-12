package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func w5cCacheCounts() (hits, misses float64) {
	return testutil.ToFloat64(observability.WFCacheHitsTotal), testutil.ToFloat64(observability.WFCacheMissesTotal)
}

func TestCompiledPlan_VersionNotFoundAfterAMiss_CountsOneMissAndReturnsTheError(t *testing.T) {
	registerServiceMetrics(t)
	tenantID, versionID := uuid.New(), uuid.New()
	var readTenant, readVersion uuid.UUID
	versions := &planVersions{get: func(gotTenant, gotVersion uuid.UUID) (*domain.WorkflowVersion, error) {
		readTenant, readVersion = gotTenant, gotVersion
		return nil, domain.ErrNotFound
	}}
	cache := newPlanCache()
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour})
	hitsBefore, missesBefore := w5cCacheCounts()

	got, err := svc.Get(context.Background(), tenantID, versionID)

	require.ErrorIs(t, err, domain.ErrNotFound)
	assert.Nil(t, got)
	hitsAfter, missesAfter := w5cCacheCounts()
	assert.Equal(t, missesBefore+1, missesAfter)
	assert.Equal(t, hitsBefore, hitsAfter)
	assert.Equal(t, tenantID, readTenant)
	assert.Equal(t, versionID, readVersion)
	assert.Empty(t, cache.store, "nothing is cached for a version that does not exist")
}

func TestCompiledPlan_SecondReadOfAPublishedVersion_CountsAHitAndSkipsTheDatabase(t *testing.T) {
	registerServiceMetrics(t)
	tenantID, versionID, workflowID := uuid.New(), uuid.New(), uuid.New()
	versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		return publishedPlanVersion(versionID, workflowID, `{"schema_version":1}`), nil
	}}
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: newPlanCache(), CacheTTL: time.Hour})
	hitsBefore, missesBefore := w5cCacheCounts()

	_, err := svc.Get(context.Background(), tenantID, versionID)
	require.NoError(t, err)
	hitsAfterFirst, missesAfterFirst := w5cCacheCounts()
	readsAfterFirst := versions.reads

	got, err := svc.Get(context.Background(), tenantID, versionID)

	require.NoError(t, err)
	assert.Equal(t, workflowID, got.WorkflowID)
	assert.Equal(t, missesBefore+1, missesAfterFirst, "the first read finds nothing cached")
	assert.Equal(t, hitsBefore, hitsAfterFirst)
	hitsAfterSecond, missesAfterSecond := w5cCacheCounts()
	assert.Equal(t, hitsBefore+1, hitsAfterSecond)
	assert.Equal(t, missesAfterFirst, missesAfterSecond)
	assert.Equal(t, readsAfterFirst, versions.reads, "a hit does not read the database")
}

func TestCompiledPlan_VersionThatIsNotAValidPublishedOne_CountsAMissOnEveryRead(t *testing.T) {
	registerServiceMetrics(t)
	cases := []struct {
		name    string
		status  domain.VersionStatus
		isValid bool
	}{
		{"a draft", domain.VersionStatusDraft, true},
		{"an archived valid version", domain.VersionStatusArchived, true},
		{"an archived invalid version", domain.VersionStatusArchived, false},
		{"a published version flagged invalid", domain.VersionStatusPublished, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, versionID := uuid.New(), uuid.New()
			versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
				v := publishedPlanVersion(versionID, uuid.New(), `{"schema_version":1}`)
				v.Status = tc.status
				v.IsValid = tc.isValid
				return v, nil
			}}
			cache := newPlanCache()
			svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour})
			hitsBefore, missesBefore := w5cCacheCounts()

			for range 2 {
				got, err := svc.Get(context.Background(), tenantID, versionID)
				require.NoError(t, err)
				assert.Equal(t, tc.status, got.Status)
				assert.Equal(t, tc.isValid, got.IsValid)
			}

			hitsAfter, missesAfter := w5cCacheCounts()
			assert.Equal(t, missesBefore+2, missesAfter)
			assert.Equal(t, hitsBefore, hitsAfter)
			assert.Zero(t, cache.sets)
			assert.Empty(t, cache.store)
			assert.Equal(t, 2, versions.reads)
		})
	}
}

func TestCompiledPlan_SameVersionUnderTwoTenants_IsCachedUnderTwoLiteralKeys(t *testing.T) {
	registerServiceMetrics(t)
	tenantA, tenantB, versionID := uuid.New(), uuid.New(), uuid.New()
	versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		return publishedPlanVersion(versionID, uuid.New(), `{"schema_version":1}`), nil
	}}
	cache := newPlanCache()
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour})

	_, err := svc.Get(context.Background(), tenantA, versionID)
	require.NoError(t, err)
	_, err = svc.Get(context.Background(), tenantB, versionID)
	require.NoError(t, err)

	require.Len(t, cache.store, 2, "the same version id under two tenants is two cache entries")
	assert.Contains(t, cache.store, "wf:plan:"+tenantA.String()+":"+versionID.String())
	assert.Contains(t, cache.store, "wf:plan:"+tenantB.String()+":"+versionID.String())
}

func TestCompiledPlan_VersionRevokedDuringTheRead_IsEvictedFromTheLiteralKey(t *testing.T) {
	registerServiceMetrics(t)
	tenantID, versionID := uuid.New(), uuid.New()
	versions := &planVersions{}
	versions.get = func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		v := publishedPlanVersion(versionID, uuid.New(), `{"schema_version":1}`)
		if versions.reads > 1 {
			v.Status = domain.VersionStatusArchived
		}
		return v, nil
	}
	cache := newPlanCache()
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour})

	_, err := svc.Get(context.Background(), tenantID, versionID)

	require.NoError(t, err)
	assert.Equal(t, 1, cache.sets, "the plan was written before the recheck found it revoked")
	assert.NotContains(t, cache.store, "wf:plan:"+tenantID.String()+":"+versionID.String())
	assert.Empty(t, cache.store)
}
