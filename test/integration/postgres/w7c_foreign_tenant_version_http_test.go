//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type w7cOwnerSeed struct {
	tenantID uuid.UUID
	userID   uuid.UUID
	workflow *domain.Workflow
	active   *domain.WorkflowVersion
	other    *domain.WorkflowVersion
}

func w7cPublishedWithDepartments(t *testing.T, workflowID, tenantID uuid.UUID, number int32, departments ...string) *domain.WorkflowVersion {
	t.Helper()
	depts := make([]dsl.DepartmentDef, 0, len(departments))
	for _, id := range departments {
		depts = append(depts, dsl.DepartmentDef{ID: id, Label: id})
	}
	raw, err := json.Marshal(dsl.CompiledCollaboration{
		MainPlan: "main", SchemaVersion: dsl.CurrentSchemaVersion,
		Plans: []*dsl.CompiledPlan{{Name: "main", Departments: depts}},
	})
	require.NoError(t, err)
	plan := string(raw)
	v := newDraftVersion(workflowID, tenantID)
	v.Status = domain.VersionStatusPublished
	v.VersionNumber = &number
	v.CompiledPlanJSON = &plan
	return v
}

func w7cSeedOwner(t *testing.T, pool *pgcommon.Pool) w7cOwnerSeed {
	t.Helper()
	ctx := context.Background()
	s := w7cOwnerSeed{tenantID: uuid.New(), userID: uuid.New()}
	s.workflow = &domain.Workflow{
		ID: uuid.New(), TenantID: s.tenantID, CreatedByUserID: s.userID,
		TemplateKey: "w7c-" + uuid.NewString(), Name: "Foreign tenant reads",
	}
	require.NoError(t, postgres.NewWorkflowRepo(pool).Create(ctx, s.workflow))
	versions := postgres.NewWorkflowVersionRepo(pool)
	s.active = w7cPublishedWithDepartments(t, s.workflow.ID, s.tenantID, 1, "finance")
	s.other = w7cPublishedWithDepartments(t, s.workflow.ID, s.tenantID, 2, "finance", "operations")
	require.NoError(t, versions.Create(ctx, s.active))
	require.NoError(t, versions.Create(ctx, s.other))
	require.NoError(t, postgres.NewWorkflowRepo(pool).UpdateActiveVersion(ctx, s.tenantID, s.workflow.ID, &s.active.ID))
	return s
}

func w7cRouter(pool *pgcommon.Pool) *gin.Engine {
	modules := postgres.NewModuleVersionRepo(pool)
	versionSvc := service.NewVersionService(service.VersionDeps{
		Transactor:     postgres.NewTransactor(pool),
		Workflows:      postgres.NewWorkflowRepo(pool),
		Versions:       postgres.NewWorkflowVersionRepo(pool),
		Compiler:       bpmn_compiler.New(),
		ModuleVersions: modules,
	})
	validationSvc := service.NewValidationService(service.ValidationDeps{Compiler: bpmn_compiler.New(), ModuleVersions: modules})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(r.Group("/api/v1"), handler.New(handler.Services{Versions: versionSvc, Validation: validationSvc}))
	return r
}

func w7cCall(router *gin.Engine, method, path string, tenantID, userID uuid.UUID) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", userID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func w7cActiveVersionID(t *testing.T, pool *pgcommon.Pool, s w7cOwnerSeed) *uuid.UUID {
	t.Helper()
	wf, err := postgres.NewWorkflowRepo(pool).GetByID(context.Background(), s.tenantID, s.workflow.ID)
	require.NoError(t, err)
	return wf.ActiveVersionID
}

func TestVersionEndpoints_AVersionOfAnotherTenant_IsNotFoundForPromoteValidationAndDiff(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)

	endpoints := []struct {
		name   string
		method string
		path   func(s w7cOwnerSeed) string
	}{
		{
			name:   "promote",
			method: http.MethodPost,
			path: func(s w7cOwnerSeed) string {
				return "/api/v1/workflows/" + s.workflow.ID.String() + "/versions/" + s.other.ID.String() + "/promote"
			},
		},
		{
			name:   "validation",
			method: http.MethodGet,
			path: func(s w7cOwnerSeed) string {
				return "/api/v1/workflows/" + s.workflow.ID.String() + "/versions/" + s.other.ID.String() + "/validation"
			},
		},
		{
			name:   "diff",
			method: http.MethodGet,
			path: func(s w7cOwnerSeed) string {
				return "/api/v1/workflows/" + s.workflow.ID.String() + "/versions/" + s.active.ID.String() + "/diff/" + s.other.ID.String()
			},
		},
	}
	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			owner := w7cSeedOwner(t, superPool)
			attacker, attackerUser := uuid.New(), uuid.New()
			attackerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &attacker, &attackerUser)
			ownerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &owner.tenantID, &owner.userID)
			path := ep.path(owner)

			t.Run("the other tenant naming its own tenant gets not found", func(t *testing.T) {
				w := w7cCall(w7cRouter(attackerPool), ep.method, path, attacker, attackerUser)

				require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
				var prob map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &prob), w.Body.String())
				assert.Equal(t, "NOT_FOUND", prob["code"])
				assert.Contains(t, prob["detail"], "resource not found")
				assert.Equal(t, path, prob["instance"])
				assert.NotContains(t, w.Body.String(), "is_valid")
				assert.NotContains(t, w.Body.String(), "change_type")
				assert.Equal(t, &owner.active.ID, w7cActiveVersionID(t, superPool, owner), "the owner's active version is untouched")
			})

			t.Run("the other tenant naming the owner's tenant is stopped by row-level security", func(t *testing.T) {
				w := w7cCall(w7cRouter(attackerPool), ep.method, path, owner.tenantID, attackerUser)

				require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
				var prob map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &prob), w.Body.String())
				assert.Equal(t, "NOT_FOUND", prob["code"])
				assert.NotContains(t, w.Body.String(), "is_valid")
				assert.NotContains(t, w.Body.String(), "change_type")
				assert.Equal(t, &owner.active.ID, w7cActiveVersionID(t, superPool, owner), "the owner's active version is untouched")
			})

			t.Run("the owner still reaches its own version", func(t *testing.T) {
				w := w7cCall(w7cRouter(ownerPool), ep.method, path, owner.tenantID, owner.userID)

				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				switch ep.name {
				case "promote":
					assert.Equal(t, &owner.other.ID, w7cActiveVersionID(t, superPool, owner))
				case "validation":
					assert.Contains(t, w.Body.String(), owner.other.ID.String())
				case "diff":
					assert.Contains(t, w.Body.String(), `"added_departments":["operations"]`)
				}
			})
		})
	}
}
