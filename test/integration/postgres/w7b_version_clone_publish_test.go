//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type w7bUnlimitedPlans struct{}

func (w7bUnlimitedPlans) WorkflowTemplateLimit(context.Context, string) (*int64, error) {
	return nil, nil
}

type w7bRefusingVersionCreate struct {
	port.WorkflowVersionRepository
}

func (w7bRefusingVersionCreate) Create(context.Context, *domain.WorkflowVersion) error {
	return errors.New("w7b version insert refused")
}

type w7bResponse struct {
	Code          string `json:"code"`
	Type          string `json:"type"`
	VersionNumber *int32 `json:"version_number"`
	WorkflowID    string `json:"workflow_id"`
	VersionID     string `json:"version_id"`
}

func w7bRouter(pool *pgcommon.Pool, versions port.WorkflowVersionRepository) *gin.Engine {
	if versions == nil {
		versions = postgres.NewWorkflowVersionRepo(pool)
	}
	svc := service.NewVersionService(service.VersionDeps{
		PlanLimits:     w7bUnlimitedPlans{},
		Transactor:     postgres.NewTransactor(pool),
		Workflows:      postgres.NewWorkflowRepo(pool),
		Versions:       versions,
		Assignees:      postgres.NewAssigneeRepo(pool),
		Compiler:       bpmn_compiler.New(),
		ModuleVersions: postgres.NewModuleVersionRepo(pool),
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

func w7bPost(router *gin.Engine, tenantID, userID, workflowID, versionID uuid.UUID, action, body string) (*httptest.ResponseRecorder, w7bResponse) {
	r := httptest.NewRequest(http.MethodPost,
		"/api/v1/workflows/"+workflowID.String()+"/versions/"+versionID.String()+"/"+action, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", userID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	r.Header.Set("x-plan", "w7b-plan")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	var parsed w7bResponse
	_ = json.Unmarshal(w.Body.Bytes(), &parsed)
	return w, parsed
}

func w7bSeedPublished(t *testing.T, pool *pgcommon.Pool, tenantID, userID uuid.UUID, key string) (*domain.Workflow, *domain.WorkflowVersion) {
	t.Helper()
	ctx := context.Background()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID, CreatedByUserID: userID, TemplateKey: key, Name: "W7b " + key,
	}
	require.NoError(t, postgres.NewWorkflowRepo(pool).Create(ctx, wf))
	one := int32(1)
	v := &domain.WorkflowVersion{
		ID: uuid.New(), WorkflowID: wf.ID, TenantID: tenantID, CreatedByUserID: userID,
		Status: domain.VersionStatusPublished, VersionNumber: &one, BPMNXML: `<definitions id="w7b-source"/>`, IsValid: true,
	}
	require.NoError(t, postgres.NewWorkflowVersionRepo(pool).Create(ctx, v))
	return wf, v
}

func w7bWorkflowCount(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID) int64 {
	t.Helper()
	_, total, err := postgres.NewWorkflowRepo(pool).List(context.Background(), tenantID, port.WorkflowFilter{Page: 1, Limit: 100})
	require.NoError(t, err)
	return total
}

func TestCloneVersionHTTP_FailuresAgainstARealDatabase_AreMappedAndLeaveNoRows(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()

	t.Run("a new key equal to an existing key of the tenant is a 409 and writes nothing", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		_, source := w7bSeedPublished(t, superPool, tenantID, userID, "w7b-dup-source")
		taken, _ := w7bSeedPublished(t, superPool, tenantID, userID, "w7b-dup-taken")
		router := w7bRouter(superPool, nil)

		w, resp := w7bPost(router, tenantID, userID, source.WorkflowID, source.ID, "clone", `{"new_key":"w7b-dup-taken","new_name":"Copy"}`)

		require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
		assert.Equal(t, "DUPLICATE_TEMPLATE_KEY", resp.Code)
		assert.Equal(t, "https://api.workflow.platform/errors/conflict", resp.Type)
		assert.EqualValues(t, 2, w7bWorkflowCount(t, superPool, tenantID), "no workflow row was added")
		stillTaken, err := postgres.NewWorkflowRepo(superPool).GetByTemplateKey(ctx, tenantID, "w7b-dup-taken")
		require.NoError(t, err)
		assert.Equal(t, taken.ID, stillTaken.ID, "the existing workflow keeps its key")

		w, resp = w7bPost(router, tenantID, userID, source.WorkflowID, source.ID, "clone", `{"new_key":"w7b-dup-free","new_name":"Copy"}`)

		require.Equal(t, http.StatusCreated, w.Code, "the same clone with an unused key succeeds: %s", w.Body.String())
		assert.EqualValues(t, 3, w7bWorkflowCount(t, superPool, tenantID))
		clone, err := postgres.NewWorkflowRepo(superPool).GetByTemplateKey(ctx, tenantID, "w7b-dup-free")
		require.NoError(t, err)
		assert.Equal(t, clone.ID.String(), resp.WorkflowID)
	})

	t.Run("a failing version insert is a 500 and the workflow row written first is rolled back", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		_, source := w7bSeedPublished(t, superPool, tenantID, userID, "w7b-ob-source")
		router := w7bRouter(superPool, w7bRefusingVersionCreate{postgres.NewWorkflowVersionRepo(superPool)})

		w, resp := w7bPost(router, tenantID, userID, source.WorkflowID, source.ID, "clone", `{"new_key":"w7b-ob-copy","new_name":"Copy"}`)

		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		assert.Equal(t, "INTERNAL_ERROR", resp.Code)
		assert.NotContains(t, w.Body.String(), "w7b version insert refused")
		assert.EqualValues(t, 1, w7bWorkflowCount(t, superPool, tenantID), "only the source workflow remains")
		_, err := postgres.NewWorkflowRepo(superPool).GetByTemplateKey(ctx, tenantID, "w7b-ob-copy")
		assert.ErrorIs(t, err, domain.ErrNotFound, "the workflow row of the failed clone was rolled back")
	})

	t.Run("a workflow insert refused for a reason other than a duplicate key is neither a duplicate nor an outage and writes nothing", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		_, source := w7bSeedPublished(t, superPool, tenantID, userID, "w7b-eh-source")
		svc := service.NewVersionService(service.VersionDeps{
			PlanLimits: w7bUnlimitedPlans{},
			Transactor: postgres.NewTransactor(superPool),
			Workflows:  postgres.NewWorkflowRepo(superPool),
			Versions:   postgres.NewWorkflowVersionRepo(superPool),
		})

		gotWF, gotVersion, err := svc.Clone(ctx, tenantID, userID, source.WorkflowID, source.ID, "w7b-plan",
			service.CloneReq{NewKey: strings.Repeat("k", 256), NewName: "Copy"})

		require.Error(t, err)
		assert.Nil(t, gotWF)
		assert.Nil(t, gotVersion)
		assert.NotErrorIs(t, err, domain.ErrDuplicateTemplateKey)
		assert.NotErrorIs(t, err, domain.ErrUpstreamUnavailable)
		assert.NotErrorIs(t, err, domain.ErrNotFound)
		assert.EqualValues(t, 1, w7bWorkflowCount(t, superPool, tenantID), "only the source workflow remains")
	})

	t.Run("a version id that does not exist is a 404", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		wf, _ := w7bSeedPublished(t, superPool, tenantID, userID, "w7b-nf-source")

		w, resp := w7bPost(w7bRouter(superPool, nil), tenantID, userID, wf.ID, uuid.New(), "clone", `{"new_key":"w7b-nf-copy","new_name":"Copy"}`)

		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
		assert.Equal(t, "NOT_FOUND", resp.Code)
		assert.Equal(t, "https://api.workflow.platform/errors/not-found", resp.Type)
		assert.EqualValues(t, 1, w7bWorkflowCount(t, superPool, tenantID))
	})

	t.Run("a version of another tenant is a 404 for the caller and the owner can still clone it", func(t *testing.T) {
		ownerTenant, ownerUser := uuid.New(), uuid.New()
		otherTenant, otherUser := uuid.New(), uuid.New()
		_, source := w7bSeedPublished(t, superPool, ownerTenant, ownerUser, "w7b-mt-source")
		ownerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &ownerTenant, &ownerUser)
		otherPool := fixtures.NewRestrictedRolePool(t, dsn, false, &otherTenant, &otherUser)

		w, resp := w7bPost(w7bRouter(otherPool, nil), otherTenant, otherUser, source.WorkflowID, source.ID, "clone", `{"new_key":"w7b-mt-copy","new_name":"Copy"}`)

		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
		assert.Equal(t, "NOT_FOUND", resp.Code)
		assert.EqualValues(t, 1, w7bWorkflowCount(t, superPool, ownerTenant), "the owner's tenant gained no workflow")
		assert.Zero(t, w7bWorkflowCount(t, superPool, otherTenant), "the caller's tenant gained no workflow")

		_, versionErr := postgres.NewWorkflowVersionRepo(otherPool).GetByID(ctx, ownerTenant, source.ID)
		assert.ErrorIs(t, versionErr, domain.ErrNotFound, "row-level security hides the owner's version from the other tenant's pool even with the owner's tenant id")
		_, workflowErr := postgres.NewWorkflowRepo(otherPool).GetByID(ctx, ownerTenant, source.WorkflowID)
		assert.ErrorIs(t, workflowErr, domain.ErrNotFound, "row-level security hides the owner's workflow from the other tenant's pool even with the owner's tenant id")

		ownVersion, err := postgres.NewWorkflowVersionRepo(ownerPool).GetByID(ctx, ownerTenant, source.ID)
		require.NoError(t, err, "positive control: the owner's pool reads the same version")
		assert.Equal(t, source.ID, ownVersion.ID)
		ownWorkflow, err := postgres.NewWorkflowRepo(ownerPool).GetByID(ctx, ownerTenant, source.WorkflowID)
		require.NoError(t, err, "positive control: the owner's pool reads the same workflow")
		assert.Equal(t, source.WorkflowID, ownWorkflow.ID)

		w, _ = w7bPost(w7bRouter(ownerPool, nil), ownerTenant, ownerUser, source.WorkflowID, source.ID, "clone", `{"new_key":"w7b-mt-copy","new_name":"Copy"}`)

		require.Equal(t, http.StatusCreated, w.Code, "the owner clones the same version: %s", w.Body.String())
		assert.EqualValues(t, 2, w7bWorkflowCount(t, superPool, ownerTenant))
		assert.Zero(t, w7bWorkflowCount(t, superPool, otherTenant))
	})
}

func TestPublishVersionHTTP_SecondPublishOfAWorkflow_GetsTheNextNumber(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	bpmnXML, err := os.ReadFile("../../unit/service/testdata/single-process.bpmn")
	require.NoError(t, err)
	tenantID, userID := uuid.New(), uuid.New()
	wf, first := publishRaceSeedDraft(t, pool, tenantID, "w7b-second-publish", string(bpmnXML))
	router := w7bRouter(pool, nil)
	vRepo := postgres.NewWorkflowVersionRepo(pool)

	w, resp := w7bPost(router, tenantID, userID, wf.ID, first.ID, "publish", "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, resp.VersionNumber)
	assert.EqualValues(t, 1, *resp.VersionNumber)

	second := newDraftVersion(wf.ID, tenantID)
	second.BPMNXML = string(bpmnXML)
	require.NoError(t, vRepo.Create(ctx, second))

	w, resp = w7bPost(router, tenantID, userID, wf.ID, second.ID, "publish", "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, resp.VersionNumber)
	assert.EqualValues(t, 2, *resp.VersionNumber, "the second publish of the workflow is number 2")
	assert.Equal(t, second.ID.String(), resp.VersionID)
	storedSecond, err := vRepo.GetByID(ctx, tenantID, second.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.VersionStatusPublished, storedSecond.Status)
	require.NotNil(t, storedSecond.VersionNumber)
	assert.EqualValues(t, 2, *storedSecond.VersionNumber)
	storedFirst, err := vRepo.GetByID(ctx, tenantID, first.ID)
	require.NoError(t, err)
	require.NotNil(t, storedFirst.VersionNumber)
	assert.EqualValues(t, 1, *storedFirst.VersionNumber, "the first version keeps its number")
	reloaded, err := postgres.NewWorkflowRepo(pool).GetByID(ctx, tenantID, wf.ID)
	require.NoError(t, err)
	require.NotNil(t, reloaded.ActiveVersionID)
	assert.Equal(t, second.ID, *reloaded.ActiveVersionID)
}
