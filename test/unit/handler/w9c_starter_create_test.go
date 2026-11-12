package handler_test

import (
	"context"
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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const w9cStarterCallerXML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_Caller" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_W9cMain" name="Main" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_eng" name="Engineering"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="2200a5ae-2466-54f7-a1d8-243106e05705"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CA_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
    <bpmn:callActivity id="CA_1" name="Review">
      <bpmn:extensionElements><zeebe:calledElement processId="Process_W9cReview"/></bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="Start_1" targetRef="CA_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="CA_1" targetRef="End_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_W9cMain">
      <bpmndi:BPMNShape id="Lane_eng_di" bpmnElement="Lane_eng"/>
      <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"/>
      <bpmndi:BPMNShape id="CA_1_di" bpmnElement="CA_1"/>
      <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"/>
      <bpmndi:BPMNEdge id="Flow_1_di" bpmnElement="Flow_1"/>
      <bpmndi:BPMNEdge id="Flow_2_di" bpmnElement="Flow_2"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

const w9cStarterReviewXML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_Review" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_W9cReview" name="Review" isExecutable="true">
    <bpmn:startEvent id="Review_Start"/>
    <bpmn:userTask id="Review_Task" name="Review">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="Review_End"/>
    <bpmn:sequenceFlow id="Review_F1" sourceRef="Review_Start" targetRef="Review_Task"/>
    <bpmn:sequenceFlow id="Review_F2" sourceRef="Review_Task" targetRef="Review_End"/>
  </bpmn:process>
</bpmn:definitions>`

type w9cStarterWiring struct {
	starters       *mocks.MockStarterTemplateRepository
	globalStarters *mocks.MockStarterTemplateRepository
	versions       *mocks.MockModuleVersionRepository
	globalVersions *mocks.MockModuleVersionRepository
	router         *gin.Engine
}

func w9cNewStarterWiring(t *testing.T) *w9cStarterWiring {
	t.Helper()
	ctrl := gomock.NewController(t)
	w := &w9cStarterWiring{
		starters:       mocks.NewMockStarterTemplateRepository(ctrl),
		globalStarters: mocks.NewMockStarterTemplateRepository(ctrl),
		versions:       mocks.NewMockModuleVersionRepository(ctrl),
		globalVersions: mocks.NewMockModuleVersionRepository(ctrl),
	}
	svc := service.NewStarterService(service.StarterDeps{
		Starters:             w.starters,
		GlobalStarters:       w.globalStarters,
		Versions:             mocks.NewMockWorkflowVersionRepository(ctrl),
		Compiler:             bpmn_compiler.New(),
		ModuleVersions:       w.versions,
		GlobalModuleVersions: w.globalVersions,
	})
	w.router = newRouter(handler.New(handler.Services{Starters: svc}))
	return w
}

func TestStarterCreate_OnlyWarningFindings_Returns201AndStoresTheStarter(t *testing.T) {
	t.Parallel()
	w := newStarterCreateWiring(t)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bundled/>", nil)
	w.compiler.EXPECT().Validate(gomock.Any(), "<bundled/>").Return([]domain.BPMNValidationError{
		{Code: domain.BPMNErrorCode("W9C_ADVISORY_ONE"), NodeID: "Task_1", Message: "worth a look", Severity: domain.SeverityWarning},
		{Code: domain.BPMNErrorCode("W9C_ADVISORY_TWO"), NodeID: "Task_2", Message: "also worth a look", Severity: domain.SeverityWarning},
	}, nil)
	var stored *domain.StarterTemplate
	w.starters.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, st *domain.StarterTemplate) error {
			stored = st
			return nil
		})

	rec := do(w.router, adminReq(http.MethodPost, starterCreatePath, starterCreateBody("")))

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.NotNil(t, stored, "warnings do not stop the starter from being stored")
	assert.Equal(t, "<bpmn/>", stored.BPMNXML)
	assert.Equal(t, "Review", stored.Name)
	assert.Equal(t, domain.ScopeTenant, stored.Scope)
	require.NotNil(t, stored.TenantID)
	assert.Equal(t, testTenantID, *stored.TenantID)
	body := w8dBody(t, rec)
	assert.Equal(t, stored.ID.String(), body["id"])
	assert.Equal(t, "<bpmn/>", body["bpmn_xml"])
}

func w9cExpectReviewModule(repo *mocks.MockModuleVersionRepository, tenantID uuid.UUID) {
	n := int32(1)
	repo.EXPECT().Resolve(gomock.Any(), tenantID, "Process_W9cReview", int32(0)).Return(&domain.ModuleVersion{
		ID: uuid.New(), ModuleID: uuid.New(), ProcessID: "Process_W9cReview", VersionNumber: &n,
		Status: domain.VersionStatusPublished, BPMNXML: w9cStarterReviewXML,
	}, nil).AnyTimes()
}

func TestStarterCreate_GlobalScopeCallingAPublishedGlobalModule_Returns201WithTheSubmittedXML(t *testing.T) {
	t.Parallel()
	w := w9cNewStarterWiring(t)
	w9cExpectReviewModule(w.globalVersions, uuid.Nil)
	var stored *domain.StarterTemplate
	w.globalStarters.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, st *domain.StarterTemplate) error {
			stored = st
			return nil
		})

	rec := do(w.router, platformOperatorReq(http.MethodPost, starterCreatePath,
		map[string]any{"name": "Global review", "bpmn_xml": w9cStarterCallerXML, "scope": "global"}))

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.NotNil(t, stored, "the starter is written through the global repository")
	assert.Equal(t, w9cStarterCallerXML, stored.BPMNXML, "the stored text is the submitted one, without the module bundled in")
	assert.Equal(t, domain.ScopeGlobal, stored.Scope)
	assert.Nil(t, stored.TenantID, "a global starter belongs to no tenant")
	require.NotNil(t, stored.CreatedByUserID)
	assert.Equal(t, testUserID, *stored.CreatedByUserID)
	body := w8dBody(t, rec)
	assert.Equal(t, stored.ID.String(), body["id"])
	assert.Equal(t, "global", body["scope"])
	assert.Equal(t, w9cStarterCallerXML, body["bpmn_xml"])
	assert.NotContains(t, body, "tenant_id")
}

func TestStarterCreate_TenantScopeCallingAPublishedModule_Returns201WithTheSubmittedXML(t *testing.T) {
	t.Parallel()
	w := w9cNewStarterWiring(t)
	w9cExpectReviewModule(w.versions, testTenantID)
	var stored *domain.StarterTemplate
	w.starters.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, st *domain.StarterTemplate) error {
			stored = st
			return nil
		})

	rec := do(w.router, adminReq(http.MethodPost, starterCreatePath,
		map[string]any{"name": "Tenant review", "bpmn_xml": w9cStarterCallerXML}))

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.NotNil(t, stored)
	assert.Equal(t, w9cStarterCallerXML, stored.BPMNXML, "the stored text is the submitted one, without the module bundled in")
	assert.NotContains(t, stored.BPMNXML, "Review_Task", "the module's own elements are not copied into the stored text")
	assert.Equal(t, domain.ScopeTenant, stored.Scope)
	require.NotNil(t, stored.TenantID)
	assert.Equal(t, testTenantID, *stored.TenantID)
	body := w8dBody(t, rec)
	assert.Equal(t, stored.ID.String(), body["id"])
	assert.Equal(t, "tenant", body["scope"])
	assert.Equal(t, testTenantID.String(), body["tenant_id"])
	assert.Equal(t, w9cStarterCallerXML, body["bpmn_xml"])
}
