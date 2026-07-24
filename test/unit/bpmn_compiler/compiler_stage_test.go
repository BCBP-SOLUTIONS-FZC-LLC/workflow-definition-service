package bpmn_compiler_test

import (
	"context"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
)

// compilableBPMN wraps a process body and includes BPMNDI shapes for the three
// standard nodes (StartEvent_1, Task_1, EndEvent_1) so diagram-completeness
// validation passes. Add extra <bpmndi:BPMNShape> entries via extraShapes.
func compilableBPMN(processBody, extraShapes string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>` + processBody + `
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1">
      <bpmndi:BPMNShape id="StartEvent_1_di" bpmnElement="StartEvent_1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Task_1_di" bpmnElement="Task_1"><dc:Bounds x="250" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="EndEvent_1_di" bpmnElement="EndEvent_1"><dc:Bounds x="412" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Flow_1_di" bpmnElement="Flow_1"/>
      <bpmndi:BPMNEdge id="Flow_2_di" bpmnElement="Flow_2"/>` + extraShapes + `
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`
}

// validSendTask returns a minimal sendTask element referencing an optional messageRef.
// Pass "" for msgRef to omit the attribute.
func validSendTask(id, msgRef string) string {
	ref := ""
	if msgRef != "" {
		ref = ` messageRef="` + msgRef + `"`
	}
	return `<bpmn:sendTask id="` + id + `" name="` + id + `"` + ref + `>
      <bpmn:extensionElements>
        <zeebe:assignmentDefinition candidateGroups="design" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:sendTask>`
}

func TestCompile_DataStoreReference_CollectedAsVisualElement(t *testing.T) {
	bpmn := compilableBPMN(`
    <bpmn:startEvent id="StartEvent_1"><bpmn:outgoing>Flow_1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="Task_1" name="Prep">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="`+aliceUUID+`"/>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="EndEvent_1"><bpmn:incoming>Flow_2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Task_1" targetRef="EndEvent_1"/>
    <bpmn:dataStoreReference id="DS_1" name="Document Store"/>`,
		`<bpmndi:BPMNShape id="DS_1_di" bpmnElement="DS_1"><dc:Bounds x="155" y="175" width="50" height="50"/></bpmndi:BPMNShape>`)

	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() unexpected error: %v", err)
	}
	if len(plan.VisualElements) != 1 {
		t.Fatalf("expected 1 visual element, got %d", len(plan.VisualElements))
	}
	ve := plan.VisualElements[0]
	if ve.Kind != "dataStoreReference" {
		t.Errorf("Kind = %q; want %q", ve.Kind, "dataStoreReference")
	}
	if ve.ID != "DS_1" {
		t.Errorf("ID = %q; want %q", ve.ID, "DS_1")
	}
	if ve.Name != "Document Store" {
		t.Errorf("Name = %q; want %q", ve.Name, "Document Store")
	}
}

func TestCompile_UnknownStageType_PassthroughWithEngineNote(t *testing.T) {
	bpmn := compilableBPMN(`
    <bpmn:startEvent id="StartEvent_1"><bpmn:outgoing>Flow_1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="Task_1" name="Custom Step">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="custom-stage"/>
        <zeebe:assignmentDefinition candidateGroups="custom-role" candidateUsers="`+aliceUUID+`"/>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="EndEvent_1"><bpmn:incoming>Flow_2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Task_1" targetRef="EndEvent_1"/>`, "")

	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() unexpected error: %v", err)
	}
	if len(plan.Departments) == 0 || len(plan.Departments[0].Stages) == 0 {
		t.Fatal("expected at least one stage")
	}
	stage := plan.Departments[0].Stages[0]
	if stage.Type != "custom-stage" {
		t.Errorf("Type = %q; want %q", stage.Type, "custom-stage")
	}
	if stage.EngineNote == "" {
		t.Error("expected non-empty EngineNote for unknown stage type")
	}
}

func TestCompile_StageDefNodeID(t *testing.T) {
	bpmn := mustReadBPMN(t, "initial-diagram.bpmn")
	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	for _, dept := range plan.Departments {
		for _, s := range dept.Stages {
			if s.NodeID == "" {
				t.Errorf("stage %q in dept %q has empty node_id", s.Type, dept.ID)
			}
		}
	}
}

func TestCompile_SendTask(t *testing.T) {
	const msgID = "Msg_rfq"
	const msgName = "RFQ Message"
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="` + msgID + `" name="` + msgName + `"/>
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_send</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1"/>
    ` + validSendTask("Task_send", msgID) + `
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_send"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_send" targetRef="EndEvent_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1">
      <bpmndi:BPMNShape id="Sh_Start" bpmnElement="StartEvent_1"><dc:Bounds x="0" y="0" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Send"  bpmnElement="Task_send"><dc:Bounds x="100" y="0" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End"   bpmnElement="EndEvent_1"><dc:Bounds x="250" y="0" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	if len(plan.Departments) == 0 || len(plan.Departments[0].Stages) == 0 {
		t.Fatal("expected at least one stage")
	}
	s := plan.Departments[0].Stages[0]
	if s.Type != "send_task" {
		t.Errorf("Type = %q; want %q", s.Type, "send_task")
	}
	if s.NodeID != "Task_send" {
		t.Errorf("NodeID = %q; want %q", s.NodeID, "Task_send")
	}
	if s.Extras["message"] != msgName {
		t.Errorf("Extras[message] = %q; want %q", s.Extras["message"], msgName)
	}
}

func TestCompile_ReceiveTask(t *testing.T) {
	const msgID = "Msg_ack"
	const msgName = "Ack Message"
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="` + msgID + `" name="` + msgName + `"/>
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_recv</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:receiveTask id="Task_recv" name="Task_recv" messageRef="` + msgID + `">
      <bpmn:extensionElements>
        <zeebe:assignmentDefinition candidateGroups="design" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:receiveTask>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_recv"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_recv" targetRef="EndEvent_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1">
      <bpmndi:BPMNShape id="Sh_Start" bpmnElement="StartEvent_1"><dc:Bounds x="0" y="0" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Recv"  bpmnElement="Task_recv"><dc:Bounds x="100" y="0" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End"   bpmnElement="EndEvent_1"><dc:Bounds x="250" y="0" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	if len(plan.Departments) == 0 || len(plan.Departments[0].Stages) == 0 {
		t.Fatal("expected at least one stage")
	}
	s := plan.Departments[0].Stages[0]
	if s.Type != "receive_task" {
		t.Errorf("Type = %q; want %q", s.Type, "receive_task")
	}
	if s.NodeID != "Task_recv" {
		t.Errorf("NodeID = %q; want %q", s.NodeID, "Task_recv")
	}
	if s.Extras["message"] != msgName {
		t.Errorf("Extras[message] = %q; want %q", s.Extras["message"], msgName)
	}
}

func TestCompile_UnknownZeebeProperties(t *testing.T) {
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:userTask id="Task_1" name="Task 1">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="design" candidateUsers="` + aliceUUID + `"/>
        <zeebe:properties>
          <zeebe:property name="requires_comment" value="false"/>
          <zeebe:property name="sla_category" value="high"/>
          <zeebe:property name="priority" value="3"/>
        </zeebe:properties>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_1" targetRef="EndEvent_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1">
      <bpmndi:BPMNShape id="Sh_Start"  bpmnElement="StartEvent_1"><dc:Bounds x="0" y="0" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task1"  bpmnElement="Task_1"><dc:Bounds x="100" y="0" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End"    bpmnElement="EndEvent_1"><dc:Bounds x="250" y="0" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	s := plan.Departments[0].Stages[0]
	if s.Extras["sla_category"] != "high" {
		t.Errorf("Extras[sla_category] = %q; want %q", s.Extras["sla_category"], "high")
	}
	if s.Extras["priority"] != "3" {
		t.Errorf("Extras[priority] = %q; want %q", s.Extras["priority"], "3")
	}
	if s.Extras["requires_comment"] != "false" {
		t.Errorf("Extras[requires_comment] = %q; want %q", s.Extras["requires_comment"], "false")
	}
}

func TestCompile_SendTaskInExclusiveBranch(t *testing.T) {
	const msgID = "Msg_notify"
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="` + msgID + `" name="Notify"/>
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_prep</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>XOR_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_send</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Join_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1"/>
    ` + validTask("Task_prep", "", "prep") + `
    <bpmn:exclusiveGateway id="XOR_1"/>
    ` + validSendTask("Task_send", msgID) + `
    <bpmn:exclusiveGateway id="Join_1"/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_prep"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_prep" targetRef="XOR_1"/>
    <bpmn:sequenceFlow id="F3" sourceRef="XOR_1" targetRef="Task_send">
      <bpmn:conditionExpression>= notify = true</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="F4" sourceRef="XOR_1" targetRef="Join_1">
      <bpmn:conditionExpression>= notify = false</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="F5" sourceRef="Task_send" targetRef="Join_1"/>
    <bpmn:sequenceFlow id="F6" sourceRef="Join_1" targetRef="EndEvent_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1">
      <bpmndi:BPMNShape id="Sh_Start"  bpmnElement="StartEvent_1"><dc:Bounds x="0" y="0" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Prep"   bpmnElement="Task_prep"><dc:Bounds x="60" y="0" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_XOR"    bpmnElement="XOR_1"><dc:Bounds x="200" y="0" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Send"   bpmnElement="Task_send"><dc:Bounds x="300" y="0" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Join"   bpmnElement="Join_1"><dc:Bounds x="450" y="0" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End"    bpmnElement="EndEvent_1"><dc:Bounds x="550" y="0" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
      <bpmndi:BPMNEdge id="Edge_F3" bpmnElement="F3"/>
      <bpmndi:BPMNEdge id="Edge_F4" bpmnElement="F4"/>
      <bpmndi:BPMNEdge id="Edge_F5" bpmnElement="F5"/>
      <bpmndi:BPMNEdge id="Edge_F6" bpmnElement="F6"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	var sendBranch *struct{ stage, nodeID string }
	for _, step := range plan.Execution.Steps {
		for _, b := range step.Exclusive {
			if b.TargetStage == "send_task" {
				sendBranch = &struct{ stage, nodeID string }{b.TargetStage, b.TargetNodeID}
			}
		}
	}
	if sendBranch == nil {
		t.Fatal("expected an exclusive branch with target_stage=send_task")
	}
	if sendBranch.nodeID == "" {
		t.Error("expected non-empty target_node_id on send_task branch")
	}
}

// TestCompile_SendTask_WithZeebeProperties verifies that zeebe:property elements on a
// sendTask are forwarded into StageDef.Extras (covers BuildMessageStageDef extras loop).
func TestCompile_SendTask_WithZeebeProperties(t *testing.T) {
	const msgID = "Msg_notify"
	const msgName = "order.notify"
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="` + msgID + `" name="` + msgName + `"/>
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_send</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:sendTask id="Task_send" name="Notify" messageRef="` + msgID + `">
      <bpmn:extensionElements>
        <zeebe:properties>
          <zeebe:property name="templateKey" value="order-notify-v2"/>
          <zeebe:property name="channel" value="email"/>
        </zeebe:properties>
      </bpmn:extensionElements>
    </bpmn:sendTask>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_send"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_send"    targetRef="EndEvent_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1">
      <bpmndi:BPMNShape id="Sh_StartEvent_1" bpmnElement="StartEvent_1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_send"    bpmnElement="Task_send"><dc:Bounds x="250" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_EndEvent_1"   bpmnElement="EndEvent_1"><dc:Bounds x="402" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	var stage *struct{ extras map[string]string }
	for _, dept := range plan.Departments {
		for _, s := range dept.Stages {
			if s.Type == "send_task" {
				stage = &struct{ extras map[string]string }{s.Extras}
			}
		}
	}
	if stage == nil {
		t.Fatal("expected send_task stage in compiled plan")
	}
	if stage.extras["templateKey"] != "order-notify-v2" {
		t.Errorf("Extras[templateKey] = %q; want %q", stage.extras["templateKey"], "order-notify-v2")
	}
	if stage.extras["channel"] != "email" {
		t.Errorf("Extras[channel] = %q; want %q", stage.extras["channel"], "email")
	}
}

// TestCompile_ReceiveTaskInExclusiveBranch covers processExclusiveMessageTask receive_task
// path and firstTaskNodeAhead walking through an exclusive gateway to a receive task.
func TestCompile_ReceiveTaskInExclusiveBranch(t *testing.T) {
	const msgID = "Msg_ack"
	const msgName = "order.ack"
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                  id="Def_RecvXOR" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="` + msgID + `" name="` + msgName + `"/>
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_prep</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>XOR_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_recv</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_after</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Join_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:userTask id="Task_prep" name="Prepare">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="design" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:exclusiveGateway id="XOR_1"/>
    <bpmn:receiveTask id="Task_recv" name="Await Ack" messageRef="` + msgID + `"/>
    <bpmn:userTask id="Task_after" name="After">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="design" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:exclusiveGateway id="Join_1"/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_prep"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_prep"   targetRef="XOR_1"/>
    <bpmn:sequenceFlow id="F3" name="recv" sourceRef="XOR_1" targetRef="Task_recv"/>
    <bpmn:sequenceFlow id="F4" name="skip" sourceRef="XOR_1" targetRef="Task_after"/>
    <bpmn:sequenceFlow id="F5" sourceRef="Task_recv"   targetRef="Join_1"/>
    <bpmn:sequenceFlow id="F6" sourceRef="Task_after"  targetRef="Join_1"/>
    <bpmn:sequenceFlow id="F7" sourceRef="Join_1"      targetRef="EndEvent_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1">
      <bpmndi:BPMNShape id="Sh_Start"     bpmnElement="StartEvent_1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_prep"      bpmnElement="Task_prep"><dc:Bounds x="240" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_XOR"       bpmnElement="XOR_1"><dc:Bounds x="395" y="75" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_recv"      bpmnElement="Task_recv"><dc:Bounds x="500" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_after"     bpmnElement="Task_after"><dc:Bounds x="500" y="180" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Join"      bpmnElement="Join_1"><dc:Bounds x="655" y="75" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End"       bpmnElement="EndEvent_1"><dc:Bounds x="762" y="82" width="36" height="36"/></bpmndi:BPMNShape>
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

	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	var recvBranch *struct{ stage, nodeID string }
	for _, step := range plan.Execution.Steps {
		for _, b := range step.Exclusive {
			if b.TargetStage == "receive_task" {
				recvBranch = &struct{ stage, nodeID string }{b.TargetStage, b.TargetNodeID}
			}
		}
	}
	if recvBranch == nil {
		t.Fatal("expected an exclusive branch with target_stage=receive_task")
	}
	if recvBranch.nodeID == "" {
		t.Error("expected non-empty target_node_id on receive_task branch")
	}
}
