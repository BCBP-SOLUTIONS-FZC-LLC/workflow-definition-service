package element

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/v2/pkg/registry"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const w4dDept = "018e1f2a-0000-7000-8000-0000000000d4"

func w4dFlow(source, target string) bpmncore.BPMNSequenceFlow {
	return bpmncore.BPMNSequenceFlow{ID: "w4d_" + source + "_" + target, SourceRef: source, TargetRef: target}
}

func w4dChain(ids ...string) []bpmncore.BPMNSequenceFlow {
	flows := make([]bpmncore.BPMNSequenceFlow, 0, len(ids)-1)
	for i := 0; i+1 < len(ids); i++ {
		flows = append(flows, w4dFlow(ids[i], ids[i+1]))
	}
	return flows
}

func w4dLane(name, deptID string, refs ...string) bpmncore.BPMNLane {
	lane := bpmncore.BPMNLane{ID: "lane_" + name, Name: name, FlowNodeRefs: refs}
	if deptID != "" {
		lane.ExtensionElements.ZeebeProps.Items = []bpmncore.ZeebeProperty{{Name: "dept_id", Value: deptID}}
	}
	return lane
}

func w4dState(proc *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions) *bpmncore.CompileState {
	g := bpmncore.BuildGraph(proc)
	backEdges := map[[2]string]bool{}
	if len(proc.StartEvents) > 0 {
		backEdges = bpmncore.ProcessBackEdges(proc, g, proc.StartEvents[0].ID)
	}
	return bpmncore.NewCompileState(proc, g, bpmncore.BuildLaneLabels(proc), bpmncore.BuildCondExprs(proc), backEdges,
		map[string]bpmncore.StageTypeHandler{}, DefaultElementHandlers(), defs)
}

func w4dCompileWith(t *testing.T, root *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions, elements map[bpmncore.FlowNodeType]bpmncore.ElementHandler) *dsl.CompiledPlan {
	t.Helper()
	plan, _, err := bpmncore.Compile(root, bpmncore.BuildGraph(root), map[string]bpmncore.StageTypeHandler{}, elements, defs)
	require.NoError(t, err)
	return plan
}

func w4dCompile(t *testing.T, root *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions) *dsl.CompiledPlan {
	t.Helper()
	return w4dCompileWith(t, root, defs, DefaultElementHandlers())
}

func w4dStageIDs(plan *dsl.CompiledPlan) []string {
	var ids []string
	for _, d := range plan.Departments {
		for _, s := range d.Stages {
			ids = append(ids, s.NodeID)
		}
	}
	return ids
}

func w4dStage(t *testing.T, plan *dsl.CompiledPlan, nodeID string) dsl.StageDef {
	t.Helper()
	for _, d := range plan.Departments {
		for _, s := range d.Stages {
			if s.NodeID == nodeID {
				return s
			}
		}
	}
	require.Failf(t, "stage not found", "no stage for node %q in %v", nodeID, w4dStageIDs(plan))
	return dsl.StageDef{}
}

func w4dDeptOf(t *testing.T, plan *dsl.CompiledPlan, nodeID string) string {
	t.Helper()
	for _, d := range plan.Departments {
		for _, s := range d.Stages {
			if s.NodeID == nodeID {
				return d.ID
			}
		}
	}
	require.Failf(t, "stage not found", "no stage for node %q in %v", nodeID, w4dStageIDs(plan))
	return ""
}

func w4dModule(id string) *bpmncore.BPMNProcess {
	return &bpmncore.BPMNProcess{
		ID:          id,
		Name:        id,
		StartEvents: []bpmncore.BPMNEvent{{ID: id + "_start"}},
		EndEvents:   []bpmncore.BPMNEvent{{ID: id + "_end"}},
		UserTasks:   []bpmncore.BPMNUserTask{{ID: id + "_task", Name: "Module task"}},
		SequenceFlows: w4dChain(
			id+"_start", id+"_task", id+"_end"),
	}
}

func w4dCallExt(processID string) bpmncore.BPMNExtensionElements {
	return bpmncore.BPMNExtensionElements{ZeebeCalledElement: &bpmncore.ZeebeCalledElement{ProcessID: processID}}
}

func TestToIOMapping_ReservedInputsAreDroppedAndOutputsAreKept(t *testing.T) {
	cases := []struct {
		name    string
		mapping *bpmncore.ZeebeIOMapping
		want    *dsl.IOMapping
	}{
		{name: "no mapping", mapping: nil, want: nil},
		{name: "a mapping with nothing in it", mapping: &bpmncore.ZeebeIOMapping{}, want: nil},
		{
			name: "reserved inputs between ordinary ones leave only the ordinary ones",
			mapping: &bpmncore.ZeebeIOMapping{Inputs: []bpmncore.ZeebeIOVar{
				{Source: "=a", Target: "first"},
				{Source: "{}", Target: "Depts"},
				{Source: "ops", Target: "dept_id"},
				{Source: "=b", Target: "second"},
				{Source: "{}", Target: "Assignees"},
			}},
			want: &dsl.IOMapping{Inputs: []dsl.IOVar{{Source: "=a", Target: "first"}, {Source: "=b", Target: "second"}}},
		},
		{
			name: "an output aimed at a reserved name is kept",
			mapping: &bpmncore.ZeebeIOMapping{Outputs: []bpmncore.ZeebeIOVar{
				{Source: "=x", Target: "Depts"},
				{Source: "=y", Target: "dept_id"},
				{Source: "=z", Target: "Assignees"},
			}},
			want: &dsl.IOMapping{Outputs: []dsl.IOVar{
				{Source: "=x", Target: "Depts"}, {Source: "=y", Target: "dept_id"}, {Source: "=z", Target: "Assignees"},
			}},
		},
		{
			name: "only reserved inputs but one output keeps the mapping with that output",
			mapping: &bpmncore.ZeebeIOMapping{
				Inputs:  []bpmncore.ZeebeIOVar{{Source: "{}", Target: "Depts"}},
				Outputs: []bpmncore.ZeebeIOVar{{Source: "=r", Target: "result"}},
			},
			want: &dsl.IOMapping{Outputs: []dsl.IOVar{{Source: "=r", Target: "result"}}},
		},
		{
			name: "an input and an output both survive",
			mapping: &bpmncore.ZeebeIOMapping{
				Inputs:  []bpmncore.ZeebeIOVar{{Source: "=i", Target: "in"}},
				Outputs: []bpmncore.ZeebeIOVar{{Source: "=o", Target: "out"}},
			},
			want: &dsl.IOMapping{
				Inputs:  []dsl.IOVar{{Source: "=i", Target: "in"}},
				Outputs: []dsl.IOVar{{Source: "=o", Target: "out"}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := toIOMapping(tc.mapping)

			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCallActivityHandler_Compile_ExtrasFollowTheCallsProperties(t *testing.T) {
	cases := []struct {
		name       string
		props      []bpmncore.ZeebeProperty
		wantExtras map[string]string
	}{
		{name: "no properties leaves Extras nil", props: nil, wantExtras: nil},
		{
			name:       "properties become Extras",
			props:      []bpmncore.ZeebeProperty{{Name: "priority", Value: "high"}, {Name: "owner", Value: "ops"}},
			wantExtras: map[string]string{"priority": "high", "owner": "ops"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ext := w4dCallExt("Module")
			ext.ZeebeProps.Items = tc.props
			root := &bpmncore.BPMNProcess{
				ID:             "Root",
				Name:           "Root",
				LaneSet:        bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{w4dLane("ops", w4dDept, "CA")}},
				StartEvents:    []bpmncore.BPMNEvent{{ID: "start"}},
				EndEvents:      []bpmncore.BPMNEvent{{ID: "end"}},
				CallActivities: []bpmncore.BPMNCallActivity{{ID: "CA", Name: "Call", ExtensionElements: ext}},
				SequenceFlows:  w4dChain("start", "CA", "end"),
			}
			cs := w4dState(root, &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root, *w4dModule("Module")}})

			require.NoError(t, CallActivityHandler{}.Compile("CA", cs))

			steps := cs.CollectedSteps()
			require.Len(t, steps, 1)
			require.NotNil(t, steps[0].CallPlan)
			if tc.wantExtras == nil {
				assert.Nil(t, steps[0].Extras)
				return
			}
			assert.Equal(t, tc.wantExtras, steps[0].Extras)
		})
	}
}

func TestWithOpenSlots_AddsMissingOpenSlotsOnceInLaneOrder(t *testing.T) {
	opsOpen := w4dLane("Ops", "", "T1")
	engOpen := w4dLane("Eng", "", "T2")
	qaBound := w4dLane("Qa", w4dDept, "T3")
	qaOpen := w4dLane("Qa", "", "T4")
	existing := dsl.DepartmentDef{ID: "Ops", Label: "kept label", IAMDepartmentID: w4dDept, Stages: []dsl.StageDef{{NodeID: "T1"}}}

	cases := []struct {
		name  string
		proc  *bpmncore.BPMNProcess
		depts []dsl.DepartmentDef
		want  []dsl.DepartmentDef
	}{
		{
			name:  "an open lane that is not a department is appended with its name as id and label",
			proc:  &bpmncore.BPMNProcess{ID: "M", LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{opsOpen}}, UserTasks: []bpmncore.BPMNUserTask{{ID: "T1"}}},
			depts: nil,
			want:  []dsl.DepartmentDef{{ID: "Ops", Label: "Ops"}},
		},
		{
			name:  "an open lane that is already a department is not duplicated or altered",
			proc:  &bpmncore.BPMNProcess{ID: "M", LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{opsOpen}}, UserTasks: []bpmncore.BPMNUserTask{{ID: "T1"}}},
			depts: []dsl.DepartmentDef{existing},
			want:  []dsl.DepartmentDef{existing},
		},
		{
			name: "missing slots follow the existing departments in lane order",
			proc: &bpmncore.BPMNProcess{
				ID:        "M",
				LaneSet:   bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{engOpen, opsOpen, qaOpen}},
				UserTasks: []bpmncore.BPMNUserTask{{ID: "T1"}, {ID: "T2"}, {ID: "T4"}},
			},
			depts: []dsl.DepartmentDef{existing},
			want:  []dsl.DepartmentDef{existing, {ID: "Eng", Label: "Eng"}, {ID: "Qa", Label: "Qa"}},
		},
		{
			name: "a lane that already names an IAM department is not an open slot",
			proc: &bpmncore.BPMNProcess{
				ID:        "M",
				LaneSet:   bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{qaBound, opsOpen}},
				UserTasks: []bpmncore.BPMNUserTask{{ID: "T1"}, {ID: "T3"}},
			},
			depts: nil,
			want:  []dsl.DepartmentDef{{ID: "Ops", Label: "Ops"}},
		},
		{
			name:  "a process without lanes gets its implicit slot, without the version suffix",
			proc:  &bpmncore.BPMNProcess{ID: "Module@v2"},
			depts: nil,
			want:  []dsl.DepartmentDef{{ID: "Module", Label: "Module"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := withOpenSlots(slices.Clone(tc.depts), tc.proc)

			assert.Equal(t, tc.want, got)
		})
	}
}

type w4dHostKind string

const (
	w4dSubProcess   w4dHostKind = "sub-process"
	w4dCallActivity w4dHostKind = "call activity"
)

type w4dHostPaths struct {
	errors   []dsl.ErrorPath
	timers   []dsl.TimerPath
	messages []dsl.MessagePath
}

func w4dMessageBoundary(id, host, messageRef, cancelActivity string) bpmncore.BPMNBoundaryEvent {
	return bpmncore.BPMNBoundaryEvent{
		ID: id, AttachedToRef: host, CancelActivity: cancelActivity,
		Message: &bpmncore.BPMNMessageEventDef{MessageRef: messageRef},
	}
}

func w4dHostRoot(kind w4dHostKind, boundaries []bpmncore.BPMNBoundaryEvent, targets []string) (*bpmncore.BPMNProcess, *bpmncore.BPMNDefinitions) {
	refs := append([]string{"HOST", "T_after", "T_other"}, targets...)
	root := &bpmncore.BPMNProcess{
		ID:             "Root",
		Name:           "Root",
		LaneSet:        bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{w4dLane("ops", w4dDept, refs...)}},
		StartEvents:    []bpmncore.BPMNEvent{{ID: "start"}},
		EndEvents:      []bpmncore.BPMNEvent{{ID: "end"}},
		UserTasks:      []bpmncore.BPMNUserTask{{ID: "T_after", Name: "After"}, {ID: "T_other", Name: "Other"}},
		BoundaryEvents: boundaries,
		SequenceFlows:  w4dChain("start", "HOST", "T_after", "end"),
	}
	for _, id := range targets {
		root.UserTasks = append(root.UserTasks, bpmncore.BPMNUserTask{ID: id, Name: id})
	}
	otherAdded := false
	for _, b := range boundaries {
		switch b.AttachedToRef {
		case "HOST":
			root.SequenceFlows = append(root.SequenceFlows, w4dFlow(b.ID, "T_"+b.ID), w4dFlow("T_"+b.ID, "end"))
		default:
			root.SequenceFlows = append(root.SequenceFlows, w4dFlow(b.ID, "T_other"))
			if !otherAdded {
				root.SequenceFlows = append(root.SequenceFlows, w4dFlow("T_other", "end"))
				otherAdded = true
			}
		}
	}
	defs := &bpmncore.BPMNDefinitions{
		Messages: []bpmncore.BPMNMessage{{ID: "Msg_1", Name: "order-paid"}},
		Errors:   []bpmncore.BPMNError{{ID: "Err_rej", ErrorCode: "REJECTED"}, {ID: "Err_other", ErrorCode: "OTHER"}},
	}
	switch kind {
	case w4dSubProcess:
		root.SubProcesses = []bpmncore.BPMNSubProcess{{
			ID:            "HOST",
			Name:          "Host",
			StartEvents:   []bpmncore.BPMNEvent{{ID: "in_start"}},
			EndEvents:     []bpmncore.BPMNEvent{{ID: "in_end"}},
			UserTasks:     []bpmncore.BPMNUserTask{{ID: "in_task", Name: "Inside"}},
			SequenceFlows: w4dChain("in_start", "in_task", "in_end"),
		}}
		defs.Processes = []bpmncore.BPMNProcess{*root}
	case w4dCallActivity:
		root.CallActivities = []bpmncore.BPMNCallActivity{{ID: "HOST", Name: "Host", ExtensionElements: w4dCallExt("Module")}}
		defs.Processes = []bpmncore.BPMNProcess{*root, *w4dModule("Module")}
	}
	return root, defs
}

func w4dHostPathsOf(t *testing.T, kind w4dHostKind, plan *dsl.CompiledPlan) w4dHostPaths {
	t.Helper()
	for _, step := range plan.Execution.Steps {
		switch {
		case kind == w4dSubProcess && step.SubWorkflow != nil:
			return w4dHostPaths{errors: step.SubWorkflow.ErrorPaths, timers: step.SubWorkflow.TimerPaths, messages: step.SubWorkflow.MessagePaths}
		case kind == w4dCallActivity && step.CallPlan != nil:
			return w4dHostPaths{errors: step.CallPlan.ErrorPaths, timers: step.CallPlan.TimerPaths, messages: step.CallPlan.MessagePaths}
		}
	}
	require.Failf(t, "host step not found", "no %s step in %+v", kind, plan.Execution.Steps)
	return w4dHostPaths{}
}

func TestBoundaryPaths_OnePathPerKindForTheHostAndNoneForOtherNodes(t *testing.T) {
	for _, kind := range []w4dHostKind{w4dSubProcess, w4dCallActivity} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			boundaries := []bpmncore.BPMNBoundaryEvent{
				{ID: "BE_err", AttachedToRef: "HOST", Error: &bpmncore.BPMNErrorDef{ErrorRef: "Err_rej"}},
				{ID: "BE_tmr", AttachedToRef: "HOST", CancelActivity: "true", Timer: &bpmncore.BPMNTimerDef{Duration: "PT36H"}},
				w4dMessageBoundary("BE_msg", "HOST", "Msg_1", "false"),
				{ID: "BE_elsewhere_tmr", AttachedToRef: "T_after", Timer: &bpmncore.BPMNTimerDef{Duration: "PT1H"}},
				{ID: "BE_elsewhere_err", AttachedToRef: "T_after", Error: &bpmncore.BPMNErrorDef{ErrorRef: "Err_other"}},
				w4dMessageBoundary("BE_elsewhere_msg", "T_after", "Msg_1", ""),
			}
			root, defs := w4dHostRoot(kind, boundaries, []string{"T_BE_err", "T_BE_tmr", "T_BE_msg"})

			plan := w4dCompile(t, root, defs)

			paths := w4dHostPathsOf(t, kind, plan)
			require.Len(t, paths.errors, 1)
			assert.Equal(t, "BE_err", paths.errors[0].BoundaryID)
			assert.Equal(t, "REJECTED", paths.errors[0].ErrorCode)
			assert.Equal(t, w4dDeptOf(t, plan, "T_BE_err"), paths.errors[0].TargetDept)
			assert.False(t, paths.errors[0].Terminates)

			require.Len(t, paths.timers, 1)
			assert.Equal(t, "BE_tmr", paths.timers[0].BoundaryID)
			assert.Equal(t, "PT36H", paths.timers[0].Duration)
			assert.True(t, paths.timers[0].Interrupting)
			assert.Equal(t, w4dDeptOf(t, plan, "T_BE_tmr"), paths.timers[0].TargetDept)

			require.Len(t, paths.messages, 1)
			assert.Equal(t, "BE_msg", paths.messages[0].BoundaryID)
			assert.Equal(t, "order-paid", paths.messages[0].MessageName)
			assert.False(t, paths.messages[0].Interrupting)
			assert.Equal(t, w4dDeptOf(t, plan, "T_BE_msg"), paths.messages[0].TargetDept)
		})
	}
}

func TestBoundaryPaths_HostWithNoBoundariesHasNoPaths(t *testing.T) {
	for _, kind := range []w4dHostKind{w4dSubProcess, w4dCallActivity} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			boundaries := []bpmncore.BPMNBoundaryEvent{
				{ID: "BE_elsewhere_tmr", AttachedToRef: "T_after", Timer: &bpmncore.BPMNTimerDef{Duration: "PT1H"}},
			}
			root, defs := w4dHostRoot(kind, boundaries, nil)

			paths := w4dHostPathsOf(t, kind, w4dCompile(t, root, defs))

			assert.Nil(t, paths.errors)
			assert.Nil(t, paths.timers)
			assert.Nil(t, paths.messages)
		})
	}
}

func TestBoundaryPaths_MessageNameFallsBackFromMessageRefToMessageFlowToEmpty(t *testing.T) {
	flowTo := func(target string, mf bpmncore.BPMNMessageFlow) *bpmncore.BPMNCollaboration {
		mf.ID, mf.TargetRef = "mf1", target
		return &bpmncore.BPMNCollaboration{MessageFlows: []bpmncore.BPMNMessageFlow{mf}}
	}
	cases := []struct {
		name          string
		messageRef    string
		collaboration *bpmncore.BPMNCollaboration
		want          string
	}{
		{name: "a defined messageRef wins over a message flow", messageRef: "Msg_1", collaboration: flowTo("BE_msg", bpmncore.BPMNMessageFlow{Name: "from-flow"}), want: "order-paid"},
		{name: "no messageRef uses the name of the message flow into the boundary", messageRef: "", collaboration: flowTo("BE_msg", bpmncore.BPMNMessageFlow{Name: "from-flow"}), want: "from-flow"},
		{name: "an undefined messageRef uses the message flow", messageRef: "Msg_missing", collaboration: flowTo("BE_msg", bpmncore.BPMNMessageFlow{Name: "from-flow"}), want: "from-flow"},
		{name: "an unnamed message flow uses the message it references", messageRef: "", collaboration: flowTo("BE_msg", bpmncore.BPMNMessageFlow{MessageRef: "Msg_1"}), want: "order-paid"},
		{name: "a message flow into another node does not count", messageRef: "", collaboration: flowTo("T_after", bpmncore.BPMNMessageFlow{Name: "from-flow"}), want: ""},
		{name: "a message flow leaving the boundary does not count", messageRef: "", collaboration: &bpmncore.BPMNCollaboration{MessageFlows: []bpmncore.BPMNMessageFlow{{ID: "mf1", SourceRef: "BE_msg", TargetRef: "T_after", Name: "from-flow"}}}, want: ""},
		{name: "neither a messageRef nor a message flow gives an empty name", messageRef: "", collaboration: nil, want: ""},
	}
	for _, kind := range []w4dHostKind{w4dSubProcess, w4dCallActivity} {
		for _, tc := range cases {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				boundaries := []bpmncore.BPMNBoundaryEvent{w4dMessageBoundary("BE_msg", "HOST", tc.messageRef, "")}
				root, defs := w4dHostRoot(kind, boundaries, []string{"T_BE_msg"})
				defs.Collaboration = tc.collaboration

				paths := w4dHostPathsOf(t, kind, w4dCompile(t, root, defs))

				require.Len(t, paths.messages, 1)
				assert.Equal(t, tc.want, paths.messages[0].MessageName)
				assert.True(t, paths.messages[0].Interrupting, "no cancelActivity attribute means interrupting")
			})
		}
	}
}

func TestReceiveTaskHandler_Compile_MessageNameResolution(t *testing.T) {
	flowTo := func(mf bpmncore.BPMNMessageFlow) *bpmncore.BPMNCollaboration {
		mf.ID = "mf1"
		return &bpmncore.BPMNCollaboration{MessageFlows: []bpmncore.BPMNMessageFlow{mf}}
	}
	cases := []struct {
		name          string
		messageRef    string
		collaboration *bpmncore.BPMNCollaboration
		want          string
	}{
		{name: "a defined messageRef wins over a message flow", messageRef: "Msg_1", collaboration: flowTo(bpmncore.BPMNMessageFlow{TargetRef: "R", Name: "from-flow"}), want: "order-paid"},
		{name: "no messageRef uses the name of the message flow into the task", collaboration: flowTo(bpmncore.BPMNMessageFlow{TargetRef: "R", Name: "from-flow"}), want: "from-flow"},
		{name: "an unnamed message flow uses the message it references", collaboration: flowTo(bpmncore.BPMNMessageFlow{TargetRef: "R", MessageRef: "Msg_1"}), want: "order-paid"},
		{name: "an undefined messageRef falls back to the message flow", messageRef: "Msg_missing", collaboration: flowTo(bpmncore.BPMNMessageFlow{TargetRef: "R", Name: "from-flow"}), want: "from-flow"},
		{name: "no messageRef and no message flow uses the task id", want: "R"},
		{name: "an undefined messageRef and no message flow uses the task id", messageRef: "Msg_missing", want: "R"},
		{name: "a message flow leaving the task does not count", collaboration: flowTo(bpmncore.BPMNMessageFlow{SourceRef: "R", TargetRef: "elsewhere", Name: "from-flow"}), want: "R"},
		{name: "a message flow into another node does not count", collaboration: flowTo(bpmncore.BPMNMessageFlow{TargetRef: "elsewhere", Name: "from-flow"}), want: "R"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := &bpmncore.BPMNProcess{
				ID:           "Root",
				Name:         "Root",
				LaneSet:      bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{w4dLane("ops", w4dDept, "R")}},
				StartEvents:  []bpmncore.BPMNEvent{{ID: "start"}},
				EndEvents:    []bpmncore.BPMNEvent{{ID: "end"}},
				ReceiveTasks: []bpmncore.BPMNReceiveTask{{ID: "R", Name: "Wait", MessageRef: tc.messageRef}},
				SequenceFlows: w4dChain(
					"start", "R", "end"),
			}
			defs := &bpmncore.BPMNDefinitions{
				Processes:     []bpmncore.BPMNProcess{*root},
				Messages:      []bpmncore.BPMNMessage{{ID: "Msg_1", Name: "order-paid"}},
				Collaboration: tc.collaboration,
			}

			plan := w4dCompile(t, root, defs)

			stage := w4dStage(t, plan, "R")
			assert.Equal(t, "receive_task", stage.Type)
			assert.Equal(t, tc.want, stage.Extras["message"])
		})
	}
}

func TestElementHandlers_Compile_NodeMissingFromProcess_ReturnsAnErrorAndAppendsNothing(t *testing.T) {
	root := &bpmncore.BPMNProcess{
		ID:          "Root",
		Name:        "Root",
		LaneSet:     bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{w4dLane("ops", w4dDept, "UT")}},
		StartEvents: []bpmncore.BPMNEvent{{ID: "start"}},
		EndEvents:   []bpmncore.BPMNEvent{{ID: "end"}},
		UserTasks:   []bpmncore.BPMNUserTask{{ID: "UT", Name: "Review"}},
		SequenceFlows: w4dChain(
			"start", "UT", "end"),
	}
	defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root}}

	cases := []struct {
		name    string
		handler bpmncore.ElementHandler
		nodeID  string
		wantErr string
	}{
		{name: "send task with an unknown id", handler: SendTaskHandler{}, nodeID: "ghost", wantErr: `sendTask "ghost" not found in process`},
		{name: "send task given the id of a user task", handler: SendTaskHandler{}, nodeID: "UT", wantErr: `sendTask "UT" not found in process`},
		{name: "receive task with an unknown id", handler: ReceiveTaskHandler{}, nodeID: "ghost", wantErr: `receiveTask "ghost" not found in process`},
		{name: "receive task given the id of a user task", handler: ReceiveTaskHandler{}, nodeID: "UT", wantErr: `receiveTask "UT" not found in process`},
		{name: "user task with an unknown id", handler: UserTaskHandler{}, nodeID: "ghost", wantErr: `task "ghost" not found in process`},
		{name: "user task given the id of the start event", handler: UserTaskHandler{}, nodeID: "start", wantErr: `task "start" not found in process`},
		{name: "sub-process with an unknown id", handler: SubProcessHandler{}, nodeID: "ghost", wantErr: `subprocess "ghost" not found in process`},
		{name: "sub-process given the id of a user task", handler: SubProcessHandler{}, nodeID: "UT", wantErr: `subprocess "UT" not found in process`},
		{name: "call activity with an unknown id", handler: CallActivityHandler{}, nodeID: "ghost", wantErr: `callActivity "ghost" not found in process`},
		{name: "call activity given the id of a user task", handler: CallActivityHandler{}, nodeID: "UT", wantErr: `callActivity "UT" not found in process`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cs := w4dState(root, defs)
			cs.AppendStep(dsl.ExecutionStep{Sequential: []string{"earlier"}})

			err := tc.handler.Compile(tc.nodeID, cs)

			require.Error(t, err)
			assert.Equal(t, tc.wantErr, err.Error())
			assert.Equal(t, 1, cs.StepsCount(), "no step may be appended for a node the process lacks")
			assert.Empty(t, cs.CollectedDepts(), "no department may be created for a node the process lacks")
			assert.Equal(t, 0, cs.SeqBufCount())
		})
	}
}

func TestCompileSubProcessBody_NodesWithoutALaneRunInTheParentsLane(t *testing.T) {
	cases := []struct {
		name          string
		innerLanes    []bpmncore.BPMNLane
		wantInOps     []string
		wantElsewhere map[string]string
	}{
		{
			name:       "an inner process with no lanes runs entirely in the parent's lane",
			innerLanes: nil,
			wantInOps:  []string{"in_a", "in_b"},
		},
		{
			name:          "an inner node outside every inner lane still runs in the parent's lane",
			innerLanes:    []bpmncore.BPMNLane{w4dLane("qa", w4dDept, "in_b")},
			wantInOps:     []string{"in_a"},
			wantElsewhere: map[string]string{"in_b": "qa"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const opsDept = "018e1f2a-0000-7000-8000-0000000000d5"
			root := &bpmncore.BPMNProcess{
				ID:          "Root",
				Name:        "Root",
				LaneSet:     bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{w4dLane("ops", opsDept, "SP", "after")}},
				StartEvents: []bpmncore.BPMNEvent{{ID: "start"}},
				EndEvents:   []bpmncore.BPMNEvent{{ID: "end"}},
				UserTasks:   []bpmncore.BPMNUserTask{{ID: "after", Name: "After"}},
				SubProcesses: []bpmncore.BPMNSubProcess{{
					ID:            "SP",
					Name:          "Inner",
					LaneSet:       bpmncore.BPMNLaneSet{Lanes: tc.innerLanes},
					StartEvents:   []bpmncore.BPMNEvent{{ID: "in_start"}},
					EndEvents:     []bpmncore.BPMNEvent{{ID: "in_end"}},
					UserTasks:     []bpmncore.BPMNUserTask{{ID: "in_a", Name: "A"}, {ID: "in_b", Name: "B"}},
					SequenceFlows: w4dChain("in_start", "in_a", "in_b", "in_end"),
				}},
				SequenceFlows: w4dChain("start", "SP", "after", "end"),
			}
			defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root}}

			plan := w4dCompile(t, root, defs)

			var opsStages []string
			byDept := map[string]string{}
			for _, d := range plan.Departments {
				for _, s := range d.Stages {
					byDept[s.NodeID] = d.ID
					if d.ID == "ops" || strings.HasPrefix(d.ID, "ops"+bpmncore.SegmentSeparator) {
						assert.Equal(t, opsDept, d.IAMDepartmentID, "the parent's lane carries its IAM department to inner stages")
						opsStages = append(opsStages, s.NodeID)
					}
				}
			}
			for _, id := range tc.wantInOps {
				assert.Contains(t, opsStages, id, "inner node %s must be placed in the parent's lane", id)
			}
			assert.Contains(t, opsStages, "after", "the task after the sub-process shares the same department collector")
			for id, dept := range tc.wantElsewhere {
				assert.Equal(t, dept, byDept[id])
			}
			assert.Len(t, w4dStageIDs(plan), 3, "inner stages and the later task all land in the shared collector exactly once")
		})
	}
}

func TestDefaultElementHandlers_RegistersOneHandlerPerNodeType(t *testing.T) {
	want := map[bpmncore.FlowNodeType]reflect.Type{
		bpmncore.NodeTypeStartEvent:           reflect.TypeOf(StartEventHandler{}),
		bpmncore.NodeTypeEndEvent:             reflect.TypeOf(EndEventHandler{}),
		bpmncore.NodeTypeUserTask:             reflect.TypeOf(UserTaskHandler{}),
		bpmncore.NodeTypeSendTask:             reflect.TypeOf(SendTaskHandler{}),
		bpmncore.NodeTypeReceiveTask:          reflect.TypeOf(ReceiveTaskHandler{}),
		bpmncore.NodeTypeSubProcess:           reflect.TypeOf(SubProcessHandler{}),
		bpmncore.NodeTypeCallActivity:         reflect.TypeOf(CallActivityHandler{}),
		bpmncore.NodeTypeExclusiveGateway:     reflect.TypeOf(ExclusiveGatewayHandler{}),
		bpmncore.NodeTypeParallelGateway:      reflect.TypeOf(ParallelGatewayHandler{}),
		bpmncore.NodeTypeInclusiveGateway:     reflect.TypeOf(InclusiveGatewayHandler{}),
		bpmncore.NodeTypeErrorBoundaryEvent:   reflect.TypeOf(ErrorBoundaryHandler{}),
		bpmncore.NodeTypeTimerBoundaryEvent:   reflect.TypeOf(TimerBoundaryHandler{}),
		bpmncore.NodeTypeMessageBoundaryEvent: reflect.TypeOf(MessageBoundaryHandler{}),
	}

	got := DefaultElementHandlers()

	require.Len(t, got, len(want))
	for nodeType, wantType := range want {
		h, ok := got[nodeType]
		if assert.True(t, ok, "no handler for %s", nodeType) {
			assert.Equal(t, wantType, reflect.TypeOf(h), "handler registered for %s", nodeType)
		}
	}
}

func TestTraverseNode_NodeTypeWithoutAHandlerPassesThroughToItsForwardFlow(t *testing.T) {
	root := &bpmncore.BPMNProcess{
		ID:          "Root",
		Name:        "Root",
		LaneSet:     bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{w4dLane("ops", w4dDept, "first", "SEND", "last")}},
		StartEvents: []bpmncore.BPMNEvent{{ID: "start"}},
		EndEvents:   []bpmncore.BPMNEvent{{ID: "end"}},
		UserTasks:   []bpmncore.BPMNUserTask{{ID: "first", Name: "First"}, {ID: "last", Name: "Last"}},
		SendTasks:   []bpmncore.BPMNSendTask{{ID: "SEND", Name: "Notify"}},
		SequenceFlows: w4dChain(
			"start", "first", "SEND", "last", "end"),
	}
	defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root}}
	withoutSend := DefaultElementHandlers()
	delete(withoutSend, bpmncore.NodeTypeSendTask)

	cases := []struct {
		name     string
		elements map[bpmncore.FlowNodeType]bpmncore.ElementHandler
		want     []string
	}{
		{name: "with the registered send handler the send task becomes a stage", elements: DefaultElementHandlers(), want: []string{"first", "SEND", "last"}},
		{name: "without a send handler the send task is skipped and its successor is still compiled", elements: withoutSend, want: []string{"first", "last"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := w4dCompileWith(t, root, defs, tc.elements)

			assert.Equal(t, tc.want, w4dStageIDs(plan))
		})
	}
}

func w4dValidateProcess(lanes []bpmncore.BPMNLane, task bpmncore.BPMNUserTask) *bpmncore.BPMNProcess {
	return &bpmncore.BPMNProcess{
		ID:          "Root",
		Name:        "Root",
		LaneSet:     bpmncore.BPMNLaneSet{Lanes: lanes},
		StartEvents: []bpmncore.BPMNEvent{{ID: "start"}},
		EndEvents:   []bpmncore.BPMNEvent{{ID: "end"}},
		UserTasks:   []bpmncore.BPMNUserTask{task},
		SequenceFlows: w4dChain(
			"start", task.ID, "end"),
	}
}

func w4dCodes(errs []domain.BPMNValidationError) []string {
	codes := make([]string, 0, len(errs))
	for _, e := range errs {
		codes = append(codes, string(e.Code))
	}
	slices.Sort(codes)
	return codes
}

func TestUserTaskHandler_Validate_ConnectorTasksAndOrdinaryTasksTakeDifferentRuleSets(t *testing.T) {
	connectorType := slices.Sorted(maps.Keys(registry.All()))[0]
	connectorDef := &bpmncore.ZeebeTaskDefinition{Type: bpmncore.ConnectorTaskDefPrefix + connectorType}
	assignment := &bpmncore.ZeebeAssignmentDefinition{CandidateGroups: "not-a-role-level"}
	stageTypes := map[string]bpmncore.StageTypeHandler{}
	inLane := []bpmncore.BPMNLane{w4dLane("ops", w4dDept, "TASK")}

	cases := []struct {
		name      string
		ext       bpmncore.BPMNExtensionElements
		lanes     []bpmncore.BPMNLane
		wantCodes []string
	}{
		{
			name:      "a connector task without an assignment is clean",
			ext:       bpmncore.BPMNExtensionElements{TaskDefinition: connectorDef},
			lanes:     inLane,
			wantCodes: []string{},
		},
		{
			name:      "a connector task with an assignment gets only the ignored-assignment warning",
			ext:       bpmncore.BPMNExtensionElements{TaskDefinition: connectorDef, AssignmentDefinition: assignment},
			lanes:     inLane,
			wantCodes: []string{string(domain.BPMNWarnConnectorTaskHasAssignment)},
		},
		{
			name:      "an ordinary task without an assignment is missing the assignment",
			ext:       bpmncore.BPMNExtensionElements{TaskDefinition: &bpmncore.ZeebeTaskDefinition{Type: "review"}},
			lanes:     inLane,
			wantCodes: []string{string(domain.BPMNErrMissingAssignmentDefinition), string(domain.BPMNWarnUnknownStageType)},
		},
		{
			name:  "an ordinary task with a bad role gets the assignment rules, not the connector warning",
			ext:   bpmncore.BPMNExtensionElements{TaskDefinition: &bpmncore.ZeebeTaskDefinition{Type: "review"}, AssignmentDefinition: assignment},
			lanes: inLane,
			wantCodes: []string{
				string(domain.BPMNErrCandidateGroupsInvalid),
				string(domain.BPMNErrCandidateUsersEmpty),
				string(domain.BPMNWarnUnknownStageType),
			},
		},
		{
			name:      "a connector task outside every lane still gets the lane membership rule",
			ext:       bpmncore.BPMNExtensionElements{TaskDefinition: connectorDef},
			lanes:     []bpmncore.BPMNLane{w4dLane("ops", w4dDept, "someone-else")},
			wantCodes: []string{string(domain.BPMNErrTaskNotInLane)},
		},
		{
			name:      "an ordinary task in a lane without a department still gets the department rule",
			ext:       bpmncore.BPMNExtensionElements{TaskDefinition: &bpmncore.ZeebeTaskDefinition{Type: "review"}, AssignmentDefinition: &bpmncore.ZeebeAssignmentDefinition{CandidateGroups: "reviewer", CandidateUsers: "018f2d3c-0000-7000-8000-0000000000a1"}},
			lanes:     []bpmncore.BPMNLane{w4dLane("ops", "", "TASK")},
			wantCodes: []string{string(domain.BPMNErrMissingDeptID), string(domain.BPMNWarnUnknownStageType)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			task := bpmncore.BPMNUserTask{ID: "TASK", Name: "Work", ExtensionElements: tc.ext}
			proc := w4dValidateProcess(tc.lanes, task)
			defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*proc}}

			errs := UserTaskHandler{}.Validate("TASK", proc, bpmncore.BuildGraph(proc), defs, stageTypes, DefaultElementHandlers())

			assert.Equal(t, tc.wantCodes, w4dCodes(errs))
			for _, e := range errs {
				assert.Equal(t, "TASK", e.NodeID)
			}
		})
	}

	t.Run("an id the process does not hold yields nil", func(t *testing.T) {
		t.Parallel()
		proc := w4dValidateProcess(inLane, bpmncore.BPMNUserTask{ID: "TASK", ExtensionElements: bpmncore.BPMNExtensionElements{TaskDefinition: connectorDef}})
		defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*proc}}

		errs := UserTaskHandler{}.Validate("ghost", proc, bpmncore.BuildGraph(proc), defs, stageTypes, DefaultElementHandlers())

		assert.Nil(t, errs)
	})
}

func TestCallActivityHandler_Validate_ReturnsTheCallActivityValidatorsErrorsAnchoredOnTheNode(t *testing.T) {
	noStart := &bpmncore.BPMNProcess{ID: "NoStart", Name: "NoStart", UserTasks: []bpmncore.BPMNUserTask{{ID: "x"}}}
	cases := []struct {
		name      string
		ext       bpmncore.BPMNExtensionElements
		wantCodes []string
	}{
		{name: "no calledElement", ext: bpmncore.BPMNExtensionElements{}, wantCodes: []string{string(domain.BPMNErrUnresolvedCalledElement)}},
		{name: "a calledElement naming no process", ext: w4dCallExt("Missing"), wantCodes: []string{string(domain.BPMNErrUnresolvedCalledElement)}},
		{name: "a called process without a start event", ext: w4dCallExt("NoStart"), wantCodes: []string{string(domain.BPMNErrNoStartEvent)}},
		{name: "a usable called module", ext: w4dCallExt("Module"), wantCodes: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := &bpmncore.BPMNProcess{
				ID:             "Root",
				Name:           "Root",
				LaneSet:        bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{w4dLane("ops", w4dDept, "CA")}},
				StartEvents:    []bpmncore.BPMNEvent{{ID: "start"}},
				EndEvents:      []bpmncore.BPMNEvent{{ID: "end"}},
				CallActivities: []bpmncore.BPMNCallActivity{{ID: "CA", Name: "Call", ExtensionElements: tc.ext}},
				SequenceFlows:  w4dChain("start", "CA", "end"),
			}
			defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root, *w4dModule("Module"), *noStart}}

			got := CallActivityHandler{}.Validate("CA", root, bpmncore.BuildGraph(root), defs, nil, nil)

			assert.Equal(t, tc.wantCodes, w4dCodes(got))
			for _, e := range got {
				assert.Equal(t, "CA", e.NodeID)
			}
		})
	}

	t.Run("an id the process does not hold yields nil even when a call activity is broken", func(t *testing.T) {
		t.Parallel()
		root := &bpmncore.BPMNProcess{
			ID:             "Root",
			CallActivities: []bpmncore.BPMNCallActivity{{ID: "CA"}},
		}
		defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root}}

		got := CallActivityHandler{}.Validate("ghost", root, bpmncore.BuildGraph(root), defs, nil, nil)

		assert.Nil(t, got)
		assert.NotEmpty(t, CallActivityHandler{}.Validate("CA", root, bpmncore.BuildGraph(root), defs, nil, nil), "the same process does report the broken call by its own id")
	})
}
