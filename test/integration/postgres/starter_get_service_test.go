//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func sgDGetStarter(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID, idSegment string) (int, map[string]any) {
	t.Helper()
	repo := postgres.NewStarterRepo(pool)
	svc := service.NewStarterService(service.StarterDeps{Starters: repo, GlobalStarters: repo})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(r.Group("/api/v1"), handler.New(handler.Services{Starters: svc}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/starters/"+idSegment, nil)
	request.Header.Set("x-tenant-id", tenantID.String())
	request.Header.Set("x-user-id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, request)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
	return w.Code, body
}

func TestGetStarter_StoredTenantStarter_IsServedWithEveryFieldAndTheStoredScope(t *testing.T) {
	pool := newTestPool(t)
	tenantID, userID := uuid.New(), uuid.New()
	st := &domain.StarterTemplate{
		ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant, Name: "Onboarding",
		Description: "Hires", Category: "hr", BPMNXML: "<bpmn/>", CreatedByUserID: &userID,
	}
	require.NoError(t, postgres.NewStarterRepo(pool).Create(context.Background(), st))

	code, body := sgDGetStarter(t, pool, tenantID, st.ID.String())

	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, st.ID.String(), body["id"])
	assert.Equal(t, tenantID.String(), body["tenant_id"])
	assert.Equal(t, "tenant", body["scope"])
	assert.Equal(t, "Onboarding", body["name"])
	assert.Equal(t, "Hires", body["description"])
	assert.Equal(t, "hr", body["category"])
	assert.Equal(t, "<bpmn/>", body["bpmn_xml"])
	assert.NotEmpty(t, body["created_at"])
	assert.NotEmpty(t, body["updated_at"])
	assert.NotContains(t, body, "source_workflow_version_id")
}

func TestGetStarter_NoRowForAWellFormedId_Is404NotFoundProblem(t *testing.T) {
	pool := newTestPool(t)
	tenantID := uuid.New()
	cases := []struct {
		name string
		id   string
	}{
		{name: "random uuid", id: uuid.NewString()},
		{name: "nil uuid parses and finds nothing", id: uuid.Nil.String()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := sgDGetStarter(t, pool, tenantID, tc.id)

			require.Equal(t, http.StatusNotFound, code, body)
			assert.Equal(t, "https://api.workflow.platform/errors/not-found", body["type"])
			assert.Equal(t, "NOT_FOUND", body["code"])
			assert.Equal(t, "Resource Not Found", body["title"])
			assert.Contains(t, body["detail"], "resource not found")
			assert.Equal(t, "/api/v1/starters/"+tc.id, body["instance"])
			assert.Equal(t, float64(http.StatusNotFound), body["status"])
		})
	}
}

func TestGetStarter_GlobalStarter_IsServedToAnyTenantWithoutATenantIDKey(t *testing.T) {
	pool := newTestPool(t)
	st := &domain.StarterTemplate{
		ID: uuid.New(), Scope: domain.ScopeGlobal, Name: "Global onboarding", BPMNXML: "<bpmn/>",
	}
	require.NoError(t, postgres.NewStarterRepo(pool).Create(context.Background(), st))

	for _, tenant := range []uuid.UUID{uuid.New(), uuid.New()} {
		code, body := sgDGetStarter(t, pool, tenant, st.ID.String())

		require.Equal(t, http.StatusOK, code, body)
		assert.Equal(t, st.ID.String(), body["id"])
		assert.Equal(t, "global", body["scope"])
		assert.Equal(t, "Global onboarding", body["name"])
		assert.NotContains(t, body, "tenant_id")
	}
}
