package bpmncore

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/stretchr/testify/assert"
)

func TestResolveTargets_TargetNodeNoSegmentHolds_FallsBackToTheLane(t *testing.T) {
	tests := []struct {
		name   string
		placed map[string]string
		step   dsl.ExecutionStep
		stage  *dsl.StageDef
		check  func(t *testing.T, s *CompileState)
	}{
		{
			name:   "a revert to a placed node takes its segment",
			placed: map[string]string{"Review": "Legal~2"},
			step:   dsl.ExecutionStep{Exclusive: []dsl.ExclusiveBranch{{RevertToDept: segmentOfPending("Review")}}},
			check: func(t *testing.T, s *CompileState) {
				assert.Equal(t, "Legal~2", s.CollectedSteps()[0].Exclusive[0].RevertToDept)
			},
		},
		{
			name: "a revert to a node that was never placed names the lane",
			step: dsl.ExecutionStep{Exclusive: []dsl.ExclusiveBranch{{RevertToDept: segmentOfPending("Ghost")}}},
			check: func(t *testing.T, s *CompileState) {
				assert.Equal(t, s.defaultLane, s.CollectedSteps()[0].Exclusive[0].RevertToDept)
			},
		},
		{
			name: "a revert whose way back reaches no task at all names the lane",
			step: dsl.ExecutionStep{Exclusive: []dsl.ExclusiveBranch{{RevertToDept: segmentOfPending("")}}},
			check: func(t *testing.T, s *CompileState) {
				assert.Equal(t, s.defaultLane, s.CollectedSteps()[0].Exclusive[0].RevertToDept)
			},
		},
		{
			name: "a message path to a node that was never placed names the lane",
			step: dsl.ExecutionStep{MessagePaths: []dsl.MessagePath{{BoundaryID: "B1", TargetDept: segmentOfPending("Phantom")}}},
			check: func(t *testing.T, s *CompileState) {
				assert.Equal(t, s.defaultLane, s.CollectedSteps()[0].MessagePaths[0].TargetDept)
			},
		},
		{
			name:  "a stage timer boundary to a node that was never placed names the lane",
			stage: &dsl.StageDef{BoundaryTimer: &dsl.BoundaryTimer{BoundaryID: "B2", TargetDept: segmentOfPending("Missing")}},
			check: func(t *testing.T, s *CompileState) {
				assert.Equal(t, s.defaultLane, s.sd.depts[0].Stages[0].BoundaryTimer.TargetDept)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := w2cNewState()
			for node, segment := range tc.placed {
				s.MarkNodeSegment(node, segment)
			}
			if tc.stage != nil {
				s.sd.depts = append(s.sd.depts, dsl.DepartmentDef{ID: "Dept", Stages: []dsl.StageDef{*tc.stage}})
			} else {
				s.AppendStep(tc.step)
			}

			s.ResolveTargets()

			tc.check(t, s)
		})
	}
}
