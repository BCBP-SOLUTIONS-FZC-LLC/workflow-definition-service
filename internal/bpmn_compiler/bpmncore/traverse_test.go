package bpmncore

import (
	"errors"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// stubElementHandler is a minimal ElementHandler used to drive TraverseNode's
// handler-found branches without depending on the sibling element package.
type stubElementHandler struct {
	nodeType FlowNodeType
	err      error
}

func (h *stubElementHandler) NodeType() FlowNodeType { return h.nodeType }

func (h *stubElementHandler) Validate(
	nodeID string, proc *BPMNProcess, g *Graph, defs *BPMNDefinitions,
	stageTypes map[string]StageTypeHandler, elements map[FlowNodeType]ElementHandler,
) []domain.BPMNValidationError {
	return nil
}

func (h *stubElementHandler) Compile(nodeID string, cs *CompileState) error {
	if h.err != nil {
		return h.err
	}
	cs.AppendStep(dsl.ExecutionStep{Sequential: []string{nodeID}})
	return nil
}

func TestDeptOf(t *testing.T) {
	s := newMinimalState()
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"t1"}, ExtensionElements: BPMNExtensionElements{
		ZeebeProps: BPMNZeebeProperties{Items: []ZeebeProperty{{Name: "dept_id", Value: "018e1f2a-0000-7000-8000-000000000099"}}},
	}}}

	id, label, iamDeptID := s.DeptOf("t1")
	if id != "Ops" || label != "Ops" || iamDeptID != "018e1f2a-0000-7000-8000-000000000099" {
		t.Errorf("got (%q,%q,%q); want (Ops,Ops,018e1f2a-0000-7000-8000-000000000099)", id, label, iamDeptID)
	}
	id2, label2, iamDeptID2 := s.DeptOf("unknown")
	if id2 != "" || label2 != "" || iamDeptID2 != "" {
		t.Errorf("got (%q,%q,%q); want (\"\",\"\",\"\")", id2, label2, iamDeptID2)
	}
}

func TestEnsureDept_Idempotent(t *testing.T) {
	s := newMinimalState()
	s.EnsureDept("ops", "Ops", "018e1f2a-0000-7000-8000-000000000099")
	s.EnsureDept("ops", "OpsRenamed", "018e1f2a-0000-7000-8000-000000000000")

	depts := s.CollectedDepts()
	if len(depts) != 1 || depts[0].Label != "Ops" || depts[0].IAMDepartmentID != "018e1f2a-0000-7000-8000-000000000099" {
		t.Errorf("expected dept to stay single with original label/iam id; got %v", depts)
	}
}

func TestAppendStage(t *testing.T) {
	s := newMinimalState()
	s.EnsureDept("ops", "Ops", "")
	s.AppendStage("ops", dsl.StageDef{NodeID: "t1"})

	depts := s.CollectedDepts()
	if len(depts[0].Stages) != 1 || depts[0].Stages[0].NodeID != "t1" {
		t.Errorf("expected stage t1 appended; got %v", depts[0].Stages)
	}
}

func TestFlushSeqBuf_EmptyNoOp(t *testing.T) {
	s := newMinimalState()
	s.FlushSeqBuf()
	if s.StepsCount() != 0 {
		t.Error("flushing an empty seqBuf must not add a step")
	}
}

func TestFlushSeqBuf_NonEmpty(t *testing.T) {
	s := newMinimalState()
	s.AddToSeqBuf("a")
	s.AddToSeqBuf("b")
	s.FlushSeqBuf()

	steps := s.CollectedSteps()
	if len(steps) != 1 || len(steps[0].Sequential) != 2 {
		t.Fatalf("expected one step with 2 sequential entries; got %v", steps)
	}
	if s.SeqBufCount() != 0 {
		t.Error("expected seqBuf cleared after flush")
	}
}

func TestTraverseNode_AlreadyVisited(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["a"] = NodeTypeUserTask
	s.Proc.UserTasks = []BPMNUserTask{{ID: "a"}}
	s.visited["a"] = true

	if err := s.TraverseNode("a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.StepsCount() != 0 {
		t.Error("already-visited node must be a no-op")
	}
}

func TestTraverseNode_HandlerFound(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["t1"] = NodeTypeUserTask
	s.Elements[NodeTypeUserTask] = &stubElementHandler{nodeType: NodeTypeUserTask}

	if err := s.TraverseNode("t1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.StepsCount() != 1 {
		t.Fatalf("expected handler.Compile to append a step; got %d", s.StepsCount())
	}
}

func TestTraverseNode_HandlerError(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["t1"] = NodeTypeUserTask
	wantErr := errors.New("boom")
	s.Elements[NodeTypeUserTask] = &stubElementHandler{nodeType: NodeTypeUserTask, err: wantErr}

	if err := s.TraverseNode("t1"); !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v; got %v", wantErr, err)
	}
}

func TestTraverseNode_PassthroughErrorPropagation(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["gw"] = NodeTypeInclusiveGateway // no handler registered
	s.G.NodeType["t1"] = NodeTypeUserTask
	s.G.Outgoing["gw"] = []string{"t1"}
	wantErr := errors.New("boom")
	s.Elements[NodeTypeUserTask] = &stubElementHandler{nodeType: NodeTypeUserTask, err: wantErr}

	if err := s.TraverseNode("gw"); !errors.Is(err, wantErr) {
		t.Fatalf("expected error to propagate through passthrough; got %v", err)
	}
}

func TestTraverseNode_ExternalParticipant_ErrorPropagation(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["pool_ext"] = NodeTypeExternalParticipant
	s.G.NodeType["t1"] = NodeTypeUserTask
	s.G.Outgoing["pool_ext"] = []string{"t1"}
	wantErr := errors.New("boom")
	s.Elements[NodeTypeUserTask] = &stubElementHandler{nodeType: NodeTypeUserTask, err: wantErr}

	if err := s.TraverseNode("pool_ext"); !errors.Is(err, wantErr) {
		t.Fatalf("expected error to propagate from external participant successor; got %v", err)
	}
}

func TestHandleGateway_NoForwardNoBack(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["gw"] = NodeTypeExclusiveGateway

	if err := s.HandleGateway("gw", ExclusiveGateway); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.StepsCount() != 0 {
		t.Error("expected no steps when gateway has no forward or back edges")
	}
}

func TestHandleGateway_SingleForwardNoBack(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["gw"] = NodeTypeExclusiveGateway
	s.G.NodeType["t1"] = NodeTypeUserTask
	s.G.Outgoing["gw"] = []string{"t1"}
	s.Elements[NodeTypeUserTask] = &stubElementHandler{nodeType: NodeTypeUserTask}

	if err := s.HandleGateway("gw", ExclusiveGateway); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.StepsCount() != 1 {
		t.Errorf("expected direct traversal into t1's handler; got %d steps", s.StepsCount())
	}
}

func TestHandleGateway_SingleForwardWithRevertsOnly(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["gw"] = NodeTypeExclusiveGateway
	s.G.NodeType["back1"] = NodeTypeUserTask
	s.G.Outgoing["gw"] = []string{"back1"}
	s.backEdges = map[[2]string]bool{{"gw", "back1"}: true}
	s.Proc.UserTasks = []BPMNUserTask{{ID: "back1"}}

	if err := s.HandleGateway("gw", ExclusiveGateway); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := s.CollectedSteps()
	if len(steps) != 1 || len(steps[0].Exclusive) != 1 {
		t.Fatalf("expected 1 step with 1 revert branch; got %v", steps)
	}
	if steps[0].Exclusive[0].RevertToNodeID != "back1" {
		t.Errorf("RevertToNodeID = %q", steps[0].Exclusive[0].RevertToNodeID)
	}
}

func TestHandleGateway_SingleForwardWithRevertAndForward(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["gw"] = NodeTypeExclusiveGateway
	s.G.NodeType["fwd1"] = NodeTypeEndEvent
	s.G.NodeType["back1"] = NodeTypeUserTask
	s.G.Outgoing["gw"] = []string{"fwd1", "back1"}
	s.backEdges = map[[2]string]bool{{"gw", "back1"}: true}
	s.Proc.UserTasks = []BPMNUserTask{{ID: "back1"}}

	if err := s.HandleGateway("gw", ExclusiveGateway); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := s.CollectedSteps()
	if len(steps) != 1 || len(steps[0].Exclusive) != 2 {
		t.Fatalf("expected 1 step with 2 exclusive branches (forward+revert); got %v", steps)
	}
	if !steps[0].Exclusive[0].Terminates {
		t.Error("expected the forward branch to terminate (end event)")
	}
}

func TestHandleGateway_SplitNoJoin(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["gw"] = NodeTypeExclusiveGateway
	s.G.NodeType["end1"] = NodeTypeEndEvent
	s.G.NodeType["end2"] = NodeTypeEndEvent
	s.G.Outgoing["gw"] = []string{"end1", "end2"}

	if err := s.HandleGateway("gw", ExclusiveGateway); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := s.CollectedSteps()
	if len(steps) != 1 || len(steps[0].Exclusive) != 2 {
		t.Fatalf("expected 1 step w/ 2 branches; got %v", steps)
	}
}

func TestHandleGateway_SplitWithJoin(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["gw"] = NodeTypeParallelGateway
	s.G.NodeType["a"] = NodeTypeUserTask
	s.G.NodeType["b"] = NodeTypeUserTask
	s.G.NodeType["join"] = NodeTypeParallelGateway
	s.G.Outgoing["gw"] = []string{"a", "b"}
	s.G.Outgoing["a"] = []string{"join"}
	s.G.Outgoing["b"] = []string{"join"}
	s.G.Incoming["join"] = []string{"a", "b"}
	s.Proc.UserTasks = []BPMNUserTask{{ID: "a"}, {ID: "b"}}

	if err := s.HandleGateway("gw", ParallelGateway); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := s.CollectedSteps()
	if len(steps) != 1 || len(steps[0].Parallel) != 2 {
		t.Fatalf("expected 1 step w/ 2 parallel branches; got %v", steps)
	}
	if !s.IsVisited("join") {
		t.Error("expected join to be traversed after split")
	}
}

func TestHandleSingleForwardWithReverts_SubprocessForward(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["sp1"] = NodeTypeSubProcess
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"sp1"}}}
	s.Proc.SubProcesses = []BPMNSubProcess{{ID: "sp1", Name: "Sub"}}

	if err := s.handleSingleForwardWithReverts("gw", []string{"sp1"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := s.CollectedSteps()
	if len(steps) != 1 || len(steps[0].Exclusive) != 1 {
		t.Fatalf("expected 1 step with 1 exclusive branch; got %v", steps)
	}
	if steps[0].Exclusive[0].TargetNodeID != "sp1" {
		t.Errorf("TargetNodeID = %q; want sp1", steps[0].Exclusive[0].TargetNodeID)
	}
}

func TestHandleSplitNoJoin_ParallelKindErrors(t *testing.T) {
	s := newMinimalState()
	if err := s.handleSplitNoJoin("gw", []string{"a", "b"}, nil, ParallelGateway); err == nil {
		t.Fatal("expected error for parallel gateway with no matching join")
	}
}

func TestHandleSplitNoJoin_ExclusiveWithContinuation(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["end1"] = NodeTypeEndEvent
	s.G.NodeType["t1"] = NodeTypeUserTask
	s.Proc.UserTasks = []BPMNUserTask{{ID: "t1"}}
	s.Elements[NodeTypeUserTask] = &stubElementHandler{nodeType: NodeTypeUserTask}

	if err := s.handleSplitNoJoin("gw", []string{"end1", "t1"}, nil, ExclusiveGateway); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := s.CollectedSteps()
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps (split branches + t1's continuation); got %d: %v", len(steps), steps)
	}
	if len(steps[0].Exclusive) != 2 {
		t.Fatalf("expected 2 exclusive branches; got %v", steps[0].Exclusive)
	}
}

func TestCompileSubprocBranches_SkipsAndProcesses(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["plain"] = NodeTypeUserTask // not a subprocess -> skip
	s.G.NodeType["join"] = NodeTypeExclusiveGateway
	s.G.NodeType["sp1"] = NodeTypeSubProcess
	s.G.Outgoing["sp1"] = []string{"join"}
	s.visited["visited1"] = true // already visited -> skip
	s.Elements[NodeTypeSubProcess] = &stubElementHandler{nodeType: NodeTypeSubProcess}

	err := s.compileSubprocBranches([]string{"plain", "join", "visited1", "sp1"}, "join")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := s.CollectedSteps()
	if len(steps) != 1 || len(steps[0].Sequential) != 1 || steps[0].Sequential[0] != "sp1" {
		t.Fatalf("expected sp1's branch step to be merged into parent steps; got %v", steps)
	}
}

func TestHandleSplitWithJoin_Parallel(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["a"] = NodeTypeUserTask
	s.G.NodeType["join"] = NodeTypeParallelGateway
	s.G.Outgoing["gw"] = []string{"a"}
	s.G.Outgoing["a"] = []string{"join"}
	s.Proc.UserTasks = []BPMNUserTask{{ID: "a"}}

	if err := s.handleSplitWithJoin("gw", []string{"a"}, nil, "join", ParallelGateway); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := s.CollectedSteps()
	if len(steps) != 1 || len(steps[0].Parallel) != 1 {
		t.Fatalf("expected 1 step with 1 parallel branch; got %v", steps)
	}
	if !s.IsVisited("join") {
		t.Error("expected join node to be traversed")
	}
}

func TestHandleSplitWithJoin_ExclusiveError(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["gw"] = NodeTypeExclusiveGateway
	s.G.NodeType["badtask"] = NodeTypeUserTask
	s.G.Outgoing["gw"] = []string{"badtask"}
	// No matching proc.UserTasks entry -> FindTask returns nil -> error.

	if err := s.handleSplitWithJoin("gw", []string{"badtask"}, nil, "join", ExclusiveGateway); err == nil {
		t.Fatal("expected error to propagate from traverseExclusiveBranches")
	}
}

func TestHandleSplitWithJoin_SubprocBranchError(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["gw"] = NodeTypeExclusiveGateway
	s.G.NodeType["sp1"] = NodeTypeSubProcess
	s.G.NodeType["join"] = NodeTypeExclusiveGateway
	wantErr := errors.New("boom")
	s.Elements[NodeTypeSubProcess] = &stubElementHandler{nodeType: NodeTypeSubProcess, err: wantErr}

	err := s.handleSplitWithJoin("gw", []string{"sp1"}, nil, "join", ExclusiveGateway)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected subprocess branch error to propagate; got %v", err)
	}
}

func TestHandleSplitWithJoin_ParallelError(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["gw"] = NodeTypeParallelGateway
	s.G.NodeType["a"] = NodeTypeUserTask
	s.G.Outgoing["gw"] = []string{"a"}
	wantErr := errors.New("boom")
	s.Elements[NodeTypeUserTask] = &stubElementHandler{nodeType: NodeTypeUserTask, err: wantErr}

	err := s.handleSplitWithJoin("gw", []string{"a"}, nil, "join", ParallelGateway)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected parallel branch error to propagate; got %v", err)
	}
}

func TestFillBoundaryTimerTarget_NilStageField(t *testing.T) {
	s := newMinimalState()
	s.FillBoundaryTimerTarget("t1", &dsl.StageDef{}) // BoundaryTimer nil -> no-op, no panic
}

func TestFillBoundaryTimerTarget_NoBoundaryEvent(t *testing.T) {
	s := newMinimalState()
	stage := &dsl.StageDef{BoundaryTimer: &dsl.BoundaryTimer{}}
	s.FillBoundaryTimerTarget("t1", stage)
	if stage.BoundaryTimer.TargetDept != "" {
		t.Error("expected TargetDept unset when no matching boundary event")
	}
}

func TestFillBoundaryTimerTarget_NoOutgoingTarget(t *testing.T) {
	s := newMinimalState()
	s.Proc.BoundaryEvents = []BPMNBoundaryEvent{{ID: "be1", AttachedToRef: "t1", Timer: &BPMNTimerDef{}}}
	stage := &dsl.StageDef{BoundaryTimer: &dsl.BoundaryTimer{}}
	s.FillBoundaryTimerTarget("t1", stage)
	if stage.BoundaryTimer.TargetDept != "" {
		t.Error("expected TargetDept unset when boundary event has no outgoing flow")
	}
}

func TestFillBoundaryTimerTarget_Resolved(t *testing.T) {
	s := newMinimalState()
	s.Proc.BoundaryEvents = []BPMNBoundaryEvent{{ID: "be1", AttachedToRef: "t1", Timer: &BPMNTimerDef{Duration: "PT1H"}}}
	s.Proc.SequenceFlows = []BPMNSequenceFlow{{SourceRef: "be1", TargetRef: "target1"}}
	s.G.NodeType["target1"] = NodeTypeUserTask
	s.Proc.UserTasks = []BPMNUserTask{{ID: "target1"}}
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"target1"}}}

	stage := &dsl.StageDef{BoundaryTimer: &dsl.BoundaryTimer{}}
	s.FillBoundaryTimerTarget("t1", stage)
	if stage.BoundaryTimer.TargetDept != "Ops" {
		t.Errorf("TargetDept = %q; want Ops", stage.BoundaryTimer.TargetDept)
	}
}

func TestFillBoundaryMessageTarget_NilStageField(t *testing.T) {
	s := newMinimalState()
	s.FillBoundaryMessageTarget("t1", &dsl.StageDef{})
}

func TestFillBoundaryMessageTarget_NoBoundaryEvent(t *testing.T) {
	s := newMinimalState()
	stage := &dsl.StageDef{BoundaryMessage: &dsl.MessagePath{}}
	s.FillBoundaryMessageTarget("t1", stage)
	if stage.BoundaryMessage.TargetDept != "" {
		t.Error("expected no target when no matching boundary event")
	}
}

func TestFillBoundaryMessageTarget_NoOutgoingTarget(t *testing.T) {
	s := newMinimalState()
	s.Proc.BoundaryEvents = []BPMNBoundaryEvent{{ID: "be1", AttachedToRef: "t1", Message: &BPMNMessageEventDef{}}}
	stage := &dsl.StageDef{BoundaryMessage: &dsl.MessagePath{}}
	s.FillBoundaryMessageTarget("t1", stage)
	if stage.BoundaryMessage.TargetDept != "" {
		t.Error("expected TargetDept unset when boundary event has no outgoing flow")
	}
}

func TestFillBoundaryMessageTarget_Resolved(t *testing.T) {
	s := newMinimalState()
	s.Proc.BoundaryEvents = []BPMNBoundaryEvent{{ID: "be1", AttachedToRef: "t1", Message: &BPMNMessageEventDef{}}}
	s.Proc.SequenceFlows = []BPMNSequenceFlow{{SourceRef: "be1", TargetRef: "target1"}}
	s.G.NodeType["target1"] = NodeTypeUserTask
	s.Proc.UserTasks = []BPMNUserTask{{ID: "target1"}}
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"target1"}}}

	stage := &dsl.StageDef{BoundaryMessage: &dsl.MessagePath{}}
	s.FillBoundaryMessageTarget("t1", stage)
	if stage.BoundaryMessage.TargetDept != "Ops" {
		t.Errorf("TargetDept = %q; want Ops", stage.BoundaryMessage.TargetDept)
	}
}

func TestCompileBoundaryPaths_SkipsAndTraverses(t *testing.T) {
	s := newMinimalState()
	s.Proc.BoundaryEvents = []BPMNBoundaryEvent{
		{ID: "be_error", AttachedToRef: "t1", Error: &BPMNErrorDef{}}, // neither timer nor message -> skip
		{ID: "be_visited", AttachedToRef: "t1", Timer: &BPMNTimerDef{}},
		{ID: "be_ok", AttachedToRef: "t1", Message: &BPMNMessageEventDef{}},
	}
	s.G.NodeType["be_ok"] = NodeTypeMessageBoundaryEvent
	s.visited["be_visited"] = true

	if err := s.CompileBoundaryPaths(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.IsVisited("be_ok") {
		t.Error("expected be_ok to be traversed")
	}
}

func TestCompileBoundaryPaths_ErrorPropagation(t *testing.T) {
	s := newMinimalState()
	s.Proc.BoundaryEvents = []BPMNBoundaryEvent{{ID: "be1", AttachedToRef: "t1", Timer: &BPMNTimerDef{}}}
	s.G.NodeType["be1"] = NodeTypeTimerBoundaryEvent
	wantErr := errors.New("boom")
	s.Elements[NodeTypeTimerBoundaryEvent] = &stubElementHandler{nodeType: NodeTypeTimerBoundaryEvent, err: wantErr}

	err := s.CompileBoundaryPaths()
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped error; got %v", err)
	}
}

func TestFirstDeptAhead(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["t1"] = NodeTypeUserTask
	s.Proc.UserTasks = []BPMNUserTask{{ID: "t1"}}
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"t1"}}}

	if got := s.FirstDeptAhead("t1"); got != "Ops" {
		t.Errorf("got %q; want Ops", got)
	}
}

func TestBackNexts(t *testing.T) {
	s := newMinimalState()
	s.G.Outgoing["gw"] = []string{"a", "b"}
	s.backEdges = map[[2]string]bool{{"gw", "b"}: true}

	got := s.backNexts("gw")
	if len(got) != 1 || got[0] != "b" {
		t.Errorf("got %v; want [b]", got)
	}
	if got2 := s.backNexts("nonexistent"); got2 != nil {
		t.Errorf("expected nil for node with no outgoing edges; got %v", got2)
	}
}

func TestRevertBranches_SubWorkflowAndEmpty(t *testing.T) {
	s := newMinimalState()
	if got := s.revertBranches("gw", nil); len(got) != 0 {
		t.Errorf("expected empty slice for no back targets; got %v", got)
	}

	s.G.NodeType["sp1"] = NodeTypeSubProcess
	s.Proc.SubProcesses = []BPMNSubProcess{{ID: "sp1", Name: "Sub"}}
	got := s.revertBranches("gw", []string{"sp1"})
	if len(got) != 1 || got[0].RevertToStage != "sub_workflow" {
		t.Fatalf("expected sub_workflow revert branch; got %v", got)
	}
	if got[0].RevertToNodeID != "sp1" {
		t.Errorf("RevertToNodeID = %q; want sp1", got[0].RevertToNodeID)
	}
}

func TestFirstTaskAhead(t *testing.T) {
	s := newMinimalState()

	s.G.NodeType["dead"] = NodeTypeExclusiveGateway
	if dept, stage := s.firstTaskAhead("dead"); dept != "" || stage != "" {
		t.Errorf("dead end: got (%q,%q)", dept, stage)
	}

	s.G.NodeType["loop"] = NodeTypeExclusiveGateway
	s.G.Outgoing["loop"] = []string{"loop"}
	if dept, stage := s.firstTaskAhead("loop"); dept != "" || stage != "" {
		t.Errorf("cycle: got (%q,%q)", dept, stage)
	}

	s.G.NodeType["sp1"] = NodeTypeSubProcess
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"sp1"}}}
	if dept, stage := s.firstTaskAhead("sp1"); dept != "Ops" || stage != "sub_workflow" {
		t.Errorf("subprocess: got (%q,%q); want (Ops,sub_workflow)", dept, stage)
	}

	s.G.NodeType["gw2"] = NodeTypeExclusiveGateway
	s.G.NodeType["t1"] = NodeTypeUserTask
	s.G.Outgoing["gw2"] = []string{"t1"}
	s.Proc.UserTasks = []BPMNUserTask{{ID: "t1"}}
	s.firstTaskAhead("gw2") // exercises the passthrough-then-userTask branch
}

func TestFirstTaskNodeAhead_DanglingReferences(t *testing.T) {
	// Node types registered in the graph but with no matching entry in the
	// process's own slices — exercises the "not found" return in each switch case.
	s := newMinimalState()
	s.G.NodeType["ut_dangling"] = NodeTypeUserTask
	s.G.NodeType["st_dangling"] = NodeTypeSendTask
	s.G.NodeType["rt_dangling"] = NodeTypeReceiveTask
	s.G.NodeType["sp_dangling"] = NodeTypeSubProcess
	s.G.NodeType["ca_dangling"] = NodeTypeCallActivity

	for _, id := range []string{"ut_dangling", "st_dangling", "rt_dangling", "sp_dangling", "ca_dangling"} {
		if nodeID, name := s.firstTaskNodeAhead(id); nodeID != "" || name != "" {
			t.Errorf("%s: got (%q,%q); want (\"\",\"\")", id, nodeID, name)
		}
	}
}

func TestResolveUserTaskDept_WithTaskDefinition(t *testing.T) {
	s := newMinimalState()
	s.Proc.UserTasks = []BPMNUserTask{{
		ID:                "t1",
		ExtensionElements: BPMNExtensionElements{TaskDefinition: &ZeebeTaskDefinition{Type: "prep"}},
	}}
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"t1"}}}

	dept, stage := s.resolveUserTaskDept("t1")
	if dept != "Ops" || stage != "prep" {
		t.Errorf("got (%q,%q); want (Ops,prep)", dept, stage)
	}
}

func TestReachesEndEvent_True(t *testing.T) {
	g := &Graph{
		NodeType: map[string]FlowNodeType{"a": NodeTypeUserTask, "end": NodeTypeEndEvent},
		Outgoing: map[string][]string{"a": {"end"}},
	}
	if !reachesEndEvent(g, nil, "a", "") {
		t.Error("expected true: end event reachable")
	}
}

func TestReachesEndEvent_StopsAtBoundary(t *testing.T) {
	g := &Graph{
		NodeType: map[string]FlowNodeType{"a": NodeTypeUserTask, "stop": NodeTypeUserTask, "end": NodeTypeEndEvent},
		Outgoing: map[string][]string{"a": {"stop"}, "stop": {"end"}},
	}
	if reachesEndEvent(g, nil, "a", "stop") {
		t.Error("expected false: traversal must not continue past the stop node")
	}
}

func TestReachesEndEvent_BackEdgeFiltered(t *testing.T) {
	g := &Graph{
		NodeType: map[string]FlowNodeType{"a": NodeTypeUserTask, "end": NodeTypeEndEvent},
		Outgoing: map[string][]string{"a": {"end"}},
	}
	backEdges := map[[2]string]bool{{"a", "end"}: true}
	if reachesEndEvent(g, backEdges, "a", "") {
		t.Error("expected false: only edge to end is a back edge")
	}
}

func TestCompileExclusiveBranch_NoTaskFallsBackToJoin(t *testing.T) {
	s := newMinimalState()
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"join"}}}
	s.G.NodeType["mid"] = NodeTypeExclusiveGateway
	s.G.NodeType["join"] = NodeTypeUserTask
	s.G.Outgoing["mid"] = []string{"join"}
	s.Proc.UserTasks = []BPMNUserTask{{ID: "join", Name: "AfterJoin"}}

	branch, err := s.compileExclusiveBranch("gw", "mid", "join")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch.Terminates {
		t.Error("expected non-terminating branch")
	}
	if branch.Target != "Ops" {
		t.Errorf("Target = %q; want %q", branch.Target, "Ops")
	}
	if branch.TargetNodeID != "join" {
		t.Errorf("TargetNodeID = %q; want %q", branch.TargetNodeID, "join")
	}
}

func TestCompileExclusiveBranch_DeadEndFallsBackToJoinTaskNode(t *testing.T) {
	s := newMinimalState()
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"join"}}}
	s.G.NodeType["dead1"] = NodeTypeExclusiveGateway
	s.G.NodeType["join"] = NodeTypeUserTask
	s.Proc.UserTasks = []BPMNUserTask{{ID: "join", Name: "AfterJoin"}}
	// dead1 has no outgoing edges -> the branch walk dead-ends before reaching joinID.

	branch, err := s.compileExclusiveBranch("gw", "dead1", "join")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch.TargetNodeID != "join" {
		t.Errorf("expected fallback to join's task node; got TargetNodeID=%q", branch.TargetNodeID)
	}
}

func TestCompileExclusiveBranch_CycleGuard(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["loop"] = NodeTypeExclusiveGateway
	s.G.Outgoing["loop"] = []string{"loop"}

	branch, err := s.compileExclusiveBranch("gw", "loop", "join")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch.Terminates {
		t.Error("expected non-terminating branch for a self-loop with no reachable end event")
	}
}

func TestProcessExclusiveNode_AllBranches(t *testing.T) {
	s := newMinimalState()
	s.Proc.UserTasks = []BPMNUserTask{{ID: "ut1"}}
	s.Proc.SendTasks = []BPMNSendTask{{ID: "st1"}}
	s.Proc.ReceiveTasks = []BPMNReceiveTask{{ID: "rt1"}}
	s.G.NodeType["ut1"] = NodeTypeUserTask
	s.G.NodeType["st1"] = NodeTypeSendTask
	s.G.NodeType["rt1"] = NodeTypeReceiveTask
	s.G.NodeType["sp1"] = NodeTypeSubProcess
	s.G.NodeType["ca1"] = NodeTypeCallActivity
	s.G.NodeType["gw1"] = NodeTypeExclusiveGateway // structural passthrough

	var dept, stage string
	if err := s.processExclusiveNode("ut1", &dept, &stage); err != nil {
		t.Fatalf("userTask: unexpected error: %v", err)
	}
	if !s.IsVisited("ut1") {
		t.Error("expected userTask to be marked visited")
	}

	dept, stage = "", ""
	if err := s.processExclusiveNode("st1", &dept, &stage); err != nil {
		t.Fatalf("sendTask: unexpected error: %v", err)
	}
	if stage != "send_task" {
		t.Errorf("stage = %q; want send_task", stage)
	}

	dept, stage = "", ""
	if err := s.processExclusiveNode("rt1", &dept, &stage); err != nil {
		t.Fatalf("receiveTask: unexpected error: %v", err)
	}
	if stage != "receive_task" {
		t.Errorf("stage = %q; want receive_task", stage)
	}

	dept, stage = "", ""
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"sp1"}}}
	if err := s.processExclusiveNode("sp1", &dept, &stage); err != nil {
		t.Fatalf("subProcess: unexpected error: %v", err)
	}
	if dept != "Ops" || stage != "sub_workflow" {
		t.Errorf("subProcess: got dept=%q stage=%q", dept, stage)
	}

	dept, stage = "already", "already-stage"
	if err := s.processExclusiveNode("ca1", &dept, &stage); err != nil {
		t.Fatalf("callActivity: unexpected error: %v", err)
	}
	if dept != "already" {
		t.Error("expected no overwrite when branchDept already set")
	}

	dept, stage = "", ""
	if err := s.processExclusiveNode("gw1", &dept, &stage); err != nil {
		t.Fatalf("gateway passthrough: unexpected error: %v", err)
	}
	if dept != "" || stage != "" {
		t.Errorf("expected structural passthrough to leave dept/stage unset; got %q/%q", dept, stage)
	}
}

func TestProcessExclusiveUserTask_NotFound(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["missing"] = NodeTypeUserTask
	var dept, stage string
	if err := s.processExclusiveUserTask("missing", &dept, &stage); err == nil {
		t.Fatal("expected error when task not found in process")
	}
}

func TestProcessExclusiveUserTask_DeptAlreadySet(t *testing.T) {
	s := newMinimalState()
	s.Proc.UserTasks = []BPMNUserTask{{ID: "ut1"}}
	s.G.NodeType["ut1"] = NodeTypeUserTask
	dept, stage := "preset", "preset-stage"
	if err := s.processExclusiveUserTask("ut1", &dept, &stage); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dept != "preset" || stage != "preset-stage" {
		t.Errorf("expected no overwrite of already-set dept/stage; got %q/%q", dept, stage)
	}
}

func TestProcessExclusiveUserTask_WithTaskDefinition(t *testing.T) {
	s := newMinimalState()
	s.Proc.UserTasks = []BPMNUserTask{{
		ID:                "ut1",
		ExtensionElements: BPMNExtensionElements{TaskDefinition: &ZeebeTaskDefinition{Type: "review"}},
	}}
	s.G.NodeType["ut1"] = NodeTypeUserTask
	var dept, stage string
	if err := s.processExclusiveUserTask("ut1", &dept, &stage); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage != "review" {
		t.Errorf("stage = %q; want review", stage)
	}
}

func TestProcessExclusiveMessageTask_SendNotFound(t *testing.T) {
	s := newMinimalState()
	var dept, stage string
	if err := s.processExclusiveMessageTask("missing", "send_task", &dept, &stage); err == nil {
		t.Fatal("expected error for missing send task")
	}
}

func TestProcessExclusiveMessageTask_ReceiveNotFound(t *testing.T) {
	s := newMinimalState()
	var dept, stage string
	if err := s.processExclusiveMessageTask("missing", "receive_task", &dept, &stage); err == nil {
		t.Fatal("expected error for missing receive task")
	}
}

func TestProcessExclusiveMessageTask_SendSuccess(t *testing.T) {
	s := newMinimalState()
	s.Proc.SendTasks = []BPMNSendTask{{ID: "st1", Name: "Notify"}}
	dept, stage := "", ""
	if err := s.processExclusiveMessageTask("st1", "send_task", &dept, &stage); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage != "send_task" {
		t.Errorf("stage = %q; want send_task", stage)
	}
	if !s.IsVisited("st1") {
		t.Error("expected send task marked visited")
	}
}

func TestProcessExclusiveMessageTask_ReceiveSuccess(t *testing.T) {
	s := newMinimalState()
	s.Proc.ReceiveTasks = []BPMNReceiveTask{{ID: "rt1", Name: "Await"}}
	dept, stage := "", ""
	if err := s.processExclusiveMessageTask("rt1", "receive_task", &dept, &stage); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage != "receive_task" {
		t.Errorf("stage = %q; want receive_task", stage)
	}
}

func TestProcessExclusiveMessageTask_DeptAlreadySet(t *testing.T) {
	s := newMinimalState()
	s.Proc.SendTasks = []BPMNSendTask{{ID: "st1"}}
	dept, stage := "preset", "preset-stage"
	if err := s.processExclusiveMessageTask("st1", "send_task", &dept, &stage); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dept != "preset" || stage != "preset-stage" {
		t.Errorf("expected no overwrite; got %q/%q", dept, stage)
	}
}
