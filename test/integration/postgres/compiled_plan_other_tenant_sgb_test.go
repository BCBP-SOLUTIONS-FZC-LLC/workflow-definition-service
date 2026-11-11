//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

const sgbStoredPlanJSON = `{"name":"owned-by-tenant-a"}`

func sgbSeedPublishedVersion(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID) (workflowID, versionID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
		TemplateKey: "sgb-" + uuid.NewString(), Name: "Compiled plan owner",
	}
	require.NoError(t, postgres.NewWorkflowRepo(pool).Create(ctx, wf))
	number := int32(1)
	plan := sgbStoredPlanJSON
	v := newDraftVersion(wf.ID, tenantID)
	v.Status = domain.VersionStatusPublished
	v.VersionNumber = &number
	v.CompiledPlanJSON = &plan
	require.NoError(t, postgres.NewWorkflowVersionRepo(pool).Create(ctx, v))
	return wf.ID, v.ID
}

func sgbInternalCompiledPlanRouter(pool *pgcommon.Pool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: postgres.NewWorkflowVersionRepo(pool)})
	r := gin.New()
	httpadapter.RegisterInternalRoutes(r.Group("/api/v1/internal"), handler.New(handler.Services{CompiledPlans: svc}))
	return r
}

func sgbGetCompiledPlan(r *gin.Engine, versionID, tenantID uuid.UUID) *httptest.ResponseRecorder {
	target := "/api/v1/internal/workflow-versions/" + versionID.String() + "/compiled-plan?tenant_id=" + tenantID.String()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func TestGetCompiledPlan_VersionOwnedByAnotherTenant_Returns404AndTheOwnerStillReadsIt(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	owner, caller := uuid.New(), uuid.New()
	workflowID, versionID := sgbSeedPublishedVersion(t, superPool, owner)
	router := sgbInternalCompiledPlanRouter(fixtures.NewContextGUCRolePool(t, dsn))

	cross := sgbGetCompiledPlan(router, versionID, caller)

	require.Equal(t, http.StatusNotFound, cross.Code, cross.Body.String())
	var prob struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(cross.Body.Bytes(), &prob))
	assert.Equal(t, "NOT_FOUND", prob.Code)
	assert.NotContains(t, cross.Body.String(), sgbStoredPlanJSON, "the other tenant's plan must not leak")
	assert.NotContains(t, cross.Body.String(), workflowID.String(), "the other tenant's workflow id must not leak")

	own := sgbGetCompiledPlan(router, versionID, owner)
	require.Equal(t, http.StatusOK, own.Code, own.Body.String())
	var plan struct {
		WorkflowID   uuid.UUID       `json:"workflow_id"`
		VersionID    uuid.UUID       `json:"version_id"`
		CompiledPlan json.RawMessage `json:"compiled_plan"`
	}
	require.NoError(t, json.Unmarshal(own.Body.Bytes(), &plan))
	assert.Equal(t, workflowID, plan.WorkflowID)
	assert.Equal(t, versionID, plan.VersionID)
	assert.JSONEq(t, sgbStoredPlanJSON, string(plan.CompiledPlan))
}

func TestWorkflowVersionRepo_GetByID_OtherTenantsVersionOnAPoolBoundToTheCaller_ReturnsNotFound(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	owner, caller := uuid.New(), uuid.New()
	_, versionID := sgbSeedPublishedVersion(t, superPool, owner)
	user := uuid.New()
	callerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &user)
	callerRepo := postgres.NewWorkflowVersionRepo(callerPool)

	for name, queryTenant := range map[string]uuid.UUID{
		"query names the caller":                      caller,
		"query names the owner, security still hides": owner,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := callerRepo.GetByID(context.Background(), queryTenant, versionID)

			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("GetByID = %v, %v; want ErrNotFound", got, err)
			}
			if got != nil {
				t.Fatalf("GetByID returned the other tenant's version %s", got.ID)
			}
		})
	}

	ownerGot, err := postgres.NewWorkflowVersionRepo(superPool).GetByID(context.Background(), owner, versionID)
	require.NoError(t, err)
	assert.Equal(t, versionID, ownerGot.ID)
}
