package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

type w5aWarning struct {
	msg    string
	fields map[string]any
}

type w5aWarnLog struct {
	testkit.FakeLogger
	mu       sync.Mutex
	warnings []w5aWarning
}

func (l *w5aWarnLog) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warnings = append(l.warnings, w5aWarning{msg: msg, fields: fields})
}

func (l *w5aWarnLog) recorded() []w5aWarning {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]w5aWarning(nil), l.warnings...)
}

func TestCompiledPlan_CachedEntry_IsServedWithoutReadingTheStoreAndCountsAHit(t *testing.T) {
	registerServiceMetrics(t)
	tenantID, versionID, workflowID := uuid.New(), uuid.New(), uuid.New()
	plan := `{"schema_version":1,"main_plan":"cached"}`
	number := int32(9)
	entry, err := json.Marshal(service.CompiledPlan{
		WorkflowID: workflowID, VersionID: versionID, VersionNumber: &number,
		Status: domain.VersionStatusPublished, IsValid: true, PlanJSON: &plan,
	})
	require.NoError(t, err)
	cache := newPlanCache()
	cache.store[domain.CompiledPlanCacheKey(tenantID, versionID)] = string(entry)
	versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		t.Error("the versions store was read although the cache held a valid entry")
		return nil, errors.New("must not be read")
	}}
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour})
	hitsBefore := testutil.ToFloat64(observability.WFCacheHitsTotal)
	missesBefore := testutil.ToFloat64(observability.WFCacheMissesTotal)

	got, err := svc.Get(context.Background(), tenantID, versionID)

	require.NoError(t, err)
	assert.Equal(t, workflowID, got.WorkflowID)
	assert.Equal(t, versionID, got.VersionID)
	require.NotNil(t, got.VersionNumber)
	assert.Equal(t, int32(9), *got.VersionNumber)
	require.NotNil(t, got.PlanJSON)
	assert.Equal(t, plan, *got.PlanJSON)
	assert.Equal(t, 0, versions.reads)
	assert.Zero(t, cache.sets, "a hit does not rewrite the entry")
	assert.Equal(t, hitsBefore+1, testutil.ToFloat64(observability.WFCacheHitsTotal))
	assert.Equal(t, missesBefore, testutil.ToFloat64(observability.WFCacheMissesTotal))
}

func TestCompiledPlan_SecondGetAfterAMiss_IsServedFromTheCacheWithNoExtraStoreRead(t *testing.T) {
	registerServiceMetrics(t)
	tenantID, versionID, workflowID := uuid.New(), uuid.New(), uuid.New()
	versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		return publishedPlanVersion(versionID, workflowID, `{"schema_version":1}`), nil
	}}
	cache := newPlanCache()
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour})
	hitsBefore := testutil.ToFloat64(observability.WFCacheHitsTotal)
	missesBefore := testutil.ToFloat64(observability.WFCacheMissesTotal)

	_, err := svc.Get(context.Background(), tenantID, versionID)
	require.NoError(t, err)
	readsAfterMiss := versions.reads
	second, err := svc.Get(context.Background(), tenantID, versionID)

	require.NoError(t, err)
	assert.Equal(t, readsAfterMiss, versions.reads, "the second Get does not touch the versions store")
	assert.Equal(t, 1, cache.sets, "the second Get does not rewrite the entry")
	assert.Equal(t, workflowID, second.WorkflowID)
	require.NotNil(t, second.PlanJSON)
	assert.Equal(t, `{"schema_version":1}`, *second.PlanJSON)
	assert.Equal(t, hitsBefore+1, testutil.ToFloat64(observability.WFCacheHitsTotal))
	assert.Equal(t, missesBefore+1, testutil.ToFloat64(observability.WFCacheMissesTotal))
}

func TestCompiledPlan_CacheReadError_WarnsCountsAMissAndServesTheDatabasePlan(t *testing.T) {
	registerServiceMetrics(t)
	tenantID, versionID, workflowID := uuid.New(), uuid.New(), uuid.New()
	plan := `{"schema_version":1,"main_plan":"from-db"}`
	cache := newPlanCache()
	cache.getErr = errors.New("valkey down")
	versions := &planVersions{get: func(gotTenant, gotVersion uuid.UUID) (*domain.WorkflowVersion, error) {
		assert.Equal(t, tenantID, gotTenant)
		assert.Equal(t, versionID, gotVersion)
		return publishedPlanVersion(versionID, workflowID, plan), nil
	}}
	log := &w5aWarnLog{}
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour, Log: log})
	hitsBefore := testutil.ToFloat64(observability.WFCacheHitsTotal)
	missesBefore := testutil.ToFloat64(observability.WFCacheMissesTotal)

	got, err := svc.Get(context.Background(), tenantID, versionID)

	require.NoError(t, err)
	require.NotNil(t, got.PlanJSON)
	assert.Equal(t, plan, *got.PlanJSON)
	assert.Equal(t, workflowID, got.WorkflowID)
	assert.Equal(t, missesBefore+1, testutil.ToFloat64(observability.WFCacheMissesTotal))
	assert.Equal(t, hitsBefore, testutil.ToFloat64(observability.WFCacheHitsTotal))
	warnings := log.recorded()
	require.NotEmpty(t, warnings)
	assert.Equal(t, "compiled-plan cache read failed", warnings[0].msg)
	assert.Equal(t, "valkey down", warnings[0].fields["error"])
	assert.Equal(t, domain.CompiledPlanCacheKey(tenantID, versionID), warnings[0].fields["key"])
}

func TestCompiledPlan_VersionThatIsNotPublishedAndValid_IsServedButNeverCached(t *testing.T) {
	cases := []struct {
		name    string
		status  domain.VersionStatus
		isValid bool
	}{
		{name: "draft", status: domain.VersionStatusDraft, isValid: true},
		{name: "archived and valid", status: domain.VersionStatusArchived, isValid: true},
		{name: "archived and invalid", status: domain.VersionStatusArchived, isValid: false},
		{name: "published but invalid", status: domain.VersionStatusPublished, isValid: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tenantID, versionID, workflowID := uuid.New(), uuid.New(), uuid.New()
			plan := `{"schema_version":1}`
			versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
				v := publishedPlanVersion(versionID, workflowID, plan)
				v.Status = tc.status
				v.IsValid = tc.isValid
				return v, nil
			}}
			cache := newPlanCache()
			svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour})

			got, err := svc.Get(context.Background(), tenantID, versionID)

			require.NoError(t, err)
			assert.Equal(t, tc.status, got.Status)
			assert.Equal(t, tc.isValid, got.IsValid)
			require.NotNil(t, got.PlanJSON)
			assert.Equal(t, plan, *got.PlanJSON)
			assert.Zero(t, cache.sets, "only a published valid version is written to the cache")
			assert.Empty(t, cache.store)
			assert.Equal(t, 1, versions.reads, "no recheck follows a version that was not cached")
		})
	}
}

func TestCompiledPlan_NoLoggerGiven_ACacheReadErrorStillFallsBackToTheDatabase(t *testing.T) {
	tenantID, versionID := uuid.New(), uuid.New()
	cache := newPlanCache()
	cache.getErr = errors.New("valkey down")
	versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		return publishedPlanVersion(versionID, uuid.New(), `{"schema_version":1}`), nil
	}}
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour})

	var got *service.CompiledPlan
	var err error
	require.NotPanics(t, func() { got, err = svc.Get(context.Background(), tenantID, versionID) })

	require.NoError(t, err)
	require.NotNil(t, got.PlanJSON)
	assert.Equal(t, `{"schema_version":1}`, *got.PlanJSON)
}
