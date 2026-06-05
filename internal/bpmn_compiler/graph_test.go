package bpmn_compiler

import (
	"context"
	"testing"
)

func TestBuildGraph_NodeTypesAndAdjacency(t *testing.T) {
	proc := &bpmnProcess{
		StartEvents:       []bpmnEvent{{ID: "S1"}},
		EndEvents:         []bpmnEvent{{ID: "E1"}},
		UserTasks:         []bpmnUserTask{{ID: "T1"}, {ID: "T2"}},
		ParallelGateways:  []bpmnGateway{{ID: "PGW"}},
		ExclusiveGateways: []bpmnGateway{{ID: "EGW"}},
		SequenceFlows: []bpmnSequenceFlow{
			{ID: "F1", SourceRef: "S1", TargetRef: "T1"},
			{ID: "F2", SourceRef: "T1", TargetRef: "T2"},
			{ID: "F3", SourceRef: "T2", TargetRef: "E1"},
		},
	}
	g := buildGraph(proc)

	wantTypes := map[string]FlowNodeType{
		"S1":  NodeTypeStartEvent,
		"E1":  NodeTypeEndEvent,
		"T1":  NodeTypeUserTask,
		"T2":  NodeTypeUserTask,
		"PGW": NodeTypeParallelGateway,
		"EGW": NodeTypeExclusiveGateway,
	}
	for id, want := range wantTypes {
		if got := g.nodeType[id]; got != want {
			t.Errorf("nodeType[%q] = %q, want %q", id, got, want)
		}
	}

	if got := g.outgoing["S1"]; len(got) != 1 || got[0] != "T1" {
		t.Errorf("outgoing[S1] = %v, want [T1]", got)
	}
	if got := g.incoming["E1"]; len(got) != 1 || got[0] != "T2" {
		t.Errorf("incoming[E1] = %v, want [T2]", got)
	}
	if _, ok := g.nodeIDs["PGW"]; !ok {
		t.Error("PGW missing from nodeIDs")
	}
}

func TestBuildLaneIndex(t *testing.T) {
	proc := &bpmnProcess{
		LaneSet: bpmnLaneSet{Lanes: []bpmnLane{
			{ID: "L1", Name: "Design", FlowNodeRefs: []string{"T1", "S1"}},
			{ID: "L2", Name: "QA", FlowNodeRefs: []string{"T2"}},
		}},
	}
	idx := buildLaneIndex(proc)

	if idx["T1"].Name != "Design" {
		t.Errorf("laneIndex[T1].Name = %q, want Design", idx["T1"].Name)
	}
	if idx["S1"].Name != "Design" {
		t.Errorf("laneIndex[S1].Name = %q, want Design", idx["S1"].Name)
	}
	if idx["T2"].Name != "QA" {
		t.Errorf("laneIndex[T2].Name = %q, want QA", idx["T2"].Name)
	}
	if _, ok := idx["ghost"]; ok {
		t.Error("ghost should not be in lane index")
	}
}

func TestBuildCondExprs(t *testing.T) {
	proc := &bpmnProcess{
		SequenceFlows: []bpmnSequenceFlow{
			{
				ID: "F1", SourceRef: "GW1", TargetRef: "TA",
				ExtensionElements: bpmnExtensionElements{
					ZeebeProps: bpmnZeebeProperties{
						Items: []zeebeProperty{
							{Name: "condition_expression", Value: "$.approved == true"},
						},
					},
				},
			},
			{
				ID: "F2", SourceRef: "GW1", TargetRef: "TB",
				ExtensionElements: bpmnExtensionElements{
					ZeebeProps: bpmnZeebeProperties{
						Items: []zeebeProperty{
							{Name: "condition_expression", Value: "$.approved == false"},
						},
					},
				},
			},
			{
				ID: "F3", SourceRef: "TA", TargetRef: "E1",
			},
		},
	}
	exprs := buildCondExprs(proc)

	if got := exprs[[2]string{"GW1", "TA"}]; got != "$.approved == true" {
		t.Errorf("condExprs[GW1→TA] = %q, want %q", got, "$.approved == true")
	}
	if got := exprs[[2]string{"GW1", "TB"}]; got != "$.approved == false" {
		t.Errorf("condExprs[GW1→TB] = %q, want %q", got, "$.approved == false")
	}
	if _, ok := exprs[[2]string{"TA", "E1"}]; ok {
		t.Error("flow without condition_expression should not appear in condExprs")
	}
}

func TestFindJoin_Success(t *testing.T) {
	g := &graph{
		outgoing: map[string][]string{
			"split": {"A", "B"},
			"A":     {"join"},
			"B":     {"join"},
			"join":  {"next"},
		},
		incoming: map[string][]string{
			"A":    {"split"},
			"B":    {"split"},
			"join": {"A", "B"},
			"next": {"join"},
		},
	}
	got, ok := findJoin("split", g)
	if !ok {
		t.Fatal("findJoin returned not-ok for valid graph")
	}
	if got != "join" {
		t.Errorf("findJoin = %q, want \"join\"", got)
	}
}

func TestFindJoin_NoBranches(t *testing.T) {
	g := &graph{outgoing: map[string][]string{}}
	_, ok := findJoin("split", g)
	if ok {
		t.Error("findJoin should return false when split has no outgoing branches")
	}
}

func TestFindJoin_DeadEndBranch(t *testing.T) {
	g := &graph{
		outgoing: map[string][]string{
			"split": {"A"},
		},
		incoming: map[string][]string{
			"A": {"split"},
		},
	}
	_, ok := findJoin("split", g)
	if ok {
		t.Error("findJoin should return false for a dead-end branch")
	}
}

func TestFindTask_Found(t *testing.T) {
	proc := &bpmnProcess{
		UserTasks: []bpmnUserTask{{ID: "T1", Name: "Task One"}, {ID: "T2", Name: "Task Two"}},
	}
	got := findTask(proc, "T2")
	if got == nil || got.ID != "T2" {
		t.Errorf("findTask(T2) = %v, want task with ID T2", got)
	}
}

func TestFindTask_NotFound(t *testing.T) {
	proc := &bpmnProcess{
		UserTasks: []bpmnUserTask{{ID: "T1"}},
	}
	if got := findTask(proc, "ghost"); got != nil {
		t.Errorf("findTask(ghost) = %v, want nil", got)
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
				"dept_id":          "design",
				"stage_type":       tt.stageType,
				"role":             "preparer",
				"default_user_ids": aliceUUID,
			})
			stage, err := buildStageDef(task)
			if err != nil {
				t.Fatalf("buildStageDef() error: %v", err)
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
		"dept_id":          "design",
		"stage_type":       "review",
		"role":             "reviewer",
		"default_user_ids": aliceUUID + ",661f9511-f3ac-52e5-b827-557766551111",
		"assignee_mode":    "all",
		"requires_comment": "true",
		"sla_duration":     "48h",
	})
	stage, err := buildStageDef(task)
	if err != nil {
		t.Fatalf("buildStageDef() error: %v", err)
	}
	if stage.AssigneeMode != "all" {
		t.Errorf("AssigneeMode = %q, want \"all\"", stage.AssigneeMode)
	}
	if !stage.RequiresComment {
		t.Error("RequiresComment = false, want true")
	}
	if stage.SLADuration == nil || *stage.SLADuration != "48h" {
		t.Errorf("SLADuration = %v, want \"48h\"", stage.SLADuration)
	}
	if len(stage.DefaultAssignees) != 2 {
		t.Errorf("DefaultAssignees len = %d, want 2", len(stage.DefaultAssignees))
	}
}

func TestBuildStageDef_AbsentOptionalFields(t *testing.T) {
	task := taskWithProps(map[string]string{
		"dept_id":          "design",
		"stage_type":       "prep",
		"role":             "preparer",
		"default_user_ids": aliceUUID,
	})
	stage, err := buildStageDef(task)
	if err != nil {
		t.Fatalf("buildStageDef() error: %v", err)
	}
	if stage.AssigneeMode != "" {
		t.Errorf("AssigneeMode = %q, want empty", stage.AssigneeMode)
	}
	if stage.RequiresComment {
		t.Error("RequiresComment = true, want false")
	}
	if stage.SLADuration != nil {
		t.Errorf("SLADuration = %v, want nil", stage.SLADuration)
	}
}

func TestBuildStageDef_UnknownStageType(t *testing.T) {
	task := taskWithProps(map[string]string{
		"stage_type":       "audit",
		"role":             "preparer",
		"default_user_ids": aliceUUID,
	})
	_, err := buildStageDef(task)
	if err == nil {
		t.Error("buildStageDef() with unknown stage_type should return error")
	}
}

func TestNormalizeLaneName(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Design", "design"},
		{"HSE", "hse"},
		{"QA", "qa"},
		{"  Procurement  ", "procurement"},
		{"Contract Management", "contract management"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := normalizeLaneName(tt.input); got != tt.want {
				t.Errorf("normalizeLaneName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPropsMap(t *testing.T) {
	ext := bpmnExtensionElements{ZeebeProps: bpmnZeebeProperties{Items: []zeebeProperty{
		{Name: "dept_id", Value: "design"},
		{Name: "stage_type", Value: "prep"},
		{Name: "role", Value: "preparer"},
	}}}
	m := propsMap(ext)
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
	m := propsMap(bpmnExtensionElements{})
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestCompile_PassthroughGateway(t *testing.T) {
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Passthrough Test" isExecutable="true">
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
      <bpmn:extensionElements><zeebe:properties>
        <zeebe:property name="dept_id"          value="design"/>
        <zeebe:property name="stage_type"       value="prep"/>
        <zeebe:property name="role"             value="preparer"/>
        <zeebe:property name="default_user_ids" value="` + aliceUUID + `"/>
      </zeebe:properties></bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:parallelGateway id="Gateway_pass" name="Pass"/>
    <bpmn:userTask id="Task_review" name="Review">
      <bpmn:extensionElements><zeebe:properties>
        <zeebe:property name="dept_id"          value="design"/>
        <zeebe:property name="stage_type"       value="review"/>
        <zeebe:property name="role"             value="reviewer"/>
        <zeebe:property name="default_user_ids" value="` + aliceUUID + `"/>
      </zeebe:properties></bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="EndEvent_1" name="End"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_prep"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_prep"    targetRef="Gateway_pass"/>
    <bpmn:sequenceFlow id="F3" sourceRef="Gateway_pass" targetRef="Task_review"/>
    <bpmn:sequenceFlow id="F4" sourceRef="Task_review"  targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`

	c := New()
	plan, err := c.Compile(context.TODO(), bpmn)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	if len(plan.Departments) != 1 || plan.Departments[0].ID != "design" {
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
	proc := &bpmnProcess{}
	g := buildGraph(proc)
	_, err := compile(proc, g)
	if err == nil {
		t.Error("compile() should return error when process has no start events")
	}
}

func TestHandleUserTask_NilTask(t *testing.T) {
	proc := &bpmnProcess{}
	g := &graph{
		outgoing: map[string][]string{"ghost": {"E1"}},
		nodeType: map[string]FlowNodeType{"ghost": NodeTypeUserTask},
	}
	state := newCompileState(proc, g, map[string]bpmnLane{"ghost": {Name: "Design"}}, nil)
	err := state.handleUserTask("ghost")
	if err == nil {
		t.Error("handleUserTask() with missing task should return error")
	}
}

func TestHandleUserTask_NoOutgoing(t *testing.T) {
	proc := &bpmnProcess{
		UserTasks: []bpmnUserTask{*taskWithProps(map[string]string{
			"dept_id":          "design",
			"stage_type":       "prep",
			"role":             "preparer",
			"default_user_ids": aliceUUID,
		})},
	}
	proc.UserTasks[0].ID = "T1"
	g := &graph{
		outgoing: map[string][]string{},
		nodeType: map[string]FlowNodeType{"T1": NodeTypeUserTask},
	}
	state := newCompileState(proc, g, map[string]bpmnLane{"T1": {Name: "Design"}}, nil)
	err := state.handleUserTask("T1")
	if err != nil {
		t.Errorf("handleUserTask() with no outgoing should return nil, got: %v", err)
	}
}

func TestHandleGateway_NoOutgoingAfterJoin(t *testing.T) {
	g := &graph{
		outgoing: map[string][]string{
			"split": {"A", "B"},
			"A":     {"join"},
			"B":     {"join"},
		},
		incoming: map[string][]string{
			"A":    {"split"},
			"B":    {"split"},
			"join": {"A", "B"},
		},
		nodeType: map[string]FlowNodeType{
			"split": NodeTypeParallelGateway,
			"join":  NodeTypeParallelGateway,
			"A":     NodeTypeEndEvent,
			"B":     NodeTypeEndEvent,
		},
	}
	state := newCompileState(&bpmnProcess{}, g, map[string]bpmnLane{}, map[[2]string]string{})
	err := state.handleGateway("split", false)
	if err != nil {
		t.Errorf("handleGateway() with no outgoing after join should return nil, got: %v", err)
	}
}

func TestHandleGateway_JoinPassthrough(t *testing.T) {
	g := &graph{
		outgoing: map[string][]string{
			"gw": {"E1"},
		},
		nodeType: map[string]FlowNodeType{
			"gw": NodeTypeParallelGateway,
			"E1": NodeTypeEndEvent,
		},
	}
	proc := &bpmnProcess{EndEvents: []bpmnEvent{{ID: "E1"}}}
	state := newCompileState(proc, g, map[string]bpmnLane{}, map[[2]string]string{})
	err := state.handleGateway("gw", false)
	if err != nil {
		t.Errorf("handleGateway() join passthrough error: %v", err)
	}
	if !state.visited["E1"] {
		t.Error("expected E1 (end event) to be visited via gateway passthrough")
	}
}

func TestTraverseBranches_NilTask(t *testing.T) {
	proc := &bpmnProcess{}
	g := &graph{
		outgoing: map[string][]string{
			"split": {"ghost"},
			"ghost": {"join"},
		},
		nodeType: map[string]FlowNodeType{
			"ghost": NodeTypeUserTask,
			"join":  NodeTypeParallelGateway,
		},
	}
	state := newCompileState(proc, g, map[string]bpmnLane{"ghost": {Name: "Design"}}, nil)
	_, err := state.traverseBranches("split", "join")
	if err == nil {
		t.Error("traverseBranches() with missing task should return error")
	}
}

func TestTraverseExclusiveBranches_NilTask(t *testing.T) {
	proc := &bpmnProcess{}
	g := &graph{
		outgoing: map[string][]string{
			"split": {"ghost"},
			"ghost": {"join"},
		},
		nodeType: map[string]FlowNodeType{
			"ghost": NodeTypeUserTask,
			"join":  NodeTypeExclusiveGateway,
		},
	}
	state := newCompileState(proc, g, map[string]bpmnLane{"ghost": {Name: "Design"}}, map[[2]string]string{})
	_, err := state.traverseExclusiveBranches("split", "join")
	if err == nil {
		t.Error("traverseExclusiveBranches() with missing task should return error")
	}
}

func taskWithProps(props map[string]string) *bpmnUserTask {
	items := make([]zeebeProperty, 0, len(props))
	for k, v := range props {
		items = append(items, zeebeProperty{Name: k, Value: v})
	}
	return &bpmnUserTask{
		ID: "T_test",
		ExtensionElements: bpmnExtensionElements{
			ZeebeProps: bpmnZeebeProperties{Items: items},
		},
	}
}
