package bpmn_compiler_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const sgAOutsidePoolCallingTheRoot = `  <bpmn:process id="Process_Outside" isExecutable="true">
    <bpmn:extensionElements><zeebe:properties><zeebe:property name="ignore" value="true"/></zeebe:properties></bpmn:extensionElements>
    <bpmn:startEvent id="O_S"/>
    <bpmn:callActivity id="O_CA"><bpmn:extensionElements><zeebe:calledElement processId="Process_Main"/></bpmn:extensionElements></bpmn:callActivity>
    <bpmn:endEvent id="O_E"/>
    <bpmn:sequenceFlow id="O_F1" sourceRef="O_S" targetRef="O_CA"/>
    <bpmn:sequenceFlow id="O_F2" sourceRef="O_CA" targetRef="O_E"/>
  </bpmn:process>`

func sgAMainWithTaskInLane(deptID, taskXML string, procs ...string) string {
	dept := ""
	if deptID != "" {
		dept = fmt.Sprintf(`<bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value=%q/></zeebe:properties></bpmn:extensionElements>`, deptID)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_L" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LS"><bpmn:lane id="Lane_ops" name="Ops">%s
      <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef><bpmn:flowNodeRef>Task_A</bpmn:flowNodeRef><bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
    </bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
%s    <bpmn:endEvent id="End_1"/>
%s%s  </bpmn:process>
%s
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="Process_Main">
%s  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`, dept, taskXML, flowXML("F1", "Start_1", "Task_A"), flowXML("F2", "Task_A", "End_1"),
		strings.Join(procs, "\n"), diagramOf("Lane_ops", "Start_1", "Task_A", "End_1", "F1", "F2"))
}

func sgAMainCallingAModuleFromALaneWithoutADepartment(module string) string {
	return strings.Replace(mainWithCalls([]callSite{{ID: "CA_1", Lane: "Ops", Calls: "Process_M"}}, module, sgAOutsidePoolCallingTheRoot),
		fmt.Sprintf(`<zeebe:property name="dept_id" value=%q/>`, opsDeptID), "", 1)
}

func sgACompileRefusal(t *testing.T, bpmnXML string) []domain.BPMNValidationError {
	t.Helper()
	_, err := bpmn_compiler.New().CompileForPublish(context.Background(), bpmnXML)
	var failed *domain.ValidationFailedError
	require.True(t, errors.As(err, &failed), "err = %v, want a ValidationFailedError", err)
	return failed.Errors
}

func TestCompileForPublish_HumanTaskInALaneWithoutADepartment_IsRefusedOnTheTask(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		bpmnXML     string
		wantNode    string
		wantMessage string
	}{
		{
			name:        "top-level task, rejected by the lane check",
			bpmnXML:     sgAMainWithTaskInLane("", userTaskXML("Task_A")),
			wantNode:    "Task_A",
			wantMessage: `lane "Lane_ops" has no dept_id`,
		},
		{
			name:        "top-level task of a root that an outside pool calls, rejected on the compiled plan",
			bpmnXML:     sgAMainWithTaskInLane("", userTaskXML("Task_A"), sgAOutsidePoolCallingTheRoot),
			wantNode:    "Task_A",
			wantMessage: "task Ops/Task_A runs in no IAM department",
		},
		{
			name:        "task of a module run from the lane, rejected on the expanded plan",
			bpmnXML:     sgAMainCallingAModuleFromALaneWithoutADepartment(moduleWithTask("Process_M", validCandidateUser)),
			wantNode:    "CA_1",
			wantMessage: "task CA_1::Process_M/Process_M_Task runs in no IAM department",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			errs := sgACompileRefusal(t, tc.bpmnXML)

			require.Len(t, errs, 1, "errors = %+v", errs)
			assert.Equal(t, domain.BPMNErrMissingDeptID, errs[0].Code)
			assert.Equal(t, tc.wantNode, errs[0].NodeID)
			assert.Equal(t, domain.SeverityError, errs[0].Severity)
			assert.Contains(t, errs[0].Message, tc.wantMessage)

			reported, err := bpmn_compiler.New().ValidateForPublish(context.Background(), tc.bpmnXML)
			require.NoError(t, err)
			got := findCode(blockingErrors(reported), domain.BPMNErrMissingDeptID)
			require.NotNil(t, got, "ValidateForPublish = %+v", reported)
			assert.Equal(t, tc.wantNode, got.NodeID)
		})
	}
}

func TestCompileForPublish_LaneDepartmentGivenToAnOutsideCalledRoot_Publishes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, bpmnXML, department string
	}{
		{"top-level task", sgAMainWithTaskInLane(opsDeptID, userTaskXML("Task_A"), sgAOutsidePoolCallingTheRoot), "Ops"},
		{"module task", mainWithCalls([]callSite{{ID: "CA_1", Lane: "Ops", Calls: "Process_M"}}, moduleWithTask("Process_M", validCandidateUser), sgAOutsidePoolCallingTheRoot), "CA_1::Process_M"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			main := expandedMain(t, compileForPublish(t, tc.bpmnXML))
			assert.Equal(t, opsDeptID, departmentNamed(t, main, tc.department).IAMDepartmentID)
		})
	}
}

func sgAModuleWithSendTask(id string) string {
	return fmt.Sprintf(`  <bpmn:process id=%[1]q isExecutable="true">
    <bpmn:startEvent id="%[1]s_Start"/>
    <bpmn:sendTask id="%[1]s_Send" name="Notify"/>
    <bpmn:endEvent id="%[1]s_End"/>
    <bpmn:sequenceFlow id="%[1]s_F1" sourceRef="%[1]s_Start" targetRef="%[1]s_Send"/>
    <bpmn:sequenceFlow id="%[1]s_F2" sourceRef="%[1]s_Send" targetRef="%[1]s_End"/>
  </bpmn:process>`, id)
}

func TestCompileForPublish_TaskWithNoDepartmentAndNoDefaultUser_ReportsBothCodesOnOneNode(t *testing.T) {
	t.Parallel()
	noCandidates := strings.Replace(userTaskXML("Task_A"), `candidateUsers="`+validCandidateUser+`"`, `candidateUsers=""`, 1)

	cases := []struct {
		name     string
		bpmnXML  string
		wantNode string
		wantTask string
	}{
		{
			name:     "top-level task",
			bpmnXML:  sgAMainWithTaskInLane("", noCandidates, sgAOutsidePoolCallingTheRoot),
			wantNode: "Task_A",
			wantTask: "Ops/Task_A",
		},
		{
			name:     "task of a module run from the lane",
			bpmnXML:  sgAMainCallingAModuleFromALaneWithoutADepartment(moduleWithTask("Process_M", "")),
			wantNode: "CA_1",
			wantTask: "CA_1::Process_M/Process_M_Task",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			errs := sgACompileRefusal(t, tc.bpmnXML)

			require.Len(t, errs, 2, "errors = %+v", errs)
			assert.Equal(t, domain.BPMNErrMissingDeptID, errs[0].Code)
			assert.Equal(t, domain.BPMNErrCandidateUsersEmpty, errs[1].Code)
			for _, e := range errs {
				assert.Equal(t, tc.wantNode, e.NodeID)
				assert.Equal(t, domain.SeverityError, e.Severity)
				assert.Contains(t, e.Message, tc.wantTask)
			}
		})
	}
}

func TestCompileForPublish_TaskWithADefaultUserAndNoDepartment_ReportsOnlyTheDepartment(t *testing.T) {
	t.Parallel()
	bpmnXML := sgAMainCallingAModuleFromALaneWithoutADepartment(moduleWithTask("Process_M", ""))
	bpmnXML = strings.Replace(bpmnXML, `<zeebe:calledElement processId="Process_M"/>`,
		`<zeebe:calledElement processId="Process_M"/><zeebe:ioMapping><zeebe:input source="{&quot;Process_M_Task&quot;:&quot;`+otherUser+`&quot;}" target="Assignees"/></zeebe:ioMapping>`, 1)

	errs := sgACompileRefusal(t, bpmnXML)

	require.Len(t, errs, 1, "errors = %+v", errs)
	assert.Equal(t, domain.BPMNErrMissingDeptID, errs[0].Code)
	assert.Equal(t, "CA_1", errs[0].NodeID)
}

func TestCompileForPublish_ModuleWithNoHumanTaskInALaneWithoutADepartment_Publishes(t *testing.T) {
	t.Parallel()
	bpmnXML := sgAMainCallingAModuleFromALaneWithoutADepartment(sgAModuleWithSendTask("Process_M"))

	main := expandedMain(t, compileForPublish(t, bpmnXML))

	stage := stageNamed(t, main, "Process_M_Send")
	assert.False(t, stage.CreatesHumanTask(), "stage = %+v", stage)
	assert.Empty(t, departmentNamed(t, main, "CA_1::Process_M").IAMDepartmentID)
}
