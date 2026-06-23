package bpmn_compiler_test

import (
	"context"
	_ "embed"
	"errors"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// The reference BPMN files are embedded at compile time from testdata/ so tests
// run correctly in CI without any filesystem dependencies.

//go:embed testdata/initial-diagram.bpmn
var initialDiagramBPMN string

//go:embed testdata/parallel-diagram.bpmn
var parallelDiagramBPMN string

//go:embed testdata/loop-diagram.bpmn
var loopDiagramBPMN string

//go:embed testdata/timer-boundary.bpmn
var timerBoundaryBPMN string

//go:embed testdata/timer-boundary-noninterrupting.bpmn
var timerBoundaryNIBPMN string

//go:embed testdata/subprocess.bpmn
var subprocessBPMN string

//go:embed testdata/subprocess-error.bpmn
var subprocessErrorBPMN string

func mustReadBPMN(_ *testing.T, name string) string {
	switch name {
	case "initial-diagram.bpmn":
		return initialDiagramBPMN
	case "parallel-diagram.bpmn":
		return parallelDiagramBPMN
	case "loop-diagram.bpmn":
		return loopDiagramBPMN
	case "timer-boundary.bpmn":
		return timerBoundaryBPMN
	case "timer-boundary-noninterrupting.bpmn":
		return timerBoundaryNIBPMN
	case "subprocess.bpmn":
		return subprocessBPMN
	case "subprocess-error.bpmn":
		return subprocessErrorBPMN
	default:
		panic("unknown fixture: " + name)
	}
}

// minimalBPMN wraps a process body in valid namespace declarations.
func minimalBPMN(processBody string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design">
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>` + processBody + `
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1"/>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`
}

const aliceUUID = "550e8400-e29b-41d4-a716-446655440000"

// validTask returns a minimal valid userTask XML block.
// deptID is ignored — department is derived from the lane the task appears in.
func validTask(id, deptID, stageType string) string {
	_ = deptID
	return `<bpmn:userTask id="` + id + `" name="` + id + `">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="` + stageType + `"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>`
}

func TestValidate_ReferenceFiles(t *testing.T) {
	c := bpmn_compiler.New()
	tests := []struct {
		name string
		file string
	}{
		{"initial-diagram (sequential)", "initial-diagram.bpmn"},
		{"parallel-diagram (parallel + exclusive gateways)", "parallel-diagram.bpmn"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bpmn := mustReadBPMN(t, tt.file)
			errs, err := c.Validate(context.Background(), bpmn)
			if err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if len(errs) > 0 {
				t.Errorf("expected 0 validation errors, got %d:", len(errs))
				for _, e := range errs {
					t.Errorf("  [%s] %s: %s", e.Code, e.NodeID, e.Message)
				}
			}
		})
	}
}

func TestValidate_StructuralRules(t *testing.T) {
	c := bpmn_compiler.New()

	tests := []struct {
		name     string
		bpmn     string
		wantCode domain.BPMNErrorCode
	}{
		{
			name: "no start event",
			bpmn: minimalBPMN(
				validTask("Task_1", "design", "prep") +
					`<bpmn:endEvent id="EndEvent_1" name="End"/>
          <bpmn:sequenceFlow id="F1" sourceRef="Task_1" targetRef="EndEvent_1"/>`,
			),
			wantCode: domain.BPMNErrNoStartEvent,
		},
		{
			name: "no end event",
			bpmn: minimalBPMN(
				`<bpmn:startEvent id="StartEvent_1" name="Start"/>` +
					validTask("Task_1", "design", "prep") +
					`<bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>`,
			),
			wantCode: domain.BPMNErrNoEndEvent,
		},
		{
			name: "multiple start events",
			bpmn: minimalBPMN(
				`<bpmn:startEvent id="StartEvent_1" name="Start"/>
        <bpmn:startEvent id="StartEvent_2" name="Start2"/>` +
					validTask("Task_1", "design", "prep") +
					`<bpmn:endEvent id="EndEvent_1" name="End"/>
        <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
        <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="EndEvent_1"/>`,
			),
			wantCode: domain.BPMNErrMultipleStartEvents,
		},
		{
			name: "invalid sequence flow ref",
			bpmn: minimalBPMN(
				`<bpmn:startEvent id="StartEvent_1" name="Start"/>` +
					validTask("Task_1", "design", "prep") +
					`<bpmn:endEvent id="EndEvent_1" name="End"/>
        <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
        <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="GhostNode"/>`,
			),
			wantCode: domain.BPMNErrInvalidSequenceFlowRef,
		},
		{
			name: "dangling node (no outgoing flow)",
			bpmn: minimalBPMN(
				`<bpmn:startEvent id="StartEvent_1" name="Start"/>` +
					validTask("Task_1", "design", "prep") +
					validTask("Task_orphan", "design", "review") +
					`<bpmn:endEvent id="EndEvent_1" name="End"/>
        <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
        <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="EndEvent_1"/>`,
			),
			wantCode: domain.BPMNErrDanglingNode,
		},
		{
			name: "cycle detected",
			bpmn: minimalBPMN(
				`<bpmn:startEvent id="StartEvent_1" name="Start"/>` +
					validTask("Task_1", "design", "prep") +
					validTask("Task_2", "design", "review") +
					`<bpmn:endEvent id="EndEvent_1" name="End"/>
        <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
        <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="Task_2"/>
        <bpmn:sequenceFlow id="F3" sourceRef="Task_2"       targetRef="Task_1"/>
        <bpmn:sequenceFlow id="F4" sourceRef="Task_2"       targetRef="EndEvent_1"/>`,
			),
			wantCode: domain.BPMNErrCycleDetected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs, err := c.Validate(context.Background(), tt.bpmn)
			if err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if !hasCode(errs, tt.wantCode) {
				t.Errorf("expected error code %q; got: %v", tt.wantCode, errCodes(errs))
			}
		})
	}
}

func TestValidate_ExtensionElementRules(t *testing.T) {
	c := bpmn_compiler.New()

	wrapWithFlow := func(taskXML string) string {
		return minimalBPMN(
			`<bpmn:startEvent id="StartEvent_1" name="Start"/>` +
				taskXML +
				`<bpmn:endEvent id="EndEvent_1" name="End"/>
        <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
        <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="EndEvent_1"/>`,
		)
	}

	tests := []struct {
		name     string
		taskXML  string
		wantCode domain.BPMNErrorCode
	}{
		{
			name: "missing taskDefinition",
			taskXML: `<bpmn:userTask id="Task_1" name="T">
        <bpmn:extensionElements>
          <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="` + aliceUUID + `"/>
        </bpmn:extensionElements></bpmn:userTask>`,
			wantCode: domain.BPMNErrMissingTaskDefinition,
		},
		{
			name: "invalid taskDefinition type",
			taskXML: `<bpmn:userTask id="Task_1" name="T">
        <bpmn:extensionElements>
          <zeebe:taskDefinition type="audit"/>
          <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="` + aliceUUID + `"/>
        </bpmn:extensionElements></bpmn:userTask>`,
			wantCode: domain.BPMNErrInvalidTaskDefinitionType,
		},
		{
			name: "missing assignmentDefinition",
			taskXML: `<bpmn:userTask id="Task_1" name="T">
        <bpmn:extensionElements>
          <zeebe:taskDefinition type="prep"/>
        </bpmn:extensionElements></bpmn:userTask>`,
			wantCode: domain.BPMNErrMissingAssignmentDefinition,
		},
		{
			name: "empty candidateGroups",
			taskXML: `<bpmn:userTask id="Task_1" name="T">
        <bpmn:extensionElements>
          <zeebe:taskDefinition type="prep"/>
          <zeebe:assignmentDefinition candidateGroups="" candidateUsers="` + aliceUUID + `"/>
        </bpmn:extensionElements></bpmn:userTask>`,
			wantCode: domain.BPMNErrCandidateGroupsEmpty,
		},
		{
			name: "invalid candidateUser UUID",
			taskXML: `<bpmn:userTask id="Task_1" name="T">
        <bpmn:extensionElements>
          <zeebe:taskDefinition type="prep"/>
          <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="not-a-uuid"/>
        </bpmn:extensionElements></bpmn:userTask>`,
			wantCode: domain.BPMNErrInvalidCandidateUser,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs, err := c.Validate(context.Background(), wrapWithFlow(tt.taskXML))
			if err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if !hasCode(errs, tt.wantCode) {
				t.Errorf("expected error code %q; got: %v", tt.wantCode, errCodes(errs))
			}
		})
	}
}

func TestValidate_ParseErrors(t *testing.T) {
	c := bpmn_compiler.New()

	tests := []struct {
		name        string
		bpmn        string
		wantParseEr bool // error returned as the error return, not as BPMNValidationError
	}{
		{
			name:        "DOCTYPE injection rejected",
			bpmn:        `<?xml version="1.0"?><!DOCTYPE foo [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><foo/>`,
			wantParseEr: true,
		},
		{
			// Missing namespaces are now a structured MISSING_NAMESPACE validation
			// error (see TestValidate_MissingNamespace), not a transport error.
			name:        "malformed XML",
			bpmn:        `<?xml version="1.0"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"><unclosed>`,
			wantParseEr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.Validate(context.Background(), tt.bpmn)
			if tt.wantParseEr && err == nil {
				t.Error("expected a parse error, got nil")
			}
			if !tt.wantParseEr && err != nil {
				t.Errorf("unexpected parse error: %v", err)
			}
		})
	}
}

func TestCompile_InitialDiagram(t *testing.T) {
	bpmn := mustReadBPMN(t, "initial-diagram.bpmn")
	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	// 9 departments, each with 3 stages.
	wantDepts := []struct {
		id     string
		stages int
	}{
		{"design", 3}, {"contracts", 3}, {"procurement", 3},
		{"planning", 3}, {"logistics", 3}, {"hse", 3},
		{"construction", 3}, {"qa", 3}, {"commissioning", 3},
	}
	if len(plan.Departments) != len(wantDepts) {
		t.Fatalf("departments: got %d, want %d", len(plan.Departments), len(wantDepts))
	}
	for i, wd := range wantDepts {
		d := plan.Departments[i]
		if d.ID != wd.id {
			t.Errorf("dept[%d].ID = %q, want %q", i, d.ID, wd.id)
		}
		if len(d.Stages) != wd.stages {
			t.Errorf("dept %q: got %d stages, want %d", d.ID, len(d.Stages), wd.stages)
		}
	}

	// Single sequential step containing all 9 departments in order.
	if len(plan.Execution.Steps) != 1 {
		t.Fatalf("execution steps: got %d, want 1", len(plan.Execution.Steps))
	}
	step := plan.Execution.Steps[0]
	if len(step.Sequential) != 9 {
		t.Errorf("sequential step: got %d dept IDs, want 9", len(step.Sequential))
	}
	if len(step.Parallel) != 0 || len(step.Exclusive) != 0 {
		t.Error("expected a purely sequential step")
	}

	// Verify stage types and activities for the first department.
	design := plan.Departments[0]
	wantStages := []struct{ typ, activity string }{
		{"prep", "PrepActivity"},
		{"review", "ReviewActivity"},
		{"approve", "ApproveActivity"},
	}
	for i, ws := range wantStages {
		if design.Stages[i].Type != ws.typ {
			t.Errorf("design.Stages[%d].Type = %q, want %q", i, design.Stages[i].Type, ws.typ)
		}
		if design.Stages[i].Activity != ws.activity {
			t.Errorf("design.Stages[%d].Activity = %q, want %q", i, design.Stages[i].Activity, ws.activity)
		}
	}

	// Verify TaskQueue default.
	if plan.TaskQueue != "wf-queue-default" {
		t.Errorf("TaskQueue = %q, want %q", plan.TaskQueue, "wf-queue-default")
	}
}

func TestCompile_ParallelDiagram(t *testing.T) {
	bpmn := mustReadBPMN(t, "parallel-diagram.bpmn")
	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	// Expected execution plan:
	// step 0: sequential ["design"]
	// step 1: parallel   ["procurement", "planning"]
	// step 2: exclusive  [{construction, "$.approved == true"}, {qa, "$.approved == false"}]
	// step 3: sequential ["commissioning"]
	if len(plan.Execution.Steps) != 4 {
		t.Fatalf("execution steps: got %d, want 4\n steps: %+v", len(plan.Execution.Steps), plan.Execution.Steps)
	}

	s0 := plan.Execution.Steps[0]
	if len(s0.Sequential) != 1 || s0.Sequential[0] != "design" {
		t.Errorf("step[0]: want sequential=[design], got %+v", s0)
	}

	s1 := plan.Execution.Steps[1]
	if len(s1.Parallel) != 2 || s1.Parallel[0] != "procurement" || s1.Parallel[1] != "planning" {
		t.Errorf("step[1]: want parallel=[procurement,planning], got %+v", s1)
	}

	s2 := plan.Execution.Steps[2]
	if len(s2.Exclusive) != 2 {
		t.Fatalf("step[2]: want 2 exclusive branches, got %d", len(s2.Exclusive))
	}
	if s2.Exclusive[0].Target != "construction" {
		t.Errorf("exclusive[0].Target = %q, want \"construction\"", s2.Exclusive[0].Target)
	}
	if s2.Exclusive[1].Target != "qa" {
		t.Errorf("exclusive[1].Target = %q, want \"qa\"", s2.Exclusive[1].Target)
	}
	if s2.Exclusive[0].ConditionExpression != "$.approved == true" {
		t.Errorf("exclusive[0].ConditionExpression = %q", s2.Exclusive[0].ConditionExpression)
	}
	if s2.Exclusive[1].ConditionExpression != "$.approved == false" {
		t.Errorf("exclusive[1].ConditionExpression = %q", s2.Exclusive[1].ConditionExpression)
	}

	s3 := plan.Execution.Steps[3]
	if len(s3.Sequential) != 1 || s3.Sequential[0] != "commissioning" {
		t.Errorf("step[3]: want sequential=[commissioning], got %+v", s3)
	}
}

func TestValidate_LoopDiagram_GuardedLoopValid(t *testing.T) {
	bpmn := mustReadBPMN(t, "loop-diagram.bpmn")
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("guarded loop should be valid; got %v", errCodes(errs))
	}
}

func TestCompile_LoopDiagram(t *testing.T) {
	bpmn := mustReadBPMN(t, "loop-diagram.bpmn")
	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	// Expected (parallel split/join + guarded rework loop):
	// step 0: sequential ["design"]
	// step 1: parallel   ["procurement", "planning"]
	// step 2: sequential ["construction"]
	// step 3: exclusive  [{forward, approved==true}, {revert construction/prep, approved==false}]
	if len(plan.Execution.Steps) != 4 {
		t.Fatalf("execution steps: got %d, want 4\n steps: %+v", len(plan.Execution.Steps), plan.Execution.Steps)
	}
	if s0 := plan.Execution.Steps[0]; len(s0.Sequential) != 1 || s0.Sequential[0] != "design" {
		t.Errorf("step[0]: want sequential=[design], got %+v", s0)
	}
	if s1 := plan.Execution.Steps[1]; len(s1.Parallel) != 2 || s1.Parallel[0] != "procurement" || s1.Parallel[1] != "planning" {
		t.Errorf("step[1]: want parallel=[procurement,planning], got %+v", s1)
	}
	if s2 := plan.Execution.Steps[2]; len(s2.Sequential) != 1 || s2.Sequential[0] != "construction" {
		t.Errorf("step[2]: want sequential=[construction], got %+v", s2)
	}

	s3 := plan.Execution.Steps[3]
	if len(s3.Exclusive) != 2 {
		t.Fatalf("step[3]: want 2 exclusive branches, got %d: %+v", len(s3.Exclusive), s3.Exclusive)
	}
	var revert *domain.ExclusiveBranch
	for i := range s3.Exclusive {
		if s3.Exclusive[i].RevertToDept != "" {
			revert = &s3.Exclusive[i]
		}
	}
	if revert == nil {
		t.Fatalf("expected a revert branch, got %+v", s3.Exclusive)
	}
	if revert.RevertToDept != "construction" || revert.RevertToStage != "prep" {
		t.Errorf("revert target = %q/%q, want construction/prep", revert.RevertToDept, revert.RevertToStage)
	}
	if revert.ConditionExpression != "$.approved == false" {
		t.Errorf("revert condition = %q, want $.approved == false", revert.ConditionExpression)
	}
}

func TestCompile_StageDefFields(t *testing.T) {
	bpmn := mustReadBPMN(t, "initial-diagram.bpmn")
	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	// Review stage has one assignee and requires_comment=true.
	review := plan.Departments[0].Stages[1]
	if !review.RequiresComment {
		t.Error("review.RequiresComment = false, want true")
	}
	if len(review.DefaultAssignees) != 1 {
		t.Errorf("review.DefaultAssignees length = %d, want 1", len(review.DefaultAssignees))
	}

	// Prep stage has requires_comment=false.
	prep := plan.Departments[0].Stages[0]
	if prep.RequiresComment {
		t.Error("prep.RequiresComment = true, want false")
	}
}

func TestCompile_DeptFromLane(t *testing.T) {
	// Department ID and label both come from the lane name the task appears in.
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_contracts" name="contracts">
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1" name="Start"/>
    ` + validTask("Task_1", "", "prep") + `
    <bpmn:endEvent id="EndEvent_1" name="End"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="EndEvent_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1">
      <bpmndi:BPMNShape id="Sh_StartEvent_1" bpmnElement="StartEvent_1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_1"       bpmnElement="Task_1"><dc:Bounds x="250" y="60" width="100" height="80"/></bpmndi:BPMNShape>
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
	if len(plan.Departments) != 1 {
		t.Fatalf("departments: got %d, want 1 (%+v)", len(plan.Departments), plan.Departments)
	}
	if plan.Departments[0].ID != "contracts" {
		t.Errorf("department ID = %q, want \"contracts\" (from lane name)", plan.Departments[0].ID)
	}
	if plan.Departments[0].Label != "contracts" {
		t.Errorf("department Label = %q, want \"contracts\" (identity from lane name)", plan.Departments[0].Label)
	}
}

func TestCompile_RejectedElement(t *testing.T) {
	// A serviceTask is on the §4.1.4 reject list; the upload must fail with a
	// structured REJECTED_ELEMENT rather than be silently dropped.
	bpmn := minimalBPMN(`
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:serviceTask id="SVC_1" name="Call API"/>
    ` + validTask("Task_1", "design", "prep") + `
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="EndEvent_1"/>`)

	_, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	var valErr *domain.ValidationFailedError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected ValidationFailedError; got %v", err)
	}
	if !hasCode(valErr.Errors, domain.BPMNErrRejectedElement) {
		t.Errorf("expected REJECTED_ELEMENT; got %v", valErr.Errors)
	}
}

func TestValidate_AllFailuresAggregated(t *testing.T) {
	// One parseable document carrying three faults from three different validators.
	// The validator must return all of them (not just the first), matching the LLD's
	// multi-error contract.
	taskMissingRole := `<bpmn:userTask id="Task_bad" name="Bad">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>`

	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="D">
  <bpmn:process id="P1" isExecutable="true">
    <bpmn:laneSet id="LS">
      <bpmn:lane id="L_design" name="Design">
        <bpmn:flowNodeRef>Task_bad</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_A</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_B</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1"/>
    ` + taskMissingRole + `
    <bpmn:exclusiveGateway id="GW_x"/>
    ` + validTask("Task_A", "design", "prep") + `
    ` + validTask("Task_B", "design", "review") + `
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_bad"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_bad"     targetRef="GW_x"/>
    <bpmn:sequenceFlow id="F3" sourceRef="GW_x"         targetRef="Task_A"/>
    <bpmn:sequenceFlow id="F4" sourceRef="GW_x"         targetRef="Task_B"/>
    <bpmn:sequenceFlow id="F5" sourceRef="Task_A"       targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`
	// Faults: Task_bad has empty candidateGroups (CANDIDATE_GROUPS_EMPTY); GW_x is an
	// exclusive split whose one branch (Task_B) is dangling — not all branches terminate,
	// so UNMATCHED_GATEWAY fires; Task_B has no outgoing flow (DANGLING_NODE).

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Validate() transport error: %v", err)
	}
	for _, want := range []domain.BPMNErrorCode{
		domain.BPMNErrCandidateGroupsEmpty,
		domain.BPMNErrUnmatchedGateway,
		domain.BPMNErrDanglingNode,
	} {
		if !hasCode(errs, want) {
			t.Errorf("expected %s in the aggregated errors; got %v", want, errCodes(errs))
		}
	}
	if len(errs) < 3 {
		t.Errorf("expected at least 3 aggregated errors; got %d: %v", len(errs), errCodes(errs))
	}
}

func TestCompile_MalformedBPMN_Sentinel(t *testing.T) {
	// Declares both namespaces (so it is not a MISSING_NAMESPACE case) but is
	// unparseable — must wrap domain.ErrMalformedBPMN so the HTTP layer maps to 400.
	bpmn := `<?xml version="1.0"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"><unclosed>`
	c := bpmn_compiler.New()

	_, cErr := c.Compile(context.Background(), bpmn)
	if !errors.Is(cErr, domain.ErrMalformedBPMN) {
		t.Errorf("Compile() error should wrap ErrMalformedBPMN; got %v", cErr)
	}
	_, vErr := c.Validate(context.Background(), bpmn)
	if !errors.Is(vErr, domain.ErrMalformedBPMN) {
		t.Errorf("Validate() error should wrap ErrMalformedBPMN; got %v", vErr)
	}
}

func TestCompile_ForbiddenXML_Sentinel(t *testing.T) {
	// A forbidden DOCTYPE must satisfy BOTH ErrMalformedBPMN (→ client 400
	// INVALID_BPMN_XML) and ErrForbiddenXML (→ internal security log).
	bpmn := `<?xml version="1.0"?><!DOCTYPE x [<!ENTITY a "b">]>` +
		`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"/>`
	_, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if !errors.Is(err, domain.ErrMalformedBPMN) {
		t.Errorf("forbidden XML should wrap ErrMalformedBPMN; got %v", err)
	}
	if !errors.Is(err, domain.ErrForbiddenXML) {
		t.Errorf("forbidden XML should wrap ErrForbiddenXML; got %v", err)
	}
}

func TestValidate_MissingNamespace(t *testing.T) {
	// BPMN root namespace present, Zeebe namespace absent (§4.1.1 requires both).
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="D1">
  <bpmn:process id="P1" isExecutable="true"/>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Validate() returned a transport error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrMissingNamespace) {
		t.Errorf("expected MISSING_NAMESPACE; got %v", errs)
	}
}

func TestHash_Deterministic(t *testing.T) {
	c := bpmn_compiler.New()
	bpmn := mustReadBPMN(t, "initial-diagram.bpmn")

	h1, err := c.Hash(bpmn)
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}
	h2, err := c.Hash(bpmn)
	if err != nil {
		t.Fatalf("Hash() second call error: %v", err)
	}
	if h1 != h2 {
		t.Errorf("Hash() is not deterministic: %q != %q", h1, h2)
	}
}

func TestHash_DifferentBPMNProducesDifferentHash(t *testing.T) {
	c := bpmn_compiler.New()
	h1, _ := c.Hash(mustReadBPMN(t, "initial-diagram.bpmn"))
	h2, _ := c.Hash(mustReadBPMN(t, "parallel-diagram.bpmn"))
	if h1 == h2 {
		t.Error("different BPMN files produced the same hash")
	}
}

func TestHash_AttributeOrderDoesNotAffectHash(t *testing.T) {
	// Two BPMN strings that differ only in attribute order on assignmentDefinition.
	bpmnA := minimalBPMN(
		`<bpmn:startEvent id="StartEvent_1" name="Start"/>` +
			`<bpmn:userTask id="Task_1" name="T">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements></bpmn:userTask>` +
			`<bpmn:endEvent id="EndEvent_1" name="End"/>
      <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
      <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="EndEvent_1"/>`,
	)
	// Swap attribute order on assignmentDefinition — semantically identical.
	bpmnB := strings.ReplaceAll(bpmnA,
		`<zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="`+aliceUUID+`"/>`,
		`<zeebe:assignmentDefinition candidateUsers="`+aliceUUID+`" candidateGroups="preparer"/>`,
	)

	c := bpmn_compiler.New()
	h1, err := c.Hash(bpmnA)
	if err != nil {
		t.Fatalf("Hash(A): %v", err)
	}
	h2, err := c.Hash(bpmnB)
	if err != nil {
		t.Fatalf("Hash(B): %v", err)
	}
	if h1 != h2 {
		t.Errorf("attribute order should not affect hash: %q != %q", h1, h2)
	}
}

const noProcessBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
</bpmn:definitions>`

func TestValidate_NoProcess(t *testing.T) {
	errs, err := bpmn_compiler.New().Validate(context.Background(), noProcessBPMN)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(errs) == 0 {
		t.Error("expected at least one validation error for a definitions with no process")
	}
}

func TestCompile_NoProcess(t *testing.T) {
	_, err := bpmn_compiler.New().Compile(context.Background(), noProcessBPMN)
	if err == nil {
		t.Error("Compile() should return error when definitions has no process")
	}
}

func TestHash_NoProcess(t *testing.T) {
	_, err := bpmn_compiler.New().Hash(noProcessBPMN)
	if err == nil {
		t.Error("Hash() should return error when definitions has no process")
	}
}

func TestCompile_ValidationFailure(t *testing.T) {
	// BPMN that parses but fails structural validation (cycle).
	bpmn := minimalBPMN(
		`<bpmn:startEvent id="StartEvent_1" name="Start"/>` +
			validTask("Task_1", "design", "prep") +
			validTask("Task_2", "design", "review") +
			`<bpmn:endEvent id="EndEvent_1" name="End"/>
      <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
      <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="Task_2"/>
      <bpmn:sequenceFlow id="F3" sourceRef="Task_2"       targetRef="Task_1"/>
      <bpmn:sequenceFlow id="F4" sourceRef="Task_2"       targetRef="EndEvent_1"/>`,
	)
	_, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err == nil {
		t.Fatal("Compile() should return error for BPMN with cycles")
	}
	var vErr *domain.ValidationFailedError
	if !isValidationFailedError(err, &vErr) {
		t.Errorf("expected *domain.ValidationFailedError, got %T: %v", err, err)
	}
}

func TestHash_ParseError(t *testing.T) {
	_, err := bpmn_compiler.New().Hash(`<?xml version="1.0"?><unclosed>`)
	if err == nil {
		t.Error("Hash() should return error for malformed XML")
	}
}

func TestValidate_MultipleProcesses(t *testing.T) {
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="One" isExecutable="true">
    <bpmn:startEvent id="S1"/><bpmn:endEvent id="E1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="S1" targetRef="E1"/>
  </bpmn:process>
  <bpmn:process id="P2" name="Two" isExecutable="true">
    <bpmn:startEvent id="S2"/><bpmn:endEvent id="E2"/>
  </bpmn:process>
</bpmn:definitions>`
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrMultipleProcesses) {
		t.Errorf("expected MULTIPLE_PROCESSES; got %v", errCodes(errs))
	}
}

func TestCompile_MultipleProcesses(t *testing.T) {
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="One" isExecutable="true"/>
  <bpmn:process id="P2" name="Two" isExecutable="true"/>
</bpmn:definitions>`
	_, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err == nil {
		t.Error("Compile() should return error for definitions with multiple processes")
	}
}

func TestValidate_CountTokensError(t *testing.T) {
	// An undefined entity reference passes the DOCTYPE/ENTITY security scan but
	// causes xml.Decoder.Token() to return a hard error with Strict=true.
	bpmn := `<?xml version="1.0"?>` +
		`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">` +
		`&undefined_entity;` +
		`</bpmn:definitions>`
	_, err := bpmn_compiler.New().Validate(context.Background(), bpmn)
	if err == nil {
		t.Error("Validate() should return a parse error for an undefined entity reference")
	}
}

func TestValidate_ZeroProcesses_ErrorCode(t *testing.T) {
	errs, err := bpmn_compiler.New().Validate(context.Background(), noProcessBPMN)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrMultipleProcesses) {
		t.Errorf("0-process definitions should emit MULTIPLE_PROCESSES; got %v", errCodes(errs))
	}
}

const multiProcessBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="One" isExecutable="true"/>
  <bpmn:process id="P2" name="Two" isExecutable="true"/>
</bpmn:definitions>`

func TestHash_MultipleProcesses(t *testing.T) {
	_, err := bpmn_compiler.New().Hash(multiProcessBPMN)
	if err == nil {
		t.Error("Hash() should return error when definitions has multiple processes")
	}
}

func TestValidate_ValidCandidateUser(t *testing.T) {
	// A single valid UUID v7 in candidateUsers must not produce validation errors.
	taskXML := `<bpmn:userTask id="Task_1" name="T">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements></bpmn:userTask>`
	bpmnXML := minimalBPMN(
		`<bpmn:startEvent id="StartEvent_1" name="Start"/>` +
			taskXML +
			`<bpmn:endEvent id="EndEvent_1" name="End"/>
      <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
      <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="EndEvent_1"/>`,
	)

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if hasCode(errs, domain.BPMNErrInvalidCandidateUser) {
		t.Errorf("valid UUID should not produce INVALID_CANDIDATE_USER; got %v", errCodes(errs))
	}
}

func TestCompile_ParseError_WrapsPrefix(t *testing.T) {
	_, err := bpmn_compiler.New().Compile(context.Background(), `<?xml version="1.0"?><unclosed>`)
	if err == nil {
		t.Fatal("Compile() should return error for malformed XML")
	}
	if !strings.Contains(err.Error(), "bpmn compile:") {
		t.Errorf("expected error to contain 'bpmn compile:' prefix; got: %v", err)
	}
}

func TestValidate_DanglingBoundaryEvent(t *testing.T) {
	// A timer boundary event with no outgoing sequence flow has no path to continue.
	bpmn := minimalBPMN(
		`<bpmn:startEvent id="StartEvent_1" name="Start"/>` +
			validTask("Task_1", "design", "prep") +
			`<bpmn:boundaryEvent id="BE_Timer_1" attachedToRef="Task_1" cancelActivity="true">
        <bpmn:timerEventDefinition>
          <bpmn:timeDuration>PT1H</bpmn:timeDuration>
        </bpmn:timerEventDefinition>
      </bpmn:boundaryEvent>
      <bpmn:endEvent id="EndEvent_1" name="End"/>
      <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
      <bpmn:sequenceFlow id="F2" sourceRef="Task_1" targetRef="EndEvent_1"/>`,
	)
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrDanglingNode) {
		t.Errorf("expected DANGLING_NODE for boundary event with no outgoing; got %v", errCodes(errs))
	}
}

const subprocessUnknownErrorRefBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:error id="Error_Known" errorCode="KNOWN"/>
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design">
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>SubProcess_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>BE_Error_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_after</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1" name="Start"/>
    <bpmn:subProcess id="SubProcess_1" name="Inner">
      <bpmn:laneSet id="InnerLS">
        <bpmn:lane id="Inner_design" name="design">
          <bpmn:flowNodeRef>InnerStart</bpmn:flowNodeRef>
          <bpmn:flowNodeRef>InnerTask</bpmn:flowNodeRef>
          <bpmn:flowNodeRef>InnerEnd</bpmn:flowNodeRef>
        </bpmn:lane>
      </bpmn:laneSet>
      <bpmn:startEvent id="InnerStart" name="Inner Start"/>
      <bpmn:userTask id="InnerTask" name="Inner Task">
        <bpmn:extensionElements>
          <zeebe:taskDefinition type="prep"/>
          <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
        </bpmn:extensionElements>
      </bpmn:userTask>
      <bpmn:endEvent id="InnerEnd" name="Inner End"/>
      <bpmn:sequenceFlow id="IF1" sourceRef="InnerStart" targetRef="InnerTask"/>
      <bpmn:sequenceFlow id="IF2" sourceRef="InnerTask" targetRef="InnerEnd"/>
    </bpmn:subProcess>
    <bpmn:boundaryEvent id="BE_Error_1" attachedToRef="SubProcess_1">
      <bpmn:errorEventDefinition errorRef="Error_DoesNotExist"/>
    </bpmn:boundaryEvent>
    <bpmn:userTask id="Task_after" name="Task After">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="EndEvent_1" name="End"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="SubProcess_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="SubProcess_1" targetRef="Task_after"/>
    <bpmn:sequenceFlow id="F3" sourceRef="BE_Error_1" targetRef="Task_after"/>
    <bpmn:sequenceFlow id="F4" sourceRef="Task_after" targetRef="EndEvent_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1"/>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

func TestValidate_SubProcess_UnknownErrorRef(t *testing.T) {
	// An error boundary event whose errorRef does not match any bpmn:error at the
	// definitions level must produce INVALID_BOUNDARY_ATTACHMENT.
	errs, err := bpmn_compiler.New().Validate(context.Background(), subprocessUnknownErrorRefBPMN)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrInvalidBoundaryAttachment) {
		t.Errorf("expected INVALID_BOUNDARY_ATTACHMENT for unknown errorRef; got %v", errCodes(errs))
	}
}

func TestValidate_UnguardedLoop_ExclusiveGatewayNoForwardExit(t *testing.T) {
	// GW_loop's only outgoing edge leads back to Task_1 (a back-edge). Since there
	// is no forward exit, the loop cannot terminate → UNGUARDED_LOOP.
	bpmn := minimalBPMN(
		`<bpmn:startEvent id="StartEvent_1" name="Start"/>` +
			validTask("Task_1", "design", "prep") +
			`<bpmn:exclusiveGateway id="GW_loop"/>
      <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
      <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="GW_loop"/>
      <bpmn:sequenceFlow id="F3" sourceRef="GW_loop"      targetRef="Task_1"/>`,
	)
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrUnguardedLoop) {
		t.Errorf("expected UNGUARDED_LOOP for exclusive gateway with no forward exit; got %v", errCodes(errs))
	}
}

const subprocessTimerBoundaryBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design">
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>SubProcess_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>BE_Timer_sub</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_after</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1" name="Start"/>
    <bpmn:subProcess id="SubProcess_1" name="Inner">
      <bpmn:laneSet id="InnerLS">
        <bpmn:lane id="Inner_design" name="design">
          <bpmn:flowNodeRef>InnerStart</bpmn:flowNodeRef>
          <bpmn:flowNodeRef>InnerTask</bpmn:flowNodeRef>
          <bpmn:flowNodeRef>InnerEnd</bpmn:flowNodeRef>
        </bpmn:lane>
      </bpmn:laneSet>
      <bpmn:startEvent id="InnerStart" name="Inner Start"/>
      <bpmn:userTask id="InnerTask" name="Inner Task">
        <bpmn:extensionElements>
          <zeebe:taskDefinition type="prep"/>
          <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
        </bpmn:extensionElements>
      </bpmn:userTask>
      <bpmn:endEvent id="InnerEnd" name="Inner End"/>
      <bpmn:sequenceFlow id="IF1" sourceRef="InnerStart" targetRef="InnerTask"/>
      <bpmn:sequenceFlow id="IF2" sourceRef="InnerTask" targetRef="InnerEnd"/>
    </bpmn:subProcess>
    <bpmn:boundaryEvent id="BE_Timer_sub" attachedToRef="SubProcess_1" cancelActivity="true">
      <bpmn:timerEventDefinition>
        <bpmn:timeDuration>72h</bpmn:timeDuration>
      </bpmn:timerEventDefinition>
    </bpmn:boundaryEvent>
    <bpmn:userTask id="Task_after" name="Task After">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="EndEvent_1" name="End"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="SubProcess_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="SubProcess_1" targetRef="Task_after"/>
    <bpmn:sequenceFlow id="F3" sourceRef="BE_Timer_sub" targetRef="Task_after"/>
    <bpmn:sequenceFlow id="F4" sourceRef="Task_after" targetRef="EndEvent_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1">
      <bpmndi:BPMNShape id="Sh_StartEvent_1" bpmnElement="StartEvent_1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_SubProcess_1" bpmnElement="SubProcess_1"><dc:Bounds x="250" y="50" width="300" height="200"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_InnerStart"   bpmnElement="InnerStart"><dc:Bounds x="282" y="122" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_InnerTask"    bpmnElement="InnerTask"><dc:Bounds x="370" y="100" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_InnerEnd"     bpmnElement="InnerEnd"><dc:Bounds x="522" y="122" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_BE_Timer_sub" bpmnElement="BE_Timer_sub"><dc:Bounds x="382" y="232" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_after"   bpmnElement="Task_after"><dc:Bounds x="600" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_EndEvent_1"   bpmnElement="EndEvent_1"><dc:Bounds x="752" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_IF1" bpmnElement="IF1"/>
      <bpmndi:BPMNEdge id="Edge_IF2" bpmnElement="IF2"/>
      <bpmndi:BPMNEdge id="Edge_F1"  bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2"  bpmnElement="F2"/>
      <bpmndi:BPMNEdge id="Edge_F3"  bpmnElement="F3"/>
      <bpmndi:BPMNEdge id="Edge_F4"  bpmnElement="F4"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

func TestCompile_SubProcess_WithTimerBoundary(t *testing.T) {
	// A timer boundary event attached to a subprocess populates SubWorkflowStep.TimerPaths.
	plan, err := bpmn_compiler.New().Compile(context.Background(), subprocessTimerBoundaryBPMN)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	if len(plan.Execution.Steps) == 0 {
		t.Fatal("expected at least one execution step")
	}
	var subStep *domain.SubWorkflowStep
	for i := range plan.Execution.Steps {
		if plan.Execution.Steps[i].SubWorkflow != nil {
			subStep = plan.Execution.Steps[i].SubWorkflow
			break
		}
	}
	if subStep == nil {
		t.Fatal("expected a SubWorkflow step")
	}
	if len(subStep.TimerPaths) != 1 {
		t.Fatalf("TimerPaths: got %d, want 1", len(subStep.TimerPaths))
	}
	if subStep.TimerPaths[0].Duration != "72h" {
		t.Errorf("TimerPaths[0].Duration = %q, want \"72h\"", subStep.TimerPaths[0].Duration)
	}
	if !subStep.TimerPaths[0].Interrupting {
		t.Error("expected Interrupting=true for cancelActivity=true")
	}
}

func TestCompile_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bpmn := minimalBPMN(
		`<bpmn:startEvent id="StartEvent_1"/>` + validTask("Task_1", "design", "prep") +
			`<bpmn:endEvent id="EndEvent_1"/>
      <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
      <bpmn:sequenceFlow id="F2" sourceRef="Task_1" targetRef="EndEvent_1"/>`,
	)
	_, err := bpmn_compiler.New().Compile(ctx, bpmn)
	if err == nil {
		t.Fatal("Compile() should return error for canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error should wrap context.Canceled; got: %v", err)
	}
	if errors.Is(err, domain.ErrMalformedBPMN) {
		t.Errorf("context cancellation must not wrap ErrMalformedBPMN; got: %v", err)
	}
}

func TestValidate_RejectedElement_NoID(t *testing.T) {
	// A rejected element without an id attribute should surface as REJECTED_ELEMENT
	// with the element's local name as the node identifier (elementID fallback path).
	bpmn := minimalBPMN(
		`<bpmn:startEvent id="StartEvent_1"/>` +
			`<bpmn:serviceTask name="No ID Service"/>` +
			validTask("Task_1", "design", "prep") +
			`<bpmn:endEvent id="EndEvent_1"/>
      <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
      <bpmn:sequenceFlow id="F2" sourceRef="Task_1" targetRef="EndEvent_1"/>`,
	)
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrRejectedElement) {
		t.Errorf("expected REJECTED_ELEMENT for serviceTask without id; got %v", errCodes(errs))
	}
}

func isValidationFailedError(err error, target **domain.ValidationFailedError) bool {
	if err == nil {
		return false
	}
	if vErr, ok := err.(*domain.ValidationFailedError); ok {
		if target != nil {
			*target = vErr
		}
		return true
	}
	return false
}

func hasCode(errs []domain.BPMNValidationError, code domain.BPMNErrorCode) bool {
	for _, e := range errs {
		if e.Code == code {
			return true
		}
	}
	return false
}

func errCodes(errs []domain.BPMNValidationError) []domain.BPMNErrorCode {
	codes := make([]domain.BPMNErrorCode, len(errs))
	for i, e := range errs {
		codes[i] = e.Code
	}
	return codes
}

// ---------- Timer boundary event tests ----------

func TestCompile_TimerBoundary_Interrupting(t *testing.T) {
	c := bpmn_compiler.New()
	plan, err := c.Compile(context.Background(), mustReadBPMN(t, "timer-boundary.bpmn"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "design" dept must have a stage with BoundaryTimer set
	var designDept *domain.DepartmentDef
	for i := range plan.Departments {
		if plan.Departments[i].ID == "design" {
			designDept = &plan.Departments[i]
		}
	}
	if designDept == nil {
		t.Fatal("expected department 'design' in compiled plan")
	}
	if len(designDept.Stages) == 0 {
		t.Fatal("expected at least one stage in 'design' department")
	}
	stage := designDept.Stages[0]
	if stage.BoundaryTimer == nil {
		t.Fatal("expected BoundaryTimer to be set on design prep stage")
	}
	if stage.BoundaryTimer.Duration != "72h" {
		t.Errorf("BoundaryTimer.Duration = %q; want %q", stage.BoundaryTimer.Duration, "72h")
	}
	if !stage.BoundaryTimer.Interrupting {
		t.Error("BoundaryTimer.Interrupting should be true (cancelActivity=true)")
	}
	if stage.BoundaryTimer.TargetDept != "qa" {
		t.Errorf("BoundaryTimer.TargetDept = %q; want %q", stage.BoundaryTimer.TargetDept, "qa")
	}
}

func TestCompile_TimerBoundary_NonInterrupting(t *testing.T) {
	c := bpmn_compiler.New()
	plan, err := c.Compile(context.Background(), mustReadBPMN(t, "timer-boundary-noninterrupting.bpmn"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var designDept *domain.DepartmentDef
	for i := range plan.Departments {
		if plan.Departments[i].ID == "design" {
			designDept = &plan.Departments[i]
		}
	}
	if designDept == nil || len(designDept.Stages) == 0 {
		t.Fatal("expected design department with stages")
	}
	bt := designDept.Stages[0].BoundaryTimer
	if bt == nil {
		t.Fatal("expected BoundaryTimer on design prep stage")
	}
	if bt.Duration != "P3D" {
		t.Errorf("BoundaryTimer.Duration = %q; want %q", bt.Duration, "P3D")
	}
	if bt.Interrupting {
		t.Error("BoundaryTimer.Interrupting should be false (cancelActivity=false)")
	}
}

func TestValidate_TimerBoundary_InvalidDuration(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="Def_1">
  <bpmn:process id="P1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LS1">
      <bpmn:lane id="Lane_design" name="design">
        <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>T1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>E1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="S1"/>
    <bpmn:userTask id="T1" name="Task">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:boundaryEvent id="BE1" attachedToRef="T1">
      <bpmn:timerEventDefinition><bpmn:timeDuration>not-a-duration</bpmn:timeDuration></bpmn:timerEventDefinition>
    </bpmn:boundaryEvent>
    <bpmn:endEvent id="E1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="S1" targetRef="T1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="T1" targetRef="E1"/>
    <bpmn:sequenceFlow id="F3" sourceRef="BE1" targetRef="E1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="P1"/>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrInvalidSLADuration) {
		t.Errorf("expected INVALID_SLA_DURATION; got %v", errCodes(errs))
	}
}

func TestValidate_ErrorBoundary_OnUserTask_Rejected(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="Def_1">
  <bpmn:process id="P1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LS1">
      <bpmn:lane id="Lane_design" name="design">
        <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>T1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>E1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="S1"/>
    <bpmn:userTask id="T1" name="Task">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:boundaryEvent id="BE1" attachedToRef="T1">
      <bpmn:errorEventDefinition/>
    </bpmn:boundaryEvent>
    <bpmn:endEvent id="E1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="S1" targetRef="T1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="T1" targetRef="E1"/>
    <bpmn:sequenceFlow id="F3" sourceRef="BE1" targetRef="E1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="P1"/>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrInvalidBoundaryAttachment) {
		t.Errorf("expected INVALID_BOUNDARY_ATTACHMENT for error boundary on userTask; got %v", errCodes(errs))
	}
}

// ---------- Subprocess tests ----------

func TestCompile_Subprocess_MergesDepartments(t *testing.T) {
	c := bpmn_compiler.New()
	plan, err := c.Compile(context.Background(), mustReadBPMN(t, "subprocess.bpmn"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The subprocess contains two design tasks; they should appear in the parent catalog.
	var designDept *domain.DepartmentDef
	for i := range plan.Departments {
		if plan.Departments[i].ID == "design" {
			designDept = &plan.Departments[i]
		}
	}
	if designDept == nil {
		t.Fatal("expected 'design' department merged from subprocess into parent catalog")
	}
	if len(designDept.Stages) < 2 {
		t.Errorf("expected ≥2 stages in 'design' from subprocess; got %d", len(designDept.Stages))
	}

	// QA dept from the post-subprocess task must also be present.
	var hasQA bool
	for _, d := range plan.Departments {
		if d.ID == "qa" {
			hasQA = true
		}
	}
	if !hasQA {
		t.Error("expected 'qa' department in parent catalog from post-subprocess task")
	}

	// There must be a SubWorkflow step in the execution plan.
	var hasSubWorkflow bool
	for _, step := range plan.Execution.Steps {
		if step.SubWorkflow != nil {
			hasSubWorkflow = true
			if step.SubWorkflow.Name != "Design Review Sub" {
				t.Errorf("SubWorkflow.Name = %q; want %q", step.SubWorkflow.Name, "Design Review Sub")
			}
		}
	}
	if !hasSubWorkflow {
		t.Error("expected at least one SubWorkflow execution step")
	}
}

func TestCompile_SubprocessError_ErrorPath(t *testing.T) {
	c := bpmn_compiler.New()
	plan, err := c.Compile(context.Background(), mustReadBPMN(t, "subprocess-error.bpmn"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var sw *domain.SubWorkflowStep
	for _, step := range plan.Execution.Steps {
		if step.SubWorkflow != nil {
			sw = step.SubWorkflow
		}
	}
	if sw == nil {
		t.Fatal("expected a SubWorkflow step in execution plan")
	}
	if len(sw.ErrorPaths) == 0 {
		t.Fatal("expected at least one error path on subprocess")
	}
	ep := sw.ErrorPaths[0]
	if ep.ErrorCode != "REJECTED" {
		t.Errorf("ErrorPath.ErrorCode = %q; want %q", ep.ErrorCode, "REJECTED")
	}
	if ep.TargetDept != "qa" {
		t.Errorf("ErrorPath.TargetDept = %q; want %q", ep.TargetDept, "qa")
	}
}

func TestValidate_Subprocess_Valid(t *testing.T) {
	c := bpmn_compiler.New()
	for _, fixture := range []string{"subprocess.bpmn", "subprocess-error.bpmn"} {
		errs, err := c.Validate(context.Background(), mustReadBPMN(t, fixture))
		if err != nil {
			t.Errorf("%s: unexpected parse error: %v", fixture, err)
			continue
		}
		if len(errs) > 0 {
			t.Errorf("%s: expected 0 errors, got %d:", fixture, len(errs))
			for _, e := range errs {
				t.Errorf("  [%s] %s: %s", e.Code, e.NodeID, e.Message)
			}
		}
	}
}

// TestValidate_MissingBPMNNamespace verifies that a document which declares the Zeebe
// namespace but not the BPMN namespace returns MISSING_NAMESPACE — covers the
// BPMN-namespace branch in parse (the existing MissingNamespace test covers the
// Zeebe-namespace branch).
func TestValidate_MissingBPMNNamespace(t *testing.T) {
	// Zeebe namespace declared but BPMN namespace absent.
	bpmn := `<?xml version="1.0"?><definitions xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"/>`
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrMissingNamespace) {
		t.Errorf("expected MISSING_NAMESPACE; got %v", errs)
	}
}

// TestCompile_TimerBoundary_AlreadyVisitedTarget verifies that a non-interrupting
// timer boundary whose target is also reachable via the normal sequence flow does not
// produce a duplicate stage — compileTimerBoundaryPaths must skip already-visited nodes.
func TestCompile_TimerBoundary_AlreadyVisitedTarget(t *testing.T) {
	// Start → Task_A → Task_B → End
	// Task_A has a non-interrupting timer (PT1H) → Task_B
	// Task_B is visited during main traversal; the timer path is skipped.
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_TimerVisited" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="TimerVisited" isExecutable="true">
    <bpmn:laneSet id="LS">
      <bpmn:lane id="L_ops" name="ops">
        <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_A</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_B</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>E1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>BE_timer</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="S1"/>
    <bpmn:userTask id="Task_A" name="Step A">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:boundaryEvent id="BE_timer" attachedToRef="Task_A" cancelActivity="false">
      <bpmn:timerEventDefinition>
        <bpmn:timeDuration>PT1H</bpmn:timeDuration>
      </bpmn:timerEventDefinition>
    </bpmn:boundaryEvent>
    <bpmn:userTask id="Task_B" name="Step B">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="E1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="S1"      targetRef="Task_A"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_A"  targetRef="Task_B"/>
    <bpmn:sequenceFlow id="F3" sourceRef="Task_B"  targetRef="E1"/>
    <bpmn:sequenceFlow id="F4" sourceRef="BE_timer" targetRef="Task_B"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD">
    <bpmndi:BPMNPlane id="BP" bpmnElement="P1">
      <bpmndi:BPMNShape id="Sh_S1"      bpmnElement="S1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_A"  bpmnElement="Task_A"><dc:Bounds x="250" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_BE_timer" bpmnElement="BE_timer"><dc:Bounds x="282" y="122" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_B"  bpmnElement="Task_B"><dc:Bounds x="400" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_E1"      bpmnElement="E1"><dc:Bounds x="552" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
      <bpmndi:BPMNEdge id="Edge_F3" bpmnElement="F3"/>
      <bpmndi:BPMNEdge id="Edge_F4" bpmnElement="F4"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() unexpected error: %v", err)
	}
	// Task_B must appear exactly once (timer path skipped because it was already visited).
	stageCount := 0
	for _, dept := range plan.Departments {
		for _, s := range dept.Stages {
			if s.Type == "review" {
				stageCount++
			}
		}
	}
	if stageCount != 1 {
		t.Errorf("Task_B stage count = %d; want 1 (timer path must not duplicate it)", stageCount)
	}
}

// TestValidate_XOR_LoopWithAllTerminateBranches verifies that an XOR gateway that
// has a back-edge (loop) AND forward branches that all terminate at end events is
// considered valid — exercises the back-edge skip in allBranchesTerminate.
func TestValidate_XOR_LoopWithAllTerminateBranches(t *testing.T) {
	// XOR_gw has three outgoing:
	//   → End_yes  (forward, terminates)
	//   → End_no   (forward, terminates)
	//   → Task_A   (back-edge from XOR_gw; creates loop: Start→Task_A→XOR_gw→...→Task_A)
	// findJoin returns !ok (no merge node); allBranchesTerminate skips the back-edge
	// and returns true (both forward branches terminate) → valid.
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_XORLoop" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="XOR Loop" isExecutable="true">
    <bpmn:laneSet id="LS">
      <bpmn:lane id="L_ops" name="ops">
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_A</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
    <bpmn:userTask id="Task_A" name="Evaluate">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:exclusiveGateway id="XOR_gw"/>
    <bpmn:endEvent id="End_yes"/>
    <bpmn:endEvent id="End_no"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1" targetRef="Task_A"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_A"  targetRef="XOR_gw"/>
    <bpmn:sequenceFlow id="F3" name="yes" sourceRef="XOR_gw"  targetRef="End_yes"/>
    <bpmn:sequenceFlow id="F4" name="no"  sourceRef="XOR_gw"  targetRef="End_no"/>
    <bpmn:sequenceFlow id="F5" name="retry" sourceRef="XOR_gw" targetRef="Task_A"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD">
    <bpmndi:BPMNPlane id="BP" bpmnElement="P1">
      <bpmndi:BPMNShape id="Sh_Start_1"  bpmnElement="Start_1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_A"   bpmnElement="Task_A"><dc:Bounds x="250" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_XOR_gw"   bpmnElement="XOR_gw"><dc:Bounds x="405" y="75" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End_yes"  bpmnElement="End_yes"><dc:Bounds x="510" y="62" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End_no"   bpmnElement="End_no"><dc:Bounds x="510" y="162" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
      <bpmndi:BPMNEdge id="Edge_F3" bpmnElement="F3"/>
      <bpmndi:BPMNEdge id="Edge_F4" bpmnElement="F4"/>
      <bpmndi:BPMNEdge id="Edge_F5" bpmnElement="F5"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("expected valid BPMN with guarded loop; got errors: %v", errCodes(errs))
	}
}

// TestHash_SortedZeebeItems_MultipleProps verifies that two tasks with the same
// zeebe:property elements in different orders produce identical hashes — i.e.
// sortedZeebeItems normalises property order before hashing.
func TestHash_SortedZeebeItems_MultipleProps(t *testing.T) {
	makeBPMN := func(propOrder string) string {
		return minimalBPMN(
			`<bpmn:startEvent id="StartEvent_1"/>` +
				`<bpmn:userTask id="Task_1" name="Proposal">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="` + aliceUUID + `"/>
        <zeebe:properties>` + propOrder + `</zeebe:properties>
      </bpmn:extensionElements>
    </bpmn:userTask>` +
				`<bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="EndEvent_1"/>`,
		)
	}
	propsAB := `<zeebe:property name="aaa" value="1"/><zeebe:property name="zzz" value="2"/>`
	propsBA := `<zeebe:property name="zzz" value="2"/><zeebe:property name="aaa" value="1"/>`

	c := bpmn_compiler.New()
	h1, err := c.Hash(makeBPMN(propsAB))
	if err != nil {
		t.Fatalf("Hash(AB): %v", err)
	}
	h2, err := c.Hash(makeBPMN(propsBA))
	if err != nil {
		t.Fatalf("Hash(BA): %v", err)
	}
	if h1 != h2 {
		t.Errorf("zeebe property order should not affect hash: %q != %q", h1, h2)
	}
}

// TestValidate_XOR_EarlyExitBranchWithJoin_Valid covers the branch in checkSplitJoin
// where an exclusive gateway has a reconverging join but one branch exits early at
// an end event without passing through the join.
func TestValidate_XOR_EarlyExitBranchWithJoin_Valid(t *testing.T) {
	// XOR_split → Task_B → XOR_join → End_main   (normal branch, reconverges)
	// XOR_split → Task_C → XOR_join               (normal branch, reconverges)
	// XOR_split → End_early                        (early exit; valid for exclusive GW)
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_EarlyExit" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="EarlyExit" isExecutable="true">
    <bpmn:laneSet id="LS1">
      <bpmn:lane id="L_ops" name="ops">
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_A</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_B</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_C</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
    <bpmn:userTask id="Task_A" name="Evaluate">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:exclusiveGateway id="XOR_split"/>
    <bpmn:userTask id="Task_B" name="Path B">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:userTask id="Task_C" name="Path C">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:exclusiveGateway id="XOR_join"/>
    <bpmn:endEvent id="End_main"/>
    <bpmn:endEvent id="End_early"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1"   targetRef="Task_A"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_A"    targetRef="XOR_split"/>
    <bpmn:sequenceFlow id="F3" sourceRef="XOR_split" targetRef="Task_B"/>
    <bpmn:sequenceFlow id="F4" sourceRef="XOR_split" targetRef="Task_C"/>
    <bpmn:sequenceFlow id="F5" sourceRef="XOR_split" targetRef="End_early"/>
    <bpmn:sequenceFlow id="F6" sourceRef="Task_B"    targetRef="XOR_join"/>
    <bpmn:sequenceFlow id="F7" sourceRef="Task_C"    targetRef="XOR_join"/>
    <bpmn:sequenceFlow id="F8" sourceRef="XOR_join"  targetRef="End_main"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="P1">
      <bpmndi:BPMNShape id="Sh_Start_1"   bpmnElement="Start_1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_A"    bpmnElement="Task_A"><dc:Bounds x="250" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_XOR_split" bpmnElement="XOR_split"><dc:Bounds x="405" y="75" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_B"    bpmnElement="Task_B"><dc:Bounds x="510" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_C"    bpmnElement="Task_C"><dc:Bounds x="510" y="180" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_XOR_join"  bpmnElement="XOR_join"><dc:Bounds x="665" y="75" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End_main"  bpmnElement="End_main"><dc:Bounds x="772" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End_early" bpmnElement="End_early"><dc:Bounds x="510" y="290" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
      <bpmndi:BPMNEdge id="Edge_F3" bpmnElement="F3"/>
      <bpmndi:BPMNEdge id="Edge_F4" bpmnElement="F4"/>
      <bpmndi:BPMNEdge id="Edge_F5" bpmnElement="F5"/>
      <bpmndi:BPMNEdge id="Edge_F6" bpmnElement="F6"/>
      <bpmndi:BPMNEdge id="Edge_F7" bpmnElement="F7"/>
      <bpmndi:BPMNEdge id="Edge_F8" bpmnElement="F8"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("expected valid BPMN with early-exit branch; got errors: %v", errCodes(errs))
	}
}

// TestCompile_Subprocess_CatchAllErrorBoundary verifies that a subprocess with a
// catch-all error boundary event (no errorRef) compiles to a SubWorkflow step with
// an ErrorPath whose ErrorCode is empty string.
func TestCompile_Subprocess_CatchAllErrorBoundary(t *testing.T) {
	// subprocess-error.bpmn minus the bpmn:error element, and errorEventDefinition
	// with no errorRef — a catch-all that triggers on any error.
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_CatchAll" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P_CatchAll" name="CatchAll Error" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design">
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>SP_1</bpmn:flowNodeRef>
      </bpmn:lane>
      <bpmn:lane id="Lane_qa" name="qa">
        <bpmn:flowNodeRef>Task_qa</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>BE_catchall</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
    <bpmn:subProcess id="SP_1" name="Inner Sub">
      <bpmn:laneSet id="InnerLS">
        <bpmn:lane id="InnerLane" name="design">
          <bpmn:flowNodeRef>InnerStart</bpmn:flowNodeRef>
          <bpmn:flowNodeRef>InnerTask</bpmn:flowNodeRef>
          <bpmn:flowNodeRef>InnerEnd</bpmn:flowNodeRef>
        </bpmn:lane>
      </bpmn:laneSet>
      <bpmn:startEvent id="InnerStart"/>
      <bpmn:userTask id="InnerTask" name="Inner Prep">
        <bpmn:extensionElements>
          <zeebe:taskDefinition type="prep"/>
          <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="` + aliceUUID + `"/>
        </bpmn:extensionElements>
      </bpmn:userTask>
      <bpmn:endEvent id="InnerEnd"/>
      <bpmn:sequenceFlow id="IF1" sourceRef="InnerStart" targetRef="InnerTask"/>
      <bpmn:sequenceFlow id="IF2" sourceRef="InnerTask"  targetRef="InnerEnd"/>
    </bpmn:subProcess>
    <bpmn:boundaryEvent id="BE_catchall" attachedToRef="SP_1">
      <bpmn:errorEventDefinition/>
    </bpmn:boundaryEvent>
    <bpmn:userTask id="Task_qa" name="QA">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1"    targetRef="SP_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="SP_1"       targetRef="Task_qa"/>
    <bpmn:sequenceFlow id="F3" sourceRef="BE_catchall" targetRef="Task_qa"/>
    <bpmn:sequenceFlow id="F4" sourceRef="Task_qa"    targetRef="End_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="P_CatchAll">
      <bpmndi:BPMNShape id="Sh_Start_1"    bpmnElement="Start_1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_SP_1"       bpmnElement="SP_1"><dc:Bounds x="250" y="50" width="300" height="200"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_InnerStart" bpmnElement="InnerStart"><dc:Bounds x="282" y="122" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_InnerTask"  bpmnElement="InnerTask"><dc:Bounds x="370" y="100" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_InnerEnd"   bpmnElement="InnerEnd"><dc:Bounds x="522" y="122" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_BE_catchall" bpmnElement="BE_catchall"><dc:Bounds x="382" y="232" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_qa"    bpmnElement="Task_qa"><dc:Bounds x="600" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End_1"      bpmnElement="End_1"><dc:Bounds x="752" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_IF1" bpmnElement="IF1"/>
      <bpmndi:BPMNEdge id="Edge_IF2" bpmnElement="IF2"/>
      <bpmndi:BPMNEdge id="Edge_F1"  bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2"  bpmnElement="F2"/>
      <bpmndi:BPMNEdge id="Edge_F3"  bpmnElement="F3"/>
      <bpmndi:BPMNEdge id="Edge_F4"  bpmnElement="F4"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() unexpected error: %v", err)
	}

	var sw *domain.SubWorkflowStep
	for _, step := range plan.Execution.Steps {
		if step.SubWorkflow != nil {
			sw = step.SubWorkflow
		}
	}
	if sw == nil {
		t.Fatal("expected SubWorkflow step")
	}
	if len(sw.ErrorPaths) == 0 {
		t.Fatal("expected at least one error path on catch-all subprocess")
	}
	if sw.ErrorPaths[0].ErrorCode != "" {
		t.Errorf("catch-all error path: ErrorCode = %q; want empty string", sw.ErrorPaths[0].ErrorCode)
	}
}
