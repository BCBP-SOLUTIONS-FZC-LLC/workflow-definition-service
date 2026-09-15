package bpmn_compiler_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

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
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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
  <bpmn:process id="Proc_b" name="B" isExecutable="true">
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
  <bpmn:process id="Proc_b" name="B" isExecutable="true">
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
	// a messageRef either — ResolveMessageFlowName returns "", which should warn
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
    <bpmn:messageFlow id="MF_1" sourceRef="S_a" targetRef="S_b"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_a" name="A" isExecutable="true">
    <bpmn:laneSet id="LS_a"><bpmn:lane id="L_a" name="l"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="207141b1-baba-5be9-9c4c-fda8b6fa0eaa"/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>S_a</bpmn:flowNodeRef><bpmn:flowNodeRef>E_a</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S_a"><bpmn:outgoing>F_a</bpmn:outgoing></bpmn:startEvent>
    <bpmn:endEvent id="E_a"><bpmn:incoming>F_a</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F_a" sourceRef="S_a" targetRef="E_a"/>
  </bpmn:process>
  <bpmn:process id="Proc_b" name="B" isExecutable="true">
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
  <bpmn:process id="Proc_b" name="B" isExecutable="true">
    <bpmn:laneSet id="LS_b"><bpmn:lane id="L_b" name="l"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="a6fa83e8-b796-501d-9b76-5dbf249271df"/></zeebe:properties></bpmn:extensionElements>
      <bpmn:flowNodeRef>S_b</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>T_b</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>E_b</bpmn:flowNodeRef>
    </bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S_b"><bpmn:outgoing>F_b1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="T_b" name="Task">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="l" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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

func TestCompileCollaboration_Valid(t *testing.T) {
	bpmnXML := minimalCollabBPMN(collabExtBody, collabMainLanes, collabMainBody)
	collab, err := bpmn_compiler.New().CompileCollaboration(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("CompileCollaboration() error: %v", err)
	}
	if collab == nil {
		t.Fatal("expected non-nil CompiledCollaboration")
	}
	if len(collab.Plans) == 0 {
		t.Error("expected at least one compiled plan")
	}
}

func TestCompileCollaboration_RequiresCollaboration(t *testing.T) {
	// Compile (single-process) BPMN fed to CompileCollaboration must return an error.
	bpmnXML := minimalBPMN(validTask("T1", "design", "prep") + `
    <bpmn:startEvent id="StartEvent_1"><bpmn:outgoing>F1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:endEvent id="EndEvent_1"><bpmn:incoming>F2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_1" targetRef="EndEvent_1"/>`)

	_, err := bpmn_compiler.New().CompileCollaboration(context.Background(), bpmnXML)
	if err == nil {
		t.Fatal("expected error for single-process BPMN; got nil")
	}
	var vfe *domain.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Errorf("expected ValidationFailedError; got %T: %v", err, err)
	}
}

func TestCompileCollaboration_ValidationError(t *testing.T) {
	// A collaboration with a validation failure returns ValidationFailedError.
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:collaboration id="Collab_1">
    <bpmn:participant id="P_a" processRef="Proc_a"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_a" name="A" isExecutable="true">
    <bpmn:laneSet id="LS"><bpmn:lane id="L" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="c3e61f3a-a879-5734-8bc7-de880d2bb9d9"/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>T1</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>
    <bpmn:userTask id="T1" name="T">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="g" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD"><bpmndi:BPMNPlane id="BP" bpmnElement="Collab_1"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	_, err := bpmn_compiler.New().CompileCollaboration(context.Background(), bpmnXML)
	if err == nil {
		t.Fatal("expected error for BPMN missing start event; got nil")
	}
	var vfe *domain.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Errorf("expected ValidationFailedError; got %T: %v", err, err)
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
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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
        <zeebe:assignmentDefinition candidateGroups="tender" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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
        <zeebe:assignmentDefinition candidateGroups="tender" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>Fx1_bid</bpmn:incoming><bpmn:outgoing>Fx1_3</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="End_nobid"><bpmn:incoming>Fx1_nobid</bpmn:incoming></bpmn:endEvent>
    <bpmn:endEvent id="End_main"><bpmn:incoming>Fx1_3</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Fx1_1"     sourceRef="Start_x1"    targetRef="Task_eval"/>
    <bpmn:sequenceFlow id="Fx1_2"     sourceRef="Task_eval"   targetRef="XOR_bid"/>
    <bpmn:sequenceFlow id="Fx1_bid"   sourceRef="XOR_bid"     targetRef="Task_proceed">
      <bpmn:conditionExpression>bid</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Fx1_nobid" sourceRef="XOR_bid"     targetRef="End_nobid">
      <bpmn:conditionExpression>no-bid</bpmn:conditionExpression>
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
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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
      <bpmn:conditionExpression>yes</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Fx2_no"  sourceRef="XOR_dec"     targetRef="End_no">
      <bpmn:conditionExpression>no</bpmn:conditionExpression>
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

func TestCompile_AllBranchesTerminateXOR_WithContinuation(t *testing.T) {
	plan, err := bpmn_compiler.New().Compile(context.Background(), xorAllTerminateContinuationBPMN)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	// Expect 3 steps: Sequential["tender"], Exclusive[bid/no-bid], Sequential["tender"] (proceed)
	if len(plan.Execution.Steps) != 3 {
		t.Fatalf("steps: got %d, want 3; steps: %+v", len(plan.Execution.Steps), plan.Execution.Steps)
	}
	xorStep := plan.Execution.Steps[1]
	if len(xorStep.Exclusive) != 2 {
		t.Fatalf("exclusive branches: got %d, want 2", len(xorStep.Exclusive))
	}
	// One branch targets "tender" (Task_proceed), one is empty (End_nobid early exit).
	var hasTarget, hasEmpty bool
	for _, b := range xorStep.Exclusive {
		if b.Target == "tender" {
			hasTarget = true
		}
		if b.Target == "" {
			hasEmpty = true
		}
	}
	if !hasTarget {
		t.Error("expected an exclusive branch with Target=tender (continuation)")
	}
	if !hasEmpty {
		t.Error("expected an exclusive branch with Target= (early-exit to end event)")
	}
	// Continuation task compiled after the exclusive step.
	lastStep := plan.Execution.Steps[2]
	if len(lastStep.Sequential) == 0 || lastStep.Sequential[0] != "tender" {
		t.Errorf("step[2] should be Sequential[tender] after continuation; got %+v", lastStep)
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

// ─── Collaboration: qualifySteps Parallel + Exclusive coverage ───────────────

// parallelExclusiveCollabBPMN: two-pool collaboration where Reviewer pool has a
// parallel split/join and an exclusive split/join. Exercises qualifySteps Parallel
// (line 682) and Exclusive (line 685) paths via CompileCollaboration.
const parallelExclusiveCollabBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_pe" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="Msg_pe" name="pe-trigger"/>
  <bpmn:collaboration id="Collab_pe">
    <bpmn:participant id="P_iss_pe" name="Issuer"   processRef="Proc_iss_pe"/>
    <bpmn:participant id="P_rev_pe" name="Reviewer" processRef="Proc_rev_pe"/>
    <bpmn:messageFlow id="MF_pe" sourceRef="Task_rfq_pe" targetRef="Start_rev_pe" messageRef="Msg_pe"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_iss_pe" name="Issuer">
    <bpmn:laneSet id="LS_iss_pe">
      <bpmn:lane id="Lane_cli_pe" name="client"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cf7f5730-b8d5-50ce-a147-c96e426b72cd"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_iss_pe</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_rfq_pe</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_iss_pe</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_iss_pe"><bpmn:outgoing>FI_pe1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="Task_rfq_pe" name="Issue RFQ">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="client" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FI_pe1</bpmn:incoming><bpmn:outgoing>FI_pe2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="End_iss_pe"><bpmn:incoming>FI_pe2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="FI_pe1" sourceRef="Start_iss_pe" targetRef="Task_rfq_pe"/>
    <bpmn:sequenceFlow id="FI_pe2" sourceRef="Task_rfq_pe"  targetRef="End_iss_pe"/>
  </bpmn:process>
  <bpmn:process id="Proc_rev_pe" name="Reviewer">
    <bpmn:laneSet id="LS_rev_pe">
      <bpmn:lane id="Lane_ops_pe" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="b6b23a4d-79e2-5b1f-bd6b-e856cfa0963e"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_rev_pe</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_init_pe</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_par_ops</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_appr</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_rej</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_fin_pe</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_rev_pe</bpmn:flowNodeRef>
      </bpmn:lane>
      <bpmn:lane id="Lane_fin_pe" name="finance"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="d1d6a21d-333b-5aec-958c-5ae470f74633"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Task_par_fin</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_rev_pe">
      <bpmn:messageEventDefinition messageRef="Msg_pe"/>
      <bpmn:outgoing>FR_pe1</bpmn:outgoing>
    </bpmn:startEvent>
    <bpmn:userTask id="Task_init_pe" name="Initial">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FR_pe1</bpmn:incoming><bpmn:outgoing>FR_pe2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:parallelGateway id="Par_fork_pe">
      <bpmn:incoming>FR_pe2</bpmn:incoming>
      <bpmn:outgoing>FR_pe3</bpmn:outgoing><bpmn:outgoing>FR_pe4</bpmn:outgoing>
    </bpmn:parallelGateway>
    <bpmn:userTask id="Task_par_ops" name="Ops Review">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FR_pe3</bpmn:incoming><bpmn:outgoing>FR_pe5</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:userTask id="Task_par_fin" name="Finance Review">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="finance" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FR_pe4</bpmn:incoming><bpmn:outgoing>FR_pe6</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:parallelGateway id="Par_join_pe">
      <bpmn:incoming>FR_pe5</bpmn:incoming><bpmn:incoming>FR_pe6</bpmn:incoming>
      <bpmn:outgoing>FR_pe7</bpmn:outgoing>
    </bpmn:parallelGateway>
    <bpmn:exclusiveGateway id="XOR_dec_pe">
      <bpmn:incoming>FR_pe7</bpmn:incoming>
      <bpmn:outgoing>FR_pe8</bpmn:outgoing><bpmn:outgoing>FR_pe9</bpmn:outgoing>
    </bpmn:exclusiveGateway>
    <bpmn:userTask id="Task_appr" name="Approved Path">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="approve"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FR_pe8</bpmn:incoming><bpmn:outgoing>FR_peA</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:userTask id="Task_rej" name="Rejected Path">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FR_pe9</bpmn:incoming><bpmn:outgoing>FR_peB</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:exclusiveGateway id="XOR_join_pe">
      <bpmn:incoming>FR_peA</bpmn:incoming><bpmn:incoming>FR_peB</bpmn:incoming>
      <bpmn:outgoing>FR_peC</bpmn:outgoing>
    </bpmn:exclusiveGateway>
    <bpmn:userTask id="Task_fin_pe" name="Finalize">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FR_peC</bpmn:incoming><bpmn:outgoing>FR_peD</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="End_rev_pe"><bpmn:incoming>FR_peD</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="FR_pe1" sourceRef="Start_rev_pe" targetRef="Task_init_pe"/>
    <bpmn:sequenceFlow id="FR_pe2" sourceRef="Task_init_pe" targetRef="Par_fork_pe"/>
    <bpmn:sequenceFlow id="FR_pe3" sourceRef="Par_fork_pe"  targetRef="Task_par_ops"/>
    <bpmn:sequenceFlow id="FR_pe4" sourceRef="Par_fork_pe"  targetRef="Task_par_fin"/>
    <bpmn:sequenceFlow id="FR_pe5" sourceRef="Task_par_ops" targetRef="Par_join_pe"/>
    <bpmn:sequenceFlow id="FR_pe6" sourceRef="Task_par_fin" targetRef="Par_join_pe"/>
    <bpmn:sequenceFlow id="FR_pe7" sourceRef="Par_join_pe"  targetRef="XOR_dec_pe"/>
    <bpmn:sequenceFlow id="FR_pe8" sourceRef="XOR_dec_pe"   targetRef="Task_appr">
      <bpmn:conditionExpression>approved</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="FR_pe9" sourceRef="XOR_dec_pe"   targetRef="Task_rej">
      <bpmn:conditionExpression>rejected</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="FR_peA" sourceRef="Task_appr"    targetRef="XOR_join_pe"/>
    <bpmn:sequenceFlow id="FR_peB" sourceRef="Task_rej"     targetRef="XOR_join_pe"/>
    <bpmn:sequenceFlow id="FR_peC" sourceRef="XOR_join_pe"  targetRef="Task_fin_pe"/>
    <bpmn:sequenceFlow id="FR_peD" sourceRef="Task_fin_pe"  targetRef="End_rev_pe"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD_pe"><bpmndi:BPMNPlane id="BP_pe" bpmnElement="Collab_pe"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

func TestCompileCollaboration_QualifiesParallelAndExclusiveSteps(t *testing.T) {
	collab, err := bpmn_compiler.New().CompileCollaboration(context.Background(), parallelExclusiveCollabBPMN)
	if err != nil {
		t.Fatalf("CompileCollaboration() error: %v", err)
	}
	// Find the Reviewer plan.
	var reviewer *dsl.CompiledPlan
	for _, p := range collab.Plans {
		if p.Name == "Reviewer" {
			reviewer = p
		}
	}
	if reviewer == nil {
		t.Fatal("expected Reviewer plan in collaboration")
	}
	// Dept IDs must be qualified with "Reviewer/" prefix.
	for _, d := range reviewer.Departments {
		if len(d.ID) < len("Reviewer/") || d.ID[:len("Reviewer/")] != "Reviewer/" {
			t.Errorf("dept %q not qualified with Reviewer/ prefix", d.ID)
		}
	}
	// Must have a Parallel step and an Exclusive step.
	var hasParallel, hasExclusive bool
	for _, step := range reviewer.Execution.Steps {
		if len(step.Parallel) > 0 {
			hasParallel = true
			for _, branch := range step.Parallel {
				id := branch.DeptID
				if len(id) < len("Reviewer/") || id[:len("Reviewer/")] != "Reviewer/" {
					t.Errorf("parallel dept %q not qualified", id)
				}
			}
		}
		if len(step.Exclusive) > 0 {
			hasExclusive = true
			for _, b := range step.Exclusive {
				if b.Target != "" && (len(b.Target) < len("Reviewer/") || b.Target[:len("Reviewer/")] != "Reviewer/") {
					t.Errorf("exclusive branch target %q not qualified", b.Target)
				}
			}
		}
	}
	if !hasParallel {
		t.Error("expected a Parallel step in Reviewer plan")
	}
	if !hasExclusive {
		t.Error("expected an Exclusive step in Reviewer plan")
	}
	// Message flow must resolve plan names correctly.
	if len(collab.Messages) == 0 {
		t.Fatal("expected at least one message flow")
	}
	mf := collab.Messages[0]
	if mf.SourcePlan != "Issuer" {
		t.Errorf("message SourcePlan = %q, want Issuer", mf.SourcePlan)
	}
	if mf.TargetPlan != "Reviewer" {
		t.Errorf("message TargetPlan = %q, want Reviewer", mf.TargetPlan)
	}
}

// ─── Collaboration: qualifySteps SubWorkflow + ErrorPaths + TimerPaths ───────

// subworkflowCollabBPMN: two-pool collaboration where Processor pool has a
// subprocess with an error and timer boundary event. The outer Task_prep has a
// non-interrupting timer boundary leading to Task_escalate (different lane).
// Exercises qualifySteps SubWorkflow (line 689), ErrorPaths (line 692), TimerPaths
// (line 694) and qualifyPlanDepts BoundaryTimer.TargetDept (line 669).
const subworkflowCollabBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_sw" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:error id="Err_sw" errorCode="PROC_REJECTED"/>
  <bpmn:message id="Msg_sw" name="sw-trigger"/>
  <bpmn:collaboration id="Collab_sw">
    <bpmn:participant id="P_iss_sw"  name="Issuer"    processRef="Proc_iss_sw"/>
    <bpmn:participant id="P_proc_sw" name="Processor" processRef="Proc_proc_sw"/>
    <bpmn:messageFlow id="MF_sw" sourceRef="Task_iss_sw" targetRef="Start_proc_sw" messageRef="Msg_sw"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_iss_sw" name="Issuer">
    <bpmn:laneSet id="LS_iss_sw">
      <bpmn:lane id="Lane_iss_sw" name="client"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="8d9a46a9-d049-555e-8b9e-8791ef8116bd"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_iss_sw</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_iss_sw</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_iss_sw</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_iss_sw"><bpmn:outgoing>FIS1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="Task_iss_sw" name="Issue">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="client" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FIS1</bpmn:incoming><bpmn:outgoing>FIS2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="End_iss_sw"><bpmn:incoming>FIS2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="FIS1" sourceRef="Start_iss_sw" targetRef="Task_iss_sw"/>
    <bpmn:sequenceFlow id="FIS2" sourceRef="Task_iss_sw"  targetRef="End_iss_sw"/>
  </bpmn:process>
  <bpmn:process id="Proc_proc_sw" name="Processor">
    <bpmn:laneSet id="LS_proc_sw">
      <bpmn:lane id="Lane_ops_sw" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="25c934d6-c6b6-5892-94ab-88d77191de07"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_proc_sw</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_prep_sw</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>SubProc_sw</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_final_sw</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_proc_sw</bpmn:flowNodeRef>
      </bpmn:lane>
      <bpmn:lane id="Lane_esc_sw" name="escalation"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="4c08a2ca-7e6f-50a7-8ea9-ab77b8342cc2"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Task_escalate_sw</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_esc_sw</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_proc_sw">
      <bpmn:messageEventDefinition messageRef="Msg_sw"/>
      <bpmn:outgoing>FP1</bpmn:outgoing>
    </bpmn:startEvent>
    <bpmn:userTask id="Task_prep_sw" name="Prep">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FP1</bpmn:incoming><bpmn:outgoing>FP2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:boundaryEvent id="BE_timer_prep_sw" attachedToRef="Task_prep_sw" cancelActivity="false">
      <bpmn:timerEventDefinition><bpmn:timeDuration>PT1H</bpmn:timeDuration></bpmn:timerEventDefinition>
      <bpmn:outgoing>FP_bt</bpmn:outgoing>
    </bpmn:boundaryEvent>
    <bpmn:subProcess id="SubProc_sw" name="Review Sub">
      <bpmn:incoming>FP2</bpmn:incoming><bpmn:outgoing>FP3</bpmn:outgoing>
      <bpmn:laneSet id="LS_inner_sw">
        <bpmn:lane id="Lane_inner_sw" name="inner_ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="0fc8e228-772d-5015-9975-a5f5aa09a868"/></zeebe:properties></bpmn:extensionElements>
          <bpmn:flowNodeRef>InnerStart_sw</bpmn:flowNodeRef>
          <bpmn:flowNodeRef>InnerTask_sw</bpmn:flowNodeRef>
          <bpmn:flowNodeRef>InnerEnd_sw</bpmn:flowNodeRef>
        </bpmn:lane>
      </bpmn:laneSet>
      <bpmn:startEvent id="InnerStart_sw"><bpmn:outgoing>FPI1</bpmn:outgoing></bpmn:startEvent>
      <bpmn:userTask id="InnerTask_sw" name="Inner Review">
        <bpmn:extensionElements>
          <zeebe:taskDefinition type="review"/>
          <zeebe:assignmentDefinition candidateGroups="inner_ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
        </bpmn:extensionElements>
        <bpmn:incoming>FPI1</bpmn:incoming><bpmn:outgoing>FPI2</bpmn:outgoing>
      </bpmn:userTask>
      <bpmn:endEvent id="InnerEnd_sw"><bpmn:incoming>FPI2</bpmn:incoming></bpmn:endEvent>
      <bpmn:sequenceFlow id="FPI1" sourceRef="InnerStart_sw" targetRef="InnerTask_sw"/>
      <bpmn:sequenceFlow id="FPI2" sourceRef="InnerTask_sw"  targetRef="InnerEnd_sw"/>
    </bpmn:subProcess>
    <bpmn:boundaryEvent id="BE_error_sp_sw" attachedToRef="SubProc_sw">
      <bpmn:errorEventDefinition errorRef="Err_sw"/>
      <bpmn:outgoing>FP_err</bpmn:outgoing>
    </bpmn:boundaryEvent>
    <bpmn:boundaryEvent id="BE_timer_sp_sw" attachedToRef="SubProc_sw" cancelActivity="false">
      <bpmn:timerEventDefinition><bpmn:timeDuration>PT24H</bpmn:timeDuration></bpmn:timerEventDefinition>
      <bpmn:outgoing>FP_tsp</bpmn:outgoing>
    </bpmn:boundaryEvent>
    <bpmn:userTask id="Task_final_sw" name="Finalize">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FP3</bpmn:incoming><bpmn:outgoing>FP4</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:userTask id="Task_escalate_sw" name="Escalate">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="escalation" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FP_bt</bpmn:incoming><bpmn:outgoing>FP5</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="End_proc_sw"><bpmn:incoming>FP4</bpmn:incoming></bpmn:endEvent>
    <bpmn:endEvent id="End_esc_sw"><bpmn:incoming>FP5</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="FP1"    sourceRef="Start_proc_sw"    targetRef="Task_prep_sw"/>
    <bpmn:sequenceFlow id="FP2"    sourceRef="Task_prep_sw"     targetRef="SubProc_sw"/>
    <bpmn:sequenceFlow id="FP3"    sourceRef="SubProc_sw"       targetRef="Task_final_sw"/>
    <bpmn:sequenceFlow id="FP4"    sourceRef="Task_final_sw"    targetRef="End_proc_sw"/>
    <bpmn:sequenceFlow id="FP_bt"  sourceRef="BE_timer_prep_sw" targetRef="Task_escalate_sw"/>
    <bpmn:sequenceFlow id="FP_err" sourceRef="BE_error_sp_sw"   targetRef="Task_final_sw"/>
    <bpmn:sequenceFlow id="FP_tsp" sourceRef="BE_timer_sp_sw"   targetRef="Task_final_sw"/>
    <bpmn:sequenceFlow id="FP5"    sourceRef="Task_escalate_sw" targetRef="End_esc_sw"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD_sw"><bpmndi:BPMNPlane id="BP_sw" bpmnElement="Collab_sw"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

func TestCompileCollaboration_QualifiesSubworkflowErrorTimerPaths(t *testing.T) {
	collab, err := bpmn_compiler.New().CompileCollaboration(context.Background(), subworkflowCollabBPMN)
	if err != nil {
		t.Fatalf("CompileCollaboration() error: %v", err)
	}
	var processor *dsl.CompiledPlan
	for _, p := range collab.Plans {
		if p.Name == "Processor" {
			processor = p
		}
	}
	if processor == nil {
		t.Fatal("expected Processor plan in collaboration")
	}

	// All dept IDs must carry the "Processor/" prefix.
	for _, d := range processor.Departments {
		if len(d.ID) < len("Processor/") || d.ID[:len("Processor/")] != "Processor/" {
			t.Errorf("dept %q not qualified with Processor/ prefix", d.ID)
		}
	}

	// BoundaryTimer.TargetDept on Task_prep_sw stage must be qualified.
	var foundBT bool
	for _, d := range processor.Departments {
		for _, stage := range d.Stages {
			if stage.BoundaryTimer != nil && stage.BoundaryTimer.TargetDept != "" {
				foundBT = true
				if stage.BoundaryTimer.TargetDept != "Processor/escalation" {
					t.Errorf("BoundaryTimer.TargetDept = %q, want Processor/escalation",
						stage.BoundaryTimer.TargetDept)
				}
			}
		}
	}
	if !foundBT {
		t.Error("expected a stage with qualified BoundaryTimer.TargetDept")
	}

	// Must have a SubWorkflow step with qualified ErrorPaths and TimerPaths.
	var sw *dsl.SubWorkflowStep
	for i := range processor.Execution.Steps {
		if processor.Execution.Steps[i].SubWorkflow != nil {
			sw = processor.Execution.Steps[i].SubWorkflow
			break
		}
	}
	if sw == nil {
		t.Fatal("expected a SubWorkflow step in Processor plan")
	}
	if len(sw.ErrorPaths) == 0 {
		t.Fatal("expected ErrorPaths on SubWorkflow step")
	}
	if sw.ErrorPaths[0].TargetDept != "Processor/ops" {
		t.Errorf("ErrorPaths[0].TargetDept = %q, want Processor/ops", sw.ErrorPaths[0].TargetDept)
	}
	if len(sw.TimerPaths) == 0 {
		t.Fatal("expected TimerPaths on SubWorkflow step")
	}
	if sw.TimerPaths[0].TargetDept != "Processor/ops" {
		t.Errorf("TimerPaths[0].TargetDept = %q, want Processor/ops", sw.TimerPaths[0].TargetDept)
	}
}

// messageBoundaryUserTaskBPMN: two-pool collaboration where a message flow
// targets a message boundary event (messageRef lives on the boundary event's
// messageEventDefinition, not on the messageFlow itself — the real-world
// authoring pattern) attached to a plain userTask. The boundary's escape path
// leads to a task in a different lane that is otherwise unreachable, mirroring
// subworkflowCollabBPMN's timer-boundary escalation shape but for a message
// boundary. Exercises ResolveMessageFlowName (MessageDef.Name/TargetPlan),
// StageDef.BoundaryMessage, MessageBoundaryHandler continuation compilation,
// and qualifyPlanDepts BoundaryMessage.TargetDept.
const messageBoundaryUserTaskBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_mb" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="Msg_mb" name="mb-trigger"/>
  <bpmn:collaboration id="Collab_mb">
    <bpmn:participant id="P_iss_mb"  name="Issuer"    processRef="Proc_iss_mb"/>
    <bpmn:participant id="P_proc_mb" name="Processor" processRef="Proc_proc_mb"/>
    <bpmn:messageFlow id="MF_mb" sourceRef="Task_iss_mb" targetRef="BE_msg_prep_mb"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_iss_mb" name="Issuer">
    <bpmn:laneSet id="LS_iss_mb">
      <bpmn:lane id="Lane_iss_mb" name="client"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="2099679d-bd14-5a0c-9009-5a116f503016"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_iss_mb</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_iss_mb</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_iss_mb</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_iss_mb"><bpmn:outgoing>FIS1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="Task_iss_mb" name="Issue">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="client" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FIS1</bpmn:incoming><bpmn:outgoing>FIS2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="End_iss_mb"><bpmn:incoming>FIS2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="FIS1" sourceRef="Start_iss_mb" targetRef="Task_iss_mb"/>
    <bpmn:sequenceFlow id="FIS2" sourceRef="Task_iss_mb"  targetRef="End_iss_mb"/>
  </bpmn:process>
  <bpmn:process id="Proc_proc_mb" name="Processor">
    <bpmn:laneSet id="LS_proc_mb">
      <bpmn:lane id="Lane_ops_mb" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="895bbb2f-0a7d-5d46-95c1-0794e1b571ab"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_proc_mb</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_prep_mb</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_proc_mb</bpmn:flowNodeRef>
      </bpmn:lane>
      <bpmn:lane id="Lane_esc_mb" name="escalation"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="aeb319c5-0647-5954-b8df-51967797340f"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Task_escalate_mb</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_esc_mb</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_proc_mb"><bpmn:outgoing>FP1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="Task_prep_mb" name="Prep">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FP1</bpmn:incoming><bpmn:outgoing>FP2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:boundaryEvent id="BE_msg_prep_mb" attachedToRef="Task_prep_mb" cancelActivity="false">
      <bpmn:messageEventDefinition id="MED_mb" messageRef="Msg_mb"/>
      <bpmn:outgoing>FP_bm</bpmn:outgoing>
    </bpmn:boundaryEvent>
    <bpmn:userTask id="Task_escalate_mb" name="Escalate">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="escalation" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>FP_bm</bpmn:incoming><bpmn:outgoing>FP3</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="End_proc_mb"><bpmn:incoming>FP2b</bpmn:incoming></bpmn:endEvent>
    <bpmn:endEvent id="End_esc_mb"><bpmn:incoming>FP3</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="FP1"  sourceRef="Start_proc_mb"  targetRef="Task_prep_mb"/>
    <bpmn:sequenceFlow id="FP2b" sourceRef="Task_prep_mb"   targetRef="End_proc_mb"/>
    <bpmn:sequenceFlow id="FP_bm" sourceRef="BE_msg_prep_mb" targetRef="Task_escalate_mb"/>
    <bpmn:sequenceFlow id="FP3"  sourceRef="Task_escalate_mb" targetRef="End_esc_mb"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD_mb"><bpmndi:BPMNPlane id="BP_mb" bpmnElement="Collab_mb"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

func TestCompileCollaboration_MessageBoundaryOnUserTask(t *testing.T) {
	collab, err := bpmn_compiler.New().CompileCollaboration(context.Background(), messageBoundaryUserTaskBPMN)
	if err != nil {
		t.Fatalf("CompileCollaboration() error: %v", err)
	}

	// Finding 1: MessageDef.Name/TargetPlan must resolve even though the
	// messageFlow targets a boundary event and carries neither name nor
	// messageRef itself — the messageRef lives on the boundary event.
	if len(collab.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(collab.Messages))
	}
	if collab.Messages[0].Name != "mb-trigger" {
		t.Errorf("Messages[0].Name = %q, want mb-trigger", collab.Messages[0].Name)
	}
	if collab.Messages[0].TargetPlan != "Processor" {
		t.Errorf("Messages[0].TargetPlan = %q, want Processor", collab.Messages[0].TargetPlan)
	}

	var processor *dsl.CompiledPlan
	for _, p := range collab.Plans {
		if p.Name == "Processor" {
			processor = p
		}
	}
	if processor == nil {
		t.Fatal("expected Processor plan in collaboration")
	}

	// Finding 2: StageDef.BoundaryMessage must be populated on Task_prep_mb's
	// stage, with a qualified TargetDept pointing at the escalation lane.
	var foundBM bool
	for _, d := range processor.Departments {
		for _, stage := range d.Stages {
			if stage.BoundaryMessage != nil {
				foundBM = true
				if stage.BoundaryMessage.MessageName != "mb-trigger" {
					t.Errorf("BoundaryMessage.MessageName = %q, want mb-trigger", stage.BoundaryMessage.MessageName)
				}
				if stage.BoundaryMessage.TargetDept != "Processor/escalation" {
					t.Errorf("BoundaryMessage.TargetDept = %q, want Processor/escalation", stage.BoundaryMessage.TargetDept)
				}
			}
		}
	}
	if !foundBM {
		t.Error("expected a stage with populated BoundaryMessage")
	}

	// The escalation continuation (Task_escalate_mb) is otherwise unreachable
	// except via the message boundary escape path — it must still be compiled.
	var foundEscalate bool
	for _, d := range processor.Departments {
		for _, stage := range d.Stages {
			if stage.NodeID == "Task_escalate_mb" {
				foundEscalate = true
			}
		}
	}
	if !foundEscalate {
		t.Error("expected Task_escalate_mb to be compiled via the message boundary continuation")
	}
}

func TestCompileCollaboration_ParseError(t *testing.T) {
	_, err := bpmn_compiler.New().CompileCollaboration(context.Background(), "not xml at all <<<")
	if err == nil {
		t.Fatal("expected error for invalid XML; got nil")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("unexpected context error: %v", err)
	}
}

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

// TestCompileCollaboration_WithReceiveTask exercises the receiveTask loop in
// buildElementToProcessName — the receive task in the receiver process must be
// registered so that a messageFlow targeting it resolves to the correct plan name.
func TestCompileCollaboration_WithReceiveTask(t *testing.T) {
	// Same two-pool BPMN as TestValidate_Collaboration_ReceiveTask_CollabReachable,
	// but compiled via CompileCollaboration to exercise buildElementToProcessName.
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_RcvCompile" targetNamespace="http://bpmn.io/schema/bpmn">
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
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
    </bpmn:receiveTask>
    <bpmn:endEvent id="E2"/>
    <bpmn:sequenceFlow id="F3" sourceRef="S2"       targetRef="Task_rcv"/>
    <bpmn:sequenceFlow id="F4" sourceRef="Task_rcv" targetRef="E2"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD"><bpmndi:BPMNPlane id="BP" bpmnElement="C1"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	collab, err := bpmn_compiler.New().CompileCollaboration(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("CompileCollaboration() unexpected error: %v", err)
	}
	if len(collab.Plans) != 2 {
		t.Fatalf("expected 2 plans; got %d", len(collab.Plans))
	}
	// The message flow from Task_send (Sender) to Task_rcv (Receiver) must resolve
	// to the correct plan names via buildElementToProcessName.
	if len(collab.Messages) == 0 {
		t.Fatal("expected at least 1 message flow entry")
	}
	msg := collab.Messages[0]
	if msg.SourcePlan != "Sender" {
		t.Errorf("message.SourcePlan = %q; want %q", msg.SourcePlan, "Sender")
	}
	if msg.TargetPlan != "Receiver" {
		t.Errorf("message.TargetPlan = %q; want %q", msg.TargetPlan, "Receiver")
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
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="E1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="S1"     targetRef="Task_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_1" targetRef="E1"/>
  </bpmn:process>
  <bpmn:process id="Proc_2" name="Receiver" isExecutable="true">
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
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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

// TestCompileCollaboration_ParticipantWithoutMatchingProcess verifies that a
// participant whose processRef does not match any bpmn:process is silently skipped —
// the collaboration still compiles (plans only contains the matched participants).
func TestCompileCollaboration_ParticipantWithoutMatchingProcess(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_MissingProc" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:collaboration id="C1">
    <bpmn:participant id="P_real"    name="Real"    processRef="Proc_real"/>
    <bpmn:participant id="P_phantom" name="Phantom" processRef="Proc_nonexistent"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_real" name="Real" isExecutable="true">
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
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="E1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="S1"     targetRef="Task_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_1" targetRef="E1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD"><bpmndi:BPMNPlane id="BP" bpmnElement="C1"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	collab, err := bpmn_compiler.New().CompileCollaboration(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("CompileCollaboration() unexpected error: %v", err)
	}
	if len(collab.Plans) != 1 {
		t.Errorf("expected 1 compiled plan (phantom participant skipped); got %d", len(collab.Plans))
	}
	if collab.Plans[0].Name != "Real" {
		t.Errorf("expected plan name %q; got %q", "Real", collab.Plans[0].Name)
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
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:inclusiveGateway id="INC_split"/>
    <bpmn:userTask id="Task_B" name="Path B">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:userTask id="Task_C" name="Path C">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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
