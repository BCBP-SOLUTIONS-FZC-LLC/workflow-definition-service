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
	"github.com/jackc/pgx/v5/pgxpool"
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

const (
	w10bProblemInternal    = "https://api.workflow.platform/errors/internal-error"
	w10bProblemUnavailable = "https://api.workflow.platform/errors/service-unavailable"
	w10bProblemNotFound    = "https://api.workflow.platform/errors/not-found"
	w10bProblemConflict    = "https://api.workflow.platform/errors/conflict"
)

type w10bProblem struct {
	Type     string `json:"type"`
	Status   int    `json:"status"`
	Code     string `json:"code"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func w10bDecodeProblem(t *testing.T, rec *httptest.ResponseRecorder) w10bProblem {
	t.Helper()
	var p w10bProblem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p), rec.Body.String())
	return p
}

func w10bEngine(services handler.Services) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(r.Group("/api/v1"), handler.New(services))
	return r
}

func w10bCall(router *gin.Engine, method, path string, tenantID, userID uuid.UUID, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", userID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	r.Header.Set("x-plan", "w10b-plan")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	return rec
}

func w10bRoleOf(t *testing.T, pool *pgcommon.Pool) string {
	t.Helper()
	var role string
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT current_user`).Scan(&role)
	})
	require.NoError(t, err)
	return role
}

func w10bRestrictedPool(t *testing.T, superPool *pgcommon.Pool, dsn string, tenantID, userID uuid.UUID, adjustments ...string) *pgcommon.Pool {
	t.Helper()
	pool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	role := w10bRoleOf(t, pool)
	for _, statement := range adjustments {
		err := superPool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
			_, err := conn.Exec(ctx, strings.ReplaceAll(statement, "{role}", role))
			return err
		})
		require.NoError(t, err)
	}
	return pool
}

func w10bCutPool(t *testing.T, dsn, cutOnQueryText string) (*pgcommon.Pool, *sgbCuttingProxy) {
	t.Helper()
	proxyDSN, proxy := sgbStartCuttingProxy(t, dsn, cutOnQueryText)
	pool, err := pgcommon.NewPool(context.Background(), pgcommon.Config{DSN: proxyDSN, MaxConns: 1})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool, proxy
}

func w10bPromoteRouter(txPool, pool *pgcommon.Pool) *gin.Engine {
	return w10bEngine(handler.Services{Versions: service.NewVersionService(service.VersionDeps{
		PlanLimits:     w7bUnlimitedPlans{},
		Transactor:     postgres.NewTransactor(txPool),
		Workflows:      postgres.NewWorkflowRepo(pool),
		Versions:       postgres.NewWorkflowVersionRepo(pool),
		Compiler:       bpmn_compiler.New(),
		ModuleVersions: postgres.NewModuleVersionRepo(pool),
	})})
}

func TestPromoteVersion_HTTP_ARepositoryCallInsideTheTransactionFails_IsMappedAndNothingIsActivated(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)

	cases := []struct {
		name       string
		txPool     func(t *testing.T, tenantID, userID uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy)
		wantStatus int
		wantType   string
		wantCode   string
		wantDetail string
		wantCut    bool
	}{
		{
			name: "the draft lookup is refused by the database",
			txPool: func(t *testing.T, tenantID, userID uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				return w10bRestrictedPool(t, superPool, dsn, tenantID, userID,
					"REVOKE SELECT ON workflow_version FROM {role}",
					"GRANT SELECT (id, workflow_id, tenant_id, status, version_number) ON workflow_version TO {role}"), nil
			},
			wantStatus: http.StatusInternalServerError,
			wantType:   w10bProblemInternal,
			wantCode:   "INTERNAL_ERROR",
			wantDetail: "an unexpected error occurred",
		},
		{
			name: "the connection is lost during the draft lookup",
			txPool: func(t *testing.T, _, _ uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				return w10bCutPool(t, dsn, "name: GetDraftVersion")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantType:   w10bProblemUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantDetail: "an upstream dependency is temporarily unavailable",
			wantCut:    true,
		},
		{
			name: "the update of the active version is refused by the database",
			txPool: func(t *testing.T, tenantID, userID uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				return w10bRestrictedPool(t, superPool, dsn, tenantID, userID, "REVOKE UPDATE ON workflow FROM {role}"), nil
			},
			wantStatus: http.StatusInternalServerError,
			wantType:   w10bProblemInternal,
			wantCode:   "INTERNAL_ERROR",
			wantDetail: "an unexpected error occurred",
		},
		{
			name: "the connection is lost during the update of the active version",
			txPool: func(t *testing.T, _, _ uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				return w10bCutPool(t, dsn, "name: UpdateActiveVersion")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantType:   w10bProblemUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantDetail: "an upstream dependency is temporarily unavailable",
			wantCut:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			tenantID, userID := uuid.New(), uuid.New()
			wf, version := w7bSeedPublished(t, superPool, tenantID, userID, "w10b-promote-"+uuid.NewString())
			txPool, proxy := tc.txPool(t, tenantID, userID)
			router := w10bPromoteRouter(txPool, superPool)
			path := "/api/v1/workflows/" + wf.ID.String() + "/versions/" + version.ID.String() + "/promote"

			rec := w10bCall(router, http.MethodPost, path, tenantID, userID, "")

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			p := w10bDecodeProblem(t, rec)
			assert.Equal(t, tc.wantType, p.Type)
			assert.Equal(t, tc.wantStatus, p.Status)
			assert.Equal(t, tc.wantCode, p.Code)
			assert.Equal(t, tc.wantDetail, p.Detail, "the database's own message stays out of the response")
			assert.Equal(t, path, p.Instance)
			assert.NotContains(t, rec.Body.String(), "permission denied")
			if tc.wantCut {
				require.NotNil(t, proxy)
				assert.Equal(t, 1, proxy.cutCount(), "the failing statement is the one that lost its connection, and it is not retried")
			}
			stored, err := postgres.NewWorkflowRepo(superPool).GetByID(ctx, tenantID, wf.ID)
			require.NoError(t, err)
			assert.Nil(t, stored.ActiveVersionID, "the failed promote activated nothing")
		})
	}
}

func TestWorkflowRepos_AStatementRefusedByTheDatabase_IsReturnedAsAnOrdinaryErrorNotAsNotFoundOrUnavailable(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	tenantID, userID := uuid.New(), uuid.New()
	wf, version := w7bSeedPublished(t, superPool, tenantID, userID, "w10b-refused")

	cases := []struct {
		name        string
		adjustments []string
		call        func(pool *pgcommon.Pool) error
	}{
		{
			name: "draft lookup",
			adjustments: []string{
				"REVOKE SELECT ON workflow_version FROM {role}",
				"GRANT SELECT (id, workflow_id, tenant_id, status, version_number) ON workflow_version TO {role}",
			},
			call: func(pool *pgcommon.Pool) error {
				_, err := postgres.NewWorkflowVersionRepo(pool).GetDraft(ctx, tenantID, wf.ID)
				return err
			},
		},
		{
			name:        "update of the active version",
			adjustments: []string{"REVOKE UPDATE ON workflow FROM {role}"},
			call: func(pool *pgcommon.Pool) error {
				return postgres.NewWorkflowRepo(pool).UpdateActiveVersion(ctx, tenantID, wf.ID, &version.ID)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := w10bRestrictedPool(t, superPool, dsn, tenantID, userID, tc.adjustments...)

			err := tc.call(pool)

			require.Error(t, err)
			assert.NotErrorIs(t, err, domain.ErrNotFound, "a refused statement is not an absent row")
			assert.NotErrorIs(t, err, domain.ErrUpstreamUnavailable, "a refused statement is not an outage, so the API answers 500")
		})
	}
}

func w10bCreateWorkflowRouter(pool *pgcommon.Pool) *gin.Engine {
	return w10bEngine(handler.Services{Workflows: service.NewWorkflowService(service.WorkflowDeps{
		Transactor: postgres.NewTransactor(pool),
		Workflows:  postgres.NewWorkflowRepo(pool),
		Versions:   postgres.NewWorkflowVersionRepo(pool),
		PlanLimits: service.NewPlanLimitService(service.PlanLimitDeps{Reader: w7dUnlimitedPlans{}, TTL: time.Hour}),
	})})
}

func w10bCreateWorkflowRequest(router *gin.Engine, tenantID, userID uuid.UUID, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workflows",
		strings.NewReader(`{"key":"`+key+`","name":"Name of `+key+`","bpmn_xml":"<definitions id=\"w10b-created\"/>"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", userID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	r.Header.Set("x-plan", "enterprise")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	return rec
}

type w10bStoredVersion struct {
	Status        string
	BPMN          string
	PlanNull      bool
	NumberNull    bool
	PublishedNull bool
	HashNull      bool
	ModuleRefs    string
	IsValid       bool
}

func w10bReadVersion(t *testing.T, pool *pgcommon.Pool, versionID uuid.UUID) w10bStoredVersion {
	t.Helper()
	var row w10bStoredVersion
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT status::text, bpmn_xml, compiled_plan_json IS NULL, version_number IS NULL,
			published_at IS NULL, artifact_hash IS NULL, module_refs::text, is_valid
			FROM workflow_version WHERE id = $1`, versionID).
			Scan(&row.Status, &row.BPMN, &row.PlanNull, &row.NumberNull, &row.PublishedNull, &row.HashNull, &row.ModuleRefs, &row.IsValid)
	})
	require.NoError(t, err)
	return row
}

func TestCreateWorkflow_HTTP_TheFirstVersionIsStoredAsADraftWithNullPlanNumberAndPublicationTime(t *testing.T) {
	pool := newTestPool(t)
	router := w10bCreateWorkflowRouter(pool)
	tenantID, userID := uuid.New(), uuid.New()

	rec := w10bCreateWorkflowRequest(router, tenantID, userID, "w10b-null-columns")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var body struct {
		WorkflowID    uuid.UUID `json:"workflow_id"`
		VersionID     uuid.UUID `json:"version_id"`
		Status        string    `json:"status"`
		VersionNumber *int32    `json:"version_number"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	assert.Equal(t, "DRAFT", body.Status)
	assert.Nil(t, body.VersionNumber, "a draft has no number to report")

	stored := w10bReadVersion(t, pool, body.VersionID)
	assert.Equal(t, "DRAFT", stored.Status)
	assert.Equal(t, `<definitions id="w10b-created"/>`, stored.BPMN, "the submitted diagram is stored as sent")
	assert.True(t, stored.PlanNull, "no compiled plan exists before publish")
	assert.True(t, stored.NumberNull, "no version number exists before publish")
	assert.True(t, stored.PublishedNull, "no publication time exists before publish")
	assert.True(t, stored.HashNull, "no artifact hash exists before publish")
	assert.JSONEq(t, `[]`, stored.ModuleRefs)

	wf, err := postgres.NewWorkflowRepo(pool).GetByID(context.Background(), tenantID, body.WorkflowID)
	require.NoError(t, err)
	assert.Nil(t, wf.ActiveVersionID, "the draft is not active")
	read, err := postgres.NewWorkflowVersionRepo(pool).GetByID(context.Background(), tenantID, body.VersionID)
	require.NoError(t, err)
	assert.Nil(t, read.CompiledPlanJSON)
	assert.Nil(t, read.VersionNumber)
	assert.Nil(t, read.PublishedAt)
}

func TestCreateWorkflow_HTTP_TheSameKeyUnderAnotherTenantIsCreatedAndEachTenantReadsOnlyItsOwn(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	tenantA, userA, tenantB, userB := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	poolA := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA)
	poolB := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantB, &userB)
	routerA, routerB := w10bCreateWorkflowRouter(poolA), w10bCreateWorkflowRouter(poolB)
	const key = "w10b-shared-key"

	recA := w10bCreateWorkflowRequest(routerA, tenantA, userA, key)
	require.Equal(t, http.StatusCreated, recA.Code, recA.Body.String())
	recB := w10bCreateWorkflowRequest(routerB, tenantB, userB, key)
	require.Equal(t, http.StatusCreated, recB.Code, "another tenant may use the same key: %s", recB.Body.String())
	repeat := w10bCreateWorkflowRequest(routerA, tenantA, userA, key)

	require.Equal(t, http.StatusConflict, repeat.Code, repeat.Body.String())
	p := w10bDecodeProblem(t, repeat)
	assert.Equal(t, w10bProblemConflict, p.Type)
	assert.Equal(t, "DUPLICATE_TEMPLATE_KEY", p.Code)
	assert.Equal(t, "a workflow with this key already exists", p.Detail)

	var idA, idB struct {
		WorkflowID uuid.UUID `json:"workflow_id"`
	}
	require.NoError(t, json.Unmarshal(recA.Body.Bytes(), &idA))
	require.NoError(t, json.Unmarshal(recB.Body.Bytes(), &idB))
	require.NotEqual(t, idA.WorkflowID, idB.WorkflowID)

	gotA, err := postgres.NewWorkflowRepo(poolA).GetByTemplateKey(ctx, tenantA, key)
	require.NoError(t, err)
	assert.Equal(t, idA.WorkflowID, gotA.ID)
	gotB, err := postgres.NewWorkflowRepo(poolB).GetByTemplateKey(ctx, tenantB, key)
	require.NoError(t, err)
	assert.Equal(t, idB.WorkflowID, gotB.ID)
	_, err = postgres.NewWorkflowRepo(poolA).GetByTemplateKey(ctx, tenantB, key)
	assert.ErrorIs(t, err, domain.ErrNotFound, "tenant A's pool cannot read tenant B's workflow even when asking for B's tenant id")

	var total int
	require.NoError(t, superPool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT count(*) FROM workflow WHERE template_key = $1`, key).Scan(&total)
	}))
	assert.Equal(t, 2, total, "one workflow per tenant, and the refused repeat stored nothing")
}

type w10bRedirectedWorkflowPointer struct {
	port.WorkflowRepository
	workflow uuid.UUID
}

func (r w10bRedirectedWorkflowPointer) UpdateActiveVersion(ctx context.Context, tenantID, _ uuid.UUID, versionID *uuid.UUID) error {
	return r.WorkflowRepository.UpdateActiveVersion(ctx, tenantID, r.workflow, versionID)
}

func TestArchiveWorkflow_HTTP_TheWorkflowIsGoneWhenTheActivePointerIsCleared_Is404AndTheVersionStaysPublished(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenantID, userID := uuid.New(), uuid.New()
	wf, version := w7bSeedPublished(t, pool, tenantID, userID, "w10b-archive-gone")
	require.NoError(t, postgres.NewWorkflowRepo(pool).UpdateActiveVersion(ctx, tenantID, wf.ID, &version.ID))

	t.Run("the repository answers not found for an unknown workflow when the pointer is cleared", func(t *testing.T) {
		err := postgres.NewWorkflowRepo(pool).UpdateActiveVersion(ctx, tenantID, uuid.New(), nil)

		assert.ErrorIs(t, err, domain.ErrNotFound)
		assert.NotErrorIs(t, err, domain.ErrVersionNotPublished)
	})

	t.Run("through the service the version archive is rolled back with the failed pointer update", func(t *testing.T) {
		workflows := w10bRedirectedWorkflowPointer{WorkflowRepository: postgres.NewWorkflowRepo(pool), workflow: uuid.New()}
		router := w10bEngine(handler.Services{Workflows: service.NewWorkflowService(service.WorkflowDeps{
			Transactor: postgres.NewTransactor(pool),
			Workflows:  workflows,
			Versions:   postgres.NewWorkflowVersionRepo(pool),
		})})
		path := "/api/v1/workflows/" + wf.ID.String() + "/archive"

		rec := w10bCall(router, http.MethodPost, path, tenantID, userID, "")

		require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		p := w10bDecodeProblem(t, rec)
		assert.Equal(t, w10bProblemNotFound, p.Type)
		assert.Equal(t, http.StatusNotFound, p.Status)
		assert.Equal(t, "NOT_FOUND", p.Code)
		assert.Equal(t, "resource not found", p.Detail)
		assert.Equal(t, path, p.Instance)
		assert.Equal(t, "PUBLISHED", w10bReadVersion(t, pool, version.ID).Status, "the archive of the version was rolled back")
		stored, err := postgres.NewWorkflowRepo(pool).GetByID(ctx, tenantID, wf.ID)
		require.NoError(t, err)
		require.NotNil(t, stored.ActiveVersionID, "the workflow still points at its version")
		assert.Equal(t, version.ID, *stored.ActiveVersionID)
	})
}

func w10bStarterFromVersionRouter(starterPool, pool *pgcommon.Pool) *gin.Engine {
	return w10bEngine(handler.Services{Starters: service.NewStarterService(service.StarterDeps{
		Starters:       postgres.NewStarterRepo(starterPool),
		GlobalStarters: postgres.NewStarterRepo(starterPool),
		Versions:       postgres.NewWorkflowVersionRepo(pool),
		ModuleVersions: postgres.NewModuleVersionRepo(pool),
	})})
}

func TestCreateStarterFromWorkflowVersion_HTTP_TheConnectionIsLostDuringTheStarterInsert_Is503AndNothingIsStored(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	_, version := w7bSeedPublished(t, superPool, tenantID, userID, "w10b-starter-source")
	starterPool, proxy := w10bCutPool(t, dsn, "name: CreateStarter")
	router := w10bStarterFromVersionRouter(starterPool, superPool)
	path := "/api/v1/starters/from-workflow-version/" + version.ID.String()

	rec := w10bCall(router, http.MethodPost, path, tenantID, userID, `{"name":"w10b starter"}`)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	p := w10bDecodeProblem(t, rec)
	assert.Equal(t, w10bProblemUnavailable, p.Type)
	assert.Equal(t, "UPSTREAM_UNAVAILABLE", p.Code)
	assert.Equal(t, "an upstream dependency is temporarily unavailable", p.Detail)
	assert.Equal(t, path, p.Instance)
	assert.Equal(t, 1, proxy.cutCount(), "the insert is the statement that lost its connection, and it is not retried")
	var stored int
	require.NoError(t, superPool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT count(*) FROM workflow_template WHERE tenant_id = $1`, tenantID).Scan(&stored)
	}))
	assert.Zero(t, stored, "no starter was stored")

	t.Run("the same request over a healthy connection stores the starter", func(t *testing.T) {
		healthy := w10bStarterFromVersionRouter(superPool, superPool)

		ok := w10bCall(healthy, http.MethodPost, path, tenantID, userID, `{"name":"w10b starter"}`)

		require.Equal(t, http.StatusCreated, ok.Code, ok.Body.String())
	})
}
