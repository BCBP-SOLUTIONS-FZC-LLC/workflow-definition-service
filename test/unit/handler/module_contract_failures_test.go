package handler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func moduleContractFailureCallerXML(processID, callID, calledProcessID string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_%[1]s" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="%[1]s" name="%[1]s" isExecutable="true">
    <bpmn:startEvent id="%[1]s_Start"/>
    <bpmn:callActivity id="%[2]s" name="Call %[3]s">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="%[3]s" propagateAllChildVariables="false"/>
      </bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="%[1]s_End"/>
    <bpmn:sequenceFlow id="%[1]s_F1" sourceRef="%[1]s_Start" targetRef="%[2]s"/>
    <bpmn:sequenceFlow id="%[1]s_F2" sourceRef="%[2]s" targetRef="%[1]s_End"/>
  </bpmn:process>
</bpmn:definitions>`, processID, callID, calledProcessID)
}

type moduleContractFailureWire struct {
	versions *mocks.MockModuleVersionRepository
	compiler *mocks.MockPlanCompiler
	router   *gin.Engine
}

func newModuleContractFailureWire(t *testing.T, compiler port.PlanCompiler) *moduleContractFailureWire {
	t.Helper()
	ctrl := gomock.NewController(t)
	w := &moduleContractFailureWire{
		versions: mocks.NewMockModuleVersionRepository(ctrl),
		compiler: mocks.NewMockPlanCompiler(ctrl),
	}
	if compiler == nil {
		compiler = w.compiler
	}
	svc := service.NewModuleService(service.ModuleDeps{
		Versions:       w.versions,
		GlobalVersions: mocks.NewMockModuleVersionRepository(ctrl),
		Compiler:       compiler,
	})
	w.router = newRouter(handler.New(handler.Services{Modules: svc}))
	return w
}

func (w *moduleContractFailureWire) storedVersion(processID, bpmnXML string) {
	n := int32(1)
	w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(&domain.ModuleVersion{
		ID: testVerID, ModuleID: testWFID, TenantID: &testTenantID, Scope: domain.ScopeTenant,
		ProcessID: processID, VersionNumber: &n, Status: domain.VersionStatusPublished, BPMNXML: bpmnXML,
	}, nil)
}

func TestGetModuleContract_VersionIDNotAUUID_Returns400(t *testing.T) {
	cases := []struct {
		name      string
		versionID string
	}{
		{"free text", "not-a-uuid"},
		{"one character short of a uuid", "4444444-4444-4444-4444-444444444444"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			mod := &fakeModuleSvc{contract: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*service.ModuleContract, error) {
				called = true
				return nil, nil
			}}

			w := do(newRouter(newModuleHandler(mod)),
				req(http.MethodGet, "/api/v1/modules/"+testWFID.String()+"/versions/"+tc.versionID+"/contract", nil))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "invalid version_id: not a valid UUID", prob["detail"])
			assert.False(t, called, "a malformed version id must not reach the service")
		})
	}
}

func TestGetModuleContract_FailureBehindTheVersionRead_ReturnsTheMappedProblem(t *testing.T) {
	const (
		internalDetail    = "an unexpected error occurred"
		unavailableDetail = "an upstream dependency is temporarily unavailable"
	)
	calledOther := []domain.ModuleCall{{CallID: "Call_Other", ProcessID: "Process_Other"}}
	cases := []struct {
		name       string
		arrange    func(w *moduleContractFailureWire)
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{
			name: "the version lookup finds the database unavailable",
			arrange: func(w *moduleContractFailureWire) {
				w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).
					Return(nil, fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable))
			},
			wantStatus: http.StatusServiceUnavailable, wantCode: "UPSTREAM_UNAVAILABLE", wantDetail: unavailableDetail,
		},
		{
			name: "a nested module lookup finds the database unavailable",
			arrange: func(w *moduleContractFailureWire) {
				w.storedVersion("Process_Outer", "<outer/>")
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<outer/>").Return(calledOther, nil, nil)
				w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Other", int32(0)).
					Return(nil, fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable))
			},
			wantStatus: http.StatusServiceUnavailable, wantCode: "UPSTREAM_UNAVAILABLE", wantDetail: unavailableDetail,
		},
		{
			name: "a nested module lookup fails with an untyped error",
			arrange: func(w *moduleContractFailureWire) {
				w.storedVersion("Process_Outer", "<outer/>")
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<outer/>").Return(calledOther, nil, nil)
				w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Other", int32(0)).
					Return(nil, errors.New("scan failed"))
			},
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR", wantDetail: internalDetail,
		},
		{
			name: "listing the module calls fails",
			arrange: func(w *moduleContractFailureWire) {
				w.storedVersion("Process_Outer", "<outer/>")
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<outer/>").Return(nil, nil, errors.New("calls failed"))
			},
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR", wantDetail: internalDetail,
		},
		{
			name: "bundling the modules fails",
			arrange: func(w *moduleContractFailureWire) {
				w.storedVersion("Process_Outer", "<outer/>")
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<outer/>").Return(nil, nil, nil)
				w.compiler.EXPECT().BundleModules("<outer/>", gomock.Any()).Return("", errors.New("bundle failed"))
			},
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR", wantDetail: internalDetail,
		},
		{
			name: "compiling the module fails with an untyped error",
			arrange: func(w *moduleContractFailureWire) {
				w.storedVersion("Process_Outer", "<outer/>")
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<outer/>").Return(nil, nil, nil)
				w.compiler.EXPECT().BundleModules("<outer/>", gomock.Any()).Return("<bundled/>", nil)
				w.compiler.EXPECT().CompileModule(gomock.Any(), "<bundled/>", "Process_Outer").
					Return(nil, domain.ModuleDeclarations{}, errors.New("compile failed"))
			},
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR", wantDetail: internalDetail,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newModuleContractFailureWire(t, nil)
			tc.arrange(w)

			rec := do(w.router, req(http.MethodGet,
				"/api/v1/modules/"+testWFID.String()+"/versions/"+testVerID.String()+"/contract", nil))

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			prob := decodeProblem(t, rec)
			assert.Equal(t, tc.wantCode, prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
			if tc.wantStatus == http.StatusServiceUnavailable {
				assert.NotContains(t, rec.Body.String(), "connection refused", "the cause must not reach the client")
			}
		})
	}
}

func TestGetModuleContract_CalledModuleCallsBackWithoutAPinnedVersion_Returns422(t *testing.T) {
	w := newModuleContractFailureWire(t, bpmn_compiler.New())
	w.storedVersion("Process_Ping", moduleContractFailureCallerXML("Process_Ping", "Ping_CallPong", "Process_Pong"))
	pongVersion := int32(1)
	w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Pong", int32(0)).Return(&domain.ModuleVersion{
		ID: uuid.New(), ModuleID: uuid.New(), ProcessID: "Process_Pong", VersionNumber: &pongVersion,
		Status:  domain.VersionStatusPublished,
		BPMNXML: moduleContractFailureCallerXML("Process_Pong", "Pong_CallPing", "Process_Ping"),
	}, nil)

	rec := do(w.router, req(http.MethodGet,
		"/api/v1/modules/"+testWFID.String()+"/versions/"+testVerID.String()+"/contract", nil))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	prob := decodeProblem(t, rec)
	assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
	params, ok := prob["invalid_params"].([]any)
	require.True(t, ok, "problem body carries invalid_params: %v", prob)
	require.Len(t, params, 1)
	param := params[0].(map[string]any)
	assert.Equal(t, "CALLED_ELEMENT_CYCLE", param["code"])
	assert.Equal(t, "Ping_CallPong", param["name"])
	assert.Contains(t, param["reason"], `"Process_Pong"`)
}
