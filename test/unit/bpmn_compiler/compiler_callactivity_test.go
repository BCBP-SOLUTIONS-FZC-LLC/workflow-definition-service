package bpmn_compiler_test

import (
	"context"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// callActivityBPMN: main process has one callActivity; the called process
// has its own lane ("engineering") so it does NOT inherit the parent lane.
const callActivityBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_CA" targetNamespace="http://bpmn.io/schema/bpmn">

  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_main" name="management"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="92fd92cf-4b2c-58dc-bf98-f3d0af729876"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CA_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1" name="Start"/>
    <bpmn:callActivity id="CA_1" name="Prepare Response">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="Process_PrepReview" propagateAllChildVariables="false"/>
      </bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="End_1" name="End"/>
    <bpmn:sequenceFlow id="Flow_start_ca" sourceRef="Start_1" targetRef="CA_1"/>
    <bpmn:sequenceFlow id="Flow_ca_end" sourceRef="CA_1" targetRef="End_1"/>
  </bpmn:process>

  <bpmn:process id="Process_PrepReview" name="Prepare Review" isExecutable="false">
    <bpmn:laneSet id="LaneSet_PR">
      <bpmn:lane id="Lane_eng" name="engineering"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="f14abc9f-befa-54ff-b57f-c15ab3090062"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>PR_Start</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>PR_Prep</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>PR_End</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="PR_Start" name="Start"/>
    <bpmn:userTask id="PR_Prep" name="Prepare">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="PR_End" name="End"/>
    <bpmn:sequenceFlow id="PR_Flow1" sourceRef="PR_Start" targetRef="PR_Prep"/>
    <bpmn:sequenceFlow id="PR_Flow2" sourceRef="PR_Prep" targetRef="PR_End"/>
  </bpmn:process>

  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_Main">
      <bpmndi:BPMNShape id="Lane_main_di" bpmnElement="Lane_main"/>
      <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"/>
      <bpmndi:BPMNShape id="CA_1_di" bpmnElement="CA_1"/>
      <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"/>
      <bpmndi:BPMNEdge id="Flow_start_ca_di" bpmnElement="Flow_start_ca"/>
      <bpmndi:BPMNEdge id="Flow_ca_end_di" bpmnElement="Flow_ca_end"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

// callActivityMissingRefBPMN has a callActivity with no zeebe:calledElement.
const callActivityMissingRefBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_CA_Bad" targetNamespace="http://bpmn.io/schema/bpmn">

  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_eng" name="engineering"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="f14abc9f-befa-54ff-b57f-c15ab3090062"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CA_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1" name="Start"/>
    <bpmn:callActivity id="CA_1" name="Bad Call"/>
    <bpmn:endEvent id="End_1" name="End"/>
    <bpmn:sequenceFlow id="Flow_start_ca" sourceRef="Start_1" targetRef="CA_1"/>
    <bpmn:sequenceFlow id="Flow_ca_end" sourceRef="CA_1" targetRef="End_1"/>
  </bpmn:process>

  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_Main">
      <bpmndi:BPMNShape id="Lane_eng_di" bpmnElement="Lane_eng"/>
      <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"/>
      <bpmndi:BPMNShape id="CA_1_di" bpmnElement="CA_1"/>
      <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"/>
      <bpmndi:BPMNEdge id="Flow_start_ca_di" bpmnElement="Flow_start_ca"/>
      <bpmndi:BPMNEdge id="Flow_ca_end_di" bpmnElement="Flow_ca_end"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

// callActivityUnknownRefBPMN has a callActivity referencing a non-existent process.
const callActivityUnknownRefBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_CA_Unknown" targetNamespace="http://bpmn.io/schema/bpmn">

  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_eng" name="engineering"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="f14abc9f-befa-54ff-b57f-c15ab3090062"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CA_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1" name="Start"/>
    <bpmn:callActivity id="CA_1" name="Bad Call">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="Process_DoesNotExist"/>
      </bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="End_1" name="End"/>
    <bpmn:sequenceFlow id="Flow_start_ca" sourceRef="Start_1" targetRef="CA_1"/>
    <bpmn:sequenceFlow id="Flow_ca_end" sourceRef="CA_1" targetRef="End_1"/>
  </bpmn:process>

  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_Main">
      <bpmndi:BPMNShape id="Lane_eng_di" bpmnElement="Lane_eng"/>
      <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"/>
      <bpmndi:BPMNShape id="CA_1_di" bpmnElement="CA_1"/>
      <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"/>
      <bpmndi:BPMNEdge id="Flow_start_ca_di" bpmnElement="Flow_start_ca"/>
      <bpmndi:BPMNEdge id="Flow_ca_end_di" bpmnElement="Flow_ca_end"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

// callActivityWithBoundaryBPMN: callActivity with a timer boundary event.
// Compile should return an error — boundary events on callActivities are not supported
// with flat compilation.
const callActivityWithBoundaryBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_CA_Boundary" targetNamespace="http://bpmn.io/schema/bpmn">

  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_eng" name="engineering"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="f14abc9f-befa-54ff-b57f-c15ab3090062"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CA_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>BE_Timer</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1" name="Start"/>
    <bpmn:callActivity id="CA_1" name="Prepare Response">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="Process_PR_Boundary" propagateAllChildVariables="false"/>
      </bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:boundaryEvent id="BE_Timer" attachedToRef="CA_1" cancelActivity="true">
      <bpmn:timerEventDefinition>
        <bpmn:timeDuration>48h</bpmn:timeDuration>
      </bpmn:timerEventDefinition>
    </bpmn:boundaryEvent>
    <bpmn:endEvent id="End_1" name="End"/>
    <bpmn:sequenceFlow id="Flow_start_ca" sourceRef="Start_1" targetRef="CA_1"/>
    <bpmn:sequenceFlow id="Flow_ca_end" sourceRef="CA_1" targetRef="End_1"/>
    <bpmn:sequenceFlow id="Flow_timer_end" sourceRef="BE_Timer" targetRef="End_1"/>
  </bpmn:process>

  <bpmn:process id="Process_PR_Boundary" name="Prepare Review" isExecutable="false">
    <bpmn:laneSet id="LaneSet_PR">
      <bpmn:lane id="Lane_prep" name="preparer"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="4114224f-65ed-5323-8cb4-737f55b94368"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>PR_Start</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>PR_Prep</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>PR_End</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="PR_Start" name="Start"/>
    <bpmn:userTask id="PR_Prep" name="Prepare">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="PR_End" name="End"/>
    <bpmn:sequenceFlow id="PR_Flow1" sourceRef="PR_Start" targetRef="PR_Prep"/>
    <bpmn:sequenceFlow id="PR_Flow2" sourceRef="PR_Prep" targetRef="PR_End"/>
  </bpmn:process>

  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_Main"/>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

func TestValidate_CallActivity_Valid(t *testing.T) {
	c := bpmn_compiler.New()
	errs, err := c.Validate(context.Background(), callActivityBPMN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range errs {
		t.Errorf("unexpected validation error: %s: %s", e.Code, e.Message)
	}
}

func TestValidate_CallActivity_MissingCalledElement(t *testing.T) {
	c := bpmn_compiler.New()
	errs, err := c.Validate(context.Background(), callActivityMissingRefBPMN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrUnresolvedCalledElement) {
		t.Errorf("expected UNRESOLVED_CALLED_ELEMENT error; got %v", errs)
	}
}

func TestValidate_CallActivity_UnknownProcessRef(t *testing.T) {
	c := bpmn_compiler.New()
	errs, err := c.Validate(context.Background(), callActivityUnknownRefBPMN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrUnresolvedCalledElement) {
		t.Errorf("expected UNRESOLVED_CALLED_ELEMENT error; got %v", errs)
	}
}

// TestCompile_CallActivity_IsACallToTheModulesOwnPlan asserts that a
// callActivity compiles to one call step and the called process to its own
// plan, rather than being inlined into the caller's departments.
func TestCompile_CallActivity_IsACallToTheModulesOwnPlan(t *testing.T) {
	collab, err := bpmn_compiler.New().CompileForPublish(context.Background(), callActivityBPMN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	main := collab.Plans[0]
	if len(main.Execution.Steps) != 1 || main.Execution.Steps[0].CallPlan == nil {
		t.Fatalf("main steps = %+v, want one call_plan step", main.Execution.Steps)
	}
	call := main.Execution.Steps[0].CallPlan
	if call.NodeID != "CA_1" || call.Plan != "Process_PrepReview" || call.Departments != nil {
		t.Errorf("call = %+v, want CA_1 calling Process_PrepReview with its fixed lane unbound", call)
	}
	if len(collab.Plans) != 2 || collab.Plans[1].Name != "Process_PrepReview" {
		t.Fatalf("plans = %d, want the main plan and Process_PrepReview's", len(collab.Plans))
	}
	engineering := collab.Plans[1].Departments[0]
	if engineering.ID != "engineering" || engineering.IAMDepartmentID != "f14abc9f-befa-54ff-b57f-c15ab3090062" {
		t.Errorf("module department = %+v, want its own engineering lane and IAM id", engineering)
	}
}

// TestCompile_CallActivity_BoundaryEventError asserts that a callActivity with
// a boundary event cannot be compiled (rejected at validation or compile time).
func TestCompile_CallActivity_BoundaryEventError(t *testing.T) {
	c := bpmn_compiler.New()
	_, err := c.Compile(context.Background(), callActivityWithBoundaryBPMN)
	if err == nil {
		t.Fatal("expected error for callActivity with boundary event; got nil")
	}
}

// TestCompile_CallActivity_WithTimerBoundary: boundary events on callActivities are unsupported
// with flat compilation — compile must return an error regardless of boundary event type.
func TestCompile_CallActivity_WithTimerBoundary(t *testing.T) {
	_, err := bpmn_compiler.New().Compile(context.Background(), callActivityWithBoundaryBPMN)
	if err == nil {
		t.Fatal("expected error for callActivity with boundary event")
	}
}

// callActivityNoLaneWithDeptBPMN: called process has no laneSet; caller provides dept_id via ioMapping.
const callActivityNoLaneWithDeptBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_NoLane" targetNamespace="http://bpmn.io/schema/bpmn">

  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_mgmt" name="management"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="2200a5ae-2466-54f7-a1d8-243106e05705"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CA_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1" name="Start"/>
    <bpmn:callActivity id="CA_1" name="Prepare Response">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="Process_NoLane" propagateAllChildVariables="false"/>
        <zeebe:ioMapping>
          <zeebe:input source="engineering" target="dept_id"/>
          <zeebe:input source="=clarificationId" target="clarificationId"/>
        </zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="End_1" name="End"/>
    <bpmn:sequenceFlow id="Flow_s_ca" sourceRef="Start_1" targetRef="CA_1"/>
    <bpmn:sequenceFlow id="Flow_ca_e" sourceRef="CA_1" targetRef="End_1"/>
  </bpmn:process>

  <bpmn:process id="Process_NoLane" name="No Lane Module" isExecutable="true">
    <bpmn:startEvent id="NL_Start" name="Start"/>
    <bpmn:userTask id="NL_Task" name="Prepare">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="NL_End" name="End"/>
    <bpmn:sequenceFlow id="NL_Flow1" sourceRef="NL_Start" targetRef="NL_Task"/>
    <bpmn:sequenceFlow id="NL_Flow2" sourceRef="NL_Task" targetRef="NL_End"/>
  </bpmn:process>

  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_Main">
      <bpmndi:BPMNShape id="Lane_mgmt_di" bpmnElement="Lane_mgmt"/>
      <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"/>
      <bpmndi:BPMNShape id="CA_1_di" bpmnElement="CA_1"/>
      <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"/>
      <bpmndi:BPMNEdge id="Flow_s_ca_di" bpmnElement="Flow_s_ca"/>
      <bpmndi:BPMNEdge id="Flow_ca_e_di" bpmnElement="Flow_ca_e"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

// callActivityNoLaneMissingDeptBPMN: called process has no laneSet and caller omits dept_id.
const callActivityNoLaneMissingDeptBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_NoLaneMissingDept" targetNamespace="http://bpmn.io/schema/bpmn">

  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_mgmt" name="management"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="2200a5ae-2466-54f7-a1d8-243106e05705"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CA_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1" name="Start"/>
    <bpmn:callActivity id="CA_1" name="Prepare Response">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="Process_NoLane" propagateAllChildVariables="false"/>
      </bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="End_1" name="End"/>
    <bpmn:sequenceFlow id="Flow_s_ca" sourceRef="Start_1" targetRef="CA_1"/>
    <bpmn:sequenceFlow id="Flow_ca_e" sourceRef="CA_1" targetRef="End_1"/>
  </bpmn:process>

  <bpmn:process id="Process_NoLane" name="No Lane Module" isExecutable="true">
    <bpmn:startEvent id="NL_Start" name="Start"/>
    <bpmn:userTask id="NL_Task" name="Prepare">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="NL_End" name="End"/>
    <bpmn:sequenceFlow id="NL_Flow1" sourceRef="NL_Start" targetRef="NL_Task"/>
    <bpmn:sequenceFlow id="NL_Flow2" sourceRef="NL_Task" targetRef="NL_End"/>
  </bpmn:process>

  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_Main">
      <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"/>
      <bpmndi:BPMNShape id="CA_1_di" bpmnElement="CA_1"/>
      <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"/>
      <bpmndi:BPMNEdge id="Flow_s_ca_di" bpmnElement="Flow_s_ca"/>
      <bpmndi:BPMNEdge id="Flow_ca_e_di" bpmnElement="Flow_ca_e"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

// TestValidate_CallActivity_RetiredDeptIDInput_IsRefused: a laneless module
// takes the call's lane, so the old dept_id input is refused rather than
// silently passed to the called process as a variable.
func TestValidate_CallActivity_RetiredDeptIDInput_IsRefused(t *testing.T) {
	errs, err := bpmn_compiler.New().Validate(context.Background(), callActivityNoLaneWithDeptBPMN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := findCode(errs, domain.BPMNErrInvalidZeebeProperty); got == nil || !strings.Contains(got.Message, "dept_id") {
		t.Errorf("errors = %+v, want INVALID_ZEEBE_PROPERTY about dept_id", errs)
	}
}

// TestCompile_CallActivity_NoLane_TakesTheCallsLane: a laneless module is one
// open slot, which the call binds to its own lane when Depts is absent.
func TestCompile_CallActivity_NoLane_TakesTheCallsLane(t *testing.T) {
	collab, err := bpmn_compiler.New().CompileForPublish(context.Background(), callActivityNoLaneMissingDeptBPMN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call := collab.Plans[0].Execution.Steps[0].CallPlan
	if call == nil || call.Departments["Process_NoLane"] != "management" {
		t.Errorf("call = %+v, want Process_NoLane bound to the call's lane management", call)
	}
}

// nestedSubProcessBPMN: a subProcess that contains another subProcess — must be rejected.
const nestedSubProcessBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_NestedSP" targetNamespace="http://bpmn.io/schema/bpmn">

  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_eng" name="engineering"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="f14abc9f-befa-54ff-b57f-c15ab3090062"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>SP_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1" name="Start"/>
    <bpmn:subProcess id="SP_1" name="Outer Sub">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
      <bpmn:startEvent id="SP1_Start"/>
      <bpmn:endEvent id="SP1_End"/>
      <bpmn:sequenceFlow id="SP1_Flow" sourceRef="SP1_Start" targetRef="SP1_End"/>
      <bpmn:subProcess id="SP_Nested" name="Nested Sub">
        <bpmn:startEvent id="SP2_Start"/>
        <bpmn:endEvent id="SP2_End"/>
        <bpmn:sequenceFlow id="SP2_Flow" sourceRef="SP2_Start" targetRef="SP2_End"/>
      </bpmn:subProcess>
    </bpmn:subProcess>
    <bpmn:endEvent id="End_1" name="End"/>
    <bpmn:sequenceFlow id="Flow_s_sp" sourceRef="Start_1" targetRef="SP_1"/>
    <bpmn:sequenceFlow id="Flow_sp_e" sourceRef="SP_1" targetRef="End_1"/>
  </bpmn:process>

  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_Main">
      <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"/>
      <bpmndi:BPMNShape id="SP_1_di" bpmnElement="SP_1"/>
      <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"/>
      <bpmndi:BPMNEdge id="Flow_s_sp_di" bpmnElement="Flow_s_sp"/>
      <bpmndi:BPMNEdge id="Flow_sp_e_di" bpmnElement="Flow_sp_e"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

// TestValidate_NestedSubProcess_Rejected verifies that a subProcess containing
// a nested subProcess produces NESTED_SUBPROCESS_NOT_SUPPORTED.
func TestValidate_NestedSubProcess_Rejected(t *testing.T) {
	c := bpmn_compiler.New()
	errs, err := c.Validate(context.Background(), nestedSubProcessBPMN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrNestedSubProcessNotSupported) {
		t.Errorf("expected NESTED_SUBPROCESS_NOT_SUPPORTED; got %v", errs)
	}
}

// callActivityDeptsRemapBPMN: callActivity remaps the called process's lane
// "engineering" to dept "delivery" via a zeebe:ioMapping target="Depts" JSON map.
const callActivityDeptsRemapBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_CA_Remap" targetNamespace="http://bpmn.io/schema/bpmn">

  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_main" name="management"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="92fd92cf-4b2c-58dc-bf98-f3d0af729876"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CA_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1" name="Start"/>
    <bpmn:callActivity id="CA_1" name="Prepare Response">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="Process_PrepReview" propagateAllChildVariables="false"/>
        <zeebe:ioMapping>
          <zeebe:input source="{&#34;engineering&#34;: &#34;delivery&#34;}" target="Depts" />
        </zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="End_1" name="End"/>
    <bpmn:sequenceFlow id="Flow_start_ca" sourceRef="Start_1" targetRef="CA_1"/>
    <bpmn:sequenceFlow id="Flow_ca_end" sourceRef="CA_1" targetRef="End_1"/>
  </bpmn:process>

  <bpmn:process id="Process_PrepReview" name="Prepare Review" isExecutable="false">
    <bpmn:laneSet id="LaneSet_PR">
      <bpmn:lane id="Lane_eng" name="engineering"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="f14abc9f-befa-54ff-b57f-c15ab3090062"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>PR_Start</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>PR_Prep</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>PR_End</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="PR_Start" name="Start"/>
    <bpmn:userTask id="PR_Prep" name="Prepare">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="PR_End" name="End"/>
    <bpmn:sequenceFlow id="PR_Flow1" sourceRef="PR_Start" targetRef="PR_Prep"/>
    <bpmn:sequenceFlow id="PR_Flow2" sourceRef="PR_Prep" targetRef="PR_End"/>
  </bpmn:process>

  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_Main">
      <bpmndi:BPMNShape id="Lane_main_di" bpmnElement="Lane_main"/>
      <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"/>
      <bpmndi:BPMNShape id="CA_1_di" bpmnElement="CA_1"/>
      <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"/>
      <bpmndi:BPMNEdge id="Flow_start_ca_di" bpmnElement="Flow_start_ca"/>
      <bpmndi:BPMNEdge id="Flow_ca_end_di" bpmnElement="Flow_ca_end"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

// TestCompile_CallActivity_DeptsBindsAModuleLaneToACallerLane: Depts binds an
// open module lane to a caller lane, whose department the called tasks run in.
func TestCompile_CallActivity_DeptsBindsAModuleLaneToACallerLane(t *testing.T) {
	bpmnXML := strings.Replace(callActivityDeptsRemapBPMN, "&#34;delivery&#34;", "&#34;management&#34;", 1)
	openLane := `<bpmn:lane id="Lane_eng" name="engineering">`
	bpmnXML = strings.Replace(bpmnXML, openLane+`<bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="f14abc9f-befa-54ff-b57f-c15ab3090062"/></zeebe:properties></bpmn:extensionElements>`, openLane, 1)
	collab, err := bpmn_compiler.New().CompileForPublish(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call := collab.Plans[0].Execution.Steps[0].CallPlan
	if call == nil || call.Departments["engineering"] != "management" {
		t.Fatalf("call = %+v, want engineering bound to management", call)
	}
	expanded, err := dsl.ExpandCalls(collab, domain.MaxPlanStages)
	if err != nil {
		t.Fatal(err)
	}
	if got := departmentNamed(t, expanded.Plans[0], "CA_1::engineering").IAMDepartmentID; got != "92fd92cf-4b2c-58dc-bf98-f3d0af729876" {
		t.Errorf("engineering runs in iam %q, want management's", got)
	}
}
