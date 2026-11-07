package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const sgbLanedModuleBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_Sgb" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Sgb" name="Sgb" isExecutable="true">
    <bpmn:laneSet id="Sgb_LaneSet">
      <bpmn:lane id="Lane_Author" name="Author">
        <bpmn:flowNodeRef>Sgb_Start</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Sgb_Draft</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Sgb_Revise</bpmn:flowNodeRef>
      </bpmn:lane>
      <bpmn:lane id="Lane_Quality" name="Quality"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="6f1c0e52-3b1d-5d7e-9a3c-0c4b8e2f1a77"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Sgb_Approve</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Sgb_End</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Sgb_Start"/>
    <bpmn:userTask id="Sgb_Draft" name="Draft">
      <bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparator"/></bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:userTask id="Sgb_Revise" name="Revise">
      <bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparator"/></bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:userTask id="Sgb_Approve" name="Approve">
      <bpmn:extensionElements><zeebe:taskDefinition type="approve"/><zeebe:assignmentDefinition candidateGroups="approver"/></bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="Sgb_End"/>
    <bpmn:sequenceFlow id="Sgb_F1" sourceRef="Sgb_Start" targetRef="Sgb_Draft"/>
    <bpmn:sequenceFlow id="Sgb_F2" sourceRef="Sgb_Draft" targetRef="Sgb_Revise"/>
    <bpmn:sequenceFlow id="Sgb_F3" sourceRef="Sgb_Revise" targetRef="Sgb_Approve"/>
    <bpmn:sequenceFlow id="Sgb_F4" sourceRef="Sgb_Approve" targetRef="Sgb_End"/>
  </bpmn:process>
</bpmn:definitions>`

func TestGetModuleContract_RealService_VersionStatusDecidesTheNumberNotTheRead(t *testing.T) {
	t.Parallel()

	number := int32(4)
	cases := []struct {
		name       string
		status     domain.VersionStatus
		number     *int32
		wantNumber bool
	}{
		{name: "draft has no version number", status: domain.VersionStatusDraft},
		{name: "published carries its number", status: domain.VersionStatusPublished, number: &number, wantNumber: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			versions := mocks.NewMockModuleVersionRepository(ctrl)
			versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(&domain.ModuleVersion{
				ID: testVerID, ModuleID: testWFID, TenantID: &testTenantID, Scope: domain.ScopeTenant,
				ProcessID: "Process_Sgb", VersionNumber: tc.number, Status: tc.status, BPMNXML: sgbLanedModuleBPMN,
			}, nil)
			svc := service.NewModuleService(service.ModuleDeps{
				Versions:       versions,
				GlobalVersions: mocks.NewMockModuleVersionRepository(ctrl),
				Compiler:       bpmn_compiler.New(),
			})
			router := newRouter(handler.New(handler.Services{Modules: svc}))

			w := do(router, req(http.MethodGet, "/api/v1/modules/"+testWFID.String()+"/versions/"+testVerID.String()+"/contract", nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			body := decodeProblem(t, w)
			assert.Equal(t, testWFID.String(), body["module_id"])
			assert.Equal(t, testVerID.String(), body["version_id"])
			assert.Equal(t, "Process_Sgb", body["process_id"])
			if tc.wantNumber {
				assert.EqualValues(t, number, body["version_number"])
			} else {
				assert.NotContains(t, body, "version_number")
			}
			assert.Equal(t, []any{
				map[string]any{"id": "Author", "label": "Author", "open": true},
				map[string]any{"id": "Quality", "label": "Quality", "open": false, "iam_department_id": "6f1c0e52-3b1d-5d7e-9a3c-0c4b8e2f1a77"},
			}, body["slots"], "a lane without a department is open, one with a department is fixed to it")
			tasks, isList := body["tasks"].([]any)
			require.True(t, isList)
			ids := make([]string, 0, len(tasks))
			for _, item := range tasks {
				ids = append(ids, item.(map[string]any)["node_id"].(string))
			}
			assert.ElementsMatch(t, []string{"Sgb_Draft", "Sgb_Revise", "Sgb_Approve"}, ids)
			assert.Equal(t, []any{}, body["messages"])
			assert.Equal(t, []any{}, body["error_codes"])
			assert.Equal(t, false, body["names_users"])
		})
	}
}
