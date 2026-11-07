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

func sgDNodesService(pool *pgcommon.Pool) *service.VersionService {
	return service.NewVersionService(service.VersionDeps{
		Versions: postgres.NewWorkflowVersionRepo(pool),
		Compiler: bpmn_compiler.New(),
	})
}

func sgDNodesGet(t *testing.T, svc *service.VersionService, tenantID uuid.UUID, path string) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(r.Group("/api/v1"), handler.New(handler.Services{Versions: svc}))

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("x-tenant-id", tenantID.String())
	req.Header.Set("x-user-id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
	return w.Code, body
}

func sgDPublishedWithPlan(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID) (*domain.Workflow, *domain.WorkflowVersion) {
	t.Helper()
	ctx := context.Background()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
		TemplateKey: "sgd-nodes-" + uuid.NewString(), Name: "Nodes",
	}
	require.NoError(t, postgres.NewWorkflowRepo(pool).Create(ctx, wf))

	raw, err := json.Marshal(dsl.CompiledCollaboration{
		MainPlan: "main", SchemaVersion: dsl.CurrentSchemaVersion,
		Plans: []*dsl.CompiledPlan{{
			Name:        "main",
			Departments: []dsl.DepartmentDef{{ID: "Eng", Label: "Engineering", Stages: []dsl.StageDef{{Type: "prep", NodeID: "T1", Role: "L2"}}}},
			Execution:   dsl.ExecutionPlan{Steps: []dsl.ExecutionStep{{Sequential: []string{"Eng"}}}},
		}},
	})
	require.NoError(t, err)
	plan := string(raw)
	number := int32(1)
	v := newDraftVersion(wf.ID, tenantID)
	v.Status = domain.VersionStatusPublished
	v.VersionNumber = &number
	v.CompiledPlanJSON = &plan
	require.NoError(t, postgres.NewWorkflowVersionRepo(pool).Create(ctx, v))
	return wf, v
}

func TestGetVersionNodes_VersionOfAnotherTenantOrNoVersion_Is404NotFound(t *testing.T) {
	pool := newTestPool(t)
	owner, other := uuid.New(), uuid.New()
	wf, ver := sgDPublishedWithPlan(t, pool, owner)
	svc := sgDNodesService(pool)
	path := func(workflowID, versionID uuid.UUID) string {
		return "/api/v1/workflows/" + workflowID.String() + "/versions/" + versionID.String() + "/nodes"
	}

	code, body := sgDNodesGet(t, svc, owner, path(wf.ID, ver.ID))
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, ver.ID.String(), body["version_id"])
	assert.Len(t, body["nodes"], 1)

	cases := []struct {
		name   string
		tenant uuid.UUID
		path   string
	}{
		{name: "the version belongs to another tenant", tenant: other, path: path(wf.ID, ver.ID)},
		{name: "no row has that version id", tenant: owner, path: path(wf.ID, uuid.New())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := sgDNodesGet(t, svc, tc.tenant, tc.path)

			require.Equal(t, http.StatusNotFound, code, body)
			assert.Equal(t, "NOT_FOUND", body["code"])
			assert.Equal(t, "https://api.workflow.platform/errors/not-found", body["type"])
			assert.Equal(t, tc.path, body["instance"])
			assert.NotContains(t, body, "nodes")
		})
	}
}

func TestVersionService_Nodes_RowLevelSecurityHidesAnotherTenantsVersionEvenWhenNamed(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	owner, caller := uuid.New(), uuid.New()
	wf, ver := sgDPublishedWithPlan(t, superPool, owner)
	user := uuid.New()
	callerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &user)

	got, err := sgDNodesService(callerPool).Nodes(context.Background(), owner, wf.ID, ver.ID)

	require.ErrorIs(t, err, domain.ErrNotFound)
	assert.Nil(t, got)
	ownerGot, err := sgDNodesService(superPool).Nodes(context.Background(), owner, wf.ID, ver.ID)
	require.NoError(t, err)
	require.Len(t, ownerGot.Nodes, 1)
	assert.Equal(t, "Eng/T1", ownerGot.Nodes[0].Key)
}
