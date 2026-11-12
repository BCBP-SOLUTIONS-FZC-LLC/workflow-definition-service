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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func w8dModuleRouter(tenantPool, systemPool *pgcommon.Pool) *gin.Engine {
	svc := service.NewModuleService(service.ModuleDeps{
		Transactor:       postgres.NewTransactor(tenantPool),
		GlobalTransactor: postgres.NewTransactor(systemPool),
		Modules:          postgres.NewModuleRepo(tenantPool),
		GlobalModules:    postgres.NewModuleRepo(systemPool),
		Versions:         postgres.NewModuleVersionRepo(tenantPool),
		GlobalVersions:   postgres.NewModuleVersionRepo(systemPool),
		WorkflowVersions: postgres.NewWorkflowVersionRepo(tenantPool),
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(r.Group("/api/v1"), handler.New(handler.Services{Modules: svc}))
	return r
}

func w8dArchiveRequest(moduleID, tenantID, userID uuid.UUID) *http.Request {
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/modules/"+moduleID.String(), nil)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", userID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	return r
}

type w8dModuleState struct {
	activeVersionID *uuid.UUID
	versionStatus   string
}

func w8dReadModuleState(t *testing.T, pool *pgcommon.Pool, moduleID, versionID uuid.UUID) w8dModuleState {
	t.Helper()
	var state w8dModuleState
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		if err := conn.QueryRow(ctx, `SELECT active_version_id FROM workflow_module WHERE id = $1`, moduleID).
			Scan(&state.activeVersionID); err != nil {
			return err
		}
		return conn.QueryRow(ctx, `SELECT status FROM workflow_module_version WHERE id = $1`, versionID).
			Scan(&state.versionStatus)
	})
	require.NoError(t, err)
	return state
}

func TestModuleArchive_HTTP_OwnModuleIsArchivedAndAnotherTenantsModuleIsNotFound(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	t.Run("a tenant admin archives the tenant's own module with an active version", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
		module, versions := libraryModule(t, superPool, &tenantID, "Process_Own_"+uuid.NewString(), 1)
		router := w8dModuleRouter(tenantPool, systemPool)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, w8dArchiveRequest(module.ID, tenantID, userID))

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.Equal(t, map[string]any{
			"module_id": module.ID.String(),
			"status":    "ARCHIVED",
			"message":   "Module archived successfully",
		}, body)
		state := w8dReadModuleState(t, superPool, module.ID, versions[0].ID)
		assert.Equal(t, "ARCHIVED", state.versionStatus, "the active version is archived")
		assert.Nil(t, state.activeVersionID, "the module no longer points at a version")
	})

	t.Run("another tenant's module is not found and stays active", func(t *testing.T) {
		callerTenant, ownerTenant, userID := uuid.New(), uuid.New(), uuid.New()
		callerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &callerTenant, &userID)
		module, versions := libraryModule(t, superPool, &ownerTenant, "Process_Foreign_"+uuid.NewString(), 1)
		router := w8dModuleRouter(callerPool, systemPool)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, w8dArchiveRequest(module.ID, callerTenant, userID))

		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
		var problem struct {
			Type     string `json:"type"`
			Status   int    `json:"status"`
			Detail   string `json:"detail"`
			Instance string `json:"instance"`
			Code     string `json:"code"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &problem))
		assert.Equal(t, "https://api.workflow.platform/errors/not-found", problem.Type)
		assert.Equal(t, http.StatusNotFound, problem.Status)
		assert.Equal(t, "NOT_FOUND", problem.Code)
		assert.Contains(t, problem.Detail, "resource not found")
		assert.Equal(t, "/api/v1/modules/"+module.ID.String(), problem.Instance)
		state := w8dReadModuleState(t, superPool, module.ID, versions[0].ID)
		assert.Equal(t, "PUBLISHED", state.versionStatus, "the foreign version is untouched")
		require.NotNil(t, state.activeVersionID, "the foreign module keeps its active version")
		assert.Equal(t, versions[0].ID, *state.activeVersionID)
	})

	t.Run("another tenant naming the owner's tenant is stopped by row-level security", func(t *testing.T) {
		callerTenant, ownerTenant, userID := uuid.New(), uuid.New(), uuid.New()
		callerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &callerTenant, &userID)
		module, versions := libraryModule(t, superPool, &ownerTenant, "Process_OwnerNamed_"+uuid.NewString(), 1)
		router := w8dModuleRouter(callerPool, systemPool)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, w8dArchiveRequest(module.ID, ownerTenant, userID))

		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
		var problem map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &problem), w.Body.String())
		assert.Equal(t, "NOT_FOUND", problem["code"])
		state := w8dReadModuleState(t, superPool, module.ID, versions[0].ID)
		assert.Equal(t, "PUBLISHED", state.versionStatus, "the owner's version is untouched")
		require.NotNil(t, state.activeVersionID, "the owner's module keeps its active version")
		assert.Equal(t, versions[0].ID, *state.activeVersionID)
	})
}
