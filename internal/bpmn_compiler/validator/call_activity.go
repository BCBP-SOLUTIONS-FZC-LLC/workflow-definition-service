package validator

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// ValidateCallActivity checks one call site in caller: that it names a
// process it can run, and that it binds that process's departments and
// assignees to things that exist (definition LLD §4.1.2). Every error is on
// the callActivity and names it as the author sees it.
func ValidateCallActivity(ca bpmncore.BPMNCallActivity, caller *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	ext := ca.ExtensionElements.ZeebeCalledElement
	if ext == nil || ext.ProcessID == "" {
		return AppendErr(nil, domain.BPMNErrUnresolvedCalledElement, ca.ID,
			"callActivity is missing a <zeebe:calledElement processId=\"...\"> extension element")
	}
	calledProc := bpmncore.FindProcessByID(defs, ext.ProcessID)
	if calledProc == nil {
		return AppendErr(nil, domain.BPMNErrUnresolvedCalledElement, ca.ID,
			fmt.Sprintf("callActivity references processId %q which is not defined in this BPMN document", ext.ProcessID))
	}
	if len(calledProc.StartEvents) == 0 {
		return AppendErr(nil, domain.BPMNErrNoStartEvent, ca.ID,
			fmt.Sprintf("callActivity references processId %q which has no start event", ext.ProcessID))
	}
	c := callSite{ca: ca, caller: caller, called: calledProc, defs: defs, inputs: bpmncore.ParseCallInputs(ca)}
	errs := c.retiredInputs()
	errs = append(errs, c.departments()...)
	if !defs.ModuleProcesses[caller.ID] {
		errs = append(errs, c.boundLanesHaveDepartments()...)
	}
	return append(errs, c.assignees()...)
}

type callSite struct {
	ca     bpmncore.BPMNCallActivity
	caller *bpmncore.BPMNProcess
	called *bpmncore.BPMNProcess
	defs   *bpmncore.BPMNDefinitions
	inputs bpmncore.CallInputs
}

func (c callSite) name() string   { return displayName(c.ca.Name, c.ca.ID) }
func (c callSite) module() string { return displayName(c.called.Name, c.called.ID) }

func displayName(name, id string) string {
	if name != "" {
		return name
	}
	return id
}

func (c callSite) err(code domain.BPMNErrorCode, format string, args ...any) domain.BPMNValidationError {
	return domain.BPMNValidationError{Code: code, NodeID: c.ca.ID, Message: fmt.Sprintf(format, args...), Severity: domain.SeverityError}
}

func (c callSite) retiredInputs() []domain.BPMNValidationError {
	if !c.inputs.HasDeptID {
		return nil
	}
	return []domain.BPMNValidationError{c.err(domain.BPMNErrInvalidZeebeProperty,
		"call %q uses a dept_id input, which is not used: a module with one open lane takes the call's lane, and Depts maps any other", c.name())}
}

func (c callSite) departments() []domain.BPMNValidationError {
	if c.inputs.DeptsErr != nil {
		return []domain.BPMNValidationError{c.err(domain.BPMNErrInvalidZeebeProperty,
			`call %q: Depts must be a JSON object mapping a module lane to a lane of this process, e.g. {"Sender":"Engineering"}: %v`, c.name(), c.inputs.DeptsErr)}
	}
	var errs []domain.BPMNValidationError
	mapped := map[string]bool{}
	for _, key := range slices.Sorted(maps.Keys(c.inputs.Depts)) {
		value := c.inputs.Depts[key]
		lane := c.workLane(key)
		switch {
		case lane == nil:
			errs = append(errs, c.err(domain.BPMNErrInvalidZeebeProperty,
				"call %q maps %q in Depts, but module %q has no lane of that name holding work", c.name(), key, c.module()))
		case bpmncore.LaneDeptID(lane) != "":
			errs = append(errs, c.err(domain.BPMNErrInvalidZeebeProperty,
				"call %q maps %q in Depts, but module %q fixes that lane to its own department", c.name(), key, c.module()))
		case mapped[lane.Name]:
			errs = append(errs, c.err(domain.BPMNErrInvalidZeebeProperty,
				"call %q maps lane %q twice in Depts", c.name(), lane.Name))
		default:
			mapped[lane.Name] = true
		}
		if bpmncore.LaneNamed(c.caller, value) == nil {
			errs = append(errs, c.err(domain.BPMNErrInvalidZeebeProperty,
				"call %q maps %q to %q in Depts, but this process has no lane of that name", c.name(), key, value))
		}
	}
	open := bpmncore.OpenSlots(c.called)
	if len(open) < 2 {
		return errs
	}
	var unbound []string
	for _, lane := range open {
		if !mapped[lane] {
			unbound = append(unbound, fmt.Sprintf("%q", lane))
		}
	}
	if len(unbound) > 0 {
		errs = append(errs, c.err(domain.BPMNErrMissingDeptInputForModule,
			"call %q does not say which department plays module %q's lane %s: map it in Depts, or give that lane its own dept_id",
			c.name(), c.module(), strings.Join(unbound, ", ")))
	}
	return errs
}

// boundLanesHaveDepartments refuses a binding to a workflow lane with no
// dept_id, which would leave the module's work in no IAM department until the
// instance started. A module's own open lane is left to its caller to bind.
func (c callSite) boundLanesHaveDepartments() []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	mapped := map[string]bool{}
	for _, key := range slices.Sorted(maps.Keys(c.inputs.Depts)) {
		if lane := c.workLane(key); lane != nil {
			mapped[lane.Name] = true
		}
		if lane := bpmncore.LaneNamed(c.caller, c.inputs.Depts[key]); lane != nil && bpmncore.LaneDeptID(lane) == "" {
			errs = append(errs, c.err(domain.BPMNErrMissingDeptID,
				"call %q maps %q to lane %q, which has no dept_id; give that lane its IAM department", c.name(), key, lane.Name))
		}
	}
	if open := bpmncore.OpenSlots(c.called); len(open) == 1 && !mapped[open[0]] {
		if lane := bpmncore.LaneFor(c.ca.ID, c.caller); lane != nil && bpmncore.LaneDeptID(lane) == "" {
			errs = append(errs, c.err(domain.BPMNErrMissingDeptID,
				"call %q runs module %q in its own lane %q, which has no dept_id; give that lane its IAM department", c.name(), c.module(), lane.Name))
		}
	}
	return errs
}

// hasTask reports whether proc runs a task with the id, at the top level or
// in a subProcess body, one level being all a subProcess may nest.
func hasTask(proc *bpmncore.BPMNProcess, id string) bool {
	if bpmncore.FindTask(proc, id) != nil {
		return true
	}
	return slices.ContainsFunc(proc.SubProcesses, func(sp bpmncore.BPMNSubProcess) bool {
		return slices.ContainsFunc(sp.UserTasks, func(t bpmncore.BPMNUserTask) bool { return t.ID == id })
	})
}

func (c callSite) workLane(name string) *bpmncore.BPMNLane {
	for _, lane := range bpmncore.WorkLanes(c.called) {
		if strings.EqualFold(lane.Name, name) {
			return lane
		}
	}
	return nil
}

func (c callSite) assignees() []domain.BPMNValidationError {
	if c.inputs.AssigneesErr != nil {
		return []domain.BPMNValidationError{c.err(domain.BPMNErrInvalidZeebeProperty,
			`call %q: Assignees must be a JSON object mapping a module task id, or a path of the module's calls to a task, to a user id, e.g. {"Task_review":"<user uuid>","Call_check::Task_check":"<user uuid>"}: %v`, c.name(), c.inputs.AssigneesErr)}
	}
	var errs []domain.BPMNValidationError
	for _, key := range slices.Sorted(maps.Keys(c.inputs.Assignees)) {
		if reason := c.unassignable(key); reason != "" {
			errs = append(errs, c.err(domain.BPMNErrInvalidZeebeProperty, "call %q assigns %q, but %s", c.name(), key, reason))
		}
		if !IsUserID(c.inputs.Assignees[key]) {
			errs = append(errs, c.err(domain.BPMNErrInvalidZeebeProperty,
				"call %q assigns %q to %q, which is not a user id (a UUID v7)", c.name(), key, c.inputs.Assignees[key]))
		}
	}
	return errs
}

// unassignable says why an Assignees key names no task under this call, or
// returns "" when it does. A key is a task ID of the called module, or the
// IDs of the module's calls leading to a task further down, then that task's
// ID, joined by dsl.CallScopeSeparator.
func (c callSite) unassignable(key string) string {
	proc := c.called
	for {
		callID, rest, nested := strings.Cut(key, dsl.CallScopeSeparator)
		if !nested {
			if !hasTask(proc, key) {
				return fmt.Sprintf("module %q has no task %q", displayName(proc.Name, proc.ID), key)
			}
			return ""
		}
		ca := bpmncore.FindCallActivity(proc, callID)
		if ca == nil || ca.ExtensionElements.ZeebeCalledElement == nil {
			return fmt.Sprintf("module %q makes no call %q", displayName(proc.Name, proc.ID), callID)
		}
		next := bpmncore.FindProcessByID(c.defs, ca.ExtensionElements.ZeebeCalledElement.ProcessID)
		if next == nil {
			return fmt.Sprintf("call %q in module %q names no process this document holds", callID, displayName(proc.Name, proc.ID))
		}
		proc, key = next, rest
	}
}
