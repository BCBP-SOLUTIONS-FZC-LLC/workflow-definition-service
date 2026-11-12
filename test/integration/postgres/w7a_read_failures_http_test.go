//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const (
	w7aProblemBase = "https://api.workflow.platform/errors/"
	w7aStoredPlan  = `{"main_plan":"w7a-main","plans":[{"name":"w7a-main"}],"schema_version":1}`
)

type w7aPlanCache struct {
	port.CacheStore
	sets  int
	store map[string]string
}

func (c *w7aPlanCache) Get(_ context.Context, key string) (string, error) { return c.store[key], nil }

func (c *w7aPlanCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	c.sets++
	c.store[key] = value
	return nil
}

func (c *w7aPlanCache) Del(_ context.Context, keys ...string) error {
	for _, k := range keys {
		delete(c.store, k)
	}
	return nil
}

func w7aSeedPublishedVersion(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID) (workflowID, versionID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
		TemplateKey: "w7a-" + uuid.NewString(), Name: "Plan read",
	}
	require.NoError(t, postgres.NewWorkflowRepo(pool).Create(ctx, wf))
	number := int32(1)
	plan := w7aStoredPlan
	v := newDraftVersion(wf.ID, tenantID)
	v.Status = domain.VersionStatusPublished
	v.VersionNumber = &number
	v.CompiledPlanJSON = &plan
	require.NoError(t, postgres.NewWorkflowVersionRepo(pool).Create(ctx, v))
	return wf.ID, v.ID
}

func w7aPlanRouter(pool *pgcommon.Pool, cache port.CacheStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{
		Versions: postgres.NewWorkflowVersionRepo(pool), Cache: cache, CacheTTL: time.Hour,
	})
	r := gin.New()
	httpadapter.RegisterInternalRoutes(r.Group("/api/v1/internal"), handler.New(handler.Services{CompiledPlans: svc}))
	return r
}

func w7aGetPlan(r *gin.Engine, versionID, tenantID uuid.UUID) *httptest.ResponseRecorder {
	target := "/api/v1/internal/workflow-versions/" + versionID.String() + "/compiled-plan?tenant_id=" + tenantID.String()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func w7aDecode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
	return body
}

func TestCompiledPlanHTTP_PublishedVersionMarkedInvalid_Returns200WithThePlanAndCachesNothing(t *testing.T) {
	pool := newTestPool(t)
	tenantID := uuid.New()
	workflowID, versionID := w7aSeedPublishedVersion(t, pool, tenantID)
	require.NoError(t, postgres.NewWorkflowVersionRepo(pool).SetInvalid(context.Background(), tenantID, versionID,
		`[{"node_id":"w7a-task","error":"w7a assignee left"}]`))
	cache := &w7aPlanCache{store: map[string]string{}}
	router := w7aPlanRouter(pool, cache)

	for _, attempt := range []string{"first read", "second read"} {
		w := w7aGetPlan(router, versionID, tenantID)

		require.Equal(t, http.StatusOK, w.Code, "%s: %s", attempt, w.Body.String())
		body := w7aDecode(t, w)
		assert.Equal(t, false, body["is_valid"], attempt)
		assert.Equal(t, "PUBLISHED", body["status"], attempt)
		assert.Equal(t, workflowID.String(), body["workflow_id"], attempt)
		assert.Equal(t, versionID.String(), body["version_id"], attempt)
		assert.EqualValues(t, 1, body["version_number"], attempt)
		planJSON, err := json.Marshal(body["compiled_plan"])
		require.NoError(t, err)
		assert.JSONEq(t, w7aStoredPlan, string(planJSON), attempt)
	}
	assert.Zero(t, cache.sets, "a version marked invalid is never written to the cache")
	assert.Empty(t, cache.store)

	validWorkflowID, validVersionID := w7aSeedPublishedVersion(t, pool, tenantID)
	w := w7aGetPlan(router, validVersionID, tenantID)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w7aDecode(t, w)
	assert.Equal(t, true, body["is_valid"])
	assert.Equal(t, validWorkflowID.String(), body["workflow_id"])
	assert.Equal(t, validVersionID.String(), body["version_id"])
	assert.Equal(t, 1, cache.sets, "positive control: the same router and cache do store a valid published version")
	assert.Len(t, cache.store, 1)
}

func TestCompiledPlanHTTP_UnknownVersion_Returns404NamingTheVersionAndCachesNothing(t *testing.T) {
	pool := newTestPool(t)
	tenantID, versionID := uuid.New(), uuid.New()
	cache := &w7aPlanCache{store: map[string]string{}}

	_, repoErr := postgres.NewWorkflowVersionRepo(pool).GetByID(context.Background(), tenantID, versionID)
	require.ErrorIs(t, repoErr, domain.ErrNotFound, "the repository turns the missing row into the not-found error")

	w := w7aGetPlan(w7aPlanRouter(pool, cache), versionID, tenantID)

	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	body := w7aDecode(t, w)
	assert.Equal(t, w7aProblemBase+"not-found", body["type"])
	assert.Equal(t, "NOT_FOUND", body["code"])
	assert.Contains(t, body["detail"], "resource not found")
	assert.Zero(t, cache.sets)
	assert.Empty(t, cache.store)
}

func TestCompiledPlanHTTP_DatabaseUnreachableOnACacheMiss_Returns503(t *testing.T) {
	pool := newTestPool(t)
	tenantID := uuid.New()
	_, versionID := w7aSeedPublishedVersion(t, pool, tenantID)
	cache := &w7aPlanCache{store: map[string]string{}}
	router := w7aPlanRouter(pool, cache)
	pool.Close()

	w := w7aGetPlan(router, versionID, tenantID)

	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	body := w7aDecode(t, w)
	assert.Equal(t, w7aProblemBase+"service-unavailable", body["type"])
	assert.Equal(t, "UPSTREAM_UNAVAILABLE", body["code"])
	assert.Equal(t, "an upstream dependency is temporarily unavailable", body["detail"])
	assert.Zero(t, cache.sets)
}

func w7aVersionGetRouter(pool *pgcommon.Pool) *gin.Engine {
	svc := service.NewVersionService(service.VersionDeps{Versions: postgres.NewWorkflowVersionRepo(pool)})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(r.Group("/api/v1"), handler.New(handler.Services{Versions: svc}))
	return r
}

func w7aGetVersion(ctx context.Context, r *gin.Engine, tenantID, workflowID, versionID uuid.UUID) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+workflowID.String()+"/versions/"+versionID.String(), nil).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-tenant-id", tenantID.String())
	req.Header.Set("x-user-id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestVersionGetHTTP_QueryCancelledOrDatabaseUnreachable_Returns503(t *testing.T) {
	pool := newTestPool(t)
	router := w7aVersionGetRouter(pool)

	assertUnavailable := func(t *testing.T, w *httptest.ResponseRecorder) {
		t.Helper()
		require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
		assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
		body := w7aDecode(t, w)
		assert.Equal(t, w7aProblemBase+"service-unavailable", body["type"])
		assert.Equal(t, "UPSTREAM_UNAVAILABLE", body["code"])
	}

	t.Run("the request is cancelled before the query", func(t *testing.T) {
		tenantID := uuid.New()
		workflowID, versionID := w7aSeedPublishedVersion(t, pool, tenantID)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		assertUnavailable(t, w7aGetVersion(ctx, router, tenantID, workflowID, versionID))
	})

	t.Run("the database connection pool is closed", func(t *testing.T) {
		tenantID := uuid.New()
		workflowID, versionID := w7aSeedPublishedVersion(t, pool, tenantID)
		pool.Close()

		assertUnavailable(t, w7aGetVersion(context.Background(), router, tenantID, workflowID, versionID))
	})
}

func TestWorkflowVersionRepo_NextVersionNumber_ClosedPoolIsUnavailableNotAFirstVersion(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	pool.Close()

	n, err := repo.NextVersionNumber(context.Background(), uuid.New(), uuid.New())

	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable, "a connection failure must answer 503, not a plain 500")
	assert.NotErrorIs(t, err, domain.ErrNotFound)
	assert.Zero(t, n)
}
