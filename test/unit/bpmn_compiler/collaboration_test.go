package bpmn_compiler_test

import (
	"context"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// minimalCollabBPMN builds a two-pool collaboration BPMN. The consultant pool is
// non-executable (external context only) and the contractor pool is executable.
func minimalCollabBPMN(consultantBody, contractorLanes, contractorBody string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_collab" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="Msg_1" name="msg-trigger"/>
  <bpmn:collaboration id="Collab_1">
    <bpmn:participant id="P_ext" name="External" processRef="Proc_ext"/>
    <bpmn:participant id="P_main" name="Main"     processRef="Proc_main"/>
    <bpmn:messageFlow id="MF_1" sourceRef="Task_send" targetRef="Start_main" messageRef="Msg_1"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_ext" name="External" isExecutable="false">` + consultantBody + `
  </bpmn:process>
  <bpmn:process id="Proc_main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LS_main">` + contractorLanes + `
    </bpmn:laneSet>` + contractorBody + `
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Collab_1">
      <bpmndi:BPMNShape id="Shape_Start_ext"  bpmnElement="Start_ext"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Shape_Task_send"  bpmnElement="Task_send"><dc:Bounds x="250" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Shape_End_ext"    bpmnElement="End_ext"><dc:Bounds x="402" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Shape_Start_main" bpmnElement="Start_main"><dc:Bounds x="152" y="262" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Shape_Task_main"  bpmnElement="Task_main"><dc:Bounds x="250" y="240" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Shape_End_main"   bpmnElement="End_main"><dc:Bounds x="402" y="262" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_FC1" bpmnElement="FC1"/>
      <bpmndi:BPMNEdge id="Edge_FC2" bpmnElement="FC2"/>
      <bpmndi:BPMNEdge id="Edge_FM1" bpmnElement="FM1"/>
      <bpmndi:BPMNEdge id="Edge_FM2" bpmnElement="FM2"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`
}

var collabExtBody = `
    <bpmn:startEvent id="Start_ext"><bpmn:outgoing>FC1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:sendTask id="Task_send" name="Send"><bpmn:extensionElements>
      <zeebe:taskDefinition type="prep"/>
    </bpmn:extensionElements>
    <bpmn:incoming>FC1</bpmn:incoming><bpmn:outgoing>FC2</bpmn:outgoing></bpmn:sendTask>
    <bpmn:endEvent id="End_ext"><bpmn:incoming>FC2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="FC1" sourceRef="Start_ext" targetRef="Task_send"/>
    <bpmn:sequenceFlow id="FC2" sourceRef="Task_send"  targetRef="End_ext"/>`

var collabMainLanes = `
      <bpmn:lane id="Lane_main" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="92fd92cf-4b2c-58dc-bf98-f3d0af729876"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_main</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_main</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_main</bpmn:flowNodeRef>
      </bpmn:lane>`

var collabMainBody = `
    <bpmn:startEvent id="Start_main" name="Start">
      <bpmn:messageEventDefinition messageRef="Msg_1"/>
      <bpmn:outgoing>FM1</bpmn:outgoing>
    </bpmn:startEvent>
    <bpmn:userTask id="Task_main" name="Main Task">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FM1</bpmn:incoming>
      <bpmn:outgoing>FM2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="End_main" name="End"><bpmn:incoming>FM2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="FM1" sourceRef="Start_main" targetRef="Task_main"/>
    <bpmn:sequenceFlow id="FM2" sourceRef="Task_main"  targetRef="End_main"/>`

func TestValidate_Collaboration_Valid(t *testing.T) {
	bpmnXML := minimalCollabBPMN(collabExtBody, collabMainLanes, collabMainBody)
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("expected no errors for valid collaboration; got %v", errCodes(errs))
	}
}

func TestValidate_Collaboration_UnknownMessageRef(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:collaboration id="Collab_1">
    <bpmn:participant id="P_a" processRef="Proc_a"/>
    <bpmn:participant id="P_b" processRef="Proc_b"/>
    <bpmn:messageFlow id="MF_1" sourceRef="P_a" targetRef="P_b" messageRef="Msg_unknown"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_a" name="A" isExecutable="true">
    <bpmn:laneSet id="LS_a"><bpmn:lane id="L_a" name="l"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="207141b1-baba-5be9-9c4c-fda8b6fa0eaa"/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>S_a</bpmn:flowNodeRef><bpmn:flowNodeRef>E_a</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S_a"><bpmn:outgoing>F_a</bpmn:outgoing></bpmn:startEvent>
    <bpmn:endEvent id="E_a"><bpmn:incoming>F_a</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F_a" sourceRef="S_a" targetRef="E_a"/>
  </bpmn:process>
  <bpmn:process id="Proc_b" name="B" isExecutable="true"><bpmn:extensionElements><zeebe:properties><zeebe:property name="ignore" value="true"/></zeebe:properties></bpmn:extensionElements>
    <bpmn:laneSet id="LS_b"><bpmn:lane id="L_b" name="l"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="a6fa83e8-b796-501d-9b76-5dbf249271df"/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>S_b</bpmn:flowNodeRef><bpmn:flowNodeRef>E_b</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S_b"><bpmn:outgoing>F_b</bpmn:outgoing></bpmn:startEvent>
    <bpmn:endEvent id="E_b"><bpmn:incoming>F_b</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F_b" sourceRef="S_b" targetRef="E_b"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1"><bpmndi:BPMNPlane id="P_1" bpmnElement="Collab_1"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrMissingMessageDefinition) {
		t.Errorf("expected MISSING_MESSAGE_DEFINITION; got %v", errCodes(errs))
	}
}

func TestValidate_Collaboration_UnmatchedMessageFlow_UnknownSource(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="Msg_1" name="msg"/>
  <bpmn:collaboration id="Collab_1">
    <bpmn:participant id="P_a" processRef="Proc_a"/>
    <bpmn:participant id="P_b" processRef="Proc_b"/>
    <bpmn:messageFlow id="MF_1" sourceRef="NoSuchNode" targetRef="P_b" messageRef="Msg_1"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_a" name="A" isExecutable="true">
    <bpmn:laneSet id="LS_a"><bpmn:lane id="L_a" name="l"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="207141b1-baba-5be9-9c4c-fda8b6fa0eaa"/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>S_a</bpmn:flowNodeRef><bpmn:flowNodeRef>E_a</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S_a"><bpmn:outgoing>F_a</bpmn:outgoing></bpmn:startEvent>
    <bpmn:endEvent id="E_a"><bpmn:incoming>F_a</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F_a" sourceRef="S_a" targetRef="E_a"/>
  </bpmn:process>
  <bpmn:process id="Proc_b" name="B" isExecutable="true"><bpmn:extensionElements><zeebe:properties><zeebe:property name="ignore" value="true"/></zeebe:properties></bpmn:extensionElements>
    <bpmn:laneSet id="LS_b"><bpmn:lane id="L_b" name="l"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="a6fa83e8-b796-501d-9b76-5dbf249271df"/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>S_b</bpmn:flowNodeRef><bpmn:flowNodeRef>E_b</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S_b"><bpmn:outgoing>F_b</bpmn:outgoing></bpmn:startEvent>
    <bpmn:endEvent id="E_b"><bpmn:incoming>F_b</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F_b" sourceRef="S_b" targetRef="E_b"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1"><bpmndi:BPMNPlane id="P_1" bpmnElement="Collab_1"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrUnmatchedMessageFlow) {
		t.Errorf("expected UNMATCHED_MESSAGE_FLOW for unknown sourceRef; got %v", errCodes(errs))
	}
}

func TestValidate_Collaboration_MessageFlowToFlowNode(t *testing.T) {
	// messageFlow sourceRef/targetRef may point to specific tasks/events (not only participants).
	bpmnXML := minimalCollabBPMN(collabExtBody, collabMainLanes, collabMainBody)
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if hasCode(errs, domain.BPMNErrUnmatchedMessageFlow) {
		t.Errorf("expected no UNMATCHED_MESSAGE_FLOW when pointing to a flow node; got %v", errCodes(errs))
	}
}

func TestValidate_Collaboration_MessageFlowUnresolvableName_Warns(t *testing.T) {
	// messageFlow has no name, no messageRef, and neither connected node carries
	// a messageRef, and its target is a receive task that waits — so it should warn
	// (not error) since the flow/source/target refs are otherwise all valid.
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:collaboration id="Collab_1">
    <bpmn:participant id="P_a" processRef="Proc_a"/>
    <bpmn:participant id="P_b" processRef="Proc_b"/>
    <bpmn:messageFlow id="MF_1" sourceRef="S_a" targetRef="R_b"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_a" name="A" isExecutable="true"><bpmn:extensionElements><zeebe:properties><zeebe:property name="ignore" value="true"/></zeebe:properties></bpmn:extensionElements>
    <bpmn:laneSet id="LS_a"><bpmn:lane id="L_a" name="l"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="207141b1-baba-5be9-9c4c-fda8b6fa0eaa"/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>S_a</bpmn:flowNodeRef><bpmn:flowNodeRef>E_a</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S_a"><bpmn:outgoing>F_a</bpmn:outgoing></bpmn:startEvent>
    <bpmn:endEvent id="E_a"><bpmn:incoming>F_a</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F_a" sourceRef="S_a" targetRef="E_a"/>
  </bpmn:process>
  <bpmn:process id="Proc_b" name="B" isExecutable="true">
    <bpmn:laneSet id="LS_b"><bpmn:lane id="L_b" name="l"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="a6fa83e8-b796-501d-9b76-5dbf249271df"/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>S_b</bpmn:flowNodeRef><bpmn:flowNodeRef>R_b</bpmn:flowNodeRef><bpmn:flowNodeRef>E_b</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S_b"/>
    <bpmn:receiveTask id="R_b"/>
    <bpmn:endEvent id="E_b"/>
    <bpmn:sequenceFlow id="F_b" sourceRef="S_b" targetRef="R_b"/>
    <bpmn:sequenceFlow id="F_b2" sourceRef="R_b" targetRef="E_b"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1"><bpmndi:BPMNPlane id="P_1" bpmnElement="Collab_1"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	var found bool
	for _, e := range errs {
		if e.Code == domain.BPMNErrMissingMessageDefinition && e.Severity == domain.SeverityWarning {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a warning-severity MISSING_MESSAGE_DEFINITION for unresolvable message flow name; got %v", errCodes(errs))
	}
}

func TestValidate_Collaboration_MessageFlowNameViaConnectedNode_NoWarn(t *testing.T) {
	// messageFlow itself has no name/messageRef, but the target boundary event's
	// messageEventDefinition resolves it — should not warn.
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="Msg_1" name="notify"/>
  <bpmn:collaboration id="Collab_1">
    <bpmn:participant id="P_a" processRef="Proc_a"/>
    <bpmn:participant id="P_b" processRef="Proc_b"/>
    <bpmn:messageFlow id="MF_1" sourceRef="S_a" targetRef="BE_b"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_a" name="A" isExecutable="true">
    <bpmn:laneSet id="LS_a"><bpmn:lane id="L_a" name="l"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="207141b1-baba-5be9-9c4c-fda8b6fa0eaa"/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>S_a</bpmn:flowNodeRef><bpmn:flowNodeRef>E_a</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S_a"><bpmn:outgoing>F_a</bpmn:outgoing></bpmn:startEvent>
    <bpmn:endEvent id="E_a"><bpmn:incoming>F_a</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F_a" sourceRef="S_a" targetRef="E_a"/>
  </bpmn:process>
  <bpmn:process id="Proc_b" name="B" isExecutable="true"><bpmn:extensionElements><zeebe:properties><zeebe:property name="ignore" value="true"/></zeebe:properties></bpmn:extensionElements>
    <bpmn:laneSet id="LS_b"><bpmn:lane id="L_b" name="l"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="a6fa83e8-b796-501d-9b76-5dbf249271df"/></zeebe:properties></bpmn:extensionElements>
      <bpmn:flowNodeRef>S_b</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>T_b</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>E_b</bpmn:flowNodeRef>
    </bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S_b"><bpmn:outgoing>F_b1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="T_b" name="Task">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="l" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
      <bpmn:incoming>F_b1</bpmn:incoming>
      <bpmn:outgoing>F_b2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:boundaryEvent id="BE_b" attachedToRef="T_b">
      <bpmn:messageEventDefinition messageRef="Msg_1"/>
    </bpmn:boundaryEvent>
    <bpmn:endEvent id="E_b"><bpmn:incoming>F_b2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F_b1" sourceRef="S_b" targetRef="T_b"/>
    <bpmn:sequenceFlow id="F_b2" sourceRef="T_b" targetRef="E_b"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1"><bpmndi:BPMNPlane id="P_1" bpmnElement="Collab_1"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if hasCode(errs, domain.BPMNErrMissingMessageDefinition) {
		t.Errorf("expected no MISSING_MESSAGE_DEFINITION when name resolves via connected node; got %v", errCodes(errs))
	}
}

func TestValidate_NonExecutableProcess_NoTaskExtensionErrors(t *testing.T) {
	// A non-executable pool's tasks are NOT required to have taskDefinition/assignmentDefinition.
	bpmnXML := minimalCollabBPMN(collabExtBody, collabMainLanes, collabMainBody)
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if hasCode(errs, domain.BPMNErrMissingAssignmentDefinition) || hasCode(errs, domain.BPMNErrTaskNotInLane) {
		t.Errorf("non-executable pool should not trigger task-extension errors; got %v", errCodes(errs))
	}
}

func TestValidate_SendTask_NotInLane_Error(t *testing.T) {
	// sendTask not listed in any lane's flowNodeRef produces TASK_NOT_IN_LANE.
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LS">
      <bpmn:lane id="L1" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="bd056344-b10d-5b31-8903-9f98dea26c6b"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>E1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="S1"><bpmn:outgoing>F1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:sendTask id="ST1" name="Send">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
      </bpmn:extensionElements>
      <bpmn:incoming>F1</bpmn:incoming><bpmn:outgoing>F2</bpmn:outgoing>
    </bpmn:sendTask>
    <bpmn:endEvent id="E1"><bpmn:incoming>F2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F1" sourceRef="S1"  targetRef="ST1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="ST1" targetRef="E1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD"><bpmndi:BPMNPlane id="BP" bpmnElement="P1"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrTaskNotInLane) {
		t.Errorf("expected TASK_NOT_IN_LANE for sendTask not in any lane; got %v", errCodes(errs))
	}
}

func TestValidate_SendTask_NoTaskDefRequired(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LS">
      <bpmn:lane id="L1" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="bd056344-b10d-5b31-8903-9f98dea26c6b"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>ST1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>E1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="S1"><bpmn:outgoing>F1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:sendTask id="ST1" name="Send">
      <bpmn:incoming>F1</bpmn:incoming><bpmn:outgoing>F2</bpmn:outgoing>
    </bpmn:sendTask>
    <bpmn:endEvent id="E1"><bpmn:incoming>F2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F1" sourceRef="S1"  targetRef="ST1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="ST1" targetRef="E1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD"><bpmndi:BPMNPlane id="BP" bpmnElement="P1"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if hasCode(errs, domain.BPMNErrMissingTaskDefinition) {
		t.Errorf("sendTask without taskDef should not produce MISSING_TASK_DEFINITION; got %v", errCodes(errs))
	}
}

func TestValidate_Collaboration_MissingDiagram(t *testing.T) {
	// A collaboration without a BPMNDiagram element must produce MISSING_DIAGRAM.
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:collaboration id="Collab_1">
    <bpmn:participant id="P_a" processRef="Proc_a"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_a" name="A" isExecutable="true">
    <bpmn:laneSet id="LS"><bpmn:lane id="L" name="l"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="c3e61f3a-a879-5734-8bc7-de880d2bb9d9"/></zeebe:properties></bpmn:extensionElements>
      <bpmn:flowNodeRef>S1</bpmn:flowNodeRef><bpmn:flowNodeRef>E1</bpmn:flowNodeRef>
    </bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S1"><bpmn:outgoing>F1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:endEvent id="E1"><bpmn:incoming>F1</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F1" sourceRef="S1" targetRef="E1"/>
  </bpmn:process>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrMissingDiagram) {
		t.Errorf("expected MISSING_DIAGRAM; got %v", errCodes(errs))
	}
}

func TestWithStageType_CustomStagePreventsInvalidTaskDefError(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LS">
      <bpmn:lane id="L1" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="bd056344-b10d-5b31-8903-9f98dea26c6b"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>T1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>E1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="S1"><bpmn:outgoing>F1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="T1" name="Task">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="special"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
      <bpmn:incoming>F1</bpmn:incoming><bpmn:outgoing>F2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="E1"><bpmn:incoming>F2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F1" sourceRef="S1" targetRef="T1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="T1" targetRef="E1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD"><bpmndi:BPMNPlane id="BP" bpmnElement="P1"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	c := bpmn_compiler.NewCompiler(bpmn_compiler.WithStageType(&testStageType{id: "special", activity: "SpecialActivity"}))
	errs, err := c.Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if hasCode(errs, domain.BPMNErrInvalidTaskDefinitionType) {
		t.Errorf("custom stage type 'special' should not produce INVALID_TASK_DEFINITION_TYPE; got %v", errCodes(errs))
	}
}

type testStageType struct{ id, activity string }

func (s *testStageType) ID() string           { return s.id }
func (s *testStageType) ActivityName() string { return s.activity }
func (s *testStageType) ValidateProps(_ string, _ map[string]string) []domain.BPMNValidationError {
	return nil
}

// ─── All-branches-terminate exclusive gateway ────────────────────────────────

// xorAllTerminateContinuationBPMN: XOR with one branch going to End (early exit)
// and one branch going to a continuation task, then End. No join gateway.
const xorAllTerminateContinuationBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_xor1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P_xor1" name="BidNoBid" isExecutable="true">
    <bpmn:laneSet id="LS_xor1">
      <bpmn:lane id="Lane_tender" name="tender"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="4417b1ea-a41e-5115-ad6d-a6a214bb7b07"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_x1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_eval</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>XOR_bid</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_proceed</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_nobid</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_main</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_x1"><bpmn:outgoing>Fx1_1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="Task_eval" name="Evaluate">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="tender" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
      <bpmn:incoming>Fx1_1</bpmn:incoming><bpmn:outgoing>Fx1_2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:exclusiveGateway id="XOR_bid">
      <bpmn:incoming>Fx1_2</bpmn:incoming>
      <bpmn:outgoing>Fx1_bid</bpmn:outgoing>
      <bpmn:outgoing>Fx1_nobid</bpmn:outgoing>
    </bpmn:exclusiveGateway>
    <bpmn:userTask id="Task_proceed" name="Proceed">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="tender" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
      <bpmn:incoming>Fx1_bid</bpmn:incoming><bpmn:outgoing>Fx1_3</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="End_nobid"><bpmn:incoming>Fx1_nobid</bpmn:incoming></bpmn:endEvent>
    <bpmn:endEvent id="End_main"><bpmn:incoming>Fx1_3</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Fx1_1"     sourceRef="Start_x1"    targetRef="Task_eval"/>
    <bpmn:sequenceFlow id="Fx1_2"     sourceRef="Task_eval"   targetRef="XOR_bid"/>
    <bpmn:sequenceFlow id="Fx1_bid"   sourceRef="XOR_bid"     targetRef="Task_proceed">
      <bpmn:conditionExpression>decision == "bid"</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Fx1_nobid" sourceRef="XOR_bid"     targetRef="End_nobid">
      <bpmn:conditionExpression>decision == "no-bid"</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Fx1_3"     sourceRef="Task_proceed" targetRef="End_main"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD_xor1">
    <bpmndi:BPMNPlane id="BP_xor1" bpmnElement="P_xor1">
      <bpmndi:BPMNShape id="Sh_Start_x1"    bpmnElement="Start_x1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_eval"   bpmnElement="Task_eval"><dc:Bounds x="250" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_XOR_bid"     bpmnElement="XOR_bid"><dc:Bounds x="405" y="75" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_proceed" bpmnElement="Task_proceed"><dc:Bounds x="510" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End_nobid"   bpmnElement="End_nobid"><dc:Bounds x="510" y="182" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End_main"    bpmnElement="End_main"><dc:Bounds x="662" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_Fx1_1"     bpmnElement="Fx1_1"/>
      <bpmndi:BPMNEdge id="Edge_Fx1_2"     bpmnElement="Fx1_2"/>
      <bpmndi:BPMNEdge id="Edge_Fx1_bid"   bpmnElement="Fx1_bid"/>
      <bpmndi:BPMNEdge id="Edge_Fx1_nobid" bpmnElement="Fx1_nobid"/>
      <bpmndi:BPMNEdge id="Edge_Fx1_3"     bpmnElement="Fx1_3"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

// xorAllTerminateAllEndBPMN: XOR where every branch goes directly to an end event.
const xorAllTerminateAllEndBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_xor2" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P_xor2" name="Decision" isExecutable="true">
    <bpmn:laneSet id="LS_xor2">
      <bpmn:lane id="Lane_ops_xor2" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="a10c71ba-dc1a-5691-b491-670e1f62ab2c"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_x2</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_decide</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>XOR_dec</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_yes</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_no</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_x2"><bpmn:outgoing>Fx2_1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="Task_decide" name="Decide">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
      <bpmn:incoming>Fx2_1</bpmn:incoming><bpmn:outgoing>Fx2_2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:exclusiveGateway id="XOR_dec">
      <bpmn:incoming>Fx2_2</bpmn:incoming>
      <bpmn:outgoing>Fx2_yes</bpmn:outgoing>
      <bpmn:outgoing>Fx2_no</bpmn:outgoing>
    </bpmn:exclusiveGateway>
    <bpmn:endEvent id="End_yes"><bpmn:incoming>Fx2_yes</bpmn:incoming></bpmn:endEvent>
    <bpmn:endEvent id="End_no"><bpmn:incoming>Fx2_no</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Fx2_1"   sourceRef="Start_x2"    targetRef="Task_decide"/>
    <bpmn:sequenceFlow id="Fx2_2"   sourceRef="Task_decide" targetRef="XOR_dec"/>
    <bpmn:sequenceFlow id="Fx2_yes" sourceRef="XOR_dec"     targetRef="End_yes">
      <bpmn:conditionExpression>decision == "yes"</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Fx2_no"  sourceRef="XOR_dec"     targetRef="End_no">
      <bpmn:conditionExpression>decision == "no"</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD_xor2">
    <bpmndi:BPMNPlane id="BP_xor2" bpmnElement="P_xor2">
      <bpmndi:BPMNShape id="Sh_Start_x2"   bpmnElement="Start_x2"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_decide" bpmnElement="Task_decide"><dc:Bounds x="250" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_XOR_dec"    bpmnElement="XOR_dec"><dc:Bounds x="405" y="75" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End_yes"    bpmnElement="End_yes"><dc:Bounds x="510" y="62" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End_no"     bpmnElement="End_no"><dc:Bounds x="510" y="162" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_Fx2_1"   bpmnElement="Fx2_1"/>
      <bpmndi:BPMNEdge id="Edge_Fx2_2"   bpmnElement="Fx2_2"/>
      <bpmndi:BPMNEdge id="Edge_Fx2_yes" bpmnElement="Fx2_yes"/>
      <bpmndi:BPMNEdge id="Edge_Fx2_no"  bpmnElement="Fx2_no"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

func TestCompile_AllBranchesTerminateXOR_EachBranchCarriesItsWork(t *testing.T) {
	plan, err := bpmn_compiler.New().Compile(context.Background(), xorAllTerminateContinuationBPMN)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	if len(plan.Execution.Steps) != 2 || len(plan.Execution.Steps[1].Exclusive) != 2 {
		t.Fatalf("steps = %+v, want Sequential[tender] and one exclusive step with 2 branches", plan.Execution.Steps)
	}
	bid, noBid := plan.Execution.Steps[1].Exclusive[0], plan.Execution.Steps[1].Exclusive[1]
	if !bid.Terminates || len(bid.Steps) != 1 || bid.Steps[0].Sequential[0] != "tender~2" {
		t.Errorf("bid branch = %+v, want Task_proceed's segment as its steps, then the end", bid)
	}
	assertMovesTo(t, plan, "tender~2", "tender", "Task_proceed")
	if !noBid.Terminates || len(noBid.Steps) != 0 {
		t.Errorf("no-bid branch = %+v, want no steps and the end", noBid)
	}
}

func TestCompile_AllBranchesTerminateXOR_AllTerminate(t *testing.T) {
	plan, err := bpmn_compiler.New().Compile(context.Background(), xorAllTerminateAllEndBPMN)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	// Expect 2 steps: Sequential["ops"], Exclusive[yes/no both -> ""]
	if len(plan.Execution.Steps) != 2 {
		t.Fatalf("steps: got %d, want 2; steps: %+v", len(plan.Execution.Steps), plan.Execution.Steps)
	}
	xorStep := plan.Execution.Steps[1]
	if len(xorStep.Exclusive) != 2 {
		t.Fatalf("exclusive branches: got %d, want 2", len(xorStep.Exclusive))
	}
	for i, b := range xorStep.Exclusive {
		if b.Target != "" {
			t.Errorf("branch[%d].Target = %q, want empty (all terminate at end events)", i, b.Target)
		}
	}
}

// ─── Receive tasks ───────────────────────────────────────────────────────────

func TestValidate_ReceiveTask_NoTaskDefRequired(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LS">
      <bpmn:lane id="L1" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="bd056344-b10d-5b31-8903-9f98dea26c6b"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>RT1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>E1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="S1"><bpmn:outgoing>F1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:receiveTask id="RT1" name="Receive">
      <bpmn:incoming>F1</bpmn:incoming><bpmn:outgoing>F2</bpmn:outgoing>
    </bpmn:receiveTask>
    <bpmn:endEvent id="E1"><bpmn:incoming>F2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F1" sourceRef="S1"  targetRef="RT1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="RT1" targetRef="E1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD"><bpmndi:BPMNPlane id="BP" bpmnElement="P1"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if hasCode(errs, domain.BPMNErrMissingTaskDefinition) {
		t.Errorf("receiveTask without taskDef should not produce MISSING_TASK_DEFINITION; got %v", errCodes(errs))
	}
}

// TestValidate_Collaboration_TargetRef_UnknownNode verifies that a messageFlow
// targeting an unknown node ID produces UNMATCHED_MESSAGE_FLOW — covers the
// targetRef check in validateCollaboration (sourceRef is tested separately).
func TestValidate_Collaboration_TargetRef_UnknownNode(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_TargetRef" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:collaboration id="C1">
    <bpmn:participant id="P1" name="Sender" processRef="Proc_1"/>
    <bpmn:participant id="P2" name="Receiver" processRef="Proc_2"/>
    <bpmn:messageFlow id="MF_bad" sourceRef="P1" targetRef="NONEXISTENT_ID"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_1" name="Sender" isExecutable="true">
    <bpmn:laneSet id="LS1">
      <bpmn:lane id="L1" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="bd056344-b10d-5b31-8903-9f98dea26c6b"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>E1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="S1"/>
    <bpmn:userTask id="Task_1" name="T1">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="E1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="S1"     targetRef="Task_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_1" targetRef="E1"/>
  </bpmn:process>
  <bpmn:process id="Proc_2" name="Receiver" isExecutable="true"><bpmn:extensionElements><zeebe:properties><zeebe:property name="ignore" value="true"/></zeebe:properties></bpmn:extensionElements>
    <bpmn:laneSet id="LS2">
      <bpmn:lane id="L2" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="63bb1e15-d338-5793-b74d-ba57f73778c4"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>S2</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_2</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>E2</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="S2"/>
    <bpmn:userTask id="Task_2" name="T2">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="E2"/>
    <bpmn:sequenceFlow id="F3" sourceRef="S2"     targetRef="Task_2"/>
    <bpmn:sequenceFlow id="F4" sourceRef="Task_2" targetRef="E2"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD"><bpmndi:BPMNPlane id="BP" bpmnElement="C1"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrUnmatchedMessageFlow) {
		t.Errorf("expected UNMATCHED_MESSAGE_FLOW for unknown targetRef; got %v", errCodes(errs))
	}
}

// TestValidate_InclusiveGateway_SplitJoin_Unsupported verifies that an inclusive
// gateway produces UNSUPPORTED_ELEMENT (Tier 2). The gateway is parsed and
// graph-validated correctly, but has no compile handler and is listed in
// unsupportedBPMNElems to prevent silent incorrect output.
func TestValidate_InclusiveGateway_SplitJoin_Unsupported(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_IncGW" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="InclusiveGW" isExecutable="true">
    <bpmn:laneSet id="LS">
      <bpmn:lane id="L_ops" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="14649f77-3140-51bf-bdc3-9066944b090f"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_A</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_B</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_C</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="S1"/>
    <bpmn:userTask id="Task_A" name="Evaluate">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:inclusiveGateway id="INC_split"/>
    <bpmn:userTask id="Task_B" name="Path B">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:userTask id="Task_C" name="Path C">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:inclusiveGateway id="INC_join"/>
    <bpmn:endEvent id="E1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="S1"        targetRef="Task_A"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_A"    targetRef="INC_split"/>
    <bpmn:sequenceFlow id="F3" sourceRef="INC_split" targetRef="Task_B"/>
    <bpmn:sequenceFlow id="F4" sourceRef="INC_split" targetRef="Task_C"/>
    <bpmn:sequenceFlow id="F5" sourceRef="Task_B"    targetRef="INC_join"/>
    <bpmn:sequenceFlow id="F6" sourceRef="Task_C"    targetRef="INC_join"/>
    <bpmn:sequenceFlow id="F7" sourceRef="INC_join"  targetRef="E1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD">
    <bpmndi:BPMNPlane id="BP" bpmnElement="P1">
      <bpmndi:BPMNShape id="Sh_S1"        bpmnElement="S1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_A"    bpmnElement="Task_A"><dc:Bounds x="250" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_INC_split" bpmnElement="INC_split"><dc:Bounds x="405" y="75" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_B"    bpmnElement="Task_B"><dc:Bounds x="510" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_C"    bpmnElement="Task_C"><dc:Bounds x="510" y="180" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_INC_join"  bpmnElement="INC_join"><dc:Bounds x="665" y="75" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_E1"        bpmnElement="E1"><dc:Bounds x="772" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
      <bpmndi:BPMNEdge id="Edge_F3" bpmnElement="F3"/>
      <bpmndi:BPMNEdge id="Edge_F4" bpmnElement="F4"/>
      <bpmndi:BPMNEdge id="Edge_F5" bpmnElement="F5"/>
      <bpmndi:BPMNEdge id="Edge_F6" bpmnElement="F6"/>
      <bpmndi:BPMNEdge id="Edge_F7" bpmnElement="F7"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrUnsupportedElement) {
		t.Errorf("expected UNSUPPORTED_ELEMENT for inclusive gateway; got %v", errCodes(errs))
	}
}

// TestValidate_Collaboration_ReceiveTask_CollabReachable verifies that a receive task
// in a collaboration process is registered in collabReachableIDs — exercises the
// ReceiveTask loop in collabReachableIDs so a messageFlow targeting it is accepted.
func TestValidate_Collaboration_ReceiveTask_CollabReachable(t *testing.T) {
	// Two-pool collaboration: sender pool has a sendTask, receiver pool has a
	// receiveTask. MessageFlow targets the receiveTask ID (a flow-node endpoint).
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_RcvTask" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:collaboration id="C1">
    <bpmn:participant id="P_sender"   name="Sender"   processRef="Proc_sender"/>
    <bpmn:participant id="P_receiver" name="Receiver" processRef="Proc_receiver"/>
    <bpmn:messageFlow id="MF1" sourceRef="Task_send" targetRef="Task_rcv"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_sender" name="Sender" isExecutable="true">
    <bpmn:laneSet id="LS1">
      <bpmn:lane id="L1" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="bd056344-b10d-5b31-8903-9f98dea26c6b"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_send</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>E1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="S1"/>
    <bpmn:userTask id="Task_send" name="Send RFQ">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="E1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="S1"        targetRef="Task_send"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_send" targetRef="E1"/>
  </bpmn:process>
  <bpmn:process id="Proc_receiver" name="Receiver" isExecutable="true">
    <bpmn:laneSet id="LS2">
      <bpmn:lane id="L2" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="63bb1e15-d338-5793-b74d-ba57f73778c4"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>S2</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_rcv</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>E2</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="S2"/>
    <bpmn:receiveTask id="Task_rcv" name="Receive RFQ">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:receiveTask>
    <bpmn:endEvent id="E2"/>
    <bpmn:sequenceFlow id="F3" sourceRef="S2"       targetRef="Task_rcv"/>
    <bpmn:sequenceFlow id="F4" sourceRef="Task_rcv" targetRef="E2"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD"><bpmndi:BPMNPlane id="BP" bpmnElement="C1"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if hasCode(errs, domain.BPMNErrUnmatchedMessageFlow) {
		t.Errorf("receiveTask should be a valid messageFlow target; got UNMATCHED_MESSAGE_FLOW in %v", errCodes(errs))
	}
}
