package bpmn_compiler_test

import (
	"context"
	_ "embed"
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

func mustReadBPMN(_ *testing.T, name string) string {
	switch name {
	case "initial-diagram.bpmn":
		return initialDiagramBPMN
	case "parallel-diagram.bpmn":
		return parallelDiagramBPMN
	default:
		panic("unknown fixture: " + name)
	}
}

// minimalBPMN wraps a process body in valid namespace declarations.
func minimalBPMN(processBody string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="Design">
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>` + processBody + `
  </bpmn:process>
</bpmn:definitions>`
}

const aliceUUID = "550e8400-e29b-41d4-a716-446655440000"

// validTask returns a minimal valid userTask XML block.
func validTask(id, deptID, stageType string) string {
	return `<bpmn:userTask id="` + id + `" name="` + id + `">
      <bpmn:extensionElements>
        <zeebe:properties>
          <zeebe:property name="dept_id"          value="` + deptID + `"/>
          <zeebe:property name="stage_type"       value="` + stageType + `"/>
          <zeebe:property name="role"             value="preparer"/>
          <zeebe:property name="default_user_ids" value="` + aliceUUID + `"/>
        </zeebe:properties>
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

func TestValidate_ZeebePropertyRules(t *testing.T) {
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
			name: "invalid stage_type",
			taskXML: `<bpmn:userTask id="Task_1" name="T">
        <bpmn:extensionElements><zeebe:properties>
          <zeebe:property name="dept_id"          value="design"/>
          <zeebe:property name="stage_type"       value="audit"/>
          <zeebe:property name="role"             value="preparer"/>
          <zeebe:property name="default_user_ids" value="` + aliceUUID + `"/>
        </zeebe:properties></bpmn:extensionElements></bpmn:userTask>`,
			wantCode: domain.BPMNErrInvalidStageType,
		},
		{
			name: "invalid dept_id (no matching lane)",
			taskXML: `<bpmn:userTask id="Task_1" name="T">
        <bpmn:extensionElements><zeebe:properties>
          <zeebe:property name="dept_id"          value="finance"/>
          <zeebe:property name="stage_type"       value="prep"/>
          <zeebe:property name="role"             value="preparer"/>
          <zeebe:property name="default_user_ids" value="` + aliceUUID + `"/>
        </zeebe:properties></bpmn:extensionElements></bpmn:userTask>`,
			wantCode: domain.BPMNErrInvalidDeptID,
		},
		{
			name: "missing default_user_ids",
			taskXML: `<bpmn:userTask id="Task_1" name="T">
        <bpmn:extensionElements><zeebe:properties>
          <zeebe:property name="dept_id"    value="design"/>
          <zeebe:property name="stage_type" value="prep"/>
          <zeebe:property name="role"       value="preparer"/>
        </zeebe:properties></bpmn:extensionElements></bpmn:userTask>`,
			wantCode: domain.BPMNErrMissingZeebeProperty,
		},
		{
			name: "invalid UUID in default_user_ids",
			taskXML: `<bpmn:userTask id="Task_1" name="T">
        <bpmn:extensionElements><zeebe:properties>
          <zeebe:property name="dept_id"          value="design"/>
          <zeebe:property name="stage_type"       value="prep"/>
          <zeebe:property name="role"             value="preparer"/>
          <zeebe:property name="default_user_ids" value="not-a-uuid"/>
        </zeebe:properties></bpmn:extensionElements></bpmn:userTask>`,
			wantCode: domain.BPMNErrInvalidUUID,
		},
		{
			name: "invalid sla_duration format",
			taskXML: `<bpmn:userTask id="Task_1" name="T">
        <bpmn:extensionElements><zeebe:properties>
          <zeebe:property name="dept_id"          value="design"/>
          <zeebe:property name="stage_type"       value="prep"/>
          <zeebe:property name="role"             value="preparer"/>
          <zeebe:property name="default_user_ids" value="` + aliceUUID + `"/>
          <zeebe:property name="sla_duration"     value="2 days"/>
        </zeebe:properties></bpmn:extensionElements></bpmn:userTask>`,
			wantCode: domain.BPMNErrInvalidSLADuration,
		},
		{
			name: "invalid assignee_mode",
			taskXML: `<bpmn:userTask id="Task_1" name="T">
        <bpmn:extensionElements><zeebe:properties>
          <zeebe:property name="dept_id"          value="design"/>
          <zeebe:property name="stage_type"       value="prep"/>
          <zeebe:property name="role"             value="preparer"/>
          <zeebe:property name="default_user_ids" value="` + aliceUUID + `"/>
          <zeebe:property name="assignee_mode"    value="solo"/>
        </zeebe:properties></bpmn:extensionElements></bpmn:userTask>`,
			wantCode: domain.BPMNErrInvalidZeebeProperty,
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
			name:        "missing BPMN namespace",
			bpmn:        `<?xml version="1.0"?><definitions id="D1"><process id="P1"/></definitions>`,
			wantParseEr: true,
		},
		{
			name:        "malformed XML",
			bpmn:        `<?xml version="1.0"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><unclosed>`,
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

func TestCompile_StageDefFields(t *testing.T) {
	bpmn := mustReadBPMN(t, "initial-diagram.bpmn")
	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	// Review stage has two assignees, assignee_mode=any, requires_comment=true, sla_duration=24h.
	review := plan.Departments[0].Stages[1]
	if review.AssigneeMode != "any" {
		t.Errorf("review.AssigneeMode = %q, want \"any\"", review.AssigneeMode)
	}
	if !review.RequiresComment {
		t.Error("review.RequiresComment = false, want true")
	}
	if review.SLADuration == nil || *review.SLADuration != "24h" {
		t.Errorf("review.SLADuration = %v, want \"24h\"", review.SLADuration)
	}
	if len(review.DefaultAssignees) != 2 {
		t.Errorf("review.DefaultAssignees length = %d, want 2", len(review.DefaultAssignees))
	}

	// Prep stage has requires_comment=false and sla_duration=48h.
	prep := plan.Departments[0].Stages[0]
	if prep.RequiresComment {
		t.Error("prep.RequiresComment = true, want false")
	}
	if prep.SLADuration == nil || *prep.SLADuration != "48h" {
		t.Errorf("prep.SLADuration = %v, want \"48h\"", prep.SLADuration)
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

func TestHash_PropertyOrderDoesNotAffectHash(t *testing.T) {
	// Two BPMN strings with identical semantic content but zeebe properties in different order.
	bpmnA := minimalBPMN(
		`<bpmn:startEvent id="StartEvent_1" name="Start"/>` +
			`<bpmn:userTask id="Task_1" name="T">
      <bpmn:extensionElements><zeebe:properties>
        <zeebe:property name="dept_id"          value="design"/>
        <zeebe:property name="stage_type"       value="prep"/>
        <zeebe:property name="role"             value="preparer"/>
        <zeebe:property name="default_user_ids" value="` + aliceUUID + `"/>
      </zeebe:properties></bpmn:extensionElements></bpmn:userTask>` +
			`<bpmn:endEvent id="EndEvent_1" name="End"/>
      <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
      <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="EndEvent_1"/>`,
	)
	// Same task, properties in different order.
	bpmnB := strings.ReplaceAll(bpmnA,
		`<zeebe:property name="dept_id"          value="design"/>
        <zeebe:property name="stage_type"       value="prep"/>
        <zeebe:property name="role"             value="preparer"/>
        <zeebe:property name="default_user_ids" value="`+aliceUUID+`"/>`,
		`<zeebe:property name="role"             value="preparer"/>
        <zeebe:property name="default_user_ids" value="`+aliceUUID+`"/>
        <zeebe:property name="dept_id"          value="design"/>
        <zeebe:property name="stage_type"       value="prep"/>`,
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
		t.Errorf("property order should not affect hash: %q != %q", h1, h2)
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

func TestValidate_AssigneeLimitExceeded(t *testing.T) {
	// Build a task with 11 UUIDs in default_user_ids (limit is 10).
	ids := make([]string, 11)
	for i := range ids {
		ids[i] = aliceUUID
	}
	taskXML := `<bpmn:userTask id="Task_1" name="T">
      <bpmn:extensionElements><zeebe:properties>
        <zeebe:property name="dept_id"          value="design"/>
        <zeebe:property name="stage_type"       value="prep"/>
        <zeebe:property name="role"             value="preparer"/>
        <zeebe:property name="default_user_ids" value="` + strings.Join(ids, ",") + `"/>
      </zeebe:properties></bpmn:extensionElements></bpmn:userTask>`
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
	if !hasCode(errs, domain.BPMNErrTaskAssigneeLimitExceeded) {
		t.Errorf("expected TASK_ASSIGNEE_LIMIT_EXCEEDED; got %v", errCodes(errs))
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
