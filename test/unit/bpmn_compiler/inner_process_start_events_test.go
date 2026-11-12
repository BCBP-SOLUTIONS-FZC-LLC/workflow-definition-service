package bpmn_compiler_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func tqSubProcessBPMN(innerStarts []string) string {
	starts := ""
	flows := ""
	refs := ""
	shapes := ""
	for _, id := range innerStarts {
		shapes += fmt.Sprintf(`<bpmndi:BPMNShape id="Sh_%[1]s" bpmnElement="%[1]s"/><bpmndi:BPMNEdge id="Ed_Flow_%[1]s" bpmnElement="Flow_%[1]s"/>`, id)
		starts += `<bpmn:startEvent id="` + id + `"/>`
		flows += `<bpmn:sequenceFlow id="Flow_` + id + `" sourceRef="` + id + `" targetRef="Sub_Task"/>`
		refs += fmt.Sprintf(`<bpmn:flowNodeRef>%s</bpmn:flowNodeRef>`, id)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Main_Start</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Sub_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Main_End</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Main_Start"/>
    <bpmn:subProcess id="Sub_1" name="Inner">
      <bpmn:laneSet id="InnerLS">
        <bpmn:lane id="Inner_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="07347272-d6d5-5ddc-a9b6-2ec6b07318e4"/></zeebe:properties></bpmn:extensionElements>
          %[2]s
          <bpmn:flowNodeRef>Sub_Task</bpmn:flowNodeRef>
          <bpmn:flowNodeRef>Sub_End</bpmn:flowNodeRef>
        </bpmn:lane>
      </bpmn:laneSet>
      %[1]s
      <bpmn:userTask id="Sub_Task">
        <bpmn:extensionElements>
          <zeebe:taskDefinition type="prep"/>
          <zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
        </bpmn:extensionElements>
      </bpmn:userTask>
      <bpmn:endEvent id="Sub_End"/>
      %[3]s
      <bpmn:sequenceFlow id="Flow_task_end" sourceRef="Sub_Task" targetRef="Sub_End"/>
    </bpmn:subProcess>
    <bpmn:endEvent id="Main_End"/>
    <bpmn:sequenceFlow id="Flow_m1" sourceRef="Main_Start" targetRef="Sub_1"/>
    <bpmn:sequenceFlow id="Flow_m2" sourceRef="Sub_1" targetRef="Main_End"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1">
      <bpmndi:BPMNShape id="Sh_Main_Start" bpmnElement="Main_Start"/>
      <bpmndi:BPMNShape id="Sh_Sub_1" bpmnElement="Sub_1"/>
      <bpmndi:BPMNShape id="Sh_Main_End" bpmnElement="Main_End"/>
      <bpmndi:BPMNShape id="Sh_Sub_Task" bpmnElement="Sub_Task"/>
      <bpmndi:BPMNShape id="Sh_Sub_End" bpmnElement="Sub_End"/>
      <bpmndi:BPMNEdge id="Ed_Flow_m1" bpmnElement="Flow_m1"/>
      <bpmndi:BPMNEdge id="Ed_Flow_m2" bpmnElement="Flow_m2"/>
      <bpmndi:BPMNEdge id="Ed_Flow_task_end" bpmnElement="Flow_task_end"/>
      %[4]s
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`, starts, refs, flows, shapes)
}

func tqModuleStartsBPMN(moduleStarts []string) string {
	starts := ""
	flows := ""
	for _, id := range moduleStarts {
		starts += `<bpmn:startEvent id="` + id + `"/>`
		flows += `<bpmn:sequenceFlow id="Flow_` + id + `" sourceRef="` + id + `" targetRef="Mod_Task"/>`
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Main_Start</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Call_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Main_End</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Main_Start"/>
    <bpmn:callActivity id="Call_1">
      <bpmn:extensionElements><zeebe:calledElement processId="Mod_1"/></bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="Main_End"/>
    <bpmn:sequenceFlow id="Flow_m1" sourceRef="Main_Start" targetRef="Call_1"/>
    <bpmn:sequenceFlow id="Flow_m2" sourceRef="Call_1" targetRef="Main_End"/>
  </bpmn:process>
  <bpmn:process id="Mod_1" isExecutable="true">
    %[1]s
    <bpmn:userTask id="Mod_Task">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="Mod_End"/>
    %[2]s
    <bpmn:sequenceFlow id="Flow_task_end" sourceRef="Mod_Task" targetRef="Mod_End"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1"/>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`, starts, flows)
}

func tqErrorsWithCode(errs []domain.BPMNValidationError, code domain.BPMNErrorCode) []domain.BPMNValidationError {
	var out []domain.BPMNValidationError
	for _, e := range errs {
		if e.Code == code {
			out = append(out, e)
		}
	}
	return out
}

func TestValidate_InnerProcessStartEvents_RefusedByValidation(t *testing.T) {
	cases := []struct {
		name     string
		bpmn     string
		wantCode domain.BPMNErrorCode
		wantNode string
	}{
		{name: "sub-process body with two start events", bpmn: tqSubProcessBPMN([]string{"Sub_S1", "Sub_S2"}), wantCode: domain.BPMNErrMultipleStartEvents, wantNode: "Sub_S2"},
		{name: "sub-process body with three start events", bpmn: tqSubProcessBPMN([]string{"Sub_S1", "Sub_S2", "Sub_S3"}), wantCode: domain.BPMNErrMultipleStartEvents, wantNode: "Sub_S2"},
		{name: "sub-process body with no start event", bpmn: tqSubProcessBPMN(nil), wantCode: domain.BPMNErrNoStartEvent, wantNode: "Sub_1"},
		{name: "called module with two start events", bpmn: tqModuleStartsBPMN([]string{"Mod_S1", "Mod_S2"}), wantCode: domain.BPMNErrMultipleStartEvents, wantNode: "Mod_S2"},
		{name: "called module with no start event", bpmn: tqModuleStartsBPMN(nil), wantCode: domain.BPMNErrNoStartEvent, wantNode: "Call_1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := bpmn_compiler.New()

			issues, err := c.Validate(context.Background(), tc.bpmn)
			require.NoError(t, err)
			got := tqErrorsWithCode(issues, tc.wantCode)
			require.NotEmpty(t, got, "issues: %v", issues)
			assert.Equal(t, tc.wantNode, got[0].NodeID)
			assert.Equal(t, domain.SeverityError, got[0].Severity)
			other := domain.BPMNErrMultipleStartEvents
			if tc.wantCode == domain.BPMNErrMultipleStartEvents {
				other = domain.BPMNErrNoStartEvent
			}
			assert.Empty(t, tqErrorsWithCode(issues, other))

			_, err = c.Compile(context.Background(), tc.bpmn)
			var failed *domain.ValidationFailedError
			require.True(t, errors.As(err, &failed), "Compile err = %v, want a ValidationFailedError", err)
			assert.NotEmpty(t, tqErrorsWithCode(failed.Errors, tc.wantCode))
		})
	}
}

func TestCompile_InnerProcessWithOneStartEvent_TraversesFromThatStart(t *testing.T) {
	plan, err := bpmn_compiler.New().Compile(context.Background(), tqSubProcessBPMN([]string{"Sub_S1"}))
	require.NoError(t, err)

	var sub *dsl.SubWorkflowStep
	for i := range plan.Execution.Steps {
		if plan.Execution.Steps[i].SubWorkflow != nil {
			sub = plan.Execution.Steps[i].SubWorkflow
		}
	}
	require.NotNil(t, sub, "the sub-process must compile into a sub-workflow step")
	assert.Equal(t, "Sub_1", sub.NodeID)
	require.Len(t, sub.Plan.Steps, 1)
	assert.Equal(t, []string{"design"}, sub.Plan.Steps[0].Sequential)
	var stageIDs []string
	for _, dept := range plan.Departments {
		for _, st := range dept.Stages {
			stageIDs = append(stageIDs, st.NodeID)
		}
	}
	assert.Equal(t, []string{"Sub_Task"}, stageIDs)
}
