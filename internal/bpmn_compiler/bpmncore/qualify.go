package bpmncore

import "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"

func QualifyPlanDepts(plan *domain.CompiledPlan, prefix string) {
	rename := func(id string) string {
		if id == "" {
			return ""
		}
		return prefix + "/" + id
	}
	for i := range plan.Departments {
		plan.Departments[i].ID = rename(plan.Departments[i].ID)
		for j := range plan.Departments[i].Stages {
			if bt := plan.Departments[i].Stages[j].BoundaryTimer; bt != nil {
				bt.TargetDept = rename(bt.TargetDept)
			}
			if bm := plan.Departments[i].Stages[j].BoundaryMessage; bm != nil {
				bm.TargetDept = rename(bm.TargetDept)
			}
		}
	}
	QualifySteps(plan.Execution.Steps, rename)
}

func QualifySteps(steps []domain.ExecutionStep, rename func(string) string) {
	for i := range steps {
		for j := range steps[i].Sequential {
			steps[i].Sequential[j] = rename(steps[i].Sequential[j])
		}
		for j := range steps[i].Parallel {
			steps[i].Parallel[j].DeptID = rename(steps[i].Parallel[j].DeptID)
			QualifySteps(steps[i].Parallel[j].Steps, rename)
		}
		for j := range steps[i].Exclusive {
			steps[i].Exclusive[j].Target = rename(steps[i].Exclusive[j].Target)
			steps[i].Exclusive[j].RevertToDept = rename(steps[i].Exclusive[j].RevertToDept)
		}
		if sw := steps[i].SubWorkflow; sw != nil {
			QualifySteps(sw.Plan.Steps, rename)
			for j := range sw.ErrorPaths {
				sw.ErrorPaths[j].TargetDept = rename(sw.ErrorPaths[j].TargetDept)
			}
			for j := range sw.TimerPaths {
				sw.TimerPaths[j].TargetDept = rename(sw.TimerPaths[j].TargetDept)
			}
			for j := range sw.MessagePaths {
				sw.MessagePaths[j].TargetDept = rename(sw.MessagePaths[j].TargetDept)
			}
		}
		for j := range steps[i].MessagePaths {
			steps[i].MessagePaths[j].TargetDept = rename(steps[i].MessagePaths[j].TargetDept)
		}
	}
}
