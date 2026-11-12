package bpmncore

import (
	"encoding/json"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func w5dResolveState(placed map[string]string, lanes []BPMNLane) *CompileState {
	s := w2cNewState()
	s.defaultLane = "DefaultLane"
	s.Proc.LaneSet.Lanes = lanes
	for node, segment := range placed {
		s.MarkNodeSegment(node, segment)
	}
	return s
}

func w5dRevertStep(pendingNode string) dsl.ExecutionStep {
	return dsl.ExecutionStep{Exclusive: []dsl.ExclusiveBranch{{RevertToDept: segmentOfPending(pendingNode)}}}
}

func TestResolveTargets_BoundaryPathsOnSubWorkflowAndCallSteps_AreResolvedWithTheirOwnSteps(t *testing.T) {
	lanes := []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"InOps"}}}
	placed := map[string]string{"Review": "Legal~2"}

	cases := []struct {
		name      string
		placed    map[string]string
		lanes     []BPMNLane
		target    string
		wantDept  string
		wantRevrt string
	}{
		{name: "a placed node gives its segment", placed: placed, target: "Review", wantDept: "Legal~2", wantRevrt: "Legal~2"},
		{name: "an unplaced node in a lane gives that lane", placed: placed, lanes: lanes, target: "InOps", wantDept: "Ops", wantRevrt: "Ops"},
		{name: "an unplaced node in no lane gives the default lane", placed: placed, lanes: lanes, target: "Ghost", wantDept: "DefaultLane", wantRevrt: "DefaultLane"},
	}
	for _, tc := range cases {
		for _, kind := range []string{"sub-workflow", "call"} {
			t.Run(kind+" "+tc.name, func(t *testing.T) {
				s := w5dResolveState(tc.placed, tc.lanes)
				s.sd.boundarySteps["E1"] = []dsl.ExecutionStep{w5dRevertStep(tc.target)}
				s.sd.boundarySteps["T1"] = []dsl.ExecutionStep{w5dRevertStep(tc.target)}
				s.sd.boundarySteps["M1"] = []dsl.ExecutionStep{w5dRevertStep(tc.target)}
				errs := []dsl.ErrorPath{{BoundaryID: "E1", TargetDept: segmentOfPending(tc.target)}}
				timers := []dsl.TimerPath{{BoundaryID: "T1", TargetDept: segmentOfPending(tc.target)}}
				msgs := []dsl.MessagePath{{BoundaryID: "M1", TargetDept: segmentOfPending(tc.target)}}
				if kind == "sub-workflow" {
					s.AppendStep(dsl.ExecutionStep{SubWorkflow: &dsl.SubWorkflowStep{ErrorPaths: errs, TimerPaths: timers, MessagePaths: msgs}})
				} else {
					s.AppendStep(dsl.ExecutionStep{CallPlan: &dsl.CallPlanStep{ErrorPaths: errs, TimerPaths: timers, MessagePaths: msgs}})
				}

				s.ResolveTargets()
				first, err := json.Marshal(s.CollectedSteps())
				require.NoError(t, err)
				s.ResolveTargets()
				second, err := json.Marshal(s.CollectedSteps())
				require.NoError(t, err)

				assert.JSONEq(t, string(first), string(second), "resolving twice must change nothing")
				step := s.CollectedSteps()[0]
				var gotErrs []dsl.ErrorPath
				var gotTimers []dsl.TimerPath
				var gotMsgs []dsl.MessagePath
				if kind == "sub-workflow" {
					gotErrs, gotTimers, gotMsgs = step.SubWorkflow.ErrorPaths, step.SubWorkflow.TimerPaths, step.SubWorkflow.MessagePaths
				} else {
					gotErrs, gotTimers, gotMsgs = step.CallPlan.ErrorPaths, step.CallPlan.TimerPaths, step.CallPlan.MessagePaths
				}
				require.Len(t, gotErrs, 1)
				require.Len(t, gotTimers, 1)
				require.Len(t, gotMsgs, 1)
				assert.Equal(t, tc.wantDept, gotErrs[0].TargetDept)
				assert.Equal(t, tc.wantDept, gotTimers[0].TargetDept)
				assert.Equal(t, tc.wantDept, gotMsgs[0].TargetDept)
				require.Len(t, gotErrs[0].Steps, 1)
				require.Len(t, gotTimers[0].Steps, 1)
				require.Len(t, gotMsgs[0].Steps, 1)
				assert.Equal(t, tc.wantRevrt, gotErrs[0].Steps[0].Exclusive[0].RevertToDept)
				assert.Equal(t, tc.wantRevrt, gotTimers[0].Steps[0].Exclusive[0].RevertToDept)
				assert.Equal(t, tc.wantRevrt, gotMsgs[0].Steps[0].Exclusive[0].RevertToDept)
			})
		}
	}
}

func TestResolveTargets_SubWorkflowBodyAndNonPendingValues_AreHandledPerValue(t *testing.T) {
	s := w5dResolveState(map[string]string{"Review": "Legal~2"}, nil)
	s.AppendStep(dsl.ExecutionStep{SubWorkflow: &dsl.SubWorkflowStep{
		Plan: dsl.ExecutionPlan{Steps: []dsl.ExecutionStep{
			w5dRevertStep("Review"),
			{Exclusive: []dsl.ExclusiveBranch{{RevertToDept: "Finance~3"}, {RevertToDept: ""}, {Terminates: true}}},
		}},
		ErrorPaths: []dsl.ErrorPath{{BoundaryID: "none", TargetDept: "Legal"}, {BoundaryID: "none2", Terminates: true}},
	}})

	s.ResolveTargets()

	sw := s.CollectedSteps()[0].SubWorkflow
	assert.Equal(t, "Legal~2", sw.Plan.Steps[0].Exclusive[0].RevertToDept, "a pending target inside the body is resolved")
	assert.Equal(t, "Finance~3", sw.Plan.Steps[1].Exclusive[0].RevertToDept, "a value that is already a segment is kept")
	assert.Empty(t, sw.Plan.Steps[1].Exclusive[1].RevertToDept)
	assert.Equal(t, "Legal", sw.ErrorPaths[0].TargetDept, "a target that is not pending is kept")
	assert.Empty(t, sw.ErrorPaths[1].TargetDept)
}

func TestBuildGraph_TwoFlowsBetweenTheSamePair_KeepsBothEdgesInOrder(t *testing.T) {
	proc := &BPMNProcess{
		UserTasks: []BPMNUserTask{{ID: "A"}, {ID: "B"}, {ID: "C"}},
		SequenceFlows: []BPMNSequenceFlow{
			{ID: "f1", SourceRef: "A", TargetRef: "B"},
			{ID: "f2", SourceRef: "A", TargetRef: "C"},
			{ID: "f3", SourceRef: "A", TargetRef: "B"},
		},
	}

	g := BuildGraph(proc)

	assert.Equal(t, []string{"B", "C", "B"}, g.Outgoing["A"])
	assert.Equal(t, []string{"A", "A"}, g.Incoming["B"])
	assert.Equal(t, []string{"A"}, g.Incoming["C"])
}

func TestBuildCondExprs_TwoFlowsBetweenTheSamePair_KeepOneExpressionForThePair(t *testing.T) {
	cases := []struct {
		name  string
		exprs [2]string
		want  string
	}{
		{name: "only the first carries a condition", exprs: [2]string{"$.a", ""}, want: "$.a"},
		{name: "only the second carries a condition", exprs: [2]string{"", "$.b"}, want: "$.b"},
		{name: "both carry one and the later flow's is kept", exprs: [2]string{"$.a", "$.b"}, want: "$.b"},
		{name: "neither carries one and nothing is stored", exprs: [2]string{"", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proc := &BPMNProcess{SequenceFlows: []BPMNSequenceFlow{
				{ID: "f1", SourceRef: "G", TargetRef: "T", ConditionExpression: tc.exprs[0]},
				{ID: "f2", SourceRef: "G", TargetRef: "T", ConditionExpression: tc.exprs[1]},
				{ID: "f3", SourceRef: "G", TargetRef: "U", ConditionExpression: "$.u"},
			}}

			got := BuildCondExprs(proc)

			if tc.want == "" {
				assert.NotContains(t, got, [2]string{"G", "T"})
				assert.Len(t, got, 1)
				return
			}
			assert.Len(t, got, 2)
			assert.Equal(t, tc.want, got[[2]string{"G", "T"}])
			assert.Equal(t, "$.u", got[[2]string{"G", "U"}])
		})
	}
}

func TestBuildLaneLabels_TwoLanesWithTheSameName_GiveOneLabel(t *testing.T) {
	proc := &BPMNProcess{LaneSet: BPMNLaneSet{Lanes: []BPMNLane{
		{ID: "L1", Name: "Ops", FlowNodeRefs: []string{"a"}},
		{ID: "L2", Name: "Ops", FlowNodeRefs: []string{"b"}},
		{ID: "L3", Name: "Legal", FlowNodeRefs: []string{"c"}},
	}}}

	labels := BuildLaneLabels(proc)

	assert.Equal(t, map[string]string{"Ops": "Ops", "Legal": "Legal"}, labels)
}
