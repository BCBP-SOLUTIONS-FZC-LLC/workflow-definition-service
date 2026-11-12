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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func tyModuleRouter(tenantPool, systemPool *pgcommon.Pool) *gin.Engine {
	svc := service.NewModuleService(service.ModuleDeps{
		Transactor:       postgres.NewTransactor(tenantPool),
		GlobalTransactor: postgres.NewTransactor(systemPool),
		Modules:          postgres.NewModuleRepo(tenantPool),
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

func tyModuleXML(processID string) string {
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

func tySend(t *testing.T, router *gin.Engine, method, path string, tenantID, userID uuid.UUID, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	r := httptest.NewRequest(method, path, &buf)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", userID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return rec.Code, out
}

func tyReadModuleRow(t *testing.T, pool *pgcommon.Pool, moduleID uuid.UUID) *uuid.UUID {
	t.Helper()
	var pointer *uuid.UUID
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT active_version_id FROM workflow_module WHERE id = $1`, moduleID).Scan(&pointer)
	})
	require.NoError(t, err)
	return pointer
}

func TestModulePublish_HTTP_DraftOfAnArchivedModule_ActivatesTheModuleAgain(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	tenantID, userID := uuid.New(), uuid.New()
	pool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	router := tyModuleRouter(pool, systemPool)
	processID := "Process_Ty_" + uuid.NewString()

	code, created := tySend(t, router, http.MethodPost, "/api/v1/modules", tenantID, userID,
		map[string]any{"name": processID, "bpmn_xml": tyModuleXML(processID)})
	require.Equal(t, http.StatusCreated, code, created)
	moduleID := uuid.MustParse(created["module_id"].(string))
	firstVersion := uuid.MustParse(created["version_id"].(string))
	modulePath := "/api/v1/modules/" + moduleID.String()

	code, body := tySend(t, router, http.MethodPost, modulePath+"/versions/"+firstVersion.String()+"/publish", tenantID, userID, nil)
	require.Equal(t, http.StatusOK, code, body)

	code, body = tySend(t, router, http.MethodDelete, modulePath, tenantID, userID, nil)
	require.Equal(t, http.StatusOK, code, body)
	require.Nil(t, tyReadModuleRow(t, superPool, moduleID), "the archived module has no active version")

	code, body = tySend(t, router, http.MethodPost, modulePath+"/versions", tenantID, userID,
		map[string]any{"bpmn_xml": tyModuleXML(processID)})
	require.Equal(t, http.StatusCreated, code, body)
	draftID := uuid.MustParse(body["version_id"].(string))
	require.Nil(t, tyReadModuleRow(t, superPool, moduleID), "adding a draft does not activate the module")

	code, body = tySend(t, router, http.MethodPost, modulePath+"/versions/"+draftID.String()+"/publish", tenantID, userID, nil)

	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, draftID.String(), body["version_id"])
	assert.Equal(t, "PUBLISHED", body["status"])
	assert.EqualValues(t, 2, body["version_number"], "the archived first version keeps number 1")
	pointer := tyReadModuleRow(t, superPool, moduleID)
	require.NotNil(t, pointer)
	assert.Equal(t, draftID, *pointer)

	code, got := tySend(t, router, http.MethodGet, modulePath, tenantID, userID, nil)
	require.Equal(t, http.StatusOK, code, got)
	assert.Equal(t, draftID.String(), got["active_version_id"])
	active, ok := got["active_version"].(map[string]any)
	require.True(t, ok, "the module answers with its active version: %v", got)
	assert.Equal(t, draftID.String(), active["id"])
	assert.Equal(t, "PUBLISHED", active["status"])
	assert.EqualValues(t, 2, active["version_number"])
}
