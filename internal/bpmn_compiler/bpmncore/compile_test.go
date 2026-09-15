package bpmncore

import (
	"errors"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type stubStageType struct {
	id           string
	activityName string
}

func (s *stubStageType) ID() string           { return s.id }
func (s *stubStageType) ActivityName() string { return s.activityName }
func (s *stubStageType) ValidateProps(taskID string, props map[string]string) []domain.BPMNValidationError {
	return nil
}

func TestCompile_ExplicitStart(t *testing.T) {
	proc := &BPMNProcess{StartEvents: []BPMNEvent{{ID: "start"}}}
	g := BuildGraph(proc)

	plan, err := Compile(proc, g, nil, nil, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}
}

func TestCompile_ImplicitStart(t *testing.T) {
	proc := &BPMNProcess{
		ReceiveTasks:  []BPMNReceiveTask{{ID: "rt1"}},
		EndEvents:     []BPMNEvent{{ID: "end1"}},
		SequenceFlows: []BPMNSequenceFlow{{SourceRef: "rt1", TargetRef: "end1"}},
	}
	g := BuildGraph(proc)
	elements := map[FlowNodeType]ElementHandler{NodeTypeReceiveTask: &stubElementHandler{nodeType: NodeTypeReceiveTask}}

	plan, err := Compile(proc, g, nil, elements, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Execution.Steps) != 1 {
		t.Errorf("expected 1 step from implicit-start traversal; got %d", len(plan.Execution.Steps))
	}
}

func TestCompileWithImplicitStart_ExplicitStart(t *testing.T) {
	proc := &BPMNProcess{
		StartEvents:   []BPMNEvent{{ID: "start"}},
		UserTasks:     []BPMNUserTask{{ID: "t1"}},
		SequenceFlows: []BPMNSequenceFlow{{SourceRef: "start", TargetRef: "t1"}},
	}
	g := BuildGraph(proc)
	elements := map[FlowNodeType]ElementHandler{NodeTypeUserTask: &stubElementHandler{nodeType: NodeTypeUserTask}}

	plan, err := CompileWithImplicitStart(proc, g, "", map[string]StageTypeHandler{}, elements, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Execution.Steps) != 1 {
		t.Fatalf("expected 1 step; got %d", len(plan.Execution.Steps))
	}
}

func TestCompileWithImplicitStart_Implicit(t *testing.T) {
	proc := &BPMNProcess{ReceiveTasks: []BPMNReceiveTask{{ID: "rt1"}}}
	g := BuildGraph(proc)
	elements := map[FlowNodeType]ElementHandler{NodeTypeReceiveTask: &stubElementHandler{nodeType: NodeTypeReceiveTask}}

	plan, err := CompileWithImplicitStart(proc, g, "rt1", map[string]StageTypeHandler{}, elements, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Execution.Steps) != 1 {
		t.Fatalf("expected 1 step; got %d", len(plan.Execution.Steps))
	}
}

func TestCompileWithImplicitStart_NoStartEvent(t *testing.T) {
	proc := &BPMNProcess{}
	g := BuildGraph(proc)
	if _, err := CompileWithImplicitStart(proc, g, "", nil, nil, &BPMNDefinitions{}); err == nil {
		t.Fatal("expected error when no start event and no implicit start resolved")
	}
}

func TestCompileWithImplicitStart_ExplicitStartNoForward(t *testing.T) {
	proc := &BPMNProcess{StartEvents: []BPMNEvent{{ID: "start"}}}
	g := BuildGraph(proc)

	plan, err := CompileWithImplicitStart(proc, g, "", nil, nil, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Execution.Steps) != 0 {
		t.Errorf("expected 0 steps; got %d", len(plan.Execution.Steps))
	}
}

func TestCompileWithImplicitStart_TraverseError(t *testing.T) {
	proc := &BPMNProcess{
		StartEvents:   []BPMNEvent{{ID: "start"}},
		UserTasks:     []BPMNUserTask{{ID: "t1"}},
		SequenceFlows: []BPMNSequenceFlow{{SourceRef: "start", TargetRef: "t1"}},
	}
	g := BuildGraph(proc)
	wantErr := errors.New("boom")
	elements := map[FlowNodeType]ElementHandler{NodeTypeUserTask: &stubElementHandler{nodeType: NodeTypeUserTask, err: wantErr}}

	if _, err := CompileWithImplicitStart(proc, g, "", nil, elements, &BPMNDefinitions{}); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestCompileWithImplicitStart_ImplicitTraverseError(t *testing.T) {
	proc := &BPMNProcess{ReceiveTasks: []BPMNReceiveTask{{ID: "rt1"}}}
	g := BuildGraph(proc)
	wantErr := errors.New("boom")
	elements := map[FlowNodeType]ElementHandler{NodeTypeReceiveTask: &stubElementHandler{nodeType: NodeTypeReceiveTask, err: wantErr}}

	if _, err := CompileWithImplicitStart(proc, g, "rt1", nil, elements, &BPMNDefinitions{}); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestCompileWithImplicitStart_BoundaryPathError(t *testing.T) {
	proc := &BPMNProcess{
		StartEvents:    []BPMNEvent{{ID: "start"}},
		UserTasks:      []BPMNUserTask{{ID: "t1"}},
		SequenceFlows:  []BPMNSequenceFlow{{SourceRef: "start", TargetRef: "t1"}},
		BoundaryEvents: []BPMNBoundaryEvent{{ID: "be1", AttachedToRef: "t1", Timer: &BPMNTimerDef{}}},
	}
	g := BuildGraph(proc)
	wantErr := errors.New("boom")
	elements := map[FlowNodeType]ElementHandler{
		NodeTypeUserTask:           &stubElementHandler{nodeType: NodeTypeUserTask},
		NodeTypeTimerBoundaryEvent: &stubElementHandler{nodeType: NodeTypeTimerBoundaryEvent, err: wantErr},
	}

	if _, err := CompileWithImplicitStart(proc, g, "", nil, elements, &BPMNDefinitions{}); err == nil {
		t.Fatal("expected boundary path error to propagate")
	}
}

func TestCompileWithImplicitStart_ExternalNodesMerge(t *testing.T) {
	proc := &BPMNProcess{
		StartEvents:   []BPMNEvent{{ID: "start"}},
		SequenceFlows: []BPMNSequenceFlow{{SourceRef: "start", TargetRef: "ext1"}},
	}
	g := BuildGraph(proc)
	g.NodeType["ext1"] = NodeTypeExternalParticipant
	g.NodeIDs["ext1"] = struct{}{}

	plan, err := CompileWithImplicitStart(proc, g, "", nil, nil, &BPMNDefinitions{}, map[string]string{"ext1": "Partner"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Execution.Steps) != 1 || plan.Execution.Steps[0].CallPool == nil || plan.Execution.Steps[0].CallPool.Pool != "Partner" {
		t.Fatalf("expected CallPool step for merged external node; got %v", plan.Execution.Steps)
	}
}

func TestCompileWithImplicitStart_VisualElements(t *testing.T) {
	proc := &BPMNProcess{
		StartEvents:   []BPMNEvent{{ID: "start"}},
		DataStoreRefs: []BPMNDataStoreRef{{ID: "ds1", Name: "Orders"}},
	}
	g := BuildGraph(proc)

	plan, err := CompileWithImplicitStart(proc, g, "", nil, nil, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.VisualElements) != 1 || plan.VisualElements[0].ID != "ds1" {
		t.Fatalf("expected 1 visual element; got %v", plan.VisualElements)
	}
}

func TestBuildStageDef_UnknownType(t *testing.T) {
	task := &BPMNUserTask{ID: "t1", ExtensionElements: BPMNExtensionElements{TaskDefinition: &ZeebeTaskDefinition{Type: "mystery"}}}
	stage, err := BuildStageDef(task, map[string]StageTypeHandler{}, &BPMNProcess{}, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage.Activity != "mystery" {
		t.Errorf("Activity = %q; want mystery", stage.Activity)
	}
	if stage.EngineNote == "" {
		t.Error("expected engine note for unknown stage type")
	}
}

func TestBuildStageDef_KnownTypeWithAssignmentAndProps(t *testing.T) {
	task := &BPMNUserTask{
		ID: "t1",
		ExtensionElements: BPMNExtensionElements{
			TaskDefinition:       &ZeebeTaskDefinition{Type: "prep"},
			AssignmentDefinition: &ZeebeAssignmentDefinition{CandidateGroups: "ops", CandidateUsers: " alice "},
			ZeebeProps:           BPMNZeebeProperties{Items: []ZeebeProperty{{Name: "k", Value: "v"}}},
			ZeebeUserTask:        &struct{}{},
		},
	}
	stageTypes := map[string]StageTypeHandler{"prep": &stubStageType{id: "prep", activityName: "PrepActivity"}}

	stage, err := BuildStageDef(task, stageTypes, &BPMNProcess{}, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage.Activity != "PrepActivity" {
		t.Errorf("Activity = %q; want PrepActivity", stage.Activity)
	}
	if stage.Role != "ops" {
		t.Errorf("Role = %q; want ops", stage.Role)
	}
	if len(stage.DefaultAssignees) != 1 || stage.DefaultAssignees[0] != "alice" {
		t.Errorf("DefaultAssignees = %v", stage.DefaultAssignees)
	}
	if stage.Extras["k"] != "v" {
		t.Errorf("Extras[k] = %q; want v", stage.Extras["k"])
	}
	if !stage.IsZeebeUserTask {
		t.Error("expected IsZeebeUserTask = true")
	}
	if stage.EngineNote != "" {
		t.Error("expected no engine note for a known stage type")
	}
}

func TestBuildStageDef_AssignmentNoCandidateUsers(t *testing.T) {
	task := &BPMNUserTask{
		ID:                "t1",
		ExtensionElements: BPMNExtensionElements{AssignmentDefinition: &ZeebeAssignmentDefinition{CandidateGroups: "ops", CandidateUsers: "   "}},
	}
	stage, err := BuildStageDef(task, map[string]StageTypeHandler{}, &BPMNProcess{}, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage.DefaultAssignees != nil {
		t.Errorf("expected nil assignees for blank candidate users; got %v", stage.DefaultAssignees)
	}
}

func TestBuildStageDef_BoundaryTimer(t *testing.T) {
	task := &BPMNUserTask{ID: "t1"}
	proc := &BPMNProcess{BoundaryEvents: []BPMNBoundaryEvent{
		{ID: "be1", AttachedToRef: "t1", Timer: &BPMNTimerDef{Duration: "PT1H"}, CancelActivity: ""},
	}}
	stage, err := BuildStageDef(task, map[string]StageTypeHandler{}, proc, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage.BoundaryTimer == nil || stage.BoundaryTimer.Duration != "PT1H" || !stage.BoundaryTimer.Interrupting {
		t.Fatalf("unexpected BoundaryTimer: %+v", stage.BoundaryTimer)
	}

	proc2 := &BPMNProcess{BoundaryEvents: []BPMNBoundaryEvent{
		{ID: "be2", AttachedToRef: "t1", Timer: &BPMNTimerDef{}, CancelActivity: "false"},
	}}
	stage2, err := BuildStageDef(task, map[string]StageTypeHandler{}, proc2, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage2.BoundaryTimer == nil || stage2.BoundaryTimer.Interrupting {
		t.Error("expected non-interrupting boundary timer when cancelActivity=false")
	}
}

func TestBuildStageDef_NoBoundaryTimer(t *testing.T) {
	task := &BPMNUserTask{ID: "t1"}
	stage, err := BuildStageDef(task, map[string]StageTypeHandler{}, &BPMNProcess{}, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage.BoundaryTimer != nil {
		t.Error("expected nil BoundaryTimer when none configured")
	}
}

func TestBuildStageDef_BoundaryMessage_DirectResolve(t *testing.T) {
	task := &BPMNUserTask{ID: "t1"}
	proc := &BPMNProcess{BoundaryEvents: []BPMNBoundaryEvent{
		{ID: "be1", AttachedToRef: "t1", Message: &BPMNMessageEventDef{MessageRef: "Msg_1"}},
	}}
	defs := &BPMNDefinitions{Messages: []BPMNMessage{{ID: "Msg_1", Name: "OrderPlaced"}}}

	stage, err := BuildStageDef(task, map[string]StageTypeHandler{}, proc, defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage.BoundaryMessage == nil || stage.BoundaryMessage.MessageName != "OrderPlaced" {
		t.Fatalf("unexpected BoundaryMessage: %+v", stage.BoundaryMessage)
	}
}

func TestBuildStageDef_BoundaryMessage_FallbackToMessageFlow(t *testing.T) {
	task := &BPMNUserTask{ID: "t1"}
	proc := &BPMNProcess{BoundaryEvents: []BPMNBoundaryEvent{
		{ID: "be1", AttachedToRef: "t1", Message: &BPMNMessageEventDef{}}, // empty messageRef
	}}
	defs := &BPMNDefinitions{
		Collaboration: &BPMNCollaboration{
			MessageFlows: []BPMNMessageFlow{{ID: "mf1", Name: "FlowName", SourceRef: "x", TargetRef: "be1"}},
		},
	}

	stage, err := BuildStageDef(task, map[string]StageTypeHandler{}, proc, defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage.BoundaryMessage == nil || stage.BoundaryMessage.MessageName != "FlowName" {
		t.Fatalf("expected fallback resolution via message flow; got %+v", stage.BoundaryMessage)
	}
}

func TestBuildStageDef_NoBoundaryMessage(t *testing.T) {
	task := &BPMNUserTask{ID: "t1"}
	stage, err := BuildStageDef(task, map[string]StageTypeHandler{}, &BPMNProcess{}, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage.BoundaryMessage != nil {
		t.Error("expected nil BoundaryMessage when none configured")
	}
}

func TestBuildStageDef_TaskSchedule(t *testing.T) {
	task := &BPMNUserTask{
		ID:                "t1",
		ExtensionElements: BPMNExtensionElements{TaskSchedule: &ZeebeTaskSchedule{DueDate: "2024-01-01", FollowUpDate: "2024-01-02"}},
	}
	stage, err := BuildStageDef(task, map[string]StageTypeHandler{}, &BPMNProcess{}, &BPMNDefinitions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage.DueDate != "2024-01-01" || stage.FollowUpDate != "2024-01-02" {
		t.Errorf("unexpected schedule: %+v", stage)
	}
}

func TestBuildMessageStageDef(t *testing.T) {
	ext := BPMNExtensionElements{
		AssignmentDefinition: &ZeebeAssignmentDefinition{CandidateGroups: "ops", CandidateUsers: "bob"},
		ZeebeProps:           BPMNZeebeProperties{Items: []ZeebeProperty{{Name: "k", Value: "v"}}},
	}
	stage := BuildMessageStageDef("st1", "Notify", "send_task", "OrderPlaced", ext)
	if stage.Type != "send_task" || stage.Activity != "Notify" || stage.NodeID != "st1" {
		t.Errorf("unexpected stage: %+v", stage)
	}
	if stage.Role != "ops" || len(stage.DefaultAssignees) != 1 || stage.DefaultAssignees[0] != "bob" {
		t.Errorf("unexpected assignment: %+v", stage)
	}
	if stage.Extras["k"] != "v" || stage.Extras["message"] != "OrderPlaced" {
		t.Errorf("unexpected extras: %v", stage.Extras)
	}
}

func TestBuildMessageStageDef_NoMessageNoAssignmentNoExtras(t *testing.T) {
	stage := BuildMessageStageDef("rt1", "Await", "receive_task", "", BPMNExtensionElements{})
	if stage.Role != "" || stage.DefaultAssignees != nil {
		t.Errorf("expected zero-value assignment; got %+v", stage)
	}
	if stage.Extras != nil {
		t.Errorf("expected nil extras map; got %v", stage.Extras)
	}
}

func TestResolveMessageName_Found(t *testing.T) {
	defs := &BPMNDefinitions{Messages: []BPMNMessage{{ID: "Msg_1", Name: "OrderPlaced"}}}
	if got := ResolveMessageName("Msg_1", defs); got != "OrderPlaced" {
		t.Errorf("got %q; want OrderPlaced", got)
	}
}

func TestNodeMessageRef_Found(t *testing.T) {
	defs := &BPMNDefinitions{Processes: []BPMNProcess{{
		SendTasks:    []BPMNSendTask{{ID: "st1", MessageRef: "Msg_send"}},
		ReceiveTasks: []BPMNReceiveTask{{ID: "rt1", MessageRef: "Msg_recv"}},
		BoundaryEvents: []BPMNBoundaryEvent{
			{ID: "be1", Message: &BPMNMessageEventDef{MessageRef: "Msg_be"}},
			{ID: "be2"}, // no Message -> skip
		},
	}}}
	if got := nodeMessageRef("st1", defs); got != "Msg_send" {
		t.Errorf("send task: got %q", got)
	}
	if got := nodeMessageRef("rt1", defs); got != "Msg_recv" {
		t.Errorf("receive task: got %q", got)
	}
	if got := nodeMessageRef("be1", defs); got != "Msg_be" {
		t.Errorf("boundary event: got %q", got)
	}
	if got := nodeMessageRef("be2", defs); got != "" {
		t.Errorf("boundary event w/o message: got %q; want empty", got)
	}
	if got := nodeMessageRef("nonexistent", defs); got != "" {
		t.Errorf("not found: got %q; want empty", got)
	}
}

func TestResolveMessageFlowName_ViaOwnMessageRef(t *testing.T) {
	defs := &BPMNDefinitions{Messages: []BPMNMessage{{ID: "Msg_1", Name: "ViaFlowRef"}}}
	mf := &BPMNMessageFlow{MessageRef: "Msg_1"}
	if got := ResolveMessageFlowName(mf, defs); got != "ViaFlowRef" {
		t.Errorf("got %q; want ViaFlowRef", got)
	}
}

func TestResolveMessageFlowName_ViaTargetNode(t *testing.T) {
	defs := &BPMNDefinitions{
		Processes: []BPMNProcess{{ReceiveTasks: []BPMNReceiveTask{{ID: "rt1", MessageRef: "Msg_1"}}}},
		Messages:  []BPMNMessage{{ID: "Msg_1", Name: "ViaTarget"}},
	}
	mf := &BPMNMessageFlow{TargetRef: "rt1"}
	if got := ResolveMessageFlowName(mf, defs); got != "ViaTarget" {
		t.Errorf("got %q; want ViaTarget", got)
	}
}

func TestResolveMessageFlowName_ViaSourceNode(t *testing.T) {
	defs := &BPMNDefinitions{
		Processes: []BPMNProcess{{SendTasks: []BPMNSendTask{{ID: "st1", MessageRef: "Msg_1"}}}},
		Messages:  []BPMNMessage{{ID: "Msg_1", Name: "ViaSource"}},
	}
	mf := &BPMNMessageFlow{SourceRef: "st1"}
	if got := ResolveMessageFlowName(mf, defs); got != "ViaSource" {
		t.Errorf("got %q; want ViaSource", got)
	}
}

func TestResolveMessageFlowName_NoneResolved(t *testing.T) {
	mf := &BPMNMessageFlow{SourceRef: "unknown", TargetRef: "unknown2"}
	if got := ResolveMessageFlowName(mf, &BPMNDefinitions{}); got != "" {
		t.Errorf("got %q; want empty", got)
	}
}
