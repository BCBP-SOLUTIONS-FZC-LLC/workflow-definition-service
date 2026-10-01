package element

import (
	"fmt"
	"maps"
	"slices"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type CallActivityHandler struct{}

func (CallActivityHandler) NodeType() bpmncore.FlowNodeType { return bpmncore.NodeTypeCallActivity }

func (CallActivityHandler) Validate(nodeID string, proc *bpmncore.BPMNProcess, _ *bpmncore.Graph, defs *bpmncore.BPMNDefinitions, _ map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	ca := bpmncore.FindCallActivity(proc, nodeID)
	if ca == nil {
		return nil
	}
	return validator.ValidateCallActivity(*ca, proc, defs)
}

// Compile emits one CallPlan step naming the called process's plan, which is
// compiled once per compile however many calls name it (definition LLD §4.1.2).
func (CallActivityHandler) Compile(nodeID string, cs *bpmncore.CompileState) error {
	ca := bpmncore.FindCallActivity(cs.Proc, nodeID)
	if ca == nil {
		return fmt.Errorf("callActivity %q not found in process", nodeID)
	}
	processID := ""
	if ca.ExtensionElements.ZeebeCalledElement != nil {
		processID = ca.ExtensionElements.ZeebeCalledElement.ProcessID
	}
	calledProc := bpmncore.FindProcessByID(cs.Defs, processID)
	if calledProc == nil {
		return fmt.Errorf("callActivity %q: called process %q not found", nodeID, processID)
	}
	if err := compileCalledPlan(calledProc, cs); err != nil {
		return fmt.Errorf("callActivity %q: %w", nodeID, err)
	}

	inputs := bpmncore.ParseCallInputs(*ca)
	errorPaths, timerPaths, messagePaths := boundaryPaths(nodeID, cs)
	step := dsl.ExecutionStep{
		CallPlan: &dsl.CallPlanStep{
			NodeID:       ca.ID,
			Name:         ca.Name,
			Plan:         calledProc.ID,
			Departments:  bindDepartments(ca, cs, calledProc, inputs.Depts),
			Assignees:    inputs.Assignees,
			ErrorPaths:   errorPaths,
			TimerPaths:   timerPaths,
			MessagePaths: messagePaths,
		},
		IOMapping: toIOMapping(ca.ExtensionElements.IOMapping),
	}
	if props := bpmncore.PropsMap(ca.ExtensionElements); len(props) > 0 {
		step.Extras = props
	}
	cs.FlushSeqBuf()
	cs.AppendStep(step)

	if fwd := cs.ForwardNexts(nodeID); len(fwd) > 0 {
		return cs.TraverseNode(fwd[0])
	}
	return nil
}

func compileCalledPlan(calledProc *bpmncore.BPMNProcess, cs *bpmncore.CompileState) error {
	if !cs.Called.Claim(calledProc.ID) {
		return nil
	}
	inner, err := compileInnerState(calledProc, cs)
	if err != nil {
		return err
	}
	depts, steps := inner.CollectedDepts(), inner.CollectedSteps()
	depts = ShapeCalledPlan(depts, steps, calledProc)
	cs.Called.Put(&dsl.CompiledPlan{
		Name:        calledProc.ID,
		Departments: depts,
		Execution:   dsl.ExecutionPlan{Steps: steps},
	})
	return nil
}

// ShapeCalledPlan gives a called process's compiled departments the shape a
// call binds: a laneless process's department named by its implicit slot, and
// a department for every open slot.
func ShapeCalledPlan(depts []dsl.DepartmentDef, steps []dsl.ExecutionStep, calledProc *bpmncore.BPMNProcess) []dsl.DepartmentDef {
	nameImplicitDept(depts, steps, bpmncore.ImplicitSlot(calledProc))
	return withOpenSlots(depts, calledProc)
}

// nameImplicitDept gives a laneless called process's single department,
// compiled with an empty ID, its implicit slot name, so a call can bind it.
func nameImplicitDept(depts []dsl.DepartmentDef, steps []dsl.ExecutionStep, slot string) {
	for i := range depts {
		if depts[i].ID == "" {
			depts[i].ID, depts[i].Label = slot, slot
		}
	}
	patchEmptyStageTargets(depts, slot)
	patchEmptyDepts(steps, slot)
}

// withOpenSlots adds a department for every open slot the compile left
// without one — a lane holding only a call that binds nothing in it — so a
// call can always bind every open slot.
func withOpenSlots(depts []dsl.DepartmentDef, calledProc *bpmncore.BPMNProcess) []dsl.DepartmentDef {
	for _, slot := range bpmncore.OpenSlots(calledProc) {
		if !slices.ContainsFunc(depts, func(d dsl.DepartmentDef) bool { return d.ID == slot }) {
			depts = append(depts, dsl.DepartmentDef{ID: slot, Label: slot})
		}
	}
	return depts
}

// bindDepartments maps each open department of the called process to a
// department of the caller: as Depts names it, or, for the only open
// department, the lane the call sits in. It makes sure every bound caller
// department exists in the caller's plan.
func bindDepartments(ca *bpmncore.BPMNCallActivity, cs *bpmncore.CompileState, calledProc *bpmncore.BPMNProcess, depts map[string]string) map[string]string {
	bound := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(depts)) {
		calledLane, callerLane := bpmncore.LaneNamed(calledProc, key), bpmncore.LaneNamed(cs.Proc, depts[key])
		if calledLane == nil || callerLane == nil {
			continue
		}
		bound[calledLane.Name] = callerLane.Name
		cs.EnsureDept(callerLane.Name, callerLane.Name, bpmncore.LaneDeptID(callerLane))
	}
	if open := bpmncore.OpenSlots(calledProc); len(open) == 1 {
		if _, ok := bound[open[0]]; !ok {
			id, label, iamDeptID := cs.DeptOf(ca.ID)
			bound[open[0]] = id
			cs.EnsureDept(id, label, iamDeptID)
		}
	}
	if len(bound) == 0 {
		return nil
	}
	return bound
}

func toIOMapping(m *bpmncore.ZeebeIOMapping) *dsl.IOMapping {
	if m == nil {
		return nil
	}
	result := &dsl.IOMapping{}
	for _, in := range m.Inputs {
		if bpmncore.IsCallInput(in.Target) {
			continue
		}
		result.Inputs = append(result.Inputs, dsl.IOVar{Source: in.Source, Target: in.Target})
	}
	for _, out := range m.Outputs {
		result.Outputs = append(result.Outputs, dsl.IOVar{Source: out.Source, Target: out.Target})
	}
	if len(result.Inputs) == 0 && len(result.Outputs) == 0 {
		return nil
	}
	return result
}
