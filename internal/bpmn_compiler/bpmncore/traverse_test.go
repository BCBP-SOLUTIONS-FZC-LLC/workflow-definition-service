package bpmncore

import (
	"errors"
	"slices"
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

func TestSegmentOf_StartsASegmentWhenTheLaneChangesOrAStepIntervenes(t *testing.T) {
	s := newMinimalState()
	s.Proc.LaneSet.Lanes = []BPMNLane{
		{Name: "Ops", FlowNodeRefs: []string{"a", "b", "d", "e"}, ExtensionElements: BPMNExtensionElements{
			ZeebeProps: BPMNZeebeProperties{Items: []ZeebeProperty{{Name: "dept_id", Value: "018e1f2a-0000-7000-8000-000000000099"}}},
		}},
		{Name: "Eng", FlowNodeRefs: []string{"c"}},
	}

	place := func(node string) string {
		dept := s.SegmentOf(node)
		s.AppendStage(dept, dsl.StageDef{NodeID: node})
		return dept
	}
	bound := s.LaneDepartment("c")
	got := []string{place("a"), place("b"), place("c"), place("d")}
	s.FlushSeqBuf()
	got = append(got, place("e"))

	if want := []string{"Ops", "Ops", "Eng", "Ops~2", "Ops~3"}; !slices.Equal(got, want) {
		t.Errorf("segments = %v, want %v", got, want)
	}
	if bound != "Eng" {
		t.Errorf("a call binding Eng before its first task got %q, want Eng, which that task then fills", bound)
	}
	for _, d := range s.CollectedDepts() {
		if d.ID == "Ops~2" && (d.Label != "Ops" || d.IAMDepartmentID != "018e1f2a-0000-7000-8000-000000000099") {
			t.Errorf("Ops~2 = %+v, want the Ops lane's label and IAM department", d)
		}
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

// The forward branch of a single-forward-with-reverts gateway must name no
// target. Its path is compiled as the steps that follow, so a Target here
// would make the Execution Service dispatch that department twice — once
// from this branch and once from the continuation step.
func TestHandleSingleForwardWithReverts_ForwardBranchNamesNoTarget(t *testing.T) {
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
	branch := steps[0].Exclusive[0]
	if branch.Target != "" || branch.TargetStage != "" || branch.TargetNodeID != "" || branch.TargetName != "" {
		t.Errorf("forward branch names a target (%+v); the continuation steps are the forward path", branch)
	}
	if !s.IsVisited("sp1") {
		t.Error("expected the forward node to be traversed into its own steps")
	}
}

func TestHandleSplitNoJoin_ParallelKindErrors(t *testing.T) {
	s := newMinimalState()
	if err := s.handleSplitNoJoin("gw", []string{"a", "b"}, nil, ParallelGateway); err == nil {
		t.Fatal("expected error for parallel gateway with no matching join")
	}
}

func TestHandleSplitNoJoin_EachBranchCarriesItsStepsAndTerminates(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["end1"] = NodeTypeEndEvent
	s.G.NodeType["t1"] = NodeTypeUserTask
	s.Proc.UserTasks = []BPMNUserTask{{ID: "t1"}}
	s.Elements[NodeTypeUserTask] = &stubElementHandler{nodeType: NodeTypeUserTask}

	if err := s.handleSplitNoJoin("gw", []string{"end1", "t1"}, nil, ExclusiveGateway); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := s.CollectedSteps()
	if len(steps) != 1 || len(steps[0].Exclusive) != 2 {
		t.Fatalf("expected one exclusive step with 2 branches; got %v", steps)
	}
	toEnd, toTask := steps[0].Exclusive[0], steps[0].Exclusive[1]
	if !toEnd.Terminates || len(toEnd.Steps) != 0 {
		t.Errorf("branch to the end event = %+v, want no steps and terminating", toEnd)
	}
	if !toTask.Terminates || len(toTask.Steps) != 1 || toTask.Steps[0].Sequential[0] != "t1" {
		t.Errorf("branch to t1 = %+v, want t1's step and terminating", toTask)
	}
}

func TestHandleSplitWithJoin_Parallel(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["a"] = NodeTypeUserTask
	s.G.NodeType["join"] = NodeTypeParallelGateway
	s.G.Outgoing["gw"] = []string{"a"}
	s.G.Outgoing["a"] = []string{"join"}
	s.Proc.UserTasks = []BPMNUserTask{{ID: "a"}}

	if err := s.handleSplitWithJoin("gw", nil, "join", ParallelGateway); err != nil {
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
	s.G.NodeType["sp1"] = NodeTypeSubProcess
	s.G.Outgoing["gw"] = []string{"sp1"}
	wantErr := errors.New("boom")
	s.Elements[NodeTypeSubProcess] = &stubElementHandler{nodeType: NodeTypeSubProcess, err: wantErr}

	if err := s.handleSplitWithJoin("gw", nil, "join", ExclusiveGateway); !errors.Is(err, wantErr) {
		t.Fatalf("expected the branch's error to propagate; got %v", err)
	}
}

func TestHandleSplitWithJoin_ParallelError(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["gw"] = NodeTypeParallelGateway
	s.G.NodeType["a"] = NodeTypeUserTask
	s.G.Outgoing["gw"] = []string{"a"}
	wantErr := errors.New("boom")
	s.Elements[NodeTypeUserTask] = &stubElementHandler{nodeType: NodeTypeUserTask, err: wantErr}

	err := s.handleSplitWithJoin("gw", nil, "join", ParallelGateway)
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
	if stage.BoundaryTimer.TargetDept != segmentOfPending("target1") {
		t.Errorf("TargetDept = %q; want target1's segment, pending", stage.BoundaryTimer.TargetDept)
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
	if stage.BoundaryMessage.TargetDept != segmentOfPending("target1") {
		t.Errorf("TargetDept = %q; want target1's segment, pending", stage.BoundaryMessage.TargetDept)
	}
}

func TestCompileBoundaryPaths_CompilesOnlyUnreachedPathsAndKeepsThemOffTheMainSteps(t *testing.T) {
	s := newMinimalState()
	s.Proc.BoundaryEvents = []BPMNBoundaryEvent{
		{ID: "be_end", AttachedToRef: "t1", Timer: &BPMNTimerDef{}},
		{ID: "be_done", AttachedToRef: "t1", Timer: &BPMNTimerDef{}},
		{ID: "be_new", AttachedToRef: "t1", Message: &BPMNMessageEventDef{}},
	}
	s.Proc.SequenceFlows = []BPMNSequenceFlow{{SourceRef: "be_end", TargetRef: "end"}, {SourceRef: "be_done", TargetRef: "t_done"}, {SourceRef: "be_new", TargetRef: "t_new"}}
	s.G.NodeType["end"], s.G.NodeType["t_done"], s.G.NodeType["t_new"] = NodeTypeEndEvent, NodeTypeUserTask, NodeTypeUserTask
	s.sd.nodeSegment["t_done"] = "Ops"
	var compiled []string
	s.Elements[NodeTypeUserTask] = &recordingHandler{compiled: &compiled}
	s.Elements[NodeTypeEndEvent] = &recordingHandler{compiled: &compiled}

	if err := s.CompileBoundaryPaths(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(compiled, []string{"t_new"}) {
		t.Errorf("compiled %v, want only t_new: the end needs no path and t_done is compiled", compiled)
	}
	if s.StepsCount() != 0 {
		t.Errorf("main steps = %v, want none: a boundary's path runs only when it fires", s.CollectedSteps())
	}
}

// recordingHandler records each node it compiles and emits a step for it.
type recordingHandler struct{ compiled *[]string }

func (h *recordingHandler) NodeType() FlowNodeType { return NodeTypeUserTask }

func (h *recordingHandler) Validate(string, *BPMNProcess, *Graph, *BPMNDefinitions, map[string]StageTypeHandler, map[FlowNodeType]ElementHandler) []domain.BPMNValidationError {
	return nil
}

func (h *recordingHandler) Compile(nodeID string, cs *CompileState) error {
	*h.compiled = append(*h.compiled, nodeID)
	cs.AppendStep(dsl.ExecutionStep{Sequential: []string{nodeID}})
	return nil
}

func TestCompileBoundaryPaths_ErrorPropagation(t *testing.T) {
	s := newMinimalState()
	s.Proc.BoundaryEvents = []BPMNBoundaryEvent{{ID: "be1", AttachedToRef: "t1", Timer: &BPMNTimerDef{}}}
	s.Proc.SequenceFlows = []BPMNSequenceFlow{{SourceRef: "be1", TargetRef: "t2"}}
	s.G.NodeType["t2"] = NodeTypeUserTask
	wantErr := errors.New("boom")
	s.Elements[NodeTypeUserTask] = &stubElementHandler{nodeType: NodeTypeUserTask, err: wantErr}

	err := s.CompileBoundaryPaths()
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped error; got %v", err)
	}
}

func TestBoundaryTarget(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["t1"], s.G.NodeType["end"] = NodeTypeUserTask, NodeTypeEndEvent
	s.Proc.UserTasks = []BPMNUserTask{{ID: "t1"}}
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"t1"}}}
	s.Proc.SequenceFlows = []BPMNSequenceFlow{{ID: "f1", SourceRef: "be_task", TargetRef: "t1"}, {ID: "f2", SourceRef: "be_end", TargetRef: "end"}}

	for _, tc := range []struct {
		boundary, dept string
		terminates     bool
	}{
		{"be_task", segmentOfPending("t1"), false},
		{"be_end", "", true},
		{"be_nowhere", "", true},
	} {
		if dept, terminates := s.BoundaryTarget(tc.boundary); dept != tc.dept || terminates != tc.terminates {
			t.Errorf("BoundaryTarget(%s) = %q, %v; want %q, %v", tc.boundary, dept, terminates, tc.dept, tc.terminates)
		}
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

func TestTaskAhead(t *testing.T) {
	s := newMinimalState()

	s.G.NodeType["dead"] = NodeTypeExclusiveGateway
	s.G.NodeType["loop"] = NodeTypeExclusiveGateway
	s.G.Outgoing["loop"] = []string{"loop"}
	s.G.NodeType["sp1"] = NodeTypeSubProcess
	s.G.NodeType["gw2"] = NodeTypeExclusiveGateway
	s.G.NodeType["t1"] = NodeTypeUserTask
	s.G.Outgoing["gw2"] = []string{"t1"}

	for from, want := range map[string]string{"dead": "", "loop": "", "sp1": "sp1", "gw2": "t1"} {
		if got := s.taskAhead(from); got != want {
			t.Errorf("taskAhead(%s) = %q, want %q", from, got, want)
		}
	}
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
