//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type w9cRedirectedPointer struct {
	port.ModuleRepository
	module  uuid.UUID
	version *uuid.UUID
}

func (r w9cRedirectedPointer) UpdateActiveVersion(ctx context.Context, _ uuid.UUID, _ *uuid.UUID) error {
	return r.ModuleRepository.UpdateActiveVersion(ctx, r.module, r.version)
}

func w9cModuleRouter(tenantPool, systemPool *pgcommon.Pool, wrap func(port.ModuleRepository) port.ModuleRepository) *gin.Engine {
	var modules port.ModuleRepository = postgres.NewModuleRepo(tenantPool)
	if wrap != nil {
		modules = wrap(modules)
	}
	svc := service.NewModuleService(service.ModuleDeps{
		Transactor:       postgres.NewTransactor(tenantPool),
		GlobalTransactor: postgres.NewTransactor(systemPool),
		Modules:          modules,
		GlobalModules:    postgres.NewModuleRepo(systemPool),
		Versions:         postgres.NewModuleVersionRepo(tenantPool),
		GlobalVersions:   postgres.NewModuleVersionRepo(systemPool),
		WorkflowVersions: postgres.NewWorkflowVersionRepo(tenantPool),
		Compiler:         bpmn_compiler.New(),
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

func w9cModuleXML(processID string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="%s" name="Review" isExecutable="true">
    <bpmn:startEvent id="Start_1"/>
    <bpmn:userTask id="Task_1" name="Review">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="Start_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Task_1" targetRef="End_1"/>
  </bpmn:process>
</bpmn:definitions>`, processID)
}

func w9cRequest(method, path string, tenantID, userID uuid.UUID, body any) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, path, &buf)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", userID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	return r
}

func w9cSend(router *gin.Engine, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	return rec
}

func w9cBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body
}

func w9cID(t *testing.T, body map[string]any, key string) uuid.UUID {
	t.Helper()
	raw, ok := body[key].(string)
	require.True(t, ok, "body carries %s: %v", key, body)
	return uuid.MustParse(raw)
}

func w9cCreateModule(t *testing.T, router *gin.Engine, tenantID, userID uuid.UUID, processID string) (moduleID, versionID uuid.UUID) {
	t.Helper()
	rec := w9cSend(router, w9cRequest(http.MethodPost, "/api/v1/modules", tenantID, userID,
		map[string]any{"name": processID, "bpmn_xml": w9cModuleXML(processID)}))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	body := w9cBody(t, rec)
	return w9cID(t, body, "module_id"), w9cID(t, body, "version_id")
}

func w9cAddDraft(t *testing.T, router *gin.Engine, tenantID, userID, moduleID uuid.UUID, processID string) uuid.UUID {
	t.Helper()
	rec := w9cSend(router, w9cRequest(http.MethodPost, "/api/v1/modules/"+moduleID.String()+"/versions", tenantID, userID,
		map[string]any{"bpmn_xml": w9cModuleXML(processID)}))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return w9cID(t, w9cBody(t, rec), "version_id")
}

func w9cPublishPath(moduleID, versionID uuid.UUID) string {
	return "/api/v1/modules/" + moduleID.String() + "/versions/" + versionID.String() + "/publish"
}

func w9cPublish(router *gin.Engine, tenantID, userID, moduleID, versionID uuid.UUID) *httptest.ResponseRecorder {
	return w9cSend(router, w9cRequest(http.MethodPost, w9cPublishPath(moduleID, versionID), tenantID, userID, nil))
}

type w9cVersionRow struct {
	status string
	number *int32
}

func w9cReadVersion(t *testing.T, pool *pgcommon.Pool, versionID uuid.UUID) w9cVersionRow {
	t.Helper()
	var row w9cVersionRow
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT status, version_number FROM workflow_module_version WHERE id = $1`, versionID).
			Scan(&row.status, &row.number)
	})
	require.NoError(t, err)
	return row
}

func w9cReadPointer(t *testing.T, pool *pgcommon.Pool, moduleID uuid.UUID) *uuid.UUID {
	t.Helper()
	var pointer *uuid.UUID
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT active_version_id FROM workflow_module WHERE id = $1`, moduleID).Scan(&pointer)
	})
	require.NoError(t, err)
	return pointer
}

func w9cCountVersions(t *testing.T, pool *pgcommon.Pool, moduleID uuid.UUID) int {
	t.Helper()
	var n int
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT count(*) FROM workflow_module_version WHERE module_id = $1`, moduleID).Scan(&n)
	})
	require.NoError(t, err)
	return n
}

type w9cProblem struct {
	Type     string `json:"type"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
	Code     string `json:"code"`
}

func w9cDecodeProblem(t *testing.T, rec *httptest.ResponseRecorder) w9cProblem {
	t.Helper()
	var p w9cProblem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p), rec.Body.String())
	return p
}

func TestModuleAddVersion_HTTP_AnotherTenantsModuleIsNotFoundAndNoVersionIsAdded(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	t.Run("the owner adds a version to its own module", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		pool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
		router := w9cModuleRouter(pool, systemPool, nil)
		moduleID, first := w9cCreateModule(t, router, tenantID, userID, "Process_W9cOwn")
		require.Equal(t, http.StatusOK, w9cPublish(router, tenantID, userID, moduleID, first).Code)

		rec := w9cSend(router, w9cRequest(http.MethodPost, "/api/v1/modules/"+moduleID.String()+"/versions", tenantID, userID,
			map[string]any{"bpmn_xml": w9cModuleXML("Process_W9cOwn")}))

		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.Equal(t, 2, w9cCountVersions(t, superPool, moduleID))
	})

	t.Run("another tenant is told the module does not exist", func(t *testing.T) {
		ownerTenant, callerTenant, userID := uuid.New(), uuid.New(), uuid.New()
		ownerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &ownerTenant, &userID)
		callerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &callerTenant, &userID)
		ownerRouter := w9cModuleRouter(ownerPool, systemPool, nil)
		moduleID, first := w9cCreateModule(t, ownerRouter, ownerTenant, userID, "Process_W9cForeign")
		require.Equal(t, http.StatusOK, w9cPublish(ownerRouter, ownerTenant, userID, moduleID, first).Code)
		path := "/api/v1/modules/" + moduleID.String() + "/versions"

		rec := w9cSend(w9cModuleRouter(callerPool, systemPool, nil), w9cRequest(http.MethodPost, path, callerTenant, userID,
			map[string]any{"bpmn_xml": w9cModuleXML("Process_W9cForeign")}))

		require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		p := w9cDecodeProblem(t, rec)
		assert.Equal(t, "https://api.workflow.platform/errors/not-found", p.Type)
		assert.Equal(t, http.StatusNotFound, p.Status)
		assert.Equal(t, "NOT_FOUND", p.Code)
		assert.Equal(t, "resource not found", p.Detail)
		assert.Equal(t, path, p.Instance)
		assert.Equal(t, 1, w9cCountVersions(t, superPool, moduleID), "no version row was written for the owner's module")
	})

	t.Run("naming the owner's tenant on a pool bound to the caller is stopped by row-level security", func(t *testing.T) {
		ownerTenant, callerTenant, userID := uuid.New(), uuid.New(), uuid.New()
		ownerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &ownerTenant, &userID)
		callerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &callerTenant, &userID)
		ownerRouter := w9cModuleRouter(ownerPool, systemPool, nil)
		moduleID, first := w9cCreateModule(t, ownerRouter, ownerTenant, userID, "Process_W9cNamed")
		require.Equal(t, http.StatusOK, w9cPublish(ownerRouter, ownerTenant, userID, moduleID, first).Code)
		path := "/api/v1/modules/" + moduleID.String() + "/versions"

		rec := w9cSend(w9cModuleRouter(callerPool, systemPool, nil), w9cRequest(http.MethodPost, path, ownerTenant, userID,
			map[string]any{"bpmn_xml": w9cModuleXML("Process_W9cNamed")}))

		require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		p := w9cDecodeProblem(t, rec)
		assert.Equal(t, "NOT_FOUND", p.Code)
		assert.Equal(t, "resource not found", p.Detail)
		assert.Equal(t, path, p.Instance)
		assert.Equal(t, 1, w9cCountVersions(t, superPool, moduleID))
	})
}

func TestModulePublish_HTTP_SecondDraftTakesTheNextNumberAndBecomesTheActiveVersion(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	t.Run("a second draft after a published first version", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		pool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
		router := w9cModuleRouter(pool, systemPool, nil)
		moduleID, first := w9cCreateModule(t, router, tenantID, userID, "Process_W9cSecond")
		require.Equal(t, http.StatusOK, w9cPublish(router, tenantID, userID, moduleID, first).Code)
		second := w9cAddDraft(t, router, tenantID, userID, moduleID, "Process_W9cSecond")

		rec := w9cPublish(router, tenantID, userID, moduleID, second)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := w9cBody(t, rec)
		assert.EqualValues(t, 2, body["version_number"])
		assert.Equal(t, second.String(), body["version_id"])
		assert.Equal(t, "PUBLISHED", body["status"])
		pointer := w9cReadPointer(t, superPool, moduleID)
		require.NotNil(t, pointer)
		assert.Equal(t, second, *pointer, "the module's active version moved to the new version")
		secondRow := w9cReadVersion(t, superPool, second)
		assert.Equal(t, "PUBLISHED", secondRow.status)
		require.NotNil(t, secondRow.number)
		assert.EqualValues(t, 2, *secondRow.number)
		firstRow := w9cReadVersion(t, superPool, first)
		assert.Equal(t, "PUBLISHED", firstRow.status, "the earlier version stays published so pinned calls still resolve")
		require.NotNil(t, firstRow.number)
		assert.EqualValues(t, 1, *firstRow.number)
	})

	t.Run("a new draft after the module was archived counts the archived version", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		pool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
		router := w9cModuleRouter(pool, systemPool, nil)
		moduleID, first := w9cCreateModule(t, router, tenantID, userID, "Process_W9cArchived")
		require.Equal(t, http.StatusOK, w9cPublish(router, tenantID, userID, moduleID, first).Code)
		archived := w9cSend(router, w9cRequest(http.MethodDelete, "/api/v1/modules/"+moduleID.String(), tenantID, userID, nil))
		require.Equal(t, http.StatusOK, archived.Code, archived.Body.String())
		require.Equal(t, "ARCHIVED", w9cReadVersion(t, superPool, first).status)
		require.Nil(t, w9cReadPointer(t, superPool, moduleID))
		rec := w9cSend(router, w9cRequest(http.MethodPost, "/api/v1/modules/"+moduleID.String()+"/versions", tenantID, userID,
			map[string]any{"bpmn_xml": w9cModuleXML("Process_W9cArchived")}))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		second := w9cID(t, w9cBody(t, rec), "version_id")

		rec = w9cPublish(router, tenantID, userID, moduleID, second)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.EqualValues(t, 2, w9cBody(t, rec)["version_number"], "the archived version keeps number 1")
		pointer := w9cReadPointer(t, superPool, moduleID)
		require.NotNil(t, pointer)
		assert.Equal(t, second, *pointer, "publishing makes the archived module active again")
		assert.Equal(t, "ARCHIVED", w9cReadVersion(t, superPool, first).status)
	})
}

func TestModulePublish_HTTP_ActivePointerUpdateFailure_RollsBackThePublishedVersion(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	cases := []struct {
		name       string
		redirect   func(otherModuleVersion uuid.UUID, draft uuid.UUID, moduleID uuid.UUID) w9cRedirectedPointer
		wantStatus int
		wantType   string
		wantCode   string
		wantDetail string
	}{
		{
			name: "the module is gone when the pointer is written",
			redirect: func(_, draft, _ uuid.UUID) w9cRedirectedPointer {
				return w9cRedirectedPointer{module: uuid.New(), version: &draft}
			},
			wantStatus: http.StatusNotFound,
			wantType:   "https://api.workflow.platform/errors/not-found",
			wantCode:   "NOT_FOUND",
			wantDetail: "resource not found",
		},
		{
			name: "the pointer would name a version of another module",
			redirect: func(other, _, moduleID uuid.UUID) w9cRedirectedPointer {
				return w9cRedirectedPointer{module: moduleID, version: &other}
			},
			wantStatus: http.StatusConflict,
			wantType:   "https://api.workflow.platform/errors/conflict",
			wantCode:   "INVALID_VERSION_STATUS",
			wantDetail: "workflow version is not in PUBLISHED status",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, userID := uuid.New(), uuid.New()
			pool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
			plain := w9cModuleRouter(pool, systemPool, nil)
			moduleID, draft := w9cCreateModule(t, plain, tenantID, userID, "Process_W9cRollback")
			otherModule, otherDraft := w9cCreateModule(t, plain, tenantID, userID, "Process_W9cRollbackOther")
			require.Equal(t, http.StatusOK, w9cPublish(plain, tenantID, userID, otherModule, otherDraft).Code)
			redirect := tc.redirect(otherDraft, draft, moduleID)
			failing := w9cModuleRouter(pool, systemPool, func(real port.ModuleRepository) port.ModuleRepository {
				redirect.ModuleRepository = real
				return redirect
			})

			rec := w9cPublish(failing, tenantID, userID, moduleID, draft)

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			p := w9cDecodeProblem(t, rec)
			assert.Equal(t, tc.wantType, p.Type)
			assert.Equal(t, tc.wantStatus, p.Status)
			assert.Equal(t, tc.wantCode, p.Code)
			assert.Equal(t, tc.wantDetail, p.Detail)
			assert.Equal(t, w9cPublishPath(moduleID, draft), p.Instance)
			row := w9cReadVersion(t, superPool, draft)
			assert.Equal(t, "DRAFT", row.status, "the version update is rolled back with the failed pointer update")
			assert.Nil(t, row.number, "the number taken inside the transaction is not kept")
			assert.Nil(t, w9cReadPointer(t, superPool, moduleID), "the module still has no active version")

			retry := w9cPublish(plain, tenantID, userID, moduleID, draft)

			require.Equal(t, http.StatusOK, retry.Code, "the draft can still be published once the failure is gone: %s", retry.Body.String())
			assert.EqualValues(t, 1, w9cBody(t, retry)["version_number"])
		})
	}
}
