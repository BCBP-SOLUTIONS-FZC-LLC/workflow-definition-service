package service

import (
	"context"
	"fmt"
	"slices"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// ModuleSlot is one of a module's own lanes that holds work: open for a call
// to bind, or fixed to its IAM department.
type ModuleSlot struct {
	ID              string
	Label           string
	Open            bool
	IAMDepartmentID string
}

// ModuleTask is one human task a module runs, its called modules' included.
// AssigneeKey is the key a call's Assignees takes. Slot is the module's own
// slot it runs in; a task in a called module's fixed lane has none and
// carries that lane's IAMDepartmentID instead.
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

// ModuleContract is what a call of a module version binds and names
// (definition LLD §3.3.18).
type ModuleContract struct {
	Version    *domain.ModuleVersion
	Slots      []ModuleSlot
	Tasks      []ModuleTask
	Messages   []string
	ErrorCodes []string
	NamesUsers bool
}

// Contract compiles a module version with its library calls resolved against
// the library as it is now, and reads its contract from the plan.
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
	expanded, err := dsl.ExpandCalls(collab, 0)
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
		if !d.Ignore && !slices.ContainsFunc(c.Slots, func(s ModuleSlot) bool { return s.ID == d.Label }) {
			c.Slots = append(c.Slots, ModuleSlot{ID: d.Label, Label: d.Label, Open: d.IAMDepartmentID == "", IAMDepartmentID: d.IAMDepartmentID})
		}
	}
	c.Tasks = r.tasks(root, "", func(deptID string) (string, string) { return lanes[deptID], "" })
	for _, t := range c.Tasks {
		c.NamesUsers = c.NamesUsers || len(t.DefaultUserIDs) > 0
	}
	return c, nil
}

// contractReader walks a module's unexpanded plans, tracing each called
// department back to the root slot it is bound to, and takes each task's
// default users from the expanded plan, where call-site Assignees apply.
type contractReader struct {
	plans    map[string]*dsl.CompiledPlan
	defaults map[string][]string
}

// tasks lists plan's human tasks, then its calls' in the order they run.
// prefix scopes plan's departments and task keys as ExpandCalls does, and
// place names the root slot, or the fixed IAM department, a department of
// plan runs in.
func (r contractReader) tasks(plan *dsl.CompiledPlan, prefix string, place func(deptID string) (slot, iam string)) []ModuleTask {
	var out []ModuleTask
	for _, d := range plan.Departments {
		if d.Ignore {
			continue
		}
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
	for _, cp := range callSteps(plan.Execution.Steps, nil) {
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
