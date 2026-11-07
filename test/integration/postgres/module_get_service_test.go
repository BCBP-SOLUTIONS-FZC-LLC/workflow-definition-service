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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func sgDModuleService(pool *pgcommon.Pool) *service.ModuleService {
	return service.NewModuleService(service.ModuleDeps{
		Modules:  postgres.NewModuleRepo(pool),
		Versions: postgres.NewModuleVersionRepo(pool),
	})
}

func sgDModuleWithActiveVersion(t *testing.T, pool *pgcommon.Pool, tenantID *uuid.UUID) (*domain.Module, *domain.ModuleVersion) {
	t.Helper()
	ctx := context.Background()
	scope := domain.ScopeTenant
	publisher := uuid.New()
	if tenantID == nil {
		scope = domain.ScopeGlobal
	} else {
		publisher = *tenantID
	}
	processID := "Process_" + uuid.NewString()
	m := &domain.Module{ID: uuid.New(), TenantID: tenantID, Scope: scope, Name: "Module " + processID, ProcessID: processID}
	require.NoError(t, postgres.NewModuleRepo(pool).Create(ctx, m))
	v := &domain.ModuleVersion{
		ID: uuid.New(), ModuleID: m.ID, TenantID: tenantID, Scope: scope,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>", ProcessID: processID, IsValid: true,
	}
	require.NoError(t, postgres.NewModuleVersionRepo(pool).Create(ctx, v))
	require.NoError(t, postgres.NewModuleVersionRepo(pool).Publish(ctx, publisher, v.ID, 1))
	require.NoError(t, postgres.NewModuleRepo(pool).UpdateActiveVersion(ctx, m.ID, &v.ID))
	m.ActiveVersionID = &v.ID
	return m, v
}

func sgDGetModule(t *testing.T, svc *service.ModuleService, tenantID uuid.UUID, moduleID uuid.UUID) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(r.Group("/api/v1"), handler.New(handler.Services{Modules: svc}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/modules/"+moduleID.String(), nil)
	request.Header.Set("x-tenant-id", tenantID.String())
	request.Header.Set("x-user-id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, request)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
	return w.Code, body
}

func TestGetModule_GlobalModule_IsServedWithItsActiveVersionAndNoTenantID(t *testing.T) {
	pool := newTestPool(t)
	m, v := sgDModuleWithActiveVersion(t, pool, nil)
	svc := sgDModuleService(pool)

	for _, tenant := range []uuid.UUID{uuid.New(), uuid.New()} {
		code, body := sgDGetModule(t, svc, tenant, m.ID)

		require.Equal(t, http.StatusOK, code, body)
		assert.Equal(t, m.ID.String(), body["id"])
		assert.Equal(t, "global", body["scope"])
		assert.NotContains(t, body, "tenant_id")
		assert.Equal(t, v.ID.String(), body["active_version_id"])
		active, ok := body["active_version"].(map[string]any)
		require.True(t, ok, "active_version missing: %v", body)
		assert.Equal(t, v.ID.String(), active["id"])
		assert.Equal(t, "PUBLISHED", active["status"])
		assert.Equal(t, "global", active["scope"])
		assert.NotContains(t, active, "tenant_id")
	}
}

func TestGetModule_TenantModuleOfAnotherTenant_Is404NotFoundLikeAnUnknownId(t *testing.T) {
	pool, dsn := fixtures.NewTestPoolAndDSN(t)
	owner, caller := uuid.New(), uuid.New()
	m, _ := sgDModuleWithActiveVersion(t, pool, &owner)
	svc := sgDModuleService(pool)

	ownerCode, ownerBody := sgDGetModule(t, svc, owner, m.ID)
	require.Equal(t, http.StatusOK, ownerCode, ownerBody)
	assert.Equal(t, owner.String(), ownerBody["tenant_id"])

	cases := []struct {
		name string
		id   uuid.UUID
	}{
		{name: "another tenant's module", id: m.ID},
		{name: "unknown id", id: uuid.New()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := sgDGetModule(t, svc, caller, tc.id)

			require.Equal(t, http.StatusNotFound, code, body)
			assert.Equal(t, "NOT_FOUND", body["code"])
			assert.Equal(t, "https://api.workflow.platform/errors/not-found", body["type"])
			assert.NotContains(t, body, "active_version")
			assert.NotContains(t, body, "name")
		})
	}

	user := uuid.New()
	callerSvc := sgDModuleService(fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &user))
	gotModule, gotVersion, err := callerSvc.Get(context.Background(), owner, m.ID)
	require.ErrorIs(t, err, domain.ErrNotFound, "row-level security must hide the row even when the query names the owner")
	assert.Nil(t, gotModule)
	assert.Nil(t, gotVersion)
}

func TestGetModule_ClosedDatabasePool_Returns503UpstreamUnavailable(t *testing.T) {
	pool := newTestPool(t)
	svc := sgDModuleService(pool)
	pool.Close()

	code, body := sgDGetModule(t, svc, uuid.New(), uuid.New())

	require.Equal(t, http.StatusServiceUnavailable, code, body)
	assert.Equal(t, "UPSTREAM_UNAVAILABLE", body["code"])
	assert.Equal(t, "an upstream dependency is temporarily unavailable", body["detail"])
}
