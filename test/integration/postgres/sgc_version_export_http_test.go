//go:build integration

package postgres_test

import (
	"context"
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

const sgCExportXML = `<definitions id="sgc-archived-export"/>`

func sgCExportRouter(pool *pgcommon.Pool) *gin.Engine {
	svc := service.NewVersionService(service.VersionDeps{
		Versions:  postgres.NewWorkflowVersionRepo(pool),
		Workflows: postgres.NewWorkflowRepo(pool),
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(r.Group("/api/v1"), handler.New(handler.Services{Versions: svc}))
	return r
}

func sgCExportGet(router *gin.Engine, tenantID, userID, workflowID, versionID uuid.UUID) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet,
		"/api/v1/workflows/"+workflowID.String()+"/versions/"+versionID.String()+"/export", nil)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", userID.String())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func TestVersionExport_ArchivedVersionOfAnotherTenant_IsNotFoundAndOwnerGetsTheXML(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	ownerTenant, ownerUser := uuid.New(), uuid.New()
	otherTenant, otherUser := uuid.New(), uuid.New()

	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: ownerTenant, CreatedByUserID: ownerUser,
		TemplateKey: "tender-review", Name: "Tender review",
	}
	require.NoError(t, postgres.NewWorkflowRepo(superPool).Create(ctx, wf))
	four := int32(4)
	archived := &domain.WorkflowVersion{
		ID: uuid.New(), WorkflowID: wf.ID, TenantID: ownerTenant, CreatedByUserID: ownerUser,
		Status: domain.VersionStatusArchived, VersionNumber: &four, BPMNXML: sgCExportXML, IsValid: true,
	}
	require.NoError(t, postgres.NewWorkflowVersionRepo(superPool).Create(ctx, archived))

	ownerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &ownerTenant, &ownerUser)
	otherPool := fixtures.NewRestrictedRolePool(t, dsn, false, &otherTenant, &otherUser)

	t.Run("owner exports an archived version", func(t *testing.T) {
		w := sgCExportGet(sgCExportRouter(ownerPool), ownerTenant, ownerUser, wf.ID, archived.ID)

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, sgCExportXML, w.Body.String())
		assert.Equal(t, "attachment; filename=tender-review-v4.bpmn", w.Header().Get("Content-Disposition"))
	})

	t.Run("another tenant on its own pool gets not found", func(t *testing.T) {
		w := sgCExportGet(sgCExportRouter(otherPool), otherTenant, otherUser, wf.ID, archived.ID)

		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
		assert.Contains(t, w.Header().Get("Content-Type"), "application/problem+json")
		assert.Contains(t, w.Body.String(), `"code":"NOT_FOUND"`)
		assert.Empty(t, w.Header().Get("Content-Disposition"))
		assert.NotContains(t, w.Body.String(), "sgc-archived-export")
	})

	t.Run("owner's id on the other tenant's pool gets not found", func(t *testing.T) {
		w := sgCExportGet(sgCExportRouter(otherPool), ownerTenant, ownerUser, wf.ID, archived.ID)

		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), `"code":"NOT_FOUND"`)
		assert.NotContains(t, w.Body.String(), "sgc-archived-export")
	})
}
