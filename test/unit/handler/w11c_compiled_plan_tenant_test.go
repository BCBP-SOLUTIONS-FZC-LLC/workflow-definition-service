package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type w11cOwnedVersions struct {
	port.WorkflowVersionRepository
	owner   uuid.UUID
	version *domain.WorkflowVersion
	mu      sync.Mutex
	tenants []uuid.UUID
}

func (r *w11cOwnedVersions) GetByID(_ context.Context, tenantID, _ uuid.UUID) (*domain.WorkflowVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tenants = append(r.tenants, tenantID)
	if tenantID != r.owner {
		return nil, domain.ErrNotFound
	}
	return r.version, nil
}

func (r *w11cOwnedVersions) reads() []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uuid.UUID(nil), r.tenants...)
}

type w11cKeyedCache struct {
	port.CacheStore
	mu      sync.Mutex
	items   map[string]string
	getKeys []string
	setKeys []string
}

func (c *w11cKeyedCache) Get(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getKeys = append(c.getKeys, key)
	return c.items[key], nil
}

func (c *w11cKeyedCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setKeys = append(c.setKeys, key)
	c.items[key] = value
	return nil
}

func TestInternalCompiledPlan_VersionCachedOnlyForAnotherTenant_MissesTheCacheAndAnswers404(t *testing.T) {
	ownerTenant, otherTenant, versionID := uuid.New(), uuid.New(), uuid.New()
	planJSON := `{"schema_version":1}`
	number := int32(2)
	versions := &w11cOwnedVersions{owner: ownerTenant, version: &domain.WorkflowVersion{
		ID: versionID, WorkflowID: uuid.New(), Status: domain.VersionStatusPublished, IsValid: true,
		VersionNumber: &number, CompiledPlanJSON: &planJSON,
	}}
	cache := &w11cKeyedCache{items: map[string]string{}}
	plans := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	httpadapter.RegisterInternalRoutes(r.Group("/api/v1/internal"), handler.New(handler.Services{CompiledPlans: plans}))
	get := func(tenant uuid.UUID) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			"/api/v1/internal/workflow-versions/"+versionID.String()+"/compiled-plan?tenant_id="+tenant.String(), nil))
		return w
	}

	require.Equal(t, http.StatusOK, get(ownerTenant).Code)
	readsAfterFirst := versions.reads()
	require.Equal(t, http.StatusOK, get(ownerTenant).Code)
	require.Equal(t, readsAfterFirst, versions.reads(), "the owner's second read came from the cache")
	require.Len(t, cache.setKeys, 1)
	ownerKey := cache.setKeys[0]
	require.Contains(t, ownerKey, ownerTenant.String())

	w := get(otherTenant)

	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	prob := decodeProblem(t, w)
	assert.Equal(t, "https://api.workflow.platform/errors/not-found", prob["type"])
	assert.Equal(t, "NOT_FOUND", prob["code"])
	assert.Equal(t, append(readsAfterFirst, otherTenant), versions.reads(), "the other tenant's request went to the store, once, after missing the cache")
	lookup := cache.getKeys[len(cache.getKeys)-1]
	assert.NotEqual(t, ownerKey, lookup, "the other tenant must not look up the owner's entry")
	assert.True(t, strings.Contains(lookup, otherTenant.String()) && !strings.Contains(lookup, ownerTenant.String()),
		"the cache key carries the requesting tenant only: %q", lookup)
	assert.NotContains(t, w.Body.String(), planJSON, "nothing of the owner's plan is returned")
	assert.Equal(t, []string{ownerKey}, cache.setKeys, "a refused read stores nothing")
}
