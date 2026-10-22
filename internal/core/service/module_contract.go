package service

import (
	"context"
	"fmt"
	"slices"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type ModuleSlot struct {
	ID              string
	Label           string
	Open            bool
	IAMDepartmentID string
}

type ModuleTask struct {
	AssigneeKey     string
	NodeID          string
	Name            string
	Slot            string
	IAMDepartmentID string
	StageType       string
	Role            string
	DefaultUserIDs  []string
}

type ModuleContract struct {
	Version    *domain.ModuleVersion
	Slots      []ModuleSlot
	Tasks      []ModuleTask
	Messages   []string
	ErrorCodes []string
	NamesUsers bool
}

func (s *ModuleService) Contract(ctx context.Context, callerTenantID, moduleID, versionID uuid.UUID) (*ModuleContract, error) {
	v, err := s.GetVersion(ctx, callerTenantID, moduleID, versionID)
	if err != nil {
		return nil, err
	}
	bundled, err := s.bundle(ctx, callerTenantID, v.Scope, v.BPMNXML, v.ProcessID)
	if err != nil {
		return nil, err
	}
	collab, decl, err := s.compiler.CompileModule(ctx, bundled, v.ProcessID)
	if err != nil {
		return nil, fmt.Errorf("compile module: %w", err)
	}
	expanded, err := dsl.ExpandCalls(collab, domain.MaxPlanStages)
	if err != nil {
		return nil, fmt.Errorf("expand module: %w", err)
	}
	r := contractReader{plans: map[string]*dsl.CompiledPlan{}, defaults: map[string][]string{}}
	for _, p := range collab.Plans {
		r.plans[p.Name] = p
	}
	for _, d := range expanded.Plans[0].Departments {
		for i := range d.Stages {
			r.defaults[dsl.NodeKey(d.ID, &d.Stages[i])] = d.Stages[i].DefaultAssignees
		}
	}

	root := r.plans[collab.MainPlan]
	c := &ModuleContract{Version: v, Messages: decl.Messages, ErrorCodes: decl.ErrorCodes}
	lanes := map[string]string{}
	for _, d := range root.Departments {
		lanes[d.ID] = d.Label
		if !slices.ContainsFunc(c.Slots, func(s ModuleSlot) bool { return s.ID == d.Label }) {
			c.Slots = append(c.Slots, ModuleSlot{ID: d.Label, Label: d.Label, Open: d.IAMDepartmentID == "", IAMDepartmentID: d.IAMDepartmentID})
		}
	}
	c.Tasks = r.tasks(root, "", func(deptID string) (string, string) { return lanes[deptID], "" })
	for _, t := range c.Tasks {
		c.NamesUsers = c.NamesUsers || len(t.DefaultUserIDs) > 0
	}
	return c, nil
}

type contractReader struct {
	plans    map[string]*dsl.CompiledPlan
	defaults map[string][]string
}

func (r contractReader) tasks(plan *dsl.CompiledPlan, prefix string, place func(deptID string) (slot, iam string)) []ModuleTask {
	var out []ModuleTask
	for _, d := range plan.Departments {
		slot, iam := place(d.ID)
		for i := range d.Stages {
			st := &d.Stages[i]
			if !st.CreatesHumanTask() {
				continue
			}
			out = append(out, ModuleTask{
				AssigneeKey: prefix + st.NodeID, NodeID: st.NodeID, Name: st.Name, Slot: slot, IAMDepartmentID: iam,
				StageType: st.Type, Role: st.Role, DefaultUserIDs: r.defaults[dsl.NodeKey(prefix+d.ID, st)],
			})
		}
	}
	for _, cp := range planCalls(plan) {
		called := r.plans[cp.Plan]
		out = append(out, r.tasks(called, prefix+cp.NodeID+dsl.CallScopeSeparator, func(deptID string) (string, string) {
			if bound, ok := cp.Departments[deptID]; ok {
				return place(bound)
			}
			return "", iamDepartment(called, deptID)
		})...)
	}
	return out
}

func planCalls(plan *dsl.CompiledPlan) []*dsl.CallPlanStep {
	calls := callSteps(plan.Execution.Steps, nil)
	for _, d := range plan.Departments {
		for i := range d.Stages {
			if t := d.Stages[i].BoundaryTimer; t != nil {
				calls = callSteps(t.Steps, calls)
			}
			if m := d.Stages[i].BoundaryMessage; m != nil {
				calls = callSteps(m.Steps, calls)
			}
		}
	}
	return calls
}

func callSteps(steps []dsl.ExecutionStep, into []*dsl.CallPlanStep) []*dsl.CallPlanStep {
	for i := range steps {
		st := &steps[i]
		for j := range st.Parallel {
			into = callSteps(st.Parallel[j].Steps, into)
		}
		for j := range st.Exclusive {
			into = callSteps(st.Exclusive[j].Steps, into)
		}
		if st.SubWorkflow != nil {
			into = callSteps(st.SubWorkflow.Plan.Steps, into)
		}
		if st.CallPlan != nil {
			into = append(into, st.CallPlan)
		}
		for _, path := range st.BoundarySteps() {
			into = callSteps(path, into)
		}
	}
	return into
}

func iamDepartment(plan *dsl.CompiledPlan, deptID string) string {
	for _, d := range plan.Departments {
		if d.ID == deptID {
			return d.IAMDepartmentID
		}
	}
	return ""
}
