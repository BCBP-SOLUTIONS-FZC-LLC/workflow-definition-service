package bpmncore

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type w2dStepHandler struct {
	nodeType FlowNodeType
	err      error
	compiled map[string]int
}

func (h *w2dStepHandler) NodeType() FlowNodeType { return h.nodeType }

func (h *w2dStepHandler) Validate(
	string, *BPMNProcess, *Graph, *BPMNDefinitions, map[string]StageTypeHandler, map[FlowNodeType]ElementHandler,
) []domain.BPMNValidationError {
	return nil
}

func (h *w2dStepHandler) Compile(nodeID string, cs *CompileState) error {
	if h.compiled != nil {
		h.compiled[nodeID]++
	}
	if h.err != nil {
		return h.err
	}
	cs.AppendStep(dsl.ExecutionStep{Sequential: []string{nodeID}})
	return nil
}

type w2dStageHandler struct {
	nodeType FlowNodeType
	compiled map[string]int
}

func (h *w2dStageHandler) NodeType() FlowNodeType { return h.nodeType }

func (h *w2dStageHandler) Validate(
	string, *BPMNProcess, *Graph, *BPMNDefinitions, map[string]StageTypeHandler, map[FlowNodeType]ElementHandler,
) []domain.BPMNValidationError {
	return nil
}

func (h *w2dStageHandler) Compile(nodeID string, cs *CompileState) error {
	h.compiled[nodeID]++
	dept := cs.SegmentOf(nodeID)
	cs.AppendStage(dept, dsl.StageDef{NodeID: nodeID})
	cs.AddToSeqBuf(dept)
	return cs.traverseForwardNexts(nodeID)
}

func w2dGraphState(edges map[string][]string, types map[string]FlowNodeType) *CompileState {
	s := newMinimalState()
	for from, tos := range edges {
		s.G.Outgoing[from] = tos
	}
	for id, nt := range types {
		s.G.NodeType[id] = nt
	}
	return s
}

func TestHandleSplitWithJoin_ExclusiveSplit_BuildsOneBranchPerEdgeAndTraversesTheJoinAfterwards(t *testing.T) {
	s := w2dGraphState(
		map[string][]string{"gw": {"review", "join"}, "review": {"join"}},
		map[string]FlowNodeType{
			"gw": NodeTypeExclusiveGateway, "review": NodeTypeUserTask, "join": NodeTypeUserTask,
		},
	)
	s.condExprs[[2]string{"gw", "review"}] = "=needsReview"
	s.condExprs[[2]string{"gw", "join"}] = "=skip"
	s.Proc.UserTasks = []BPMNUserTask{{
		ID: "review", Name: "Review it",
		ExtensionElements: BPMNExtensionElements{TaskDefinition: &ZeebeTaskDefinition{Type: "approval"}},
	}}
	compiled := map[string]int{}
	s.Elements[NodeTypeUserTask] = &w2dStepHandler{nodeType: NodeTypeUserTask, compiled: compiled}

	require.NoError(t, s.handleSplitWithJoin("gw", nil, "join", ExclusiveGateway))

	steps := s.CollectedSteps()
	require.Len(t, steps, 2, "the split step, then the join node traversed after it")
	require.Len(t, steps[0].Exclusive, 2)
	assert.Empty(t, steps[0].Parallel)

	withTask, direct := steps[0].Exclusive[0], steps[0].Exclusive[1]
	assert.Equal(t, "=needsReview", withTask.ConditionExpression)
	assert.Equal(t, []dsl.ExecutionStep{{Sequential: []string{"review"}}}, withTask.Steps)
	assert.Equal(t, "review", withTask.Target)
	assert.Equal(t, "review", withTask.TargetNodeID)
	assert.Equal(t, "Review it", withTask.TargetName)
	assert.Equal(t, "approval", withTask.TargetStage)

	assert.Equal(t, dsl.ExclusiveBranch{ConditionExpression: "=skip"}, direct,
		"a branch flowing straight to the join carries only its condition")

	assert.Equal(t, []dsl.ExecutionStep{{Sequential: []string{"join"}}}, steps[1:])
	assert.Equal(t, 1, compiled["join"], "the join is compiled once, by the parent state after the split")
	assert.Equal(t, 1, compiled["review"])
}

func TestHandleSplitWithJoin_BranchesMergingBeforeTheJoin_CompileTheSharedNodeOncePerBranch(t *testing.T) {
	s := w2dGraphState(
		map[string][]string{
			"gw": {"a", "b", "c"},
			"a":  {"shared"}, "b": {"shared"}, "shared": {"join"}, "c": {"join"},
		},
		map[string]FlowNodeType{
			"gw": NodeTypeExclusiveGateway, "a": NodeTypeUserTask, "b": NodeTypeUserTask,
			"shared": NodeTypeUserTask, "c": NodeTypeUserTask, "join": NodeTypeUserTask,
		},
	)
	all := []string{"a", "b", "shared", "c", "join"}
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: all}}
	for _, id := range all {
		s.Proc.UserTasks = append(s.Proc.UserTasks, BPMNUserTask{ID: id})
	}
	compiled := map[string]int{}
	s.Elements[NodeTypeUserTask] = &w2dStageHandler{nodeType: NodeTypeUserTask, compiled: compiled}

	require.NoError(t, s.handleSplitWithJoin("gw", nil, "join", ExclusiveGateway))
	s.FlushSeqBuf()

	assert.Equal(t, 2, compiled["shared"], "each branch walks the shared node with its own visited set")
	assert.Equal(t, 1, compiled["a"])
	assert.Equal(t, 1, compiled["b"])
	assert.Equal(t, 1, compiled["join"], "the join is reached by the parent only")

	stagesByNode := map[string]int{}
	for _, d := range s.CollectedDepts() {
		for _, st := range d.Stages {
			stagesByNode[st.NodeID]++
		}
	}
	assert.Equal(t, map[string]int{"a": 1, "b": 1, "shared": 2, "c": 1, "join": 1}, stagesByNode,
		"the shared node is duplicated into one stage per branch")
	assert.Equal(t, 6, s.sd.stages)
}

func TestNewBranchState_SharedNodeVisitedInOneBranch_IsStillCompiledInTheNext(t *testing.T) {
	parent := newMinimalState()
	parent.G.NodeType["shared"] = NodeTypeUserTask
	compiled := map[string]int{}
	parent.Elements[NodeTypeUserTask] = &w2dStepHandler{nodeType: NodeTypeUserTask, compiled: compiled}

	first := NewBranchState(parent, "")
	second := NewBranchState(parent, "")
	require.NoError(t, first.TraverseNode("shared"))
	require.NoError(t, first.TraverseNode("shared"))
	require.NoError(t, second.TraverseNode("shared"))

	assert.Equal(t, 2, compiled["shared"], "once in the first branch (its repeat visit is skipped), once in the second")
	assert.False(t, parent.IsVisited("shared"), "a branch never marks its parent's visited map")
}

func TestResolveErrorCode_LookupRules(t *testing.T) {
	cases := []struct {
		name string
		defs *BPMNDefinitions
		ref  string
		want string
	}{
		{"empty ref", &BPMNDefinitions{Errors: []BPMNError{{ID: "", ErrorCode: "E_EMPTY"}}}, "", ""},
		{"nil definitions", nil, "Error_1", ""},
		{"unknown ref", &BPMNDefinitions{Errors: []BPMNError{{ID: "Error_1", ErrorCode: "E1"}}}, "Error_2", ""},
		{"matching ref", &BPMNDefinitions{Errors: []BPMNError{
			{ID: "Error_1", ErrorCode: "E1"}, {ID: "Error_2", ErrorCode: "E2"},
		}}, "Error_2", "E2"},
		{"duplicate ids: first match", &BPMNDefinitions{Errors: []BPMNError{
			{ID: "Error_1", ErrorCode: "FIRST"}, {ID: "Error_1", ErrorCode: "SECOND"},
		}}, "Error_1", "FIRST"},
		{"matching ref without a code", &BPMNDefinitions{Errors: []BPMNError{{ID: "Error_1"}}}, "Error_1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newMinimalState()
			s.Defs = tc.defs
			assert.Equal(t, tc.want, s.ResolveErrorCode(tc.ref))
		})
	}
}

func TestStageTypeOf_ByNodeType(t *testing.T) {
	cases := []struct {
		name     string
		nodeType FlowNodeType
		inProc   bool
		ext      *ZeebeTaskDefinition
		want     string
	}{
		{"user task with a task definition", NodeTypeUserTask, true, &ZeebeTaskDefinition{Type: "approval"}, "approval"},
		{"user task without a task definition", NodeTypeUserTask, true, nil, ""},
		{"user task with an empty definition type", NodeTypeUserTask, true, &ZeebeTaskDefinition{}, ""},
		{"user task missing from the process", NodeTypeUserTask, false, nil, ""},
		{"send task", NodeTypeSendTask, false, nil, "send_task"},
		{"receive task", NodeTypeReceiveTask, false, nil, "receive_task"},
		{"sub-process", NodeTypeSubProcess, false, nil, "sub_workflow"},
		{"call activity", NodeTypeCallActivity, false, nil, "sub_workflow"},
		{"start event", NodeTypeStartEvent, false, nil, ""},
		{"end event", NodeTypeEndEvent, false, nil, ""},
		{"parallel gateway", NodeTypeParallelGateway, false, nil, ""},
		{"exclusive gateway", NodeTypeExclusiveGateway, false, nil, ""},
		{"inclusive gateway", NodeTypeInclusiveGateway, false, nil, ""},
		{"boundary event", NodeTypeBoundaryEvent, false, nil, ""},
		{"timer boundary event", NodeTypeTimerBoundaryEvent, false, nil, ""},
		{"error boundary event", NodeTypeErrorBoundaryEvent, false, nil, ""},
		{"message boundary event", NodeTypeMessageBoundaryEvent, false, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newMinimalState()
			s.G.NodeType["n"] = tc.nodeType
			if tc.inProc {
				s.Proc.UserTasks = []BPMNUserTask{{ID: "n", ExtensionElements: BPMNExtensionElements{TaskDefinition: tc.ext}}}
			}
			assert.Equal(t, tc.want, s.stageTypeOf("n"))
		})
	}

	t.Run("node unknown to the graph", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, newMinimalState().stageTypeOf("ghost"))
	})
}

func TestTraverseBranches_BranchesWithoutSteps_HaveNoDepartmentAndKeepEdgeOrder(t *testing.T) {
	s := w2dGraphState(
		map[string][]string{"split": {"a", "join", "b"}, "a": {"join"}, "b": {"join"}},
		map[string]FlowNodeType{
			"split": NodeTypeParallelGateway, "a": NodeTypeUserTask, "b": NodeTypeUserTask, "join": NodeTypeParallelGateway,
		},
	)
	s.Elements[NodeTypeUserTask] = &w2dStepHandler{nodeType: NodeTypeUserTask}

	branches, err := s.traverseBranches("split", "join")

	require.NoError(t, err)
	require.Len(t, branches, 3)
	assert.Equal(t, "a", branches[0].DeptID)
	assert.Equal(t, []dsl.ExecutionStep{{Sequential: []string{"a"}}}, branches[0].Steps)
	assert.Empty(t, branches[1].DeptID, "the edge straight to the join compiles no steps")
	assert.Empty(t, branches[1].Steps)
	assert.Equal(t, "b", branches[2].DeptID)
	assert.Equal(t, []dsl.ExecutionStep{{Sequential: []string{"b"}}}, branches[2].Steps)
}

func TestTraverseBranches_OnlyEmptyBranches_StillReturnsOneBranchPerEdge(t *testing.T) {
	s := w2dGraphState(
		map[string][]string{"split": {"join", "join"}},
		map[string]FlowNodeType{"split": NodeTypeParallelGateway, "join": NodeTypeParallelGateway},
	)

	branches, err := s.traverseBranches("split", "join")

	require.NoError(t, err)
	assert.Equal(t, []dsl.ParallelBranch{{}, {}}, branches)
}

func TestTraverseBranches_BranchError_ReturnsNoBranches(t *testing.T) {
	s := w2dGraphState(
		map[string][]string{"split": {"a", "bad"}, "a": {"join"}},
		map[string]FlowNodeType{
			"split": NodeTypeParallelGateway, "a": NodeTypeUserTask, "bad": NodeTypeSendTask, "join": NodeTypeParallelGateway,
		},
	)
	wantErr := errors.New("branch failed")
	s.Elements[NodeTypeUserTask] = &w2dStepHandler{nodeType: NodeTypeUserTask}
	s.Elements[NodeTypeSendTask] = &w2dStepHandler{nodeType: NodeTypeSendTask, err: wantErr}

	branches, err := s.traverseBranches("split", "join")

	require.ErrorIs(t, err, wantErr)
	assert.Nil(t, branches, "the branches built before the failing one are not returned")
}
