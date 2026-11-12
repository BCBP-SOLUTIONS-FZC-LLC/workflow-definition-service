package contract_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type planEdgeVersions struct {
	port.WorkflowVersionRepository
	version *domain.WorkflowVersion
	tenants []uuid.UUID
	ids     []uuid.UUID
}

func (r *planEdgeVersions) GetByID(_ context.Context, tenantID, id uuid.UUID) (*domain.WorkflowVersion, error) {
	r.tenants = append(r.tenants, tenantID)
	r.ids = append(r.ids, id)
	return r.version, nil
}

type planEdgeCache struct {
	port.CacheStore
	store map[string]string
}

func (c *planEdgeCache) Get(_ context.Context, key string) (string, error) { return c.store[key], nil }

func (c *planEdgeCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	c.store[key] = value
	return nil
}

func planEdgeGet(t *testing.T, router http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("x-user-id", "iam-system")
	req.Header.Set("x-tenant-roles", "iam-system")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func planEdgeRouter(t *testing.T, repo *planEdgeVersions, cache port.CacheStore) http.Handler {
	t.Helper()
	plans := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: repo, Cache: cache, CacheTTL: time.Hour})
	return internalRouterWith(t, handler.Services{CompiledPlans: plans})
}

func planEdgePublished() *domain.WorkflowVersion {
	n := int32(2)
	plan := `{"main_plan":"Main","schema_version":1}`
	return &domain.WorkflowVersion{
		ID: uuid.New(), WorkflowID: uuid.New(), Status: domain.VersionStatusPublished,
		IsValid: true, VersionNumber: &n, CompiledPlanJSON: &plan,
	}
}

func TestInternalCompiledPlan_BothParametersInvalid_ReportsVersionIDFirst(t *testing.T) {
	t.Parallel()

	repo := &planEdgeVersions{version: planEdgePublished()}
	router := planEdgeRouter(t, repo, nil)
	path := "/api/v1/internal/workflow-versions/not-a-uuid/compiled-plan?tenant_id=also-not"

	w := planEdgeGet(t, router, path, nil)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	var problem map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &problem))
	assert.Equal(t, "https://api.workflow.platform/errors/bad-request", problem["type"])
	assert.Equal(t, "BAD_REQUEST", problem["code"])
	assert.Equal(t, "invalid version_id: not a valid UUID", problem["detail"])
	assert.Empty(t, repo.ids, "the version is not read when the request is rejected")
}

func TestInternalCompiledPlan_TenantComesFromQueryOnly_IdentityHeadersIgnored(t *testing.T) {
	t.Parallel()

	queryTenant, headerTenant := uuid.New(), uuid.New()
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{name: "no identity headers", headers: nil},
		{name: "identity headers of another tenant", headers: map[string]string{
			"x-tenant-id": headerTenant.String(), "x-user-id": uuid.NewString(),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			version := planEdgePublished()
			repo := &planEdgeVersions{version: version}
			router := planEdgeRouter(t, repo, nil)
			path := "/api/v1/internal/workflow-versions/" + version.ID.String() + "/compiled-plan?tenant_id=" + queryTenant.String()

			w := planEdgeGet(t, router, path, tc.headers)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, version.ID.String(), body["version_id"])
			assert.Equal(t, []uuid.UUID{queryTenant}, repo.tenants)
		})
	}
}

func TestInternalCompiledPlan_NonCanonicalUUIDForms_AcceptedAndCachedUnderCanonicalKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		write func(uuid.UUID) string
	}{
		{name: "uppercase", write: func(id uuid.UUID) string { return strings.ToUpper(id.String()) }},
		{name: "no hyphens", write: func(id uuid.UUID) string { return strings.ReplaceAll(id.String(), "-", "") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tenantID := uuid.New()
			version := planEdgePublished()
			repo := &planEdgeVersions{version: version}
			cache := &planEdgeCache{store: map[string]string{}}
			router := planEdgeRouter(t, repo, cache)
			path := "/api/v1/internal/workflow-versions/" + tc.write(version.ID) + "/compiled-plan?tenant_id=" + tc.write(tenantID)

			w := planEdgeGet(t, router, path, nil)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, version.ID.String(), body["version_id"])
			assert.Equal(t, "PUBLISHED", body["status"])
			assert.Equal(t, uuid.UUIDs{tenantID, tenantID}, uuid.UUIDs(repo.tenants))
			assert.Equal(t, uuid.UUIDs{version.ID, version.ID}, uuid.UUIDs(repo.ids))
			require.Len(t, cache.store, 1)
			assert.Contains(t, cache.store, domain.CompiledPlanCacheKey(tenantID, version.ID))
		})
	}
}
