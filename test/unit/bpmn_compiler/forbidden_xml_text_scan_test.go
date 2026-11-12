package bpmn_compiler_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func tqDocumentedBPMN(inProcess, beforeProcess string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  %[2]s
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    %[1]s
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:userTask id="Task_1" name="Prep">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_1" targetRef="EndEvent_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1">
      <bpmndi:BPMNShape id="Sh_Start" bpmnElement="StartEvent_1"/>
      <bpmndi:BPMNShape id="Sh_Task" bpmnElement="Task_1"/>
      <bpmndi:BPMNShape id="Sh_End" bpmnElement="EndEvent_1"/>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`, inProcess, beforeProcess)
}

func TestValidate_ForbiddenMarkerAsPlainText_IsRefusedAsForbiddenXML(t *testing.T) {
	cases := []struct {
		name          string
		inProcess     string
		beforeProcess string
		wantForbidden bool
	}{
		{name: "control: documentation without a marker", inProcess: `<bpmn:documentation>Prepares the order.</bpmn:documentation>`},
		{name: "DOCTYPE quoted inside a CDATA section", inProcess: `<bpmn:documentation><![CDATA[<!DOCTYPE html>]]></bpmn:documentation>`, wantForbidden: true},
		{name: "ENTITY quoted inside a CDATA section", inProcess: `<bpmn:documentation><![CDATA[<!ENTITY x "y">]]></bpmn:documentation>`, wantForbidden: true},
		{name: "DOCTYPE inside an XML comment", beforeProcess: `<!-- <!DOCTYPE html> -->`, wantForbidden: true},
		{name: "ENTITY inside an XML comment", inProcess: `<!-- <!ENTITY x "y"> -->`, wantForbidden: true},
		{name: "lower-case doctype inside a comment", beforeProcess: `<!-- <!doctype html> -->`, wantForbidden: true},
		{name: "mixed-case entity inside a CDATA section", inProcess: `<bpmn:documentation><![CDATA[<!EnTiTy x "y">]]></bpmn:documentation>`, wantForbidden: true},
		{name: "marker without the opening angle bracket is not a declaration", inProcess: `<bpmn:documentation>write !DOCTYPE and !ENTITY as plain words</bpmn:documentation>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bpmn := tqDocumentedBPMN(tc.inProcess, tc.beforeProcess)
			c := bpmn_compiler.New()

			issues, validateErr := c.Validate(context.Background(), bpmn)
			_, compileErr := c.Compile(context.Background(), bpmn)

			if !tc.wantForbidden {
				require.NoError(t, validateErr)
				assert.Empty(t, issues)
				require.NoError(t, compileErr)
				return
			}
			for _, err := range []error{validateErr, compileErr} {
				require.Error(t, err)
				assert.ErrorIs(t, err, domain.ErrForbiddenXML)
				assert.ErrorIs(t, err, domain.ErrMalformedBPMN)
			}
			assert.Empty(t, issues)
		})
	}
}
