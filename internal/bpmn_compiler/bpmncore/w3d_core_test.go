package bpmncore

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func w3dCalls(ids ...string) []BPMNCallActivity {
	out := make([]BPMNCallActivity, 0, len(ids))
	for _, id := range ids {
		ca := BPMNCallActivity{ID: "call_" + id}
		if id != "" {
			ca.ExtensionElements.ZeebeCalledElement = &ZeebeCalledElement{ProcessID: id}
		}
		out = append(out, ca)
	}
	return out
}

func w3dProcessIDs(procs []*BPMNProcess) []string {
	ids := make([]string, 0, len(procs))
	for _, p := range procs {
		ids = append(ids, p.ID)
	}
	return ids
}

func w3dProps(kv ...string) BPMNExtensionElements {
	var items []ZeebeProperty
	for i := 0; i+1 < len(kv); i += 2 {
		items = append(items, ZeebeProperty{Name: kv[i], Value: kv[i+1]})
	}
	return BPMNExtensionElements{ZeebeProps: BPMNZeebeProperties{Items: items}}
}

func TestRunningProcesses_CallGraph_ReturnsReachableProcessesBreadthFirst(t *testing.T) {
	cases := []struct {
		name string
		defs *BPMNDefinitions
		want []string
	}{
		{
			name: "breadth first order with repeats and a call cycle",
			defs: &BPMNDefinitions{Processes: []BPMNProcess{
				{ID: "R", CallActivities: w3dCalls("A", "B")},
				{ID: "A", CallActivities: w3dCalls("C", "B")},
				{ID: "B", CallActivities: w3dCalls("D")},
				{ID: "C", CallActivities: w3dCalls("R")},
				{ID: "D"},
				{ID: "Z"},
			}},
			want: []string{"R", "A", "B", "C", "D"},
		},
		{
			name: "same process called twice by the root is returned once",
			defs: &BPMNDefinitions{Processes: []BPMNProcess{
				{ID: "R", CallActivities: w3dCalls("A", "A")},
				{ID: "A"},
			}},
			want: []string{"R", "A"},
		},
		{
			name: "unknown called id and call activity without calledElement are skipped",
			defs: &BPMNDefinitions{Processes: []BPMNProcess{
				{ID: "R", CallActivities: w3dCalls("missing", "", "A")},
				{ID: "A"},
			}},
			want: []string{"R", "A"},
		},
		{
			name: "root calling itself returns only the root",
			defs: &BPMNDefinitions{Processes: []BPMNProcess{
				{ID: "R", CallActivities: w3dCalls("R")},
			}},
			want: []string{"R"},
		},
		{
			name: "root without call activities returns only the root",
			defs: &BPMNDefinitions{Processes: []BPMNProcess{{ID: "R"}, {ID: "A"}}},
			want: []string{"R"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := &tc.defs.Processes[0]

			got := RunningProcesses(tc.defs, root)

			assert.Equal(t, tc.want, w3dProcessIDs(got))
			assert.Same(t, root, got[0])
		})
	}
}

func TestRunningProcesses_CalledProcessIsTheDefinitionsEntry(t *testing.T) {
	defs := &BPMNDefinitions{Processes: []BPMNProcess{
		{ID: "R", CallActivities: w3dCalls("A")},
		{ID: "A"},
	}}

	got := RunningProcesses(defs, &defs.Processes[0])

	require.Len(t, got, 2)
	assert.Same(t, &defs.Processes[1], got[1])
}

func TestCalledPlans_Claim_IsTrueOnlyTheFirstTime(t *testing.T) {
	c := NewCalledPlans()

	assert.True(t, c.Claim("mod"))
	assert.False(t, c.Claim("mod"))
	assert.True(t, c.Claim("other"))
	assert.False(t, c.Claim("other"))
}

func TestCalledPlans_Claim_IsFalseForAnIdAlreadyHoldingAPlan(t *testing.T) {
	c := NewCalledPlans()
	c.Put(&dsl.CompiledPlan{Name: "mod"})

	assert.False(t, c.Claim("mod"))
}

func TestCalledPlans_All_SkipsClaimedPlaceholdersAndSortsByName(t *testing.T) {
	c := NewCalledPlans()
	require.True(t, c.Claim("pending"))
	c.Put(&dsl.CompiledPlan{Name: "zeta"})
	c.Put(&dsl.CompiledPlan{Name: "alpha"})
	require.True(t, c.Claim("beta"))
	c.Put(&dsl.CompiledPlan{Name: "beta"})
	c.Put(&dsl.CompiledPlan{Name: "Mid"})

	all := c.All()

	names := make([]string, 0, len(all))
	for _, p := range all {
		names = append(names, p.Name)
	}
	assert.Equal(t, []string{"Mid", "alpha", "beta", "zeta"}, names)
	assert.Nil(t, c.Get("pending"))
	assert.Same(t, all[3], c.Get("zeta"))
}

func TestCalledPlans_All_NothingPut_IsEmpty(t *testing.T) {
	c := NewCalledPlans()
	require.True(t, c.Claim("pending"))

	assert.Empty(t, c.All())
}

func TestCompile_NoStartEventAndNoUniqueEntryNode_FailsWithoutAPlan(t *testing.T) {
	cases := []struct {
		name string
		proc *BPMNProcess
	}{
		{"empty process has no entry node", &BPMNProcess{}},
		{"two entry nodes are ambiguous", &BPMNProcess{
			UserTasks:     []BPMNUserTask{{ID: "a"}, {ID: "b"}},
			EndEvents:     []BPMNEvent{{ID: "end"}},
			SequenceFlows: []BPMNSequenceFlow{{SourceRef: "a", TargetRef: "end"}, {SourceRef: "b", TargetRef: "end"}},
		}},
		{"only an isolated node and an end event", &BPMNProcess{
			UserTasks: []BPMNUserTask{{ID: "a"}},
			EndEvents: []BPMNEvent{{ID: "end"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := BuildGraph(tc.proc)

			plan, called, err := Compile(tc.proc, g, nil, nil, &BPMNDefinitions{})

			require.EqualError(t, err, "compile: no start event")
			assert.Nil(t, plan)
			assert.Nil(t, called)
		})
	}
}

func TestCompileWithImplicitStart_NoStartEventAndEmptyImplicit_FailsWithoutAPlan(t *testing.T) {
	proc := &BPMNProcess{UserTasks: []BPMNUserTask{{ID: "a"}}}
	g := BuildGraph(proc)

	plan, called, err := CompileWithImplicitStart(proc, g, "", nil, nil, &BPMNDefinitions{})

	require.EqualError(t, err, "compile: no start event")
	assert.Nil(t, plan)
	assert.Nil(t, called)
}

func TestCompileWithImplicitStart_DataStoreReferences_BecomeVisualElementsInOrder(t *testing.T) {
	proc := &BPMNProcess{
		StartEvents: []BPMNEvent{{ID: "start"}},
		DataStoreRefs: []BPMNDataStoreRef{
			{ID: "ds2", Name: "Ledger"},
			{ID: "ds1", Name: "Orders"},
			{ID: "ds3"},
		},
	}
	g := BuildGraph(proc)

	plan, _, err := CompileWithImplicitStart(proc, g, "", nil, nil, &BPMNDefinitions{})

	require.NoError(t, err)
	assert.Equal(t, []dsl.VisualElementDef{
		{Kind: "dataStoreReference", ID: "ds2", Name: "Ledger"},
		{Kind: "dataStoreReference", ID: "ds1", Name: "Orders"},
		{Kind: "dataStoreReference", ID: "ds3", Name: ""},
	}, plan.VisualElements)
}

func TestCompileWithImplicitStart_NoDataStoreReferences_HasNoVisualElements(t *testing.T) {
	proc := &BPMNProcess{StartEvents: []BPMNEvent{{ID: "start"}}}
	g := BuildGraph(proc)

	plan, _, err := CompileWithImplicitStart(proc, g, "", nil, nil, &BPMNDefinitions{})

	require.NoError(t, err)
	assert.Empty(t, plan.VisualElements)
}

func TestBuildStageDef_NoProperties_HasNilExtras(t *testing.T) {
	cases := []struct {
		name string
		ext  BPMNExtensionElements
	}{
		{"no properties element", BPMNExtensionElements{}},
		{"empty properties list", BPMNExtensionElements{ZeebeProps: BPMNZeebeProperties{Items: []ZeebeProperty{}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			task := &BPMNUserTask{ID: "t1", ExtensionElements: tc.ext}

			stage, err := BuildStageDef(task, map[string]StageTypeHandler{}, &BPMNProcess{}, &BPMNDefinitions{})

			require.NoError(t, err)
			assert.Nil(t, stage.Extras)
		})
	}
}

func TestBuildStageDef_ConnectorTask_IgnoresAssignmentAndFallsBackToConnectorTypeForActivity(t *testing.T) {
	cases := []struct {
		name         string
		taskName     string
		wantActivity string
	}{
		{"unnamed task uses the connector type", "", "http-call"},
		{"named task uses its name", "Call the API", "Call the API"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ext := w3dProps("timeout", "5s")
			ext.TaskDefinition = &ZeebeTaskDefinition{Type: ConnectorTaskDefPrefix + "http-call"}
			ext.AssignmentDefinition = &ZeebeAssignmentDefinition{CandidateGroups: "ops", CandidateUsers: "alice"}
			ext.ZeebeUserTask = &struct{}{}
			ext.IOMapping = &ZeebeIOMapping{
				Inputs:  []ZeebeIOVar{{Source: "=a", Target: "x"}, {Source: "=b", Target: "y"}},
				Outputs: []ZeebeIOVar{{Source: "=r", Target: "res"}},
			}
			task := &BPMNUserTask{ID: "svc1", Name: tc.taskName, ExtensionElements: ext}

			stage, err := BuildStageDef(task, map[string]StageTypeHandler{}, &BPMNProcess{}, &BPMNDefinitions{})

			require.NoError(t, err)
			assert.Equal(t, "connector", stage.Type)
			assert.Equal(t, "http-call", stage.ConnectorType)
			assert.Equal(t, tc.wantActivity, stage.Activity)
			assert.Equal(t, tc.taskName, stage.Name)
			assert.Equal(t, "svc1", stage.NodeID)
			assert.Empty(t, stage.Role)
			assert.Nil(t, stage.DefaultAssignees)
			assert.False(t, stage.IsZeebeUserTask)
			assert.Empty(t, stage.EngineNote)
			assert.Equal(t, map[string]string{"timeout": "5s"}, stage.Extras)
			require.NotNil(t, stage.IOMapping)
			assert.Equal(t, []dsl.IOVar{{Source: "=a", Target: "x"}, {Source: "=b", Target: "y"}}, stage.IOMapping.Inputs)
			assert.Equal(t, []dsl.IOVar{{Source: "=r", Target: "res"}}, stage.IOMapping.Outputs)
		})
	}
}

func TestBuildMessageStageDef_MessageNameOverridesAPropertyNamedMessage(t *testing.T) {
	cases := []struct {
		name        string
		messageName string
		want        map[string]string
	}{
		{"a message name replaces the property", "OrderPlaced", map[string]string{"message": "OrderPlaced", "k": "v"}},
		{"an empty message name leaves the property", "", map[string]string{"message": "fromProp", "k": "v"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ext := w3dProps("message", "fromProp", "k", "v")

			stage := BuildMessageStageDef("st1", "Notify", "send_task", tc.messageName, ext)

			assert.Equal(t, tc.want, stage.Extras)
		})
	}
}

func TestResolveMessageName_UnresolvableInputs_ReturnEmpty(t *testing.T) {
	defs := &BPMNDefinitions{Messages: []BPMNMessage{{ID: "Msg_1", Name: "OrderPlaced"}, {ID: "Msg_blank"}}}
	cases := []struct {
		name string
		ref  string
		defs *BPMNDefinitions
		want string
	}{
		{"nil definitions with a ref", "Msg_1", nil, ""},
		{"nil definitions with no ref", "", nil, ""},
		{"empty ref", "", defs, ""},
		{"unknown ref", "Msg_unknown", defs, ""},
		{"known ref with an unnamed message", "Msg_blank", defs, ""},
		{"known ref", "Msg_1", defs, "OrderPlaced"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ResolveMessageName(tc.ref, tc.defs))
		})
	}
}

func TestResolveMessageName_DuplicateMessageIds_FirstWins(t *testing.T) {
	defs := &BPMNDefinitions{Messages: []BPMNMessage{{ID: "Msg_1", Name: "First"}, {ID: "Msg_1", Name: "Second"}}}

	assert.Equal(t, "First", ResolveMessageName("Msg_1", defs))
}

func TestResolveMessageFlowName_SeveralSourcesApply_FollowsPrecedence(t *testing.T) {
	defs := &BPMNDefinitions{
		Messages: []BPMNMessage{
			{ID: "M_flow", Name: "FromFlowRef"},
			{ID: "M_target", Name: "FromTarget"},
			{ID: "M_source", Name: "FromSource"},
			{ID: "M_boundary", Name: "FromBoundary"},
		},
		Processes: []BPMNProcess{{
			SendTasks:    []BPMNSendTask{{ID: "send", MessageRef: "M_source"}},
			ReceiveTasks: []BPMNReceiveTask{{ID: "recv", MessageRef: "M_target"}},
			BoundaryEvents: []BPMNBoundaryEvent{
				{ID: "bnd", Message: &BPMNMessageEventDef{MessageRef: "M_boundary"}},
				{ID: "bnd_unknown", Message: &BPMNMessageEventDef{MessageRef: "M_nowhere"}},
			},
		}},
	}
	cases := []struct {
		name string
		mf   BPMNMessageFlow
		want string
	}{
		{"own name beats everything", BPMNMessageFlow{Name: "Own", MessageRef: "M_flow", SourceRef: "send", TargetRef: "recv"}, "Own"},
		{"flow message ref beats the nodes", BPMNMessageFlow{MessageRef: "M_flow", SourceRef: "send", TargetRef: "recv"}, "FromFlowRef"},
		{"target node beats the source node", BPMNMessageFlow{SourceRef: "send", TargetRef: "recv"}, "FromTarget"},
		{"unknown flow message ref falls to the target node", BPMNMessageFlow{MessageRef: "M_missing", SourceRef: "send", TargetRef: "recv"}, "FromTarget"},
		{"source node used when the target has no message", BPMNMessageFlow{SourceRef: "send", TargetRef: "nowhere"}, "FromSource"},
		{"source node used when the target message is undefined", BPMNMessageFlow{SourceRef: "send", TargetRef: "bnd_unknown"}, "FromSource"},
		{"boundary event as target", BPMNMessageFlow{SourceRef: "send", TargetRef: "bnd"}, "FromBoundary"},
		{"boundary event as source", BPMNMessageFlow{SourceRef: "bnd", TargetRef: "nowhere"}, "FromBoundary"},
		{"nothing resolves", BPMNMessageFlow{SourceRef: "bnd_unknown", TargetRef: "nowhere"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mf := tc.mf
			assert.Equal(t, tc.want, ResolveMessageFlowName(&mf, defs))
		})
	}
}

func TestIsInterrupting_And_ValidCancelActivity_CoverTheAcceptedSpellings(t *testing.T) {
	cases := []struct {
		value        string
		interrupting bool
		valid        bool
	}{
		{"", true, true},
		{"true", true, true},
		{"false", false, true},
		{"1", true, true},
		{"0", false, true},
		{"False", true, false},
		{"FALSE", true, false},
		{"True", true, false},
		{"no", true, false},
		{"yes", true, false},
		{" false", true, false},
	}
	for _, tc := range cases {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.interrupting, IsInterrupting(tc.value))
			assert.Equal(t, tc.valid, ValidCancelActivity(tc.value))
		})
	}
}

func TestBuildGraph_BoundaryEvent_IsClassifiedTimerThenErrorThenMessageElseGeneric(t *testing.T) {
	timer := &BPMNTimerDef{Duration: "PT1H"}
	errDef := &BPMNErrorDef{ErrorRef: "E1"}
	msg := &BPMNMessageEventDef{MessageRef: "M1"}
	proc := &BPMNProcess{BoundaryEvents: []BPMNBoundaryEvent{
		{ID: "all", Timer: timer, Error: errDef, Message: msg},
		{ID: "timer_error", Timer: timer, Error: errDef},
		{ID: "timer_message", Timer: timer, Message: msg},
		{ID: "error_message", Error: errDef, Message: msg},
		{ID: "timer_only", Timer: timer},
		{ID: "error_only", Error: errDef},
		{ID: "message_only", Message: msg},
		{ID: "plain"},
	}}

	g := BuildGraph(proc)

	want := map[string]FlowNodeType{
		"all":           NodeTypeTimerBoundaryEvent,
		"timer_error":   NodeTypeTimerBoundaryEvent,
		"timer_message": NodeTypeTimerBoundaryEvent,
		"error_message": NodeTypeErrorBoundaryEvent,
		"timer_only":    NodeTypeTimerBoundaryEvent,
		"error_only":    NodeTypeErrorBoundaryEvent,
		"message_only":  NodeTypeMessageBoundaryEvent,
		"plain":         NodeTypeBoundaryEvent,
	}
	assert.Equal(t, want, g.NodeType)
	for id := range want {
		assert.Contains(t, g.NodeIDs, id)
	}
	assert.Len(t, g.NodeIDs, len(want))
}

func TestFindImplicitStart_NoCandidate_ReturnsEmpty(t *testing.T) {
	cases := []struct {
		name string
		proc *BPMNProcess
	}{
		{"empty process", &BPMNProcess{}},
		{"only an isolated task", &BPMNProcess{UserTasks: []BPMNUserTask{{ID: "iso"}}}},
		{"only a boundary event that has an outgoing flow", &BPMNProcess{
			BoundaryEvents: []BPMNBoundaryEvent{{ID: "be", AttachedToRef: "a", Timer: &BPMNTimerDef{}}},
			EndEvents:      []BPMNEvent{{ID: "end"}},
			SequenceFlows:  []BPMNSequenceFlow{{SourceRef: "be", TargetRef: "end"}},
		}},
		{"a closed loop has no node without an incoming flow", &BPMNProcess{
			UserTasks:     []BPMNUserTask{{ID: "a"}, {ID: "b"}},
			SequenceFlows: []BPMNSequenceFlow{{SourceRef: "a", TargetRef: "b"}, {SourceRef: "b", TargetRef: "a"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, "", FindImplicitStart(tc.proc, BuildGraph(tc.proc)))
		})
	}
}

func TestFindImplicitStart_IsolatedAndBoundaryNodesDoNotCountAsCandidates(t *testing.T) {
	proc := &BPMNProcess{
		UserTasks: []BPMNUserTask{{ID: "iso"}, {ID: "a"}, {ID: "b"}},
		EndEvents: []BPMNEvent{{ID: "end"}},
		BoundaryEvents: []BPMNBoundaryEvent{
			{ID: "be", AttachedToRef: "a", Timer: &BPMNTimerDef{}},
		},
		SequenceFlows: []BPMNSequenceFlow{
			{SourceRef: "a", TargetRef: "b"},
			{SourceRef: "b", TargetRef: "end"},
			{SourceRef: "be", TargetRef: "end"},
		},
	}

	assert.Equal(t, "a", FindImplicitStart(proc, BuildGraph(proc)))
}

func TestFindImplicitStart_StartEventPresent_ReturnsEmptyEvenWithACandidate(t *testing.T) {
	proc := &BPMNProcess{
		StartEvents:   []BPMNEvent{{ID: "s"}},
		UserTasks:     []BPMNUserTask{{ID: "a"}},
		EndEvents:     []BPMNEvent{{ID: "end"}},
		SequenceFlows: []BPMNSequenceFlow{{SourceRef: "a", TargetRef: "end"}},
	}

	assert.Equal(t, "", FindImplicitStart(proc, BuildGraph(proc)))
}

func w3dLaneProcess() *BPMNProcess {
	return &BPMNProcess{LaneSet: BPMNLaneSet{Lanes: []BPMNLane{
		{ID: "L1", Name: "First", FlowNodeRefs: []string{"shared", "only1"}, ExtensionElements: w3dProps("dept_id", "dept-first")},
		{ID: "L2", Name: "Second", FlowNodeRefs: []string{"only2", "shared", "nodept"}, ExtensionElements: w3dProps("dept_id", "dept-second")},
		{ID: "L3", Name: "Third", FlowNodeRefs: []string{"nodept"}},
	}}}
}

func TestLaneFor_NodeInTwoLanes_ReturnsTheFirstLaneInDocumentOrder(t *testing.T) {
	proc := w3dLaneProcess()

	got := LaneFor("shared", proc)

	assert.Same(t, &proc.LaneSet.Lanes[0], got)
	assert.Same(t, &proc.LaneSet.Lanes[1], LaneFor("only2", proc))
}

func TestLaneFor_NodeInNoLane_ReturnsNil(t *testing.T) {
	assert.Nil(t, LaneFor("ghost", w3dLaneProcess()))
	assert.Nil(t, LaneFor("ghost", &BPMNProcess{}))
}

func TestDeptIDFor_ResolvesThroughTheFirstLane(t *testing.T) {
	proc := w3dLaneProcess()
	cases := []struct {
		name string
		node string
		want string
	}{
		{"node in two lanes uses the first lane and its dept_id", "shared", "dept-first"},
		{"node in one lane", "only2", "dept-second"},
		{"node listed in the second and third lane uses the second lane", "nodept", "dept-second"},
		{"node in no lane", "ghost", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, DeptIDFor(tc.node, proc))
		})
	}
}

func TestDeptIDFor_FirstLaneWithoutDeptID_IsEmptyEvenWhenALaterLaneHasOne(t *testing.T) {
	proc := &BPMNProcess{LaneSet: BPMNLaneSet{Lanes: []BPMNLane{
		{Name: "Bare", FlowNodeRefs: []string{"n"}},
		{Name: "Owned", FlowNodeRefs: []string{"n"}, ExtensionElements: w3dProps("dept_id", "dept-later")},
	}}}

	assert.Equal(t, "", DeptIDFor("n", proc))
}

func TestBoundaryFor_BoundaryWithTimerAndMessage_IsReturnedByBothLookups(t *testing.T) {
	proc := &BPMNProcess{BoundaryEvents: []BPMNBoundaryEvent{
		{ID: "other_task", AttachedToRef: "t2", Timer: &BPMNTimerDef{}, Message: &BPMNMessageEventDef{}},
		{ID: "both", AttachedToRef: "t1", Timer: &BPMNTimerDef{Duration: "PT1H"}, Message: &BPMNMessageEventDef{MessageRef: "M"}},
	}}

	timer := TimerBoundaryFor("t1", proc)
	message := MessageBoundaryFor("t1", proc)

	require.NotNil(t, timer)
	require.NotNil(t, message)
	assert.Equal(t, "both", timer.ID)
	assert.Same(t, timer, message)
}

func TestBoundaryFor_SeveralBoundariesOnOneTask_FirstOfTheMatchingKindWins(t *testing.T) {
	proc := &BPMNProcess{BoundaryEvents: []BPMNBoundaryEvent{
		{ID: "timer_a", AttachedToRef: "t1", Timer: &BPMNTimerDef{Duration: "PT1H"}},
		{ID: "error_only", AttachedToRef: "t1", Error: &BPMNErrorDef{}},
		{ID: "message_a", AttachedToRef: "t1", Message: &BPMNMessageEventDef{}},
		{ID: "timer_b", AttachedToRef: "t1", Timer: &BPMNTimerDef{Duration: "PT2H"}},
		{ID: "message_b", AttachedToRef: "t1", Message: &BPMNMessageEventDef{}},
	}}

	timer := TimerBoundaryFor("t1", proc)
	message := MessageBoundaryFor("t1", proc)

	require.NotNil(t, timer)
	require.NotNil(t, message)
	assert.Equal(t, "timer_a", timer.ID)
	assert.Equal(t, "message_a", message.ID)
}

func TestClassifyBackEdges_SelfLoop_IsABackEdge(t *testing.T) {
	cases := []struct {
		name string
		out  map[string][]string
		want map[[2]string]bool
	}{
		{"self loop on the root", map[string][]string{"a": {"a"}}, map[[2]string]bool{{"a", "a"}: true}},
		{"self loop on an inner node", map[string][]string{"a": {"b"}, "b": {"b", "c"}}, map[[2]string]bool{{"b", "b"}: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := &Graph{Outgoing: tc.out}

			assert.Equal(t, tc.want, ClassifyBackEdges(g, "a"))
		})
	}
}

func TestClassifyBackEdges_CrossEdgeToAFinishedBranch_IsNotABackEdge(t *testing.T) {
	g := &Graph{Outgoing: map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"b", "e"},
		"e": {"c"},
	}}

	got := ClassifyBackEdges(g, "a")

	assert.Equal(t, map[[2]string]bool{{"e", "c"}: true}, got)
}
