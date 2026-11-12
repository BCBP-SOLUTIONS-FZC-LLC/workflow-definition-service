package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
)

func tqSteps(depts ...string) []dsl.ExecutionStep {
	steps := make([]dsl.ExecutionStep, 0, len(depts))
	for _, d := range depts {
		steps = append(steps, dsl.ExecutionStep{Sequential: []string{d}})
	}
	return steps
}

func tqStepDept(s *dsl.ExecutionStep) string {
	if s == nil {
		return ""
	}
	return s.Sequential[0]
}

func TestDiffSteps_StepInsertedAtFront_ReportsEveryShiftedPosition(t *testing.T) {
	changes := diffSteps(tqSteps("A", "B", "C"), tqSteps("X", "A", "B", "C"))

	require.Len(t, changes, 4)
	wantBefore := []string{"A", "B", "C", ""}
	wantAfter := []string{"X", "A", "B", "C"}
	for i, ch := range changes {
		assert.Equal(t, []string{"step 1 changed", "step 2 changed", "step 3 changed", "step 4 changed"}[i], ch.Description)
		assert.Equal(t, wantBefore[i], tqStepDept(ch.Before), "before at position %d", i+1)
		assert.Equal(t, wantAfter[i], tqStepDept(ch.After), "after at position %d", i+1)
	}
	assert.Nil(t, changes[3].Before, "the position past the base has no base step")
}

func TestDiffSteps_StepsComparedByPosition(t *testing.T) {
	cases := []struct {
		name            string
		base            []dsl.ExecutionStep
		target          []dsl.ExecutionStep
		wantDescription []string
	}{
		{name: "identical steps", base: tqSteps("A", "B"), target: tqSteps("A", "B")},
		{name: "step appended at the end touches only the new position", base: tqSteps("A", "B"), target: tqSteps("A", "B", "C"), wantDescription: []string{"step 3 changed"}},
		{name: "step removed from the front shifts every position", base: tqSteps("A", "B", "C"), target: tqSteps("B", "C"), wantDescription: []string{"step 1 changed", "step 2 changed", "step 3 changed"}},
		{name: "two steps swapped touch both positions", base: tqSteps("A", "B", "C"), target: tqSteps("B", "A", "C"), wantDescription: []string{"step 1 changed", "step 2 changed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, ch := range diffSteps(tc.base, tc.target) {
				got = append(got, ch.Description)
			}
			assert.Equal(t, tc.wantDescription, got)
		})
	}
}

func TestComputeDiff_StepInsertedAtFront_IsNotMetadataOnly(t *testing.T) {
	base := &dsl.CompiledPlan{Execution: dsl.ExecutionPlan{Steps: tqSteps("A", "B", "C")}}
	target := &dsl.CompiledPlan{Execution: dsl.ExecutionPlan{Steps: tqSteps("X", "A", "B", "C")}}

	changes := computeDiff(base, target)

	assert.Len(t, changes.StepChanges, 4)
	assert.False(t, changes.MetadataOnly)
}
