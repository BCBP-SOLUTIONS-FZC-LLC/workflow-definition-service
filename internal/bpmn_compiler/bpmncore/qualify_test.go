package bpmncore

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestQualifySteps_AllBranches(t *testing.T) {
	rename := func(id string) string {
		if id == "" {
			return ""
		}
		return "PREFIX/" + id
	}

	steps := []domain.ExecutionStep{
		{
			Sequential: []string{"dept-a"},
		},
		{
			Parallel: []domain.ParallelBranch{
				{
					DeptID: "dept-b",
					Steps: []domain.ExecutionStep{
						{Sequential: []string{"dept-nested"}},
					},
				},
			},
		},
		{
			Exclusive: []domain.ExclusiveBranch{
				{Target: "dept-c", RevertToDept: "dept-d"},
			},
		},
		{
			SubWorkflow: &domain.SubWorkflowStep{
				Plan: domain.ExecutionPlan{
					Steps: []domain.ExecutionStep{
						{Sequential: []string{"dept-sub"}},
					},
				},
				ErrorPaths:   []domain.ErrorPath{{TargetDept: "dept-err"}},
				TimerPaths:   []domain.TimerPath{{TargetDept: "dept-timer"}},
				MessagePaths: []domain.MessagePath{{TargetDept: "dept-msg"}},
			},
		},
		{
			MessagePaths: []domain.MessagePath{{TargetDept: "dept-top-msg"}},
		},
		{
			// Empty-string targets exercise the passthrough guard in rename().
			Exclusive: []domain.ExclusiveBranch{{Target: "", RevertToDept: ""}},
		},
	}

	QualifySteps(steps, rename)

	if steps[0].Sequential[0] != "PREFIX/dept-a" {
		t.Errorf("Sequential: got %q", steps[0].Sequential[0])
	}
	if steps[1].Parallel[0].DeptID != "PREFIX/dept-b" {
		t.Errorf("Parallel.DeptID: got %q", steps[1].Parallel[0].DeptID)
	}
	if steps[1].Parallel[0].Steps[0].Sequential[0] != "PREFIX/dept-nested" {
		t.Errorf("nested Parallel step: got %q", steps[1].Parallel[0].Steps[0].Sequential[0])
	}
	if steps[2].Exclusive[0].Target != "PREFIX/dept-c" || steps[2].Exclusive[0].RevertToDept != "PREFIX/dept-d" {
		t.Errorf("Exclusive: got %+v", steps[2].Exclusive[0])
	}
	sw := steps[3].SubWorkflow
	if sw.Plan.Steps[0].Sequential[0] != "PREFIX/dept-sub" {
		t.Errorf("SubWorkflow.Plan: got %q", sw.Plan.Steps[0].Sequential[0])
	}
	if sw.ErrorPaths[0].TargetDept != "PREFIX/dept-err" {
		t.Errorf("ErrorPaths: got %q", sw.ErrorPaths[0].TargetDept)
	}
	if sw.TimerPaths[0].TargetDept != "PREFIX/dept-timer" {
		t.Errorf("TimerPaths: got %q", sw.TimerPaths[0].TargetDept)
	}
	if sw.MessagePaths[0].TargetDept != "PREFIX/dept-msg" {
		t.Errorf("SubWorkflow.MessagePaths: got %q", sw.MessagePaths[0].TargetDept)
	}
	if steps[4].MessagePaths[0].TargetDept != "PREFIX/dept-top-msg" {
		t.Errorf("top-level MessagePaths: got %q", steps[4].MessagePaths[0].TargetDept)
	}
	if steps[5].Exclusive[0].Target != "" || steps[5].Exclusive[0].RevertToDept != "" {
		t.Errorf("expected empty strings to pass through unchanged; got %+v", steps[5].Exclusive[0])
	}
}

func TestQualifyPlanDepts(t *testing.T) {
	plan := &domain.CompiledPlan{
		Departments: []domain.DepartmentDef{
			{
				ID: "dept-a",
				Stages: []domain.StageDef{
					{
						BoundaryTimer:   &domain.BoundaryTimer{TargetDept: "dept-b"},
						BoundaryMessage: &domain.MessagePath{TargetDept: "dept-c"},
					},
					{
						// No boundary timer/message -> nil guard branches.
					},
				},
			},
			{
				// Empty department ID exercises rename()'s own "" passthrough guard.
				ID: "",
			},
		},
		Execution: domain.ExecutionPlan{
			Steps: []domain.ExecutionStep{
				{Sequential: []string{"dept-d"}},
			},
		},
	}

	QualifyPlanDepts(plan, "wf42")

	if plan.Departments[0].ID != "wf42/dept-a" {
		t.Errorf("Department ID: got %q", plan.Departments[0].ID)
	}
	if plan.Departments[0].Stages[0].BoundaryTimer.TargetDept != "wf42/dept-b" {
		t.Errorf("BoundaryTimer.TargetDept: got %q", plan.Departments[0].Stages[0].BoundaryTimer.TargetDept)
	}
	if plan.Departments[0].Stages[0].BoundaryMessage.TargetDept != "wf42/dept-c" {
		t.Errorf("BoundaryMessage.TargetDept: got %q", plan.Departments[0].Stages[0].BoundaryMessage.TargetDept)
	}
	if plan.Execution.Steps[0].Sequential[0] != "wf42/dept-d" {
		t.Errorf("Execution step: got %q", plan.Execution.Steps[0].Sequential[0])
	}
	if plan.Departments[1].ID != "" {
		t.Errorf("expected empty department ID to remain unqualified; got %q", plan.Departments[1].ID)
	}
}
