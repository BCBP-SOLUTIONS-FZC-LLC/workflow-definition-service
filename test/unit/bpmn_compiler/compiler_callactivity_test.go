package bpmn_compiler_test

import (
	"context"
	"testing"

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
      <bpmn:lane id="Lane_main" name="management">
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
      <bpmn:lane id="Lane_eng" name="engineering">
        <bpmn:flowNodeRef>PR_Start</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>PR_Prep</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>PR_End</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="PR_Start" name="Start"/>
    <bpmn:userTask id="PR_Prep" name="Prepare">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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
      <bpmn:lane id="Lane_eng" name="engineering">
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
      <bpmn:lane id="Lane_eng" name="engineering">
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
      <bpmn:lane id="Lane_eng" name="engineering">
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
      <bpmn:lane id="Lane_prep" name="preparer">
        <bpmn:flowNodeRef>PR_Start</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>PR_Prep</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>PR_End</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="PR_Start" name="Start"/>
    <bpmn:userTask id="PR_Prep" name="Prepare">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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

// TestCompile_CallActivity_Flat asserts that callActivity stages are inlined
// directly into the parent plan (no SubWorkflow wrapper).
func TestCompile_CallActivity_Flat(t *testing.T) {
	c := bpmn_compiler.New()
	plan, err := c.Compile(context.Background(), callActivityBPMN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Execution.Steps) == 0 {
		t.Fatal("expected at least one execution step")
	}
	// No SubWorkflow wrappers — callActivity is flat.
	for _, step := range plan.Execution.Steps {
		if step.SubWorkflow != nil {
			t.Errorf("callActivity should not produce a SubWorkflow step; got %+v", step.SubWorkflow)
		}
	}
	// The called process's own department ("engineering") must appear.
	found := false
	for _, d := range plan.Departments {
		if d.ID == "engineering" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected dept 'engineering' from called process; departments = %v", plan.Departments)
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
      <bpmn:lane id="Lane_mgmt" name="management">
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
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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
      <bpmn:lane id="Lane_mgmt" name="management">
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
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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

// TestCompile_CallActivity_NoLane_DeptFromIOMapping: called process has no lanes;
// the caller provides dept_id via ioMapping — steps compile into that department.
func TestCompile_CallActivity_NoLane_DeptFromIOMapping(t *testing.T) {
	c := bpmn_compiler.New()
	plan, err := c.Compile(context.Background(), callActivityNoLaneWithDeptBPMN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertHasDept(t, plan.Departments, "engineering")
	assertIOMapping(t, plan.Execution.Steps)
}

func assertHasDept(t *testing.T, depts []domain.DepartmentDef, id string) {
	t.Helper()
	for _, d := range depts {
		if d.ID == id {
			return
		}
	}
	t.Errorf("expected dept %q from dept_id ioMapping; departments = %v", id, depts)
}

func assertIOMapping(t *testing.T, steps []domain.ExecutionStep) {
	t.Helper()
	for _, step := range steps {
		if step.IOMapping == nil {
			continue
		}
		for _, in := range step.IOMapping.Inputs {
			if in.Target == "dept_id" {
				t.Error("dept_id should not be emitted in compiled IOMapping (it is compiler-internal)")
			}
		}
		return // found an ioMapping, done
	}
	t.Error("expected IOMapping on a step (non-dept_id inputs should be preserved)")
}

// TestValidate_CallActivity_NoLane_MissingDeptID: validator must emit MISSING_DEPT_INPUT_FOR_MODULE
// when the called process has no lanes and no dept_id input mapping is provided.
func TestValidate_CallActivity_NoLane_MissingDeptID(t *testing.T) {
	c := bpmn_compiler.New()
	errs, err := c.Validate(context.Background(), callActivityNoLaneMissingDeptBPMN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrMissingDeptInputForModule) {
		t.Errorf("expected MISSING_DEPT_INPUT_FOR_MODULE; got %v", errs)
	}
}

// TestValidate_CallActivity_NoLane_WithDeptID: no validation error when dept_id is provided.
func TestValidate_CallActivity_NoLane_WithDeptID(t *testing.T) {
	c := bpmn_compiler.New()
	errs, err := c.Validate(context.Background(), callActivityNoLaneWithDeptBPMN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasCode(errs, domain.BPMNErrMissingDeptInputForModule) {
		t.Errorf("unexpected MISSING_DEPT_INPUT_FOR_MODULE when dept_id is provided; errs = %v", errs)
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
      <bpmn:lane id="Lane_eng" name="engineering">
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>SP_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1" name="Start"/>
    <bpmn:subProcess id="SP_1" name="Outer Sub">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="engineering" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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
      <bpmn:lane id="Lane_main" name="management">
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
      <bpmn:lane id="Lane_eng" name="engineering">
        <bpmn:flowNodeRef>PR_Start</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>PR_Prep</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>PR_End</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="PR_Start" name="Start"/>
    <bpmn:userTask id="PR_Prep" name="Prepare">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
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

// TestCompile_CallActivity_DeptsRemap_StepsMatchDepartments is a regression test:
// zeebe:ioMapping's "Depts" remap must apply to both DepartmentDef.ID entries AND
// the ExecutionStep dept references inside the called process's flattened steps —
// previously only the former was remapped, leaving steps pointing at a department
// ID absent from plan.Departments.
func TestCompile_CallActivity_DeptsRemap_StepsMatchDepartments(t *testing.T) {
	c := bpmn_compiler.New()
	plan, err := c.Compile(context.Background(), callActivityDeptsRemapBPMN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundDept := false
	for _, d := range plan.Departments {
		if d.ID == "engineering" {
			t.Errorf("department %q should have been remapped to %q", "engineering", "delivery")
		}
		if d.ID == "delivery" {
			foundDept = true
		}
	}
	if !foundDept {
		t.Fatalf("expected remapped dept 'delivery'; departments = %v", plan.Departments)
	}

	for _, step := range plan.Execution.Steps {
		for _, dept := range step.Sequential {
			if dept == "engineering" {
				t.Errorf("step references unremapped dept %q; steps = %+v", dept, plan.Execution.Steps)
			}
		}
	}
}
