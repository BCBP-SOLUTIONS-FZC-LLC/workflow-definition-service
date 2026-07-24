package bpmn_compiler_test

import (
	"context"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const subprocessTimerBoundaryBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
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
        <bpmn:lane id="Inner_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="07347272-d6d5-5ddc-a9b6-2ec6b07318e4"/></zeebe:properties></bpmn:extensionElements>
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

// ---------- Timer boundary event tests ----------

func TestCompile_TimerBoundary_Interrupting(t *testing.T) {
	c := bpmn_compiler.New()
	plan, err := c.Compile(context.Background(), mustReadBPMN(t, "timer-boundary.bpmn"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
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
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
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

	var hasQA bool
	for _, d := range plan.Departments {
		if d.ID == "qa" {
			hasQA = true
		}
	}
	if !hasQA {
		t.Error("expected 'qa' department in parent catalog from post-subprocess task")
	}

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
// namespace but not the BPMN namespace returns MISSING_NAMESPACE.
func TestValidate_MissingBPMNNamespace(t *testing.T) {
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
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_TimerVisited" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="TimerVisited" isExecutable="true">
    <bpmn:laneSet id="LS">
      <bpmn:lane id="L_ops" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="14649f77-3140-51bf-bdc3-9066944b090f"/></zeebe:properties></bpmn:extensionElements>
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

// TestCompile_Subprocess_CatchAllErrorBoundary verifies that a subprocess with a
// catch-all error boundary event (no errorRef) compiles to a SubWorkflow step with
// an ErrorPath whose ErrorCode is empty string.
func TestCompile_Subprocess_CatchAllErrorBoundary(t *testing.T) {
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_CatchAll" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P_CatchAll" name="CatchAll Error" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>SP_1</bpmn:flowNodeRef>
      </bpmn:lane>
      <bpmn:lane id="Lane_qa" name="qa"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="a98d408d-cc9a-50a7-b09e-1d881389fdd4"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Task_qa</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>BE_catchall</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
    <bpmn:subProcess id="SP_1" name="Inner Sub">
      <bpmn:laneSet id="InnerLS">
        <bpmn:lane id="InnerLane" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="976d130a-c3b2-5932-9a84-b6a657cda616"/></zeebe:properties></bpmn:extensionElements>
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
