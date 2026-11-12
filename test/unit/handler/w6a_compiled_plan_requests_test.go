package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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

type w6aPlanLookup struct {
	calls   int
	tenants []uuid.UUID
	ids     []uuid.UUID
}

func (f *w6aPlanLookup) Get(_ context.Context, tenantID, versionID uuid.UUID) (*service.CompiledPlan, error) {
	f.calls++
	f.tenants = append(f.tenants, tenantID)
	f.ids = append(f.ids, versionID)
	return &service.CompiledPlan{VersionID: versionID, Status: domain.VersionStatusPublished}, nil
}

type w6aFailingVersions struct {
	port.WorkflowVersionRepository
	err     error
	tenants []uuid.UUID
	ids     []uuid.UUID
}

func (r *w6aFailingVersions) GetByID(_ context.Context, tenantID, id uuid.UUID) (*domain.WorkflowVersion, error) {
	r.tenants = append(r.tenants, tenantID)
	r.ids = append(r.ids, id)
	return nil, r.err
}

func w6aGetPlan(plans w6aPlans, target string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	httpadapter.RegisterInternalRoutes(r.Group("/api/v1/internal"), handler.New(handler.Services{CompiledPlans: plans}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

type w6aPlans interface {
	Get(ctx context.Context, tenantID, versionID uuid.UUID) (*service.CompiledPlan, error)
}

func w6aPlanPath(versionID, query string) string {
	p := "/api/v1/internal/workflow-versions/" + versionID + "/compiled-plan"
	if query != "" {
		p += "?" + query
	}
	return p
}

func TestInternalCompiledPlan_RepositoryErrorOfNoKnownKind_500WithAGenericProblem(t *testing.T) {
	t.Parallel()

	const driverText = "dial tcp 10.1.2.3:5432: connect: connection refused"
	repo := &w6aFailingVersions{err: errors.New(driverText)}
	plans := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: repo})
	tenantID, versionID := uuid.New(), uuid.New()

	w := w6aGetPlan(plans, w6aPlanPath(versionID.String(), "tenant_id="+tenantID.String()))

	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	prob := decodeProblem(t, w)
	assert.Equal(t, "https://api.workflow.platform/errors/internal-error", prob["type"])
	assert.Equal(t, "INTERNAL_ERROR", prob["code"])
	assert.Equal(t, "an unexpected error occurred", prob["detail"])
	assert.NotContains(t, w.Body.String(), driverText)
	assert.NotContains(t, w.Body.String(), "10.1.2.3")
	assert.Equal(t, []uuid.UUID{tenantID}, repo.tenants)
	assert.Equal(t, []uuid.UUID{versionID}, repo.ids)
}

func TestInternalCompiledPlan_VersionIDNotAUUID_400BadRequestAndNoLookup(t *testing.T) {
	t.Parallel()

	for _, versionID := range []string{"not-a-uuid", "123", uuid.NewString() + "0"} {
		t.Run(versionID, func(t *testing.T) {
			t.Parallel()
			lookup := &w6aPlanLookup{}

			w := w6aGetPlan(lookup, w6aPlanPath(versionID, "tenant_id="+uuid.NewString()))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "https://api.workflow.platform/errors/bad-request", prob["type"])
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "invalid version_id: not a valid UUID", prob["detail"])
			assert.Zero(t, lookup.calls, "the plan is not looked up")
		})
	}
}

func TestInternalCompiledPlan_TenantIDQueryAbsentOrEmpty_400BadRequestAndNoLookup(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		query string
	}{
		{name: "no query string", query: ""},
		{name: "another parameter only", query: "other=1"},
		{name: "empty value", query: "tenant_id="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lookup := &w6aPlanLookup{}

			w := w6aGetPlan(lookup, w6aPlanPath(uuid.NewString(), tc.query))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "https://api.workflow.platform/errors/bad-request", prob["type"])
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "tenant_id must be a UUID", prob["detail"])
			assert.Zero(t, lookup.calls, "the plan is not looked up")
		})
	}
}

func TestInternalCompiledPlan_TenantIDQueryNotAUUID_400BadRequestAndNoLookup(t *testing.T) {
	t.Parallel()

	for _, tenantID := range []string{"abc", "1234", uuid.NewString() + "0"} {
		t.Run(tenantID, func(t *testing.T) {
			t.Parallel()
			lookup := &w6aPlanLookup{}

			w := w6aGetPlan(lookup, w6aPlanPath(uuid.NewString(), "tenant_id="+tenantID))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "https://api.workflow.platform/errors/bad-request", prob["type"])
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "tenant_id must be a UUID", prob["detail"])
			assert.Zero(t, lookup.calls, "the plan is not looked up")
		})
	}
}
