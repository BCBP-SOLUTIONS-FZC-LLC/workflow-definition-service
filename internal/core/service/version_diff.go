package service

import (
	"fmt"
	"reflect"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"
)

func computeDiff(base, target *dsl.CompiledPlan) DiffChanges {
	changes := DiffChanges{}
	baseDepts := deptSet(base)
	targetDepts := deptSet(target)

	for id := range targetDepts {
		if _, ok := baseDepts[id]; !ok {
			changes.AddedDepartments = append(changes.AddedDepartments, id)
		}
	}
	for id := range baseDepts {
		if _, ok := targetDepts[id]; !ok {
			changes.RemovedDepartments = append(changes.RemovedDepartments, id)
		}
	}
	changes.StepChanges = diffSteps(base.Execution.Steps, target.Execution.Steps)
	changes.MetadataOnly = len(changes.AddedDepartments) == 0 &&
		len(changes.RemovedDepartments) == 0 &&
		len(changes.StepChanges) == 0
	return changes
}

func diffSteps(baseSteps, targetSteps []dsl.ExecutionStep) []StepChange {
	maxLen := len(baseSteps)
	if len(targetSteps) > maxLen {
		maxLen = len(targetSteps)
	}
	var changes []StepChange
	for i := 0; i < maxLen; i++ {
		var b, t *dsl.ExecutionStep
		if i < len(baseSteps) {
			b = &baseSteps[i]
		}
		if i < len(targetSteps) {
			t = &targetSteps[i]
		}
		if !reflect.DeepEqual(b, t) {
			changes = append(changes, StepChange{
				Description: fmt.Sprintf("step %d changed", i+1),
				Before:      b,
				After:       t,
			})
		}
	}
	return changes
}

func deptSet(plan *dsl.CompiledPlan) map[string]struct{} {
	m := make(map[string]struct{}, len(plan.Departments))
	for _, d := range plan.Departments {
		m[d.ID] = struct{}{}
	}
	return m
}
