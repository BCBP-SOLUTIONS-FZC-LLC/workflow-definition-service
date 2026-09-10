package bpmncore

import (
	"testing"
)

func TestFindSendTask_NotFound(t *testing.T) {
	proc := &BPMNProcess{}
	if FindSendTask(proc, "missing") != nil {
		t.Error("expected nil for empty process")
	}
	proc.SendTasks = []BPMNSendTask{{ID: "other"}}
	if FindSendTask(proc, "missing") != nil {
		t.Error("expected nil when ID not present")
	}
}

func TestFindReceiveTask_NotFound(t *testing.T) {
	proc := &BPMNProcess{}
	if FindReceiveTask(proc, "missing") != nil {
		t.Error("expected nil for empty process")
	}
	proc.ReceiveTasks = []BPMNReceiveTask{{ID: "other"}}
	if FindReceiveTask(proc, "missing") != nil {
		t.Error("expected nil when ID not present")
	}
}

func TestFindSubProcess_NotFound(t *testing.T) {
	proc := &BPMNProcess{}
	if FindSubProcess(proc, "missing") != nil {
		t.Error("expected nil for empty process")
	}
	proc.SubProcesses = []BPMNSubProcess{{ID: "other"}}
	if FindSubProcess(proc, "missing") != nil {
		t.Error("expected nil when ID not present")
	}
}

func TestFindCallActivity_NotFound(t *testing.T) {
	proc := &BPMNProcess{}
	if FindCallActivity(proc, "missing") != nil {
		t.Error("expected nil for empty process")
	}
	proc.CallActivities = []BPMNCallActivity{{ID: "other"}}
	if FindCallActivity(proc, "missing") != nil {
		t.Error("expected nil when ID not present")
	}
	if got := FindCallActivity(proc, "other"); got == nil || got.ID != "other" {
		t.Error("expected to find call activity by ID")
	}
}

func TestFindProcessByID_NotFound(t *testing.T) {
	defs := &BPMNDefinitions{}
	if FindProcessByID(defs, "missing") != nil {
		t.Error("expected nil for empty definitions")
	}
	defs.Processes = []BPMNProcess{{ID: "Proc_A"}}
	if FindProcessByID(defs, "missing") != nil {
		t.Error("expected nil when ID not present")
	}
	if got := FindProcessByID(defs, "Proc_A"); got == nil || got.ID != "Proc_A" {
		t.Error("expected to find process by ID")
	}
}

func TestFindProcessByID_ViaParticipant(t *testing.T) {
	defs := &BPMNDefinitions{
		Processes: []BPMNProcess{{ID: "Proc_A"}},
		Collaboration: &BPMNCollaboration{
			Participants: []BPMNParticipant{
				{ID: "P_a", ProcessRef: "Proc_A"},
				{ID: "P_dangling", ProcessRef: "Proc_missing"},
			},
		},
	}
	if got := FindProcessByID(defs, "P_a"); got == nil || got.ID != "Proc_A" {
		t.Errorf("expected to resolve process via participant ID; got %v", got)
	}
	if got := FindProcessByID(defs, "P_dangling"); got != nil {
		t.Errorf("expected nil when participant's processRef has no matching process; got %v", got)
	}
	if got := FindProcessByID(defs, "P_unknown"); got != nil {
		t.Errorf("expected nil when participant ID does not exist; got %v", got)
	}
}

func TestFirstTaskNodeAhead_ThroughCallActivity(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["ca1"] = NodeTypeCallActivity
	s.G.Outgoing["ca1"] = nil
	s.Proc.CallActivities = []BPMNCallActivity{{ID: "ca1", Name: "Prepare"}}

	id, name := s.firstTaskNodeAhead("ca1")
	if id != "ca1" {
		t.Errorf("id = %q; want %q", id, "ca1")
	}
	if name != "Prepare" {
		t.Errorf("name = %q; want %q", name, "Prepare")
	}
}

func TestOutgoingTargetOf_NotFound(t *testing.T) {
	proc := &BPMNProcess{}
	if got := OutgoingTargetOf("X", proc); got != "" {
		t.Errorf("expected empty string; got %q", got)
	}
	proc.SequenceFlows = []BPMNSequenceFlow{{SourceRef: "other", TargetRef: "T"}}
	if got := OutgoingTargetOf("X", proc); got != "" {
		t.Errorf("expected empty string; got %q", got)
	}
}

func TestFindImplicitStart_MultipleOrNone(t *testing.T) {
	// Zero candidates: process has only a start event (no implicit start)
	proc := &BPMNProcess{StartEvents: []BPMNEvent{{ID: "S"}}}
	g := &Graph{NodeType: map[string]FlowNodeType{"S": NodeTypeStartEvent}}
	if got := FindImplicitStart(proc, g); got != "" {
		t.Errorf("expected empty string when start events present; got %q", got)
	}

	// Multiple candidates: ambiguous → return ""
	proc2 := &BPMNProcess{}
	g2 := &Graph{
		NodeType: map[string]FlowNodeType{
			"A": NodeTypeUserTask,
			"B": NodeTypeUserTask,
		},
		Incoming: map[string][]string{"A": {}, "B": {}},
		Outgoing: map[string][]string{"A": {"end"}, "B": {"end"}},
	}
	if got := FindImplicitStart(proc2, g2); got != "" {
		t.Errorf("expected empty string for multiple candidates; got %q", got)
	}
}

func TestResolveMessageName_NilRef(t *testing.T) {
	defs := &BPMNDefinitions{}
	if got := ResolveMessageName("", defs); got != "" {
		t.Errorf("expected empty string for empty ref; got %q", got)
	}
	if got := ResolveMessageName("nonexistent", defs); got != "" {
		t.Errorf("expected empty string for missing message; got %q", got)
	}
}

func TestNodeMessageRef_NilGuards(t *testing.T) {
	if got := nodeMessageRef("", &BPMNDefinitions{}); got != "" {
		t.Errorf("expected empty string for empty nodeID; got %q", got)
	}
	if got := nodeMessageRef("X", nil); got != "" {
		t.Errorf("expected empty string for nil defs; got %q", got)
	}
}

func TestResolveMessageFlowName_OwnName(t *testing.T) {
	mf := &BPMNMessageFlow{ID: "MF_1", Name: "explicit-name", SourceRef: "A", TargetRef: "B"}
	if got := ResolveMessageFlowName(mf, &BPMNDefinitions{}); got != "explicit-name" {
		t.Errorf("expected flow's own name to win; got %q", got)
	}
}

func TestResolveMessageFlowTarget(t *testing.T) {
	defs := &BPMNDefinitions{
		Collaboration: &BPMNCollaboration{
			MessageFlows: []BPMNMessageFlow{
				{ID: "MF_other", SourceRef: "X", TargetRef: "Y"},
				{ID: "MF_1", Name: "resolved-via-flow", SourceRef: "A", TargetRef: "BE_1"},
			},
		},
	}
	if got := ResolveMessageFlowTarget("BE_1", defs); got != "resolved-via-flow" {
		t.Errorf("expected fallback resolution via message flow; got %q", got)
	}
	if got := ResolveMessageFlowTarget("BE_missing", defs); got != "" {
		t.Errorf("expected empty string when no flow targets the node; got %q", got)
	}
	if got := ResolveMessageFlowTarget("BE_1", &BPMNDefinitions{}); got != "" {
		t.Errorf("expected empty string when no collaboration; got %q", got)
	}
}

func TestSetDeptMeta(t *testing.T) {
	s := newMinimalState()

	// No-op when the department doesn't exist yet.
	s.SetDeptMeta("missing", true, map[string]string{"a": "b"})
	if len(s.CollectedDepts()) != 0 {
		t.Fatalf("expected no departments to be created; got %v", s.CollectedDepts())
	}

	s.EnsureDept("ops", "Ops", "")
	s.SetDeptMeta("ops", true, map[string]string{"ignore": "true", "sla": "24h"})

	depts := s.CollectedDepts()
	if len(depts) != 1 || depts[0].ID != "ops" {
		t.Fatalf("expected dept 'ops'; got %v", depts)
	}
	if !depts[0].Ignore {
		t.Error("expected Ignore = true")
	}
	if _, ok := depts[0].Props["ignore"]; ok {
		t.Error("the 'ignore' key itself should not be copied into Props")
	}
	if depts[0].Props["sla"] != "24h" {
		t.Errorf("Props[sla] = %q, want 24h", depts[0].Props["sla"])
	}
}

func TestResolveErrorCode_NotFound(t *testing.T) {
	s := &CompileState{
		Defs: &BPMNDefinitions{
			Errors: []BPMNError{{ID: "Err_other", ErrorCode: "OTHER"}},
		},
	}
	if got := s.ResolveErrorCode(""); got != "" {
		t.Errorf("empty errorRef: expected \"\"; got %q", got)
	}
	if got := s.ResolveErrorCode("nonexistent"); got != "" {
		t.Errorf("missing error: expected \"\"; got %q", got)
	}
	if got := s.ResolveErrorCode("Err_other"); got != "OTHER" {
		t.Errorf("found error: expected \"OTHER\"; got %q", got)
	}
}

func newMinimalState() *CompileState {
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

func TestTraverseNode_StopAt(t *testing.T) {
	s := newMinimalState()
	s.StopAt = "target"
	if err := s.TraverseNode("target"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.IsVisited("target") {
		t.Error("StopAt node must not be marked visited")
	}
}

func TestTraverseNode_ExternalParticipant_WithPool(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["pool_ext"] = NodeTypeExternalParticipant
	s.G.Outgoing["pool_ext"] = nil
	s.ExternalNodes["pool_ext"] = "ExternalOrg"

	if err := s.TraverseNode("pool_ext"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := s.CollectedSteps()
	if len(steps) != 1 || steps[0].CallPool == nil {
		t.Fatalf("expected one CallPool step; got %v", steps)
	}
	if steps[0].CallPool.Pool != "ExternalOrg" {
		t.Errorf("CallPool.Pool = %q; want %q", steps[0].CallPool.Pool, "ExternalOrg")
	}
}

func TestTraverseNode_ExternalParticipant_NoMapping(t *testing.T) {
	// ExternalParticipant node that has no entry in ExternalNodes — should not emit a step.
	s := newMinimalState()
	s.G.NodeType["pool_unmapped"] = NodeTypeExternalParticipant
	s.G.Outgoing["pool_unmapped"] = nil

	if err := s.TraverseNode("pool_unmapped"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.CollectedSteps()) != 0 {
		t.Error("expected no steps for unmapped external participant")
	}
}

func TestFindJoin_ReturnsOkFalseForMissingJoin(t *testing.T) {
	// A linear graph: gw → t1 → end — no reconverging join node, so FindJoin returns false.
	g := &Graph{
		NodeType: map[string]FlowNodeType{
			"gw":  NodeTypeExclusiveGateway,
			"t1":  NodeTypeUserTask,
			"end": NodeTypeEndEvent,
		},
		Outgoing: map[string][]string{
			"gw": {"t1"},
			"t1": {"end"},
		},
		Incoming: map[string][]string{
			"t1":  {"gw"},
			"end": {"t1"},
		},
	}
	_, ok := FindJoin("gw", g, nil)
	if ok {
		t.Error("expected ok=false when no exclusive-gateway join node exists")
	}
}

func TestReachesEndEvent_ReturnsFalse(t *testing.T) {
	// A graph with no end event reachable from "start" — reachesEndEvent must return false.
	g := &Graph{
		NodeType: map[string]FlowNodeType{
			"start": NodeTypeUserTask,
			"loop":  NodeTypeUserTask,
		},
		Outgoing: map[string][]string{
			"start": {"loop"},
			"loop":  {},
		},
	}
	got := reachesEndEvent(g, nil, "start", "")
	if got {
		t.Error("expected false when no end event is reachable")
	}
}

func TestResolveUserTaskDept_NilTask(t *testing.T) {
	s := newMinimalState()
	// Call with a node ID that does not exist in the (empty) process.
	dept, stageType := s.resolveUserTaskDept("nonexistent")
	if dept != "" || stageType != "" {
		t.Errorf("expected (\"\", \"\"); got (%q, %q)", dept, stageType)
	}
}

func TestTraverseNode_ExternalParticipant_WithSuccessor(t *testing.T) {
	// ExternalParticipant with a successor — exercises the for-loop in the ext-participant block.
	s := newMinimalState()
	s.G.NodeType["pool_ext"] = NodeTypeExternalParticipant
	s.G.NodeType["end"] = NodeTypeEndEvent
	s.G.Outgoing["pool_ext"] = []string{"end"}
	s.ExternalNodes["pool_ext"] = "Partner"

	if err := s.TraverseNode("pool_ext"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.CollectedSteps()) != 1 {
		t.Fatalf("expected 1 step; got %d", len(s.CollectedSteps()))
	}
}

func TestFirstTaskNodeAhead_ThroughGateway(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["gw"] = NodeTypeExclusiveGateway
	s.G.NodeType["task1"] = NodeTypeUserTask
	s.G.Outgoing["gw"] = []string{"task1"}
	s.G.Outgoing["task1"] = nil
	s.Proc.UserTasks = []BPMNUserTask{{ID: "task1", Name: "Approval"}}

	id, name := s.firstTaskNodeAhead("gw")
	if id != "task1" {
		t.Errorf("id = %q; want %q", id, "task1")
	}
	if name != "Approval" {
		t.Errorf("name = %q; want %q", name, "Approval")
	}
}

func TestFirstTaskNodeAhead_SendReceiveSubProcess(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["send1"] = NodeTypeSendTask
	s.G.NodeType["recv1"] = NodeTypeReceiveTask
	s.G.NodeType["sp1"] = NodeTypeSubProcess
	s.Proc.SendTasks = []BPMNSendTask{{ID: "send1", Name: "Notify"}}
	s.Proc.ReceiveTasks = []BPMNReceiveTask{{ID: "recv1", Name: "Await"}}
	s.Proc.SubProcesses = []BPMNSubProcess{{ID: "sp1", Name: "Sub"}}

	if id, name := s.firstTaskNodeAhead("send1"); id != "send1" || name != "Notify" {
		t.Errorf("send task: got (%q, %q)", id, name)
	}
	if id, name := s.firstTaskNodeAhead("recv1"); id != "recv1" || name != "Await" {
		t.Errorf("receive task: got (%q, %q)", id, name)
	}
	if id, name := s.firstTaskNodeAhead("sp1"); id != "sp1" || name != "Sub" {
		t.Errorf("sub process: got (%q, %q)", id, name)
	}
}

func TestFirstTaskNodeAhead_DeadEndAndCycle(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["deadend"] = NodeTypeExclusiveGateway
	s.G.Outgoing["deadend"] = nil
	if id, name := s.firstTaskNodeAhead("deadend"); id != "" || name != "" {
		t.Errorf("dead end: got (%q, %q); want (\"\", \"\")", id, name)
	}

	s.G.NodeType["loop"] = NodeTypeExclusiveGateway
	s.G.Outgoing["loop"] = []string{"loop"}
	if id, name := s.firstTaskNodeAhead("loop"); id != "" || name != "" {
		t.Errorf("self-loop: got (%q, %q); want (\"\", \"\")", id, name)
	}
}

func TestFindImplicitStart_SingleCandidate(t *testing.T) {
	proc := &BPMNProcess{}
	g := &Graph{
		NodeType: map[string]FlowNodeType{
			"A":   NodeTypeUserTask,
			"end": NodeTypeEndEvent,
		},
		Incoming: map[string][]string{"A": {}, "end": {"A"}},
		Outgoing: map[string][]string{"A": {"end"}, "end": {}},
	}
	got := FindImplicitStart(proc, g)
	if got != "A" {
		t.Errorf("expected \"A\"; got %q", got)
	}
}

func TestBuildGraph_AllNodeTypes(t *testing.T) {
	proc := &BPMNProcess{
		StartEvents:       []BPMNEvent{{ID: "start"}},
		EndEvents:         []BPMNEvent{{ID: "end"}},
		UserTasks:         []BPMNUserTask{{ID: "ut1"}},
		SendTasks:         []BPMNSendTask{{ID: "st1"}},
		ReceiveTasks:      []BPMNReceiveTask{{ID: "rt1"}},
		ParallelGateways:  []BPMNGateway{{ID: "pg1"}},
		ExclusiveGateways: []BPMNGateway{{ID: "eg1"}},
		InclusiveGateways: []BPMNGateway{{ID: "ig1"}},
		SubProcesses:      []BPMNSubProcess{{ID: "sp1"}},
		CallActivities:    []BPMNCallActivity{{ID: "ca1"}},
		BoundaryEvents: []BPMNBoundaryEvent{
			{ID: "be_timer", Timer: &BPMNTimerDef{}},
			{ID: "be_error", Error: &BPMNErrorDef{}},
			{ID: "be_message", Message: &BPMNMessageEventDef{}},
			{ID: "be_default"},
		},
		SequenceFlows: []BPMNSequenceFlow{{SourceRef: "start", TargetRef: "ut1"}},
	}
	g := BuildGraph(proc)

	want := map[string]FlowNodeType{
		"start":      NodeTypeStartEvent,
		"end":        NodeTypeEndEvent,
		"ut1":        NodeTypeUserTask,
		"st1":        NodeTypeSendTask,
		"rt1":        NodeTypeReceiveTask,
		"pg1":        NodeTypeParallelGateway,
		"eg1":        NodeTypeExclusiveGateway,
		"ig1":        NodeTypeInclusiveGateway,
		"sp1":        NodeTypeSubProcess,
		"ca1":        NodeTypeCallActivity,
		"be_timer":   NodeTypeTimerBoundaryEvent,
		"be_error":   NodeTypeErrorBoundaryEvent,
		"be_message": NodeTypeMessageBoundaryEvent,
		"be_default": NodeTypeBoundaryEvent,
	}
	for id, kind := range want {
		if g.NodeType[id] != kind {
			t.Errorf("NodeType[%q] = %v; want %v", id, g.NodeType[id], kind)
		}
		if _, ok := g.NodeIDs[id]; !ok {
			t.Errorf("NodeIDs missing %q", id)
		}
	}
	if len(g.Outgoing["start"]) != 1 || g.Outgoing["start"][0] != "ut1" {
		t.Errorf("Outgoing[start] = %v", g.Outgoing["start"])
	}
	if len(g.Incoming["ut1"]) != 1 || g.Incoming["ut1"][0] != "start" {
		t.Errorf("Incoming[ut1] = %v", g.Incoming["ut1"])
	}
}

func TestBuildLaneLabels(t *testing.T) {
	proc := &BPMNProcess{LaneSet: BPMNLaneSet{Lanes: []BPMNLane{{Name: "Ops"}, {Name: "Finance"}}}}
	labels := BuildLaneLabels(proc)
	if labels["Ops"] != "Ops" || labels["Finance"] != "Finance" {
		t.Errorf("unexpected labels: %v", labels)
	}
}

func TestBuildCondExprs(t *testing.T) {
	proc := &BPMNProcess{SequenceFlows: []BPMNSequenceFlow{
		{SourceRef: "a", TargetRef: "b", ConditionExpression: "x > 1"},
		{SourceRef: "a", TargetRef: "c"}, // no condition -> excluded
	}}
	exprs := BuildCondExprs(proc)
	if len(exprs) != 1 {
		t.Fatalf("expected 1 entry; got %d", len(exprs))
	}
	if exprs[[2]string{"a", "b"}] != "x > 1" {
		t.Errorf("got %q", exprs[[2]string{"a", "b"}])
	}
}

func TestForwardReaches(t *testing.T) {
	g := &Graph{Outgoing: map[string][]string{"a": {"b"}, "b": {"c"}}}
	if !ForwardReaches("a", "c", g, nil) {
		t.Error("expected c to be reachable from a")
	}
	if ForwardReaches("a", "z", g, nil) {
		t.Error("expected z to be unreachable")
	}
}

func TestForwardReaches_BackEdgeExcluded(t *testing.T) {
	g := &Graph{Outgoing: map[string][]string{"a": {"b"}}}
	backEdges := map[[2]string]bool{{"a", "b"}: true}
	if ForwardReaches("a", "b", g, backEdges) {
		t.Error("expected back edge to be excluded from reachability")
	}
}

func TestBuildLaneRefSet(t *testing.T) {
	proc := &BPMNProcess{LaneSet: BPMNLaneSet{Lanes: []BPMNLane{
		{Name: "Ops", FlowNodeRefs: []string{"t1", "t2"}},
	}}}
	refs := BuildLaneRefSet(proc)
	if _, ok := refs["t1"]; !ok {
		t.Error("expected t1 in ref set")
	}
	if _, ok := refs["t3"]; ok {
		t.Error("did not expect t3 in ref set")
	}
}

func TestLaneNameFor(t *testing.T) {
	proc := &BPMNProcess{LaneSet: BPMNLaneSet{Lanes: []BPMNLane{
		{Name: "Ops", FlowNodeRefs: []string{"t1"}},
	}}}
	if got := LaneNameFor("t1", proc); got != "Ops" {
		t.Errorf("got %q; want Ops", got)
	}
	if got := LaneNameFor("unknown", proc); got != "" {
		t.Errorf("got %q; want empty", got)
	}
}

func TestOutgoingTargetOf_Found(t *testing.T) {
	proc := &BPMNProcess{SequenceFlows: []BPMNSequenceFlow{{SourceRef: "a", TargetRef: "b"}}}
	if got := OutgoingTargetOf("a", proc); got != "b" {
		t.Errorf("got %q; want b", got)
	}
}

func TestInjectMessageBridges_RoundTripAndTerminal(t *testing.T) {
	g := &Graph{
		Outgoing: make(map[string][]string),
		Incoming: make(map[string][]string),
		NodeType: make(map[string]FlowNodeType),
		NodeIDs:  make(map[string]struct{}),
	}
	bridges := []MessageBridge{
		{FromTaskID: "send1", ToTaskID: "recv1", SyntheticNodeID: "syn1"},
		{FromTaskID: "send2", ToTaskID: "", SyntheticNodeID: "syn2"},
	}
	InjectMessageBridges(g, bridges)

	if g.NodeType["syn1"] != NodeTypeExternalParticipant {
		t.Errorf("syn1 NodeType = %v; want ExternalParticipant", g.NodeType["syn1"])
	}
	if len(g.Outgoing["send1"]) != 1 || g.Outgoing["send1"][0] != "syn1" {
		t.Errorf("Outgoing[send1] = %v", g.Outgoing["send1"])
	}
	if len(g.Incoming["syn1"]) != 1 || g.Incoming["syn1"][0] != "send1" {
		t.Errorf("Incoming[syn1] = %v", g.Incoming["syn1"])
	}
	if len(g.Outgoing["syn1"]) != 1 || g.Outgoing["syn1"][0] != "recv1" {
		t.Errorf("Outgoing[syn1] = %v", g.Outgoing["syn1"])
	}
	if len(g.Incoming["recv1"]) != 1 || g.Incoming["recv1"][0] != "syn1" {
		t.Errorf("Incoming[recv1] = %v", g.Incoming["recv1"])
	}

	if g.NodeType["syn2"] != NodeTypeEndEvent {
		t.Errorf("syn2 NodeType = %v; want EndEvent", g.NodeType["syn2"])
	}
	if len(g.Outgoing["syn2"]) != 0 {
		t.Errorf("expected no outgoing edges from terminal synthetic node; got %v", g.Outgoing["syn2"])
	}
	if len(g.Incoming[""]) != 0 {
		t.Errorf("expected no incoming edges registered on empty ToTaskID; got %v", g.Incoming[""])
	}
}

func TestTimerBoundaryFor(t *testing.T) {
	proc := &BPMNProcess{BoundaryEvents: []BPMNBoundaryEvent{
		{ID: "be1", AttachedToRef: "t1", Timer: &BPMNTimerDef{Duration: "PT1H"}},
		{ID: "be2", AttachedToRef: "t2", Message: &BPMNMessageEventDef{}},
	}}
	if got := TimerBoundaryFor("t1", proc); got == nil || got.ID != "be1" {
		t.Errorf("expected to find be1; got %v", got)
	}
	if got := TimerBoundaryFor("t2", proc); got != nil {
		t.Errorf("expected nil for non-timer boundary; got %v", got)
	}
	if got := TimerBoundaryFor("unknown", proc); got != nil {
		t.Errorf("expected nil for unmatched task; got %v", got)
	}
}

func TestMessageBoundaryFor(t *testing.T) {
	proc := &BPMNProcess{BoundaryEvents: []BPMNBoundaryEvent{
		{ID: "be1", AttachedToRef: "t1", Message: &BPMNMessageEventDef{}},
		{ID: "be2", AttachedToRef: "t2", Timer: &BPMNTimerDef{}},
	}}
	if got := MessageBoundaryFor("t1", proc); got == nil || got.ID != "be1" {
		t.Errorf("expected to find be1; got %v", got)
	}
	if got := MessageBoundaryFor("t2", proc); got != nil {
		t.Errorf("expected nil for non-message boundary; got %v", got)
	}
}

func TestFindJoin_BackEdgeExcludedFromSplitCount(t *testing.T) {
	// gw has 3 outgoing edges; one (gw->loop) is a back edge and must not count
	// toward splitOut, so the join at 2 incoming edges is still recognised.
	g := &Graph{
		NodeType: map[string]FlowNodeType{
			"gw":   NodeTypeExclusiveGateway,
			"a":    NodeTypeUserTask,
			"b":    NodeTypeUserTask,
			"loop": NodeTypeUserTask,
			"join": NodeTypeExclusiveGateway,
		},
		Outgoing: map[string][]string{
			"gw":   {"a", "b", "loop"},
			"a":    {"join"},
			"b":    {"join"},
			"loop": {"gw"},
		},
		Incoming: map[string][]string{
			"join": {"a", "b"},
		},
	}
	backEdges := map[[2]string]bool{{"gw", "loop"}: true}
	got, ok := FindJoin("gw", g, backEdges)
	if !ok || got != "join" {
		t.Fatalf("expected join to be found; got (%q, %v)", got, ok)
	}
}

func TestFindImplicitStart_SkipsBoundaryAndEnd(t *testing.T) {
	// Graph with a boundary event and end event — both must be skipped as candidates.
	// Only the user task qualifies.
	proc := &BPMNProcess{}
	g := &Graph{
		NodeType: map[string]FlowNodeType{
			"task": NodeTypeUserTask,
			"be":   NodeTypeBoundaryEvent,
			"end":  NodeTypeEndEvent,
		},
		Incoming: map[string][]string{"task": {}, "be": {}, "end": {"task"}},
		Outgoing: map[string][]string{"task": {"end"}, "be": {}, "end": {}},
	}
	got := FindImplicitStart(proc, g)
	if got != "task" {
		t.Errorf("expected \"task\" (boundary/end must be skipped); got %q", got)
	}
}
