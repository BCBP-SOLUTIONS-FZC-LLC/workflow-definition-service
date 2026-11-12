//go:build integration

package postgres_test

import (
	"bytes"
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
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type w9dCaller struct {
	tenantID uuid.UUID
	userID   uuid.UUID
	pool     *pgcommon.Pool
}

func w9dNewCaller(t *testing.T, dsn string) w9dCaller {
	t.Helper()
	c := w9dCaller{tenantID: uuid.New(), userID: uuid.New()}
	c.pool = fixtures.NewRestrictedRolePool(t, dsn, false, &c.tenantID, &c.userID)
	return c
}

func w9dNewRouter(t *testing.T, pool *pgcommon.Pool) *gin.Engine {
	t.Helper()
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(nil, nil, nil).AnyTimes()
	compiler.EXPECT().BundleModules(gomock.Any(), gomock.Any()).
		DoAndReturn(func(xml string, _ []domain.ResolvedModule) (string, error) { return xml, nil }).AnyTimes()
	compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	starters := service.NewStarterService(service.StarterDeps{
		Starters:       postgres.NewStarterRepo(pool),
		GlobalStarters: postgres.NewStarterRepo(pool),
		Versions:       postgres.NewWorkflowVersionRepo(pool),
		Compiler:       compiler,
		ModuleVersions: postgres.NewModuleVersionRepo(pool),
	})
	drafts := service.NewDraftService(service.DraftDeps{
		Workflows: postgres.NewWorkflowRepo(pool),
		Versions:  postgres.NewWorkflowVersionRepo(pool),
	})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		router.Use(mw)
	}
	router.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(router.Group("/api/v1"), handler.New(handler.Services{Starters: starters, Drafts: drafts}))
	return router
}

func w9dCall(router *gin.Engine, c w9dCaller, method, path string, body any) *httptest.ResponseRecorder {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", c.tenantID.String())
	r.Header.Set("x-user-id", c.userID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

type w9dProblemBody struct {
	Type     string `json:"type"`
	Status   int    `json:"status"`
	Code     string `json:"code"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func w9dDecodeProblem(t *testing.T, w *httptest.ResponseRecorder) w9dProblemBody {
	t.Helper()
	var p w9dProblemBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p), w.Body.String())
	return p
}

func w9dCount(t *testing.T, pool *pgcommon.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, query, args...).Scan(&n)
	})
	require.NoError(t, err)
	return n
}

func w9dPublishedWorkflow(t *testing.T, superPool *pgcommon.Pool, tenantID uuid.UUID, key string) (*domain.Workflow, *domain.WorkflowVersion) {
	t.Helper()
	wf := seedWorkflow(t, superPool, tenantID, key)
	version := seedDraft(t, superPool, wf, "<w9d-source/>")
	publishVersion(t, superPool, version, 1)
	require.NoError(t, postgres.NewWorkflowRepo(superPool).UpdateActiveVersion(context.Background(), tenantID, wf.ID, &version.ID))
	return wf, version
}

func TestStarterCreate_HTTP_TenantRowIsOwnedByTheCallerAndInvisibleToAnotherTenant(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	owner, other := w9dNewCaller(t, dsn), w9dNewCaller(t, dsn)

	created := w9dCall(w9dNewRouter(t, owner.pool), owner, http.MethodPost, "/api/v1/starters",
		map[string]any{"name": "Owned starter", "bpmn_xml": "<w9d-main/>"})

	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &body))
	assert.Equal(t, "tenant", body["scope"])
	assert.Equal(t, owner.tenantID.String(), body["tenant_id"])
	rawID, ok := body["id"].(string)
	require.True(t, ok, "id is a string: %v", body["id"])
	id, err := uuid.Parse(rawID)
	require.NoError(t, err)

	row := w8dReadStarterRow(t, superPool, id)
	assert.Equal(t, "tenant", row.scope)
	require.NotNil(t, row.tenantID)
	assert.Equal(t, owner.tenantID, *row.tenantID, "the stored row belongs to the caller tenant")
	require.NotNil(t, row.createdBy)
	assert.Equal(t, owner.userID, *row.createdBy)
	assert.Equal(t, "<w9d-main/>", row.bpmnXML)

	assert.Equal(t, 1, countVisibleStarters(t, owner.pool, id), "the owner sees the row")
	assert.Equal(t, 0, countVisibleStarters(t, other.pool, id), "a pool bound to another tenant does not see the row")

	otherRouter := w9dNewRouter(t, other.pool)
	read := w9dCall(otherRouter, other, http.MethodGet, "/api/v1/starters/"+id.String(), nil)
	require.Equal(t, http.StatusNotFound, read.Code, read.Body.String())
	assert.Equal(t, "NOT_FOUND", w9dDecodeProblem(t, read).Code)

	_, err = postgres.NewStarterRepo(other.pool).GetByID(context.Background(), owner.tenantID, id)
	assert.ErrorIs(t, err, domain.ErrNotFound, "naming the owner tenant on a pool bound to another tenant still finds nothing")

	for _, asTenant := range []uuid.UUID{owner.tenantID, other.tenantID} {
		listed, _, err := postgres.NewStarterRepo(other.pool).List(context.Background(), asTenant, port.StarterFilter{Page: 1, Limit: 50})
		require.NoError(t, err)
		for _, st := range listed {
			assert.NotEqual(t, id, st.ID, "the list of another tenant's pool never holds the row")
		}
	}
}

func TestStarterFromWorkflowVersion_HTTP_VersionOfAnotherTenantAnswersLikeAnUnknownIdAndStoresNothing(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	owner, caller := w9dNewCaller(t, dsn), w9dNewCaller(t, dsn)
	_, foreign := w9dPublishedWorkflow(t, superPool, owner.tenantID, "w9d-foreign-"+uuid.NewString())
	router := w9dNewRouter(t, caller.pool)
	unknownID := uuid.New()
	foreignPath := "/api/v1/starters/from-workflow-version/" + foreign.ID.String()
	unknownPath := "/api/v1/starters/from-workflow-version/" + unknownID.String()

	foreignRes := w9dCall(router, caller, http.MethodPost, foreignPath, map[string]any{"name": "Taken over"})
	unknownRes := w9dCall(router, caller, http.MethodPost, unknownPath, map[string]any{"name": "Nothing here"})

	require.Equal(t, http.StatusNotFound, foreignRes.Code, foreignRes.Body.String())
	require.Equal(t, http.StatusNotFound, unknownRes.Code, unknownRes.Body.String())
	foreignProblem, unknownProblem := w9dDecodeProblem(t, foreignRes), w9dDecodeProblem(t, unknownRes)
	assert.Equal(t, "https://api.workflow.platform/errors/not-found", foreignProblem.Type)
	assert.Equal(t, "NOT_FOUND", foreignProblem.Code)
	assert.Equal(t, "resource not found", foreignProblem.Detail)
	assert.Equal(t, foreignPath, foreignProblem.Instance)
	assert.Equal(t, unknownProblem.Type, foreignProblem.Type)
	assert.Equal(t, unknownProblem.Status, foreignProblem.Status)
	assert.Equal(t, unknownProblem.Code, foreignProblem.Code)
	assert.Equal(t, unknownProblem.Detail, foreignProblem.Detail)
	assert.Equal(t, unknownPath, unknownProblem.Instance)

	assert.Zero(t, w9dCount(t, superPool,
		`SELECT count(*) FROM workflow_template WHERE source_workflow_version_id = $1 OR tenant_id = $2`,
		foreign.ID, caller.tenantID), "no starter was created from the foreign version or for the caller")

	versions := postgres.NewWorkflowVersionRepo(caller.pool)
	_, err := versions.GetByID(context.Background(), owner.tenantID, foreign.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound, "naming the owner tenant on a pool bound to the caller still finds nothing")
	_, err = versions.GetByID(context.Background(), caller.tenantID, foreign.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	_, err = versions.GetByID(context.Background(), caller.tenantID, unknownID)
	assert.ErrorIs(t, err, domain.ErrNotFound, "an unknown id is not found by the repository either")

	ownerRes := w9dCall(w9dNewRouter(t, owner.pool), owner, http.MethodPost, foreignPath, map[string]any{"name": "Own copy"})
	require.Equal(t, http.StatusCreated, ownerRes.Code, "the same request succeeds for the owner: "+ownerRes.Body.String())
}

func TestInitDraft_HTTP_WorkflowOfAnotherTenantAnswersNotFoundAndCreatesNoDraft(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	owner, caller := w9dNewCaller(t, dsn), w9dNewCaller(t, dsn)
	wf, _ := w9dPublishedWorkflow(t, superPool, owner.tenantID, "w9d-draft-"+uuid.NewString())
	path := "/api/v1/workflows/" + wf.ID.String() + "/draft"

	res := w9dCall(w9dNewRouter(t, caller.pool), caller, http.MethodPost, path, nil)

	require.Equal(t, http.StatusNotFound, res.Code, res.Body.String())
	p := w9dDecodeProblem(t, res)
	assert.Equal(t, "https://api.workflow.platform/errors/not-found", p.Type)
	assert.Equal(t, http.StatusNotFound, p.Status)
	assert.Equal(t, "NOT_FOUND", p.Code)
	assert.Equal(t, "resource not found", p.Detail)
	assert.Equal(t, path, p.Instance)
	assert.Zero(t, w9dCount(t, superPool,
		`SELECT count(*) FROM workflow_version WHERE workflow_id = $1 AND status::text = 'DRAFT'`, wf.ID),
		"the refused call left no draft behind")

	_, err := postgres.NewWorkflowRepo(caller.pool).GetByID(context.Background(), owner.tenantID, wf.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound, "naming the owner tenant on a pool bound to the caller still finds nothing")

	ownerRes := w9dCall(w9dNewRouter(t, owner.pool), owner, http.MethodPost, path, nil)
	require.Equal(t, http.StatusCreated, ownerRes.Code, "the same request succeeds for the owner: "+ownerRes.Body.String())
	assert.Equal(t, 1, w9dCount(t, superPool,
		`SELECT count(*) FROM workflow_version WHERE workflow_id = $1 AND status::text = 'DRAFT'`, wf.ID))
}
