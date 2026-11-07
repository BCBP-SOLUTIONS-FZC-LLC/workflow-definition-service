package bpmncore

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type w2cRecordingHandler struct {
	nodeType FlowNodeType
	failOn   map[string]error
	visits   *[]string
}

func (h *w2cRecordingHandler) NodeType() FlowNodeType { return h.nodeType }

func (h *w2cRecordingHandler) Validate(
	string, *BPMNProcess, *Graph, *BPMNDefinitions,
	map[string]StageTypeHandler, map[FlowNodeType]ElementHandler,
) []domain.BPMNValidationError {
	return nil
}

func (h *w2cRecordingHandler) Compile(nodeID string, cs *CompileState) error {
	if h.visits != nil {
		*h.visits = append(*h.visits, nodeID)
	}
	if err := h.failOn[nodeID]; err != nil {
		return err
	}
	cs.AppendStep(dsl.ExecutionStep{Sequential: []string{nodeID}})
	return nil
}

func w2cNewState() *CompileState {
	g := &Graph{
		NodeType: map[string]FlowNodeType{},
		Outgoing: map[string][]string{},
		Incoming: map[string][]string{},
	}
	return NewCompileState(
		&BPMNProcess{},
		g,
		map[string]string{},
		map[[2]string]string{},
		map[[2]string]bool{},
		map[string]StageTypeHandler{},
		map[FlowNodeType]ElementHandler{},
		&BPMNDefinitions{},
	)
}

func w2cDeptIDs(s *CompileState) []string {
	ids := make([]string, 0, len(s.CollectedDepts()))
	for _, d := range s.CollectedDepts() {
		ids = append(ids, d.ID)
	}
	return ids
}

func TestNamedLaneDepartment_LaneAlreadyHasLaterSegments_ReturnsTheBaseLaneAndCreatesNothing(t *testing.T) {
	s := w2cNewState()
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"a", "b", "c"}}}

	for _, node := range []string{"a", "b", "c"} {
		s.AppendStage(s.SegmentOf(node), dsl.StageDef{NodeID: node})
		s.FlushSeqBuf()
	}
	require.Equal(t, []string{"Ops", "Ops~2", "Ops~3"}, w2cDeptIDs(s))

	got := s.NamedLaneDepartment("Ops", "other-iam")

	assert.Equal(t, "Ops", got)
	assert.Equal(t, "Ops", s.LaneDepartment("a"))
	assert.Equal(t, []string{"Ops", "Ops~2", "Ops~3"}, w2cDeptIDs(s))
	assert.Empty(t, s.CollectedDepts()[0].IAMDepartmentID)
}

func TestNamedLaneDepartment_FirstCallCreatesTheDepartmentAndLaterCallsKeepIt(t *testing.T) {
	s := w2cNewState()

	assert.Equal(t, "Eng", s.NamedLaneDepartment("Eng", "iam-1"))
	assert.Equal(t, "Eng", s.NamedLaneDepartment("Eng", "iam-2"))

	require.Len(t, s.CollectedDepts(), 1)
	assert.Equal(t, dsl.DepartmentDef{ID: "Eng", Label: "Eng", IAMDepartmentID: "iam-1"}, s.CollectedDepts()[0])
	assert.Equal(t, 1, s.sd.segments["Eng"])
}

func TestMarkNodeSegment_Placement(t *testing.T) {
	cases := []struct {
		name         string
		calls        [][2]string
		wantNodeSeg  string
		wantNodeKept bool
		wantPath     string
		wantPathSet  bool
	}{
		{name: "empty department is ignored", calls: [][2]string{{"n", ""}}},
		{name: "first call records both", calls: [][2]string{{"n", "D1"}}, wantNodeSeg: "D1", wantNodeKept: true, wantPath: "D1", wantPathSet: true},
		{name: "second call replaces only the path segment", calls: [][2]string{{"n", "D1"}, {"n", "D2"}}, wantNodeSeg: "D1", wantNodeKept: true, wantPath: "D2", wantPathSet: true},
		{name: "empty after a placement changes nothing", calls: [][2]string{{"n", "D1"}, {"n", ""}}, wantNodeSeg: "D1", wantNodeKept: true, wantPath: "D1", wantPathSet: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := w2cNewState()
			for _, c := range tc.calls {
				s.MarkNodeSegment(c[0], c[1])
			}
			seg, placed := s.sd.nodeSegment["n"]
			assert.Equal(t, tc.wantNodeKept, placed)
			assert.Equal(t, tc.wantNodeSeg, seg)
			path, onPath := s.pathSegment["n"]
			assert.Equal(t, tc.wantPathSet, onPath)
			assert.Equal(t, tc.wantPath, path)
		})
	}
}

func TestNewBranchState_StartsFreshAndSharesTheParentsCollections(t *testing.T) {
	parent := w2cNewState()
	parent.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"a"}}}
	parent.AppendStage(parent.SegmentOf("a"), dsl.StageDef{NodeID: "a"})
	parent.AddToSeqBuf("Ops")
	parent.AppendStep(dsl.ExecutionStep{Sequential: []string{"x"}})
	parent.visited["a"] = true
	parent.stopAtCompiled = true
	parent.rejoined = new(bool)
	parent.skipped["s"] = true
	require.Equal(t, "Ops", parent.openSegment)

	bs := NewBranchState(parent, "join")

	assert.Equal(t, "join", bs.StopAt)
	assert.Empty(t, bs.openLane)
	assert.Empty(t, bs.openSegment)
	assert.Zero(t, bs.SeqBufCount())
	assert.Zero(t, bs.StepsCount())
	assert.False(t, bs.IsVisited("a"))
	assert.True(t, bs.stopAtCompiled)
	assert.Same(t, parent.rejoined, bs.rejoined)
	assert.Same(t, parent.Called, bs.Called)
	assert.True(t, bs.skipped["s"])

	assert.Equal(t, "Ops", bs.pathSegment["a"])
	bs.pathSegment["b"] = "Ops~9"
	_, leaked := parent.pathSegment["b"]
	assert.False(t, leaked, "a branch's own path segments must not reach the parent")
	parent.pathSegment["c"] = "Ops~8"
	_, leaked = bs.pathSegment["c"]
	assert.False(t, leaked, "a parent's later path segments must not reach the branch")

	bs.segmentStarts["late"] = true
	assert.True(t, parent.segmentStarts["late"], "segment starts are shared")

	bs.EnsureDept("Qa", "Qa", "")
	bs.AppendStage("Qa", dsl.StageDef{NodeID: "q"})
	assert.Contains(t, w2cDeptIDs(parent), "Qa")
	assert.Equal(t, 2, parent.sd.stages)
	assert.Equal(t, 2, bs.sd.stages)
}

func TestShareSegmentsWith_AdoptsTheParentsStoreAndTheSubProcessLane(t *testing.T) {
	parent := w2cNewState()
	parent.Proc.LaneSet.Lanes = []BPMNLane{{
		Name:         "Ops",
		FlowNodeRefs: []string{"sp"},
		ExtensionElements: BPMNExtensionElements{
			ZeebeProps: BPMNZeebeProperties{Items: []ZeebeProperty{{Name: "dept_id", Value: "iam-ops"}}},
		},
	}}
	child := w2cNewState()

	child.ShareSegmentsWith(parent, "sp")

	assert.Same(t, parent.sd, child.sd)
	assert.Same(t, parent.Called, child.Called)
	assert.Equal(t, "Ops", child.defaultLane)
	assert.Equal(t, "iam-ops", child.defaultIAMDeptID)

	child.EnsureDept("Shared", "Shared", "")
	assert.Contains(t, w2cDeptIDs(parent), "Shared")
}

func TestShareSegmentsWith_SubProcessOutsideEveryLane_UsesTheParentsDefaultLane(t *testing.T) {
	parent := w2cNewState()
	parent.defaultLane, parent.defaultIAMDeptID = "Fallback", "iam-fb"
	child := w2cNewState()

	child.ShareSegmentsWith(parent, "unplaced")

	assert.Equal(t, "Fallback", child.defaultLane)
	assert.Equal(t, "iam-fb", child.defaultIAMDeptID)
}

func TestCompileWithImplicitStart_Failure_ReturnsNoPlanAndAPrefixedWrappedError(t *testing.T) {
	boom := errors.New("boom")
	stageChain := make([]BPMNUserTask, StageBudget+3)
	flows := []BPMNSequenceFlow{{SourceRef: "start", TargetRef: "u0"}}
	for i := range stageChain {
		stageChain[i] = BPMNUserTask{ID: fmt.Sprintf("u%d", i)}
		if i > 0 {
			flows = append(flows, BPMNSequenceFlow{SourceRef: fmt.Sprintf("u%d", i-1), TargetRef: fmt.Sprintf("u%d", i)})
		}
	}

	cases := []struct {
		name     string
		proc     *BPMNProcess
		implicit string
		elements func(*BPMNProcess) map[FlowNodeType]ElementHandler
		retype   map[string]FlowNodeType
		wantErr  error
		wantText string
	}{
		{
			name: "explicit start traversal fails",
			proc: &BPMNProcess{
				StartEvents:   []BPMNEvent{{ID: "start"}},
				UserTasks:     []BPMNUserTask{{ID: "t1"}},
				SequenceFlows: []BPMNSequenceFlow{{SourceRef: "start", TargetRef: "t1"}},
			},
			elements: func(*BPMNProcess) map[FlowNodeType]ElementHandler {
				return map[FlowNodeType]ElementHandler{NodeTypeUserTask: &w2cRecordingHandler{nodeType: NodeTypeUserTask, failOn: map[string]error{"t1": boom}}}
			},
			wantErr:  boom,
			wantText: "compile: boom",
		},
		{
			name:     "implicit start traversal fails",
			proc:     &BPMNProcess{ReceiveTasks: []BPMNReceiveTask{{ID: "rt1"}}},
			implicit: "rt1",
			elements: func(*BPMNProcess) map[FlowNodeType]ElementHandler {
				return map[FlowNodeType]ElementHandler{NodeTypeReceiveTask: &w2cRecordingHandler{nodeType: NodeTypeReceiveTask, failOn: map[string]error{"rt1": boom}}}
			},
			wantErr:  boom,
			wantText: "compile: boom",
		},
		{
			name: "boundary continuation fails",
			proc: &BPMNProcess{
				StartEvents:    []BPMNEvent{{ID: "start"}},
				UserTasks:      []BPMNUserTask{{ID: "t1"}},
				ReceiveTasks:   []BPMNReceiveTask{{ID: "esc"}},
				SequenceFlows:  []BPMNSequenceFlow{{SourceRef: "start", TargetRef: "t1"}, {SourceRef: "be1", TargetRef: "esc"}, {SourceRef: "esc", TargetRef: "t1"}},
				BoundaryEvents: []BPMNBoundaryEvent{{ID: "be1", AttachedToRef: "t1", Timer: &BPMNTimerDef{}}},
			},
			retype: map[string]FlowNodeType{"esc": NodeTypeUserTask},
			elements: func(*BPMNProcess) map[FlowNodeType]ElementHandler {
				return map[FlowNodeType]ElementHandler{NodeTypeUserTask: &w2cRecordingHandler{nodeType: NodeTypeUserTask, failOn: map[string]error{"esc": boom}}}
			},
			wantErr:  boom,
			wantText: `compile: boundary continuation from "be1": boom`,
		},
		{
			name:     "no explicit or implicit start event",
			proc:     &BPMNProcess{UserTasks: []BPMNUserTask{{ID: "t1"}}},
			elements: func(*BPMNProcess) map[FlowNodeType]ElementHandler { return map[FlowNodeType]ElementHandler{} },
			wantText: "compile: no start event",
		},
		{
			name: "plan over the stage budget",
			proc: &BPMNProcess{
				StartEvents:   []BPMNEvent{{ID: "start"}},
				UserTasks:     stageChain,
				SequenceFlows: flows,
			},
			elements: func(*BPMNProcess) map[FlowNodeType]ElementHandler {
				return map[FlowNodeType]ElementHandler{NodeTypeUserTask: &w2cStageChainHandler{}}
			},
			wantErr:  ErrStageBudget,
			wantText: "compile: " + ErrStageBudget.Error(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := BuildGraph(tc.proc)
			for id, nt := range tc.retype {
				g.NodeType[id] = nt
			}

			plan, called, err := CompileWithImplicitStart(tc.proc, g, tc.implicit, nil, tc.elements(tc.proc), &BPMNDefinitions{})

			require.Error(t, err)
			assert.Nil(t, plan)
			assert.Nil(t, called)
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
			}
			assert.Equal(t, tc.wantText, err.Error())
		})
	}
}

type w2cStageChainHandler struct{}

func (h *w2cStageChainHandler) NodeType() FlowNodeType { return NodeTypeUserTask }

func (h *w2cStageChainHandler) Validate(
	string, *BPMNProcess, *Graph, *BPMNDefinitions,
	map[string]StageTypeHandler, map[FlowNodeType]ElementHandler,
) []domain.BPMNValidationError {
	return nil
}

func (h *w2cStageChainHandler) Compile(nodeID string, cs *CompileState) error {
	cs.EnsureDept("d", "d", "")
	cs.AppendStage("d", dsl.StageDef{NodeID: nodeID})
	for _, next := range cs.ForwardNexts(nodeID) {
		if err := cs.TraverseNode(next); err != nil {
			return err
		}
	}
	return nil
}

func TestBuildStageDef_ConnectorPrefixWithoutAName_IsAnUnknownStageTypeNotAConnector(t *testing.T) {
	cases := []struct {
		name     string
		taskType string
		wantType string
		wantConn string
		wantNote bool
	}{
		{name: "bare prefix", taskType: "connector:", wantType: "connector:", wantNote: true},
		{name: "named connector", taskType: "connector:http", wantType: "connector", wantConn: "http"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := &BPMNUserTask{ID: "t1", ExtensionElements: BPMNExtensionElements{TaskDefinition: &ZeebeTaskDefinition{Type: tc.taskType}}}

			stage, err := BuildStageDef(task, map[string]StageTypeHandler{}, &BPMNProcess{}, &BPMNDefinitions{})

			require.NoError(t, err)
			assert.Equal(t, tc.wantType, stage.Type)
			assert.Equal(t, tc.wantConn, stage.ConnectorType)
			if tc.wantNote {
				assert.Equal(t, "connector:", stage.Activity)
				assert.Equal(t, `stage type "connector:" is not a defined class in the workflow engine`, stage.EngineNote)
				assert.Nil(t, stage.IOMapping)
			} else {
				assert.Empty(t, stage.EngineNote)
			}
		})
	}
}

func TestMarkReachable_Reachability(t *testing.T) {
	cases := []struct {
		name    string
		out     map[string][]string
		start   string
		visited map[string]bool
		want    map[string]bool
	}{
		{
			name:  "unreachable nodes stay unmarked",
			out:   map[string][]string{"a": {"b"}, "b": {"c"}, "x": {"y"}, "y": {"a"}},
			start: "a",
			want:  map[string]bool{"a": true, "b": true, "c": true},
		},
		{
			name:  "a cycle is marked once and terminates",
			out:   map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a"}},
			start: "b",
			want:  map[string]bool{"a": true, "b": true, "c": true},
		},
		{
			name:    "an already visited node blocks everything behind it",
			out:     map[string][]string{"a": {"b", "d"}, "b": {"c"}},
			start:   "a",
			visited: map[string]bool{"b": true},
			want:    map[string]bool{"a": true, "b": true, "d": true},
		},
		{
			name:    "an already visited start marks nothing new",
			out:     map[string][]string{"a": {"b"}},
			start:   "a",
			visited: map[string]bool{"a": true, "z": true},
			want:    map[string]bool{"a": true, "z": true},
		},
		{
			name:  "a start with no outgoing flows marks only itself",
			out:   map[string][]string{"a": {"b"}},
			start: "lone",
			want:  map[string]bool{"lone": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			visited := map[string]bool{}
			for k, v := range tc.visited {
				visited[k] = v
			}

			MarkReachable(tc.start, &Graph{Outgoing: tc.out}, visited)

			assert.Equal(t, tc.want, visited)
		})
	}
}

func TestFindElements_ReturnPointersIntoTheProcessSlices(t *testing.T) {
	cases := []struct {
		name    string
		known   string
		foreign []string
		rename  func(p *BPMNProcess, id string) bool
		read    func(p *BPMNProcess) []string
	}{
		{
			name: "user task", known: "u2", foreign: []string{"s2", "r2", "sp2", "c2", "missing"},
			rename: func(p *BPMNProcess, id string) bool {
				v := FindTask(p, id)
				if v == nil {
					return false
				}
				v.Name = "renamed"
				return true
			},
			read: func(p *BPMNProcess) []string { return []string{p.UserTasks[0].Name, p.UserTasks[1].Name} },
		},
		{
			name: "send task", known: "s2", foreign: []string{"u2", "r2", "sp2", "c2", "missing"},
			rename: func(p *BPMNProcess, id string) bool {
				v := FindSendTask(p, id)
				if v == nil {
					return false
				}
				v.Name = "renamed"
				return true
			},
			read: func(p *BPMNProcess) []string { return []string{p.SendTasks[0].Name, p.SendTasks[1].Name} },
		},
		{
			name: "receive task", known: "r2", foreign: []string{"u2", "s2", "sp2", "c2", "missing"},
			rename: func(p *BPMNProcess, id string) bool {
				v := FindReceiveTask(p, id)
				if v == nil {
					return false
				}
				v.Name = "renamed"
				return true
			},
			read: func(p *BPMNProcess) []string { return []string{p.ReceiveTasks[0].Name, p.ReceiveTasks[1].Name} },
		},
		{
			name: "sub process", known: "sp2", foreign: []string{"u2", "s2", "r2", "c2", "missing"},
			rename: func(p *BPMNProcess, id string) bool {
				v := FindSubProcess(p, id)
				if v == nil {
					return false
				}
				v.Name = "renamed"
				return true
			},
			read: func(p *BPMNProcess) []string { return []string{p.SubProcesses[0].Name, p.SubProcesses[1].Name} },
		},
		{
			name: "call activity", known: "c2", foreign: []string{"u2", "s2", "r2", "sp2", "missing"},
			rename: func(p *BPMNProcess, id string) bool {
				v := FindCallActivity(p, id)
				if v == nil {
					return false
				}
				v.Name = "renamed"
				return true
			},
			read: func(p *BPMNProcess) []string { return []string{p.CallActivities[0].Name, p.CallActivities[1].Name} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, id := range tc.foreign {
				p := w2cLookupProcess()
				assert.False(t, tc.rename(p, id), "id %q must not be found by this lookup", id)
				assert.Equal(t, []string{"first", "second"}, tc.read(p))
			}

			p := w2cLookupProcess()
			require.True(t, tc.rename(p, tc.known))
			assert.Equal(t, []string{"first", "renamed"}, tc.read(p), "the lookup must return the slice element, not a copy")
		})
	}
}

func w2cLookupProcess() *BPMNProcess {
	return &BPMNProcess{
		UserTasks:      []BPMNUserTask{{ID: "u1", Name: "first"}, {ID: "u2", Name: "second"}},
		SendTasks:      []BPMNSendTask{{ID: "s1", Name: "first"}, {ID: "s2", Name: "second"}},
		ReceiveTasks:   []BPMNReceiveTask{{ID: "r1", Name: "first"}, {ID: "r2", Name: "second"}},
		SubProcesses:   []BPMNSubProcess{{ID: "sp1", Name: "first"}, {ID: "sp2", Name: "second"}},
		CallActivities: []BPMNCallActivity{{ID: "c1", Name: "first"}, {ID: "c2", Name: "second"}},
	}
}

func TestEnsureDept_SecondCallWithOtherLabelAndIAMID_KeepsTheFirst(t *testing.T) {
	s := w2cNewState()

	s.EnsureDept("design", "Design", "iam-1")
	s.EnsureDept("design", "Renamed", "iam-2")
	s.EnsureDept("build", "Build", "")

	assert.Equal(t, []dsl.DepartmentDef{
		{ID: "design", Label: "Design", IAMDepartmentID: "iam-1"},
		{ID: "build", Label: "Build"},
	}, s.CollectedDepts())
}

func TestSeqBuf_DistinctDepartmentsFlushInFirstSeenOrderAndTheStepIsACopy(t *testing.T) {
	s := w2cNewState()
	for _, d := range []string{"c", "a", "c", "b", "a"} {
		s.AddToSeqBuf(d)
	}
	require.Equal(t, 3, s.SeqBufCount())

	s.FlushSeqBuf()
	s.AddToSeqBuf("x")
	s.AddToSeqBuf("y")
	s.FlushSeqBuf()

	steps := s.CollectedSteps()
	require.Len(t, steps, 2)
	assert.Equal(t, []string{"c", "a", "b"}, steps[0].Sequential)
	assert.Equal(t, []string{"x", "y"}, steps[1].Sequential)
	assert.Zero(t, s.SeqBufCount())
}

func TestFlushSeqBuf_EmptyBuffer_AppendsNoStepButClosesTheOpenSegment(t *testing.T) {
	s := w2cNewState()
	s.Proc.LaneSet.Lanes = []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"a", "b"}}}
	s.AppendStage(s.SegmentOf("a"), dsl.StageDef{NodeID: "a"})
	require.Equal(t, "Ops", s.openLane)
	require.Equal(t, "Ops", s.openSegment)

	s.FlushSeqBuf()

	assert.Zero(t, s.StepsCount())
	assert.Empty(t, s.openLane)
	assert.Empty(t, s.openSegment)
	assert.Equal(t, "Ops~2", s.SegmentOf("b"), "the next task of the same lane starts a new segment")
}

func TestForwardNexts_SkipsBackEdgesAndKeepsOutgoingOrder(t *testing.T) {
	s := w2cNewState()
	s.G.Outgoing["gw"] = []string{"back1", "f1", "f2", "back2"}
	s.backEdges = map[[2]string]bool{{"gw", "back1"}: true, {"gw", "back2"}: true, {"other", "f1"}: true}

	got := s.ForwardNexts("gw")

	assert.Equal(t, []string{"f1", "f2"}, got)
	assert.Equal(t, []string{"back1", "back2"}, s.backNexts("gw"))
	got[0] = "changed"
	assert.Equal(t, []string{"back1", "f1", "f2", "back2"}, s.G.Outgoing["gw"], "the result is a new slice")
}

func TestForwardNexts_NoForwardEdges_IsEmpty(t *testing.T) {
	s := w2cNewState()
	s.G.Outgoing["onlyback"] = []string{"b"}
	s.backEdges = map[[2]string]bool{{"onlyback", "b"}: true}

	assert.Empty(t, s.ForwardNexts("onlyback"))
	assert.Empty(t, s.ForwardNexts("noedges"))
	assert.Empty(t, s.ForwardNexts("unknown"))
}

func TestTraverseNode_StopAtCompiledMode(t *testing.T) {
	cases := []struct {
		name         string
		stopAtComp   bool
		compiledByMn bool
		nodeType     FlowNodeType
		withFlag     bool
		wantRejoined bool
		wantCompiled bool
	}{
		{name: "compiled task marks the path as rejoined", stopAtComp: true, compiledByMn: true, nodeType: NodeTypeUserTask, withFlag: true, wantRejoined: true},
		{name: "compiled end event does not", stopAtComp: true, compiledByMn: true, nodeType: NodeTypeEndEvent, withFlag: true},
		{name: "compiled task without a flag pointer is safe", stopAtComp: true, compiledByMn: true, nodeType: NodeTypeUserTask},
		{name: "uncompiled task is compiled normally", stopAtComp: true, nodeType: NodeTypeUserTask, withFlag: true, wantCompiled: true},
		{name: "compiled task outside the mode is compiled again", compiledByMn: true, nodeType: NodeTypeUserTask, withFlag: true, wantCompiled: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := w2cNewState()
			var visits []string
			s.G.NodeType["n"] = tc.nodeType
			s.Elements[tc.nodeType] = &w2cRecordingHandler{nodeType: tc.nodeType, visits: &visits}
			s.stopAtCompiled = tc.stopAtComp
			if tc.withFlag {
				s.rejoined = new(bool)
			}
			if tc.compiledByMn {
				s.sd.visited["n"] = true
			}

			require.NoError(t, s.TraverseNode("n"))

			if tc.withFlag {
				assert.Equal(t, tc.wantRejoined, *s.rejoined)
			}
			assert.Equal(t, tc.wantCompiled, len(visits) == 1)
			assert.Equal(t, tc.wantCompiled, s.IsVisited("n"))
			assert.Equal(t, tc.wantCompiled, s.StepsCount() == 1)
		})
	}
}

func TestTraverseNode_Budgets_RefuseOnlyBeyondTheLimit(t *testing.T) {
	cases := []struct {
		name    string
		stages  int
		nodes   int
		wantErr bool
	}{
		{name: "one stage over the budget", stages: StageBudget + 1, nodes: 0, wantErr: true},
		{name: "this node is the last one allowed", stages: 0, nodes: nodeBudget - 1},
		{name: "this node is one past the node budget", stages: 0, nodes: nodeBudget, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := w2cNewState()
			var visits []string
			s.G.NodeType["n"] = NodeTypeUserTask
			s.Elements[NodeTypeUserTask] = &w2cRecordingHandler{nodeType: NodeTypeUserTask, visits: &visits}
			s.sd.stages = tc.stages
			s.sd.nodes = tc.nodes

			err := s.TraverseNode("n")

			if tc.wantErr {
				assert.ErrorIs(t, err, ErrStageBudget)
				assert.Empty(t, visits)
				assert.False(t, s.IsVisited("n"))
				assert.False(t, s.sd.visited["n"])
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []string{"n"}, visits)
			assert.True(t, s.IsVisited("n"))
		})
	}
}

func TestTraverseNode_SkippedTask_EmitsNoStageAndContinuesToItsForwardNexts(t *testing.T) {
	proc := &BPMNProcess{
		UserTasks:     []BPMNUserTask{{ID: "t1"}, {ID: "t2"}},
		SequenceFlows: []BPMNSequenceFlow{{SourceRef: "t1", TargetRef: "t2"}},
		LaneSet: BPMNLaneSet{Lanes: []BPMNLane{{
			Name:         "Skip",
			FlowNodeRefs: []string{"t1"},
			ExtensionElements: BPMNExtensionElements{
				ZeebeProps: BPMNZeebeProperties{Items: []ZeebeProperty{{Name: "ignore", Value: "true"}}},
			},
		}, {Name: "Work", FlowNodeRefs: []string{"t2"}}}},
	}
	g := BuildGraph(proc)
	var visits []string
	elements := map[FlowNodeType]ElementHandler{NodeTypeUserTask: &w2cRecordingHandler{nodeType: NodeTypeUserTask, visits: &visits}}
	s := NewCompileState(proc, g, nil, nil, map[[2]string]bool{}, nil, elements, &BPMNDefinitions{})
	require.True(t, s.skipped["t1"])

	require.NoError(t, s.TraverseNode("t1"))

	assert.Equal(t, []string{"t2"}, visits, "only the next task is compiled")
	assert.True(t, s.IsVisited("t1"))
	assert.True(t, s.IsVisited("t2"))
	steps := s.CollectedSteps()
	require.Len(t, steps, 1)
	assert.Equal(t, []string{"t2"}, steps[0].Sequential)
}

func TestTraverseNode_NodeWithoutAHandler_PassesThroughAndStopsAtTheFirstFailingNext(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name       string
		failOn     map[string]error
		wantErr    error
		wantVisits []string
	}{
		{name: "all nexts compile in order", wantVisits: []string{"t1", "t2", "t3"}},
		{name: "first next fails so later nexts are never visited", failOn: map[string]error{"t1": boom}, wantErr: boom, wantVisits: []string{"t1"}},
		{name: "second next fails so the third is never visited", failOn: map[string]error{"t2": boom}, wantErr: boom, wantVisits: []string{"t1", "t2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := w2cNewState()
			var visits []string
			s.G.NodeType["gw"] = NodeTypeInclusiveGateway
			s.G.Outgoing["gw"] = []string{"t1", "t2", "t3"}
			for _, id := range []string{"t1", "t2", "t3"} {
				s.G.NodeType[id] = NodeTypeUserTask
			}
			s.Elements[NodeTypeUserTask] = &w2cRecordingHandler{nodeType: NodeTypeUserTask, failOn: tc.failOn, visits: &visits}

			err := s.TraverseNode("gw")

			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.wantVisits, visits)
			assert.Equal(t, slices.Contains(tc.wantVisits, "t3"), s.IsVisited("t3"))
		})
	}
}

func TestHandleGateway_ParallelSplitWithoutAJoin_FailsNamingTheGatewayAndAppendsNothing(t *testing.T) {
	s := w2cNewState()
	var visits []string
	s.G.NodeType["gw"] = NodeTypeParallelGateway
	s.G.Outgoing["gw"] = []string{"t1", "t2"}
	s.G.Incoming["t1"] = []string{"gw"}
	s.G.Incoming["t2"] = []string{"gw"}
	for _, id := range []string{"t1", "t2"} {
		s.G.NodeType[id] = NodeTypeUserTask
	}
	s.G.NodeType["e1"], s.G.NodeType["e2"] = NodeTypeEndEvent, NodeTypeEndEvent
	s.G.Outgoing["t1"], s.G.Outgoing["t2"] = []string{"e1"}, []string{"e2"}
	s.G.Incoming["e1"], s.G.Incoming["e2"] = []string{"t1"}, []string{"t2"}
	s.Elements[NodeTypeUserTask] = &w2cRecordingHandler{nodeType: NodeTypeUserTask, visits: &visits}

	err := s.HandleGateway("gw", ParallelGateway)

	require.Error(t, err)
	assert.Equal(t, `split gateway "gw" has no matching join`, err.Error())
	assert.Zero(t, s.StepsCount())
	assert.Empty(t, visits)
}

func TestHandleSplitNoJoin_ParallelKind_FailsNamingTheGatewayWithoutTraversingBranches(t *testing.T) {
	s := w2cNewState()
	var visits []string
	s.G.NodeType["t1"] = NodeTypeUserTask
	s.Elements[NodeTypeUserTask] = &w2cRecordingHandler{nodeType: NodeTypeUserTask, visits: &visits}

	err := s.handleSplitNoJoin("split7", []string{"t1", "t2"}, nil, ParallelGateway)

	require.Error(t, err)
	assert.Equal(t, `split gateway "split7" has no matching join`, err.Error())
	assert.Zero(t, s.StepsCount())
	assert.Empty(t, visits)
}
