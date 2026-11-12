//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type w7dCollidingVersionRepo struct {
	*postgres.WorkflowVersionRepo
	workflows       *postgres.WorkflowRepo
	takenVersionID  uuid.UUID
	workflowInTx    bool
	versionAttempts int
}

func (r *w7dCollidingVersionRepo) Create(ctx context.Context, v *domain.WorkflowVersion) error {
	r.versionAttempts++
	if _, err := r.workflows.GetByID(ctx, v.TenantID, v.WorkflowID); err == nil {
		r.workflowInTx = true
	}
	clash := *v
	clash.ID = r.takenVersionID
	return r.WorkflowVersionRepo.Create(ctx, &clash)
}

func w7dDecode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out
}

type w7dUnlimitedPlans struct{}

func (w7dUnlimitedPlans) ReadPlanTable(context.Context) (map[string]*int64, error) {
	return map[string]*int64{"enterprise": nil}, nil
}

func TestCreateWorkflow_VersionInsertFailsAfterTheWorkflowInsert_Returns500AndLeavesNoWorkflow(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	workflows := postgres.NewWorkflowRepo(pool)
	versions := postgres.NewWorkflowVersionRepo(pool)

	otherTenant := uuid.New()
	taken := &domain.Workflow{
		ID: uuid.New(), TenantID: otherTenant, CreatedByUserID: uuid.New(), TemplateKey: "w7d-taken", Name: "Taken",
	}
	require.NoError(t, workflows.Create(ctx, taken))
	takenVersion := newDraftVersion(taken.ID, otherTenant)
	require.NoError(t, versions.Create(ctx, takenVersion))

	colliding := &w7dCollidingVersionRepo{WorkflowVersionRepo: versions, workflows: workflows, takenVersionID: takenVersion.ID}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		router.Use(mw)
	}
	router.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(router.Group("/api/v1"), handler.New(handler.Services{
		Workflows: service.NewWorkflowService(service.WorkflowDeps{
			Transactor: postgres.NewTransactor(pool),
			Workflows:  workflows,
			Versions:   colliding,
			PlanLimits: service.NewPlanLimitService(service.PlanLimitDeps{Reader: w7dUnlimitedPlans{}, TTL: time.Hour}),
		}),
	}))

	tenantID := uuid.New()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workflows",
		strings.NewReader(`{"key":"w7d-rollback","name":"Rollback","bpmn_xml":"<definitions/>"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", uuid.NewString())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	r.Header.Set("x-plan", "enterprise")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, r)

	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	prob := w7dDecode(t, w)
	assert.Equal(t, "INTERNAL_ERROR", prob["code"])
	assert.Equal(t, "an unexpected error occurred", prob["detail"])
	assert.NotContains(t, w.Body.String(), "duplicate key", "the driver message stays out of the response")
	assert.Equal(t, 1, colliding.versionAttempts, "the failed insert is not retried")
	assert.True(t, colliding.workflowInTx, "the workflow row was already written inside the transaction when the version insert failed")

	_, count, err := workflows.List(ctx, tenantID, port.WorkflowFilter{Page: 1, Limit: 100})
	require.NoError(t, err)
	assert.Zero(t, count, "the workflow insert was rolled back with the failed version insert")
	_, err = workflows.GetByTemplateKey(ctx, tenantID, "w7d-rollback")
	assert.ErrorIs(t, err, domain.ErrNotFound)
	_, err = workflows.GetByID(ctx, otherTenant, taken.ID)
	assert.NoError(t, err, "the other tenant's workflow is untouched")
}
