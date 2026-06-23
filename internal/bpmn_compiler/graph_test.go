package bpmn_compiler

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/element"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestBuildGraph_NodeTypesAndAdjacency(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		StartEvents:       []bpmncore.BPMNEvent{{ID: "S1"}},
		EndEvents:         []bpmncore.BPMNEvent{{ID: "E1"}},
		UserTasks:         []bpmncore.BPMNUserTask{{ID: "T1"}, {ID: "T2"}},
		ParallelGateways:  []bpmncore.BPMNGateway{{ID: "PGW"}},
		ExclusiveGateways: []bpmncore.BPMNGateway{{ID: "EGW"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{
			{ID: "F1", SourceRef: "S1", TargetRef: "T1"},
			{ID: "F2", SourceRef: "T1", TargetRef: "T2"},
			{ID: "F3", SourceRef: "T2", TargetRef: "E1"},
		},
	}
	g := bpmncore.BuildGraph(proc)

	wantTypes := map[string]bpmncore.FlowNodeType{
		"S1":  bpmncore.NodeTypeStartEvent,
		"E1":  bpmncore.NodeTypeEndEvent,
		"T1":  bpmncore.NodeTypeUserTask,
		"T2":  bpmncore.NodeTypeUserTask,
		"PGW": bpmncore.NodeTypeParallelGateway,
		"EGW": bpmncore.NodeTypeExclusiveGateway,
	}
	for id, want := range wantTypes {
		if got := g.NodeType[id]; got != want {
			t.Errorf("NodeType[%q] = %q, want %q", id, got, want)
		}
	}

	if got := g.Outgoing["S1"]; len(got) != 1 || got[0] != "T1" {
		t.Errorf("Outgoing[S1] = %v, want [T1]", got)
	}
	if got := g.Incoming["E1"]; len(got) != 1 || got[0] != "T2" {
		t.Errorf("Incoming[E1] = %v, want [T2]", got)
	}
	if _, ok := g.NodeIDs["PGW"]; !ok {
		t.Error("PGW missing from NodeIDs")
	}
}

func TestBuildLaneLabels(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
			{ID: "L1", Name: "Design", FlowNodeRefs: []string{"T1", "S1"}},
			{ID: "L2", Name: "  QA  ", FlowNodeRefs: []string{"T2"}},
		}},
	}
	labels := bpmncore.BuildLaneLabels(proc)

	if labels["Design"] != "Design" {
		t.Errorf("labels[Design] = %q, want Design", labels["Design"])
	}
	if labels["  QA  "] != "  QA  " {
		t.Errorf("labels[  QA  ] = %q, want \"  QA  \"", labels["  QA  "])
	}
	if _, ok := labels["ghost"]; ok {
		t.Error("ghost should not be in lane labels")
	}
}

func TestBuildCondExprs(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		SequenceFlows: []bpmncore.BPMNSequenceFlow{
			{ID: "F1", SourceRef: "GW1", TargetRef: "TA", ConditionExpression: "$.approved == true"},
			{ID: "F2", SourceRef: "GW1", TargetRef: "TB", ConditionExpression: "$.approved == false"},
			{ID: "F3", SourceRef: "TA", TargetRef: "E1"},
		},
	}
	exprs := bpmncore.BuildCondExprs(proc)

	if got := exprs[[2]string{"GW1", "TA"}]; got != "$.approved == true" {
		t.Errorf("condExprs[GW1→TA] = %q, want %q", got, "$.approved == true")
	}
	if got := exprs[[2]string{"GW1", "TB"}]; got != "$.approved == false" {
		t.Errorf("condExprs[GW1→TB] = %q, want %q", got, "$.approved == false")
	}
	if _, ok := exprs[[2]string{"TA", "E1"}]; ok {
		t.Error("flow without conditionExpression should not appear in condExprs")
	}
}

func TestFindJoin_Success(t *testing.T) {
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"split": {"A", "B"},
			"A":     {"join"},
			"B":     {"join"},
			"join":  {"next"},
		},
		Incoming: map[string][]string{
			"A":    {"split"},
			"B":    {"split"},
			"join": {"A", "B"},
			"next": {"join"},
		},
	}
	got, ok := bpmncore.FindJoin("split", g, nil)
	if !ok {
		t.Fatal("FindJoin returned not-ok for valid graph")
	}
	if got != "join" {
		t.Errorf("FindJoin = %q, want \"join\"", got)
	}
}

func TestFindJoin_NoBranches(t *testing.T) {
	g := &bpmncore.Graph{Outgoing: map[string][]string{}}
	_, ok := bpmncore.FindJoin("split", g, nil)
	if ok {
		t.Error("FindJoin should return false when split has no outgoing branches")
	}
}

func TestFindJoin_DeadEndBranch(t *testing.T) {
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"split": {"A"},
		},
		Incoming: map[string][]string{
			"A": {"split"},
		},
	}
	_, ok := bpmncore.FindJoin("split", g, nil)
	if ok {
		t.Error("FindJoin should return false for a dead-end branch")
	}
}

func TestFindJoin_CycleTerminates(t *testing.T) {
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{"split": {"A"}, "A": {"B"}, "B": {"C"}, "C": {"A"}},
		Incoming: map[string][]string{"A": {"split", "C"}, "B": {"A"}, "C": {"B"}},
	}
	done := make(chan struct{})
	go func() {
		bpmncore.FindJoin("split", g, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("FindJoin did not terminate on a cyclic graph")
	}
}

func TestValidateMaxDepth_ExceedsLimit(t *testing.T) {
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{},
		Incoming: map[string][]string{},
		NodeIDs:  map[string]struct{}{},
		NodeType: map[string]bpmncore.FlowNodeType{},
	}
	const n = validator.MaxPathDepth + 5
	prev := "S"
	g.NodeIDs[prev] = struct{}{}
	g.NodeType[prev] = bpmncore.NodeTypeStartEvent
	for i := 0; i < n; i++ {
		cur := "N" + strconv.Itoa(i)
		g.Outgoing[prev] = []string{cur}
		g.Incoming[cur] = []string{prev}
		g.NodeIDs[cur] = struct{}{}
		g.NodeType[cur] = bpmncore.NodeTypeUserTask
		prev = cur
	}
	proc := &bpmncore.BPMNProcess{StartEvents: []bpmncore.BPMNEvent{{ID: "S"}}}
	errs := validator.ValidateMaxDepth(proc, g, map[[2]string]bool{})
	if !hasCodeIn(errs, domain.BPMNErrMaxDepthExceeded) {
		t.Errorf("expected MAX_DEPTH_EXCEEDED; got %v", errCodesOf(errs))
	}
}

func TestValidateMaxDepth_Boundary(t *testing.T) {
	build := func(tasks int) (*bpmncore.BPMNProcess, *bpmncore.Graph) {
		g := &bpmncore.Graph{
			Outgoing: map[string][]string{},
			Incoming: map[string][]string{},
			NodeIDs:  map[string]struct{}{},
			NodeType: map[string]bpmncore.FlowNodeType{},
		}
		prev := "S"
		g.NodeIDs[prev] = struct{}{}
		g.NodeType[prev] = bpmncore.NodeTypeStartEvent
		for i := 0; i < tasks; i++ {
			cur := "N" + strconv.Itoa(i)
			g.Outgoing[prev] = []string{cur}
			g.Incoming[cur] = []string{prev}
			g.NodeIDs[cur] = struct{}{}
			g.NodeType[cur] = bpmncore.NodeTypeUserTask
			prev = cur
		}
		return &bpmncore.BPMNProcess{StartEvents: []bpmncore.BPMNEvent{{ID: "S"}}}, g
	}

	proc, g := build(validator.MaxPathDepth - 1)
	if errs := validator.ValidateMaxDepth(proc, g, map[[2]string]bool{}); len(errs) != 0 {
		t.Errorf("path of exactly MaxPathDepth should pass; got %v", errCodesOf(errs))
	}

	proc, g = build(validator.MaxPathDepth)
	if errs := validator.ValidateMaxDepth(proc, g, map[[2]string]bool{}); !hasCodeIn(errs, domain.BPMNErrMaxDepthExceeded) {
		t.Errorf("path of MaxPathDepth+1 should fail; got %v", errCodesOf(errs))
	}
}

func TestForwardReaches(t *testing.T) {
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{"A": {"B"}, "B": {"C", "A"}},
	}
	back := map[[2]string]bool{{"B", "A"}: true}
	if !bpmncore.ForwardReaches("A", "C", g, back) {
		t.Error("A should reach C over forward edges")
	}
	if bpmncore.ForwardReaches("C", "A", g, back) {
		t.Error("C should not reach A over forward edges (only a back-edge leads there)")
	}

	gc := &bpmncore.Graph{Outgoing: map[string][]string{"A": {"B"}, "B": {"A"}}}
	done := make(chan struct{})
	go func() {
		bpmncore.ForwardReaches("A", "Z", gc, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ForwardReaches did not terminate on a cyclic graph")
	}
}

func TestRevertBranches_MultipleBackEdges(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
			{ID: "L1", Name: "design", FlowNodeRefs: []string{"TaskRev1"}},
			{ID: "L2", Name: "contracts", FlowNodeRefs: []string{"TaskRev2"}},
		}},
		UserTasks: []bpmncore.BPMNUserTask{
			{ID: "TaskRev1", ExtensionElements: bpmncore.BPMNExtensionElements{
				TaskDefinition: &bpmncore.ZeebeTaskDefinition{Type: "review"},
			}},
			{ID: "TaskRev2", ExtensionElements: bpmncore.BPMNExtensionElements{
				TaskDefinition: &bpmncore.ZeebeTaskDefinition{Type: "prep"},
			}},
		},
	}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{},
		NodeType: map[string]bpmncore.FlowNodeType{
			"TaskRev1": bpmncore.NodeTypeUserTask,
			"TaskRev2": bpmncore.NodeTypeUserTask,
		},
	}
	condExprs := map[[2]string]string{
		{"GW", "TaskRev1"}: "x",
		{"GW", "TaskRev2"}: "y",
	}
	state := bpmncore.NewCompileState(proc, g, nil, condExprs, map[[2]string]bool{}, defaultStageTypes(), element.DefaultElementHandlers(), nil)

	branches := state.RevertBranches("GW", []string{"TaskRev1", "TaskRev2"})
	if len(branches) != 2 {
		t.Fatalf("expected 2 revert branches; got %d", len(branches))
	}
	if branches[0].RevertToDept != "design" || branches[0].RevertToStage != "review" ||
		branches[0].ConditionExpression != "x" {
		t.Errorf("branch[0] = %+v, want design/review/x", branches[0])
	}
	if branches[1].RevertToDept != "contracts" || branches[1].RevertToStage != "prep" ||
		branches[1].ConditionExpression != "y" {
		t.Errorf("branch[1] = %+v, want contracts/prep/y", branches[1])
	}
}

func TestFindTask_Found(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		UserTasks: []bpmncore.BPMNUserTask{{ID: "T1", Name: "Task One"}, {ID: "T2", Name: "Task Two"}},
	}
	got := bpmncore.FindTask(proc, "T2")
	if got == nil || got.ID != "T2" {
		t.Errorf("FindTask(T2) = %v, want task with ID T2", got)
	}
}

func TestFindTask_NotFound(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		UserTasks: []bpmncore.BPMNUserTask{{ID: "T1"}},
	}
	if got := bpmncore.FindTask(proc, "ghost"); got != nil {
		t.Errorf("FindTask(ghost) = %v, want nil", got)
	}
}

func TestBuildStageDef_AllStageTypes(t *testing.T) {
	tests := []struct {
		stageType    string
		wantActivity string
	}{
		{"prep", "PrepActivity"},
		{"review", "ReviewActivity"},
		{"approve", "ApproveActivity"},
	}
	for _, tt := range tests {
		t.Run(tt.stageType, func(t *testing.T) {
			task := taskWithProps(map[string]string{
				"stage_type":       tt.stageType,
				"role":             "preparer",
				"default_user_ids": aliceUUID,
			})
			stage, err := bpmncore.BuildStageDef(task, defaultStageTypes(), &bpmncore.BPMNProcess{})
			if err != nil {
				t.Fatalf("BuildStageDef() error: %v", err)
			}
			if stage.Type != tt.stageType {
				t.Errorf("Type = %q, want %q", stage.Type, tt.stageType)
			}
			if stage.Activity != tt.wantActivity {
				t.Errorf("Activity = %q, want %q", stage.Activity, tt.wantActivity)
			}
		})
	}
}

func TestBuildStageDef_OptionalFields(t *testing.T) {
	task := taskWithProps(map[string]string{
		"stage_type":       "review",
		"role":             "reviewer",
		"default_user_ids": aliceUUID,
		"requires_comment": "true",
	})
	stage, err := bpmncore.BuildStageDef(task, defaultStageTypes(), &bpmncore.BPMNProcess{})
	if err != nil {
		t.Fatalf("BuildStageDef() error: %v", err)
	}
	if !stage.RequiresComment {
		t.Error("RequiresComment = false, want true")
	}
	if len(stage.DefaultAssignees) != 1 {
		t.Errorf("DefaultAssignees len = %d, want 1", len(stage.DefaultAssignees))
	}
}

func TestBuildStageDef_AbsentOptionalFields(t *testing.T) {
	task := taskWithProps(map[string]string{
		"stage_type":       "prep",
		"role":             "preparer",
		"default_user_ids": aliceUUID,
	})
	stage, err := bpmncore.BuildStageDef(task, defaultStageTypes(), &bpmncore.BPMNProcess{})
	if err != nil {
		t.Fatalf("BuildStageDef() error: %v", err)
	}
	if stage.RequiresComment {
		t.Error("RequiresComment = true, want false")
	}
	if stage.BoundaryTimer != nil {
		t.Errorf("BoundaryTimer = %v, want nil", stage.BoundaryTimer)
	}
}

func TestBuildStageDef_UnknownStageType(t *testing.T) {
	task := taskWithProps(map[string]string{
		"stage_type": "audit",
		"role":       "preparer",
	})
	_, err := bpmncore.BuildStageDef(task, defaultStageTypes(), &bpmncore.BPMNProcess{})
	if err == nil {
		t.Error("BuildStageDef() with unknown stage_type should return error")
	}
}

func TestPropsMap(t *testing.T) {
	ext := bpmncore.BPMNExtensionElements{ZeebeProps: bpmncore.BPMNZeebeProperties{Items: []bpmncore.ZeebeProperty{
		{Name: "dept_id", Value: "design"},
		{Name: "stage_type", Value: "prep"},
		{Name: "role", Value: "preparer"},
	}}}
	m := bpmncore.PropsMap(ext)
	if m["dept_id"] != "design" {
		t.Errorf("dept_id = %q, want design", m["dept_id"])
	}
	if m["stage_type"] != "prep" {
		t.Errorf("stage_type = %q, want prep", m["stage_type"])
	}
	if _, ok := m["missing"]; ok {
		t.Error("missing key should not be present")
	}
}

func TestPropsMap_Empty(t *testing.T) {
	m := bpmncore.PropsMap(bpmncore.BPMNExtensionElements{})
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestCompile_PassthroughGateway(t *testing.T) {
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Passthrough Test">
    <bpmn:laneSet id="LS1">
      <bpmn:lane id="Lane_design" name="Design">
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_prep</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Gateway_pass</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_review</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1" name="Start"/>
    <bpmn:userTask id="Task_prep" name="Prep">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:parallelGateway id="Gateway_pass" name="Pass"/>
    <bpmn:userTask id="Task_review" name="Review">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="EndEvent_1" name="End"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_prep"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_prep"    targetRef="Gateway_pass"/>
    <bpmn:sequenceFlow id="F3" sourceRef="Gateway_pass" targetRef="Task_review"/>
    <bpmn:sequenceFlow id="F4" sourceRef="Task_review"  targetRef="EndEvent_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="P1">
      <bpmndi:BPMNShape id="Sh_StartEvent_1"  bpmnElement="StartEvent_1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_prep"     bpmnElement="Task_prep"><dc:Bounds x="250" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Gateway_pass"  bpmnElement="Gateway_pass"><dc:Bounds x="405" y="75" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_review"   bpmnElement="Task_review"><dc:Bounds x="510" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_EndEvent_1"    bpmnElement="EndEvent_1"><dc:Bounds x="662" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
      <bpmndi:BPMNEdge id="Edge_F3" bpmnElement="F3"/>
      <bpmndi:BPMNEdge id="Edge_F4" bpmnElement="F4"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

	c := New()
	plan, err := c.Compile(context.TODO(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	if len(plan.Departments) != 1 || plan.Departments[0].ID != "Design" {
		t.Errorf("unexpected departments: %+v", plan.Departments)
	}
	if len(plan.Departments[0].Stages) != 2 {
		t.Errorf("design stages: got %d, want 2", len(plan.Departments[0].Stages))
	}
	if len(plan.Execution.Steps) != 1 || len(plan.Execution.Steps[0].Sequential) != 1 {
		t.Errorf("execution steps: %+v", plan.Execution.Steps)
	}
}

func TestCompile_NoStartEvents(t *testing.T) {
	proc := &bpmncore.BPMNProcess{}
	g := bpmncore.BuildGraph(proc)
	_, err := bpmncore.Compile(proc, g, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	if err == nil {
		t.Error("Compile() should return error when process has no start events")
	}
}

func TestHandleUserTask_NilTask(t *testing.T) {
	proc := &bpmncore.BPMNProcess{}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{"ghost": {"E1"}},
		NodeType: map[string]bpmncore.FlowNodeType{"ghost": bpmncore.NodeTypeUserTask},
	}
	state := bpmncore.NewCompileState(proc, g, map[string]string{}, nil, nil, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	err := (element.UserTaskHandler{}).Compile("ghost", state)
	if err == nil {
		t.Error("UserTaskHandler.Compile() with missing task should return error")
	}
}

func TestHandleUserTask_NoOutgoing(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
			{ID: "L1", Name: "Design", FlowNodeRefs: []string{"T1"}},
		}},
		UserTasks: []bpmncore.BPMNUserTask{*taskWithProps(map[string]string{
			"stage_type":       "prep",
			"role":             "preparer",
			"default_user_ids": aliceUUID,
		})},
	}
	proc.UserTasks[0].ID = "T1"
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{},
		NodeType: map[string]bpmncore.FlowNodeType{"T1": bpmncore.NodeTypeUserTask},
	}
	state := bpmncore.NewCompileState(proc, g, map[string]string{"Design": "Design"}, nil, nil, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	err := (element.UserTaskHandler{}).Compile("T1", state)
	if err != nil {
		t.Errorf("UserTaskHandler.Compile() with no outgoing should return nil, got: %v", err)
	}
}

func TestHandleGateway_NoOutgoingAfterJoin(t *testing.T) {
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"split": {"A", "B"},
			"A":     {"join"},
			"B":     {"join"},
		},
		Incoming: map[string][]string{
			"A":    {"split"},
			"B":    {"split"},
			"join": {"A", "B"},
		},
		NodeType: map[string]bpmncore.FlowNodeType{
			"split": bpmncore.NodeTypeParallelGateway,
			"join":  bpmncore.NodeTypeParallelGateway,
			"A":     bpmncore.NodeTypeEndEvent,
			"B":     bpmncore.NodeTypeEndEvent,
		},
	}
	state := bpmncore.NewCompileState(&bpmncore.BPMNProcess{}, g, map[string]string{}, map[[2]string]string{}, nil, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	err := state.HandleGateway("split", bpmncore.ParallelGateway)
	if err != nil {
		t.Errorf("HandleGateway() with no outgoing after join should return nil, got: %v", err)
	}
}

func TestHandleGateway_JoinPassthrough(t *testing.T) {
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"gw": {"E1"},
		},
		NodeType: map[string]bpmncore.FlowNodeType{
			"gw": bpmncore.NodeTypeParallelGateway,
			"E1": bpmncore.NodeTypeEndEvent,
		},
	}
	proc := &bpmncore.BPMNProcess{EndEvents: []bpmncore.BPMNEvent{{ID: "E1"}}}
	state := bpmncore.NewCompileState(proc, g, map[string]string{}, map[[2]string]string{}, nil, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	err := state.HandleGateway("gw", bpmncore.ParallelGateway)
	if err != nil {
		t.Errorf("HandleGateway() join passthrough error: %v", err)
	}
	if !state.IsVisited("E1") {
		t.Error("expected E1 (end event) to be visited via gateway passthrough")
	}
}

func TestTraverseBranches_NilTask(t *testing.T) {
	proc := &bpmncore.BPMNProcess{}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"split": {"ghost"},
			"ghost": {"join"},
		},
		NodeType: map[string]bpmncore.FlowNodeType{
			"ghost": bpmncore.NodeTypeUserTask,
			"join":  bpmncore.NodeTypeParallelGateway,
		},
	}
	state := bpmncore.NewCompileState(proc, g, map[string]string{}, nil, nil, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	_, err := state.TraverseBranches("split", "join")
	if err == nil {
		t.Error("TraverseBranches() with missing task should return error")
	}
}

func TestTraverseExclusiveBranches_NilTask(t *testing.T) {
	proc := &bpmncore.BPMNProcess{}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"split": {"ghost"},
			"ghost": {"join"},
		},
		NodeType: map[string]bpmncore.FlowNodeType{
			"ghost": bpmncore.NodeTypeUserTask,
			"join":  bpmncore.NodeTypeExclusiveGateway,
		},
	}
	state := bpmncore.NewCompileState(proc, g, map[string]string{}, map[[2]string]string{}, nil, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	_, err := state.TraverseExclusiveBranches("split", "join")
	if err == nil {
		t.Error("TraverseExclusiveBranches() with missing task should return error")
	}
}

func TestFlushSeqBuf_Empty(t *testing.T) {
	state := bpmncore.NewCompileState(&bpmncore.BPMNProcess{}, &bpmncore.Graph{}, nil, nil, nil, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	state.FlushSeqBuf()
	if len(state.CollectedSteps()) != 0 {
		t.Errorf("expected 0 steps after flushing empty buffer, got %d", len(state.CollectedSteps()))
	}
}

func TestEnsureDept_Idempotent(t *testing.T) {
	state := bpmncore.NewCompileState(&bpmncore.BPMNProcess{}, &bpmncore.Graph{}, nil, nil, nil, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	state.EnsureDept("design", "Design")
	state.EnsureDept("design", "Design")
	if len(state.CollectedDepts()) != 1 {
		t.Errorf("EnsureDept called twice should produce 1 dept, got %d", len(state.CollectedDepts()))
	}
}

func TestAddToSeqBuf_Dedup(t *testing.T) {
	state := bpmncore.NewCompileState(&bpmncore.BPMNProcess{}, &bpmncore.Graph{}, nil, nil, nil, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	state.AddToSeqBuf("design")
	state.AddToSeqBuf("design")
	if state.SeqBufCount() != 1 {
		t.Errorf("AddToSeqBuf with duplicate should produce len=1, got %d", state.SeqBufCount())
	}
}

func TestTraverseNode_AlreadyVisited(t *testing.T) {
	g := &bpmncore.Graph{NodeType: map[string]bpmncore.FlowNodeType{}}
	state := bpmncore.NewCompileState(&bpmncore.BPMNProcess{}, g, nil, nil, nil, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	// Mark T1 as visited by traversing an end event (no-op) and then check manually.
	// We use IsVisited after manually causing a visit via HandleGateway passthrough.
	// Simpler: just call TraverseNode on a node with no handler — it visits and returns nil.
	g.NodeType["T1"] = bpmncore.NodeTypeStartEvent // no handler → no-op
	_ = state.TraverseNode("T1")
	err := state.TraverseNode("T1") // second call should be no-op
	if err != nil {
		t.Errorf("TraverseNode() on already-visited node should return nil, got %v", err)
	}
}

func TestTraverseBranches_DeadEndBeforeJoin(t *testing.T) {
	proc := &bpmncore.BPMNProcess{}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"split": {"A"},
			"A":     {},
		},
		NodeType: map[string]bpmncore.FlowNodeType{
			"A": bpmncore.NodeTypeEndEvent,
		},
	}
	state := bpmncore.NewCompileState(proc, g, map[string]string{}, nil, nil, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	depts, err := state.TraverseBranches("split", "join")
	if err != nil {
		t.Errorf("TraverseBranches() with dead-end branch should not error, got %v", err)
	}
	if len(depts) != 0 {
		t.Errorf("expected empty depts for dead-end branch, got %v", depts)
	}
}

func TestTraverseExclusiveBranches_DeadEndBeforeJoin(t *testing.T) {
	proc := &bpmncore.BPMNProcess{}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"split": {"A"},
			"A":     {},
		},
		NodeType: map[string]bpmncore.FlowNodeType{
			"A": bpmncore.NodeTypeEndEvent,
		},
	}
	state := bpmncore.NewCompileState(proc, g, map[string]string{}, map[[2]string]string{}, nil, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	branches, err := state.TraverseExclusiveBranches("split", "join")
	if err != nil {
		t.Errorf("TraverseExclusiveBranches() with dead-end branch should not error, got %v", err)
	}
	if len(branches) != 1 {
		t.Errorf("expected 1 branch entry, got %d", len(branches))
	}
}

func TestHandleGateway_SplitNoJoin_Error(t *testing.T) {
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"split": {"A", "B"},
			"A":     {},
			"B":     {},
		},
		Incoming: map[string][]string{
			"A": {"split"},
			"B": {"split"},
		},
		NodeType: map[string]bpmncore.FlowNodeType{
			"split": bpmncore.NodeTypeParallelGateway,
		},
	}
	state := bpmncore.NewCompileState(&bpmncore.BPMNProcess{}, g, map[string]string{}, map[[2]string]string{}, nil, defaultStageTypes(), element.DefaultElementHandlers(), nil)
	err := state.HandleGateway("split", bpmncore.ParallelGateway)
	if err == nil {
		t.Error("HandleGateway() should return error when split has no matching join")
	}
}

// taskWithProps builds a BPMNUserTask from a map of named fields.
// Keys: "stage_type", "role", "default_user_ids" (single UUID), "requires_comment".
func taskWithProps(props map[string]string) *bpmncore.BPMNUserTask {
	var taskDef *bpmncore.ZeebeTaskDefinition
	if st := props["stage_type"]; st != "" {
		taskDef = &bpmncore.ZeebeTaskDefinition{Type: st}
	}
	var assignDef *bpmncore.ZeebeAssignmentDefinition
	role, userID := props["role"], props["default_user_ids"]
	if role != "" || userID != "" {
		assignDef = &bpmncore.ZeebeAssignmentDefinition{CandidateGroups: role, CandidateUsers: userID}
	}
	var items []bpmncore.ZeebeProperty
	if rc := props["requires_comment"]; rc != "" {
		items = append(items, bpmncore.ZeebeProperty{Name: "requires_comment", Value: rc})
	}
	return &bpmncore.BPMNUserTask{
		ID: "T_test",
		ExtensionElements: bpmncore.BPMNExtensionElements{
			TaskDefinition:       taskDef,
			AssignmentDefinition: assignDef,
			ZeebeProps:           bpmncore.BPMNZeebeProperties{Items: items},
		},
	}
}
