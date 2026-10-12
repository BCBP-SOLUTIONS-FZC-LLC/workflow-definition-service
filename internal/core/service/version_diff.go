package service

import (
	"fmt"
	"reflect"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
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
	changes.StageChanges = diffStages(base, target)
	changes.MetadataOnly = len(changes.AddedDepartments) == 0 &&
		len(changes.RemovedDepartments) == 0 &&
		len(changes.StepChanges) == 0 &&
		len(changes.StageChanges) == 0
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

func diffStages(base, target *dsl.CompiledPlan) []StageChange {
	baseStages := stagesByNodeKey(base)
	var changes []StageChange
	for _, dept := range target.Departments {
		for i := range dept.Stages {
			key := dsl.NodeKey(dept.ID, &dept.Stages[i])
			before, ok := baseStages[key]
			if !ok {
				continue
			}
			after := dept.Stages[i]
			switch {
			case !reflect.DeepEqual(before.BoundaryTimer, after.BoundaryTimer):
				changes = append(changes, StageChange{NodeKey: key, Description: "timer boundary changed"})
			case !reflect.DeepEqual(before.BoundaryMessage, after.BoundaryMessage):
				changes = append(changes, StageChange{NodeKey: key, Description: "message boundary changed"})
			case before.ConnectorType != after.ConnectorType:
				changes = append(changes, StageChange{NodeKey: key, Description: "connector type changed"})
			}
		}
	}
	return changes
}

func stagesByNodeKey(plan *dsl.CompiledPlan) map[string]dsl.StageDef {
	m := make(map[string]dsl.StageDef)
	for _, dept := range plan.Departments {
		for i := range dept.Stages {
			m[dsl.NodeKey(dept.ID, &dept.Stages[i])] = dept.Stages[i]
		}
	}
	return m
}

func deptSet(plan *dsl.CompiledPlan) map[string]struct{} {
	m := make(map[string]struct{}, len(plan.Departments))
	for _, d := range plan.Departments {
		m[d.ID] = struct{}{}
	}
	return m
}
