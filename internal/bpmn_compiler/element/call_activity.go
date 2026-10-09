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
			Departments:  bindDepartments(ca, cs, calledProc, cs.Called.Get(calledProc.ID), inputs.Depts),
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
	inner, err := compileCalledState(calledProc, cs)
	if err != nil {
		return err
	}
	cs.Called.Put(&dsl.CompiledPlan{
		Name:        calledProc.ID,
		Departments: ShapeCalledPlan(inner.CollectedDepts(), calledProc),
		Execution:   dsl.ExecutionPlan{Steps: inner.CollectedSteps()},
	})
	return nil
}

func ShapeCalledPlan(depts []dsl.DepartmentDef, calledProc *bpmncore.BPMNProcess) []dsl.DepartmentDef {
	return withOpenSlots(depts, calledProc)
}

func withOpenSlots(depts []dsl.DepartmentDef, calledProc *bpmncore.BPMNProcess) []dsl.DepartmentDef {
	for _, slot := range bpmncore.OpenSlots(calledProc) {
		if !slices.ContainsFunc(depts, func(d dsl.DepartmentDef) bool { return d.ID == slot }) {
			depts = append(depts, dsl.DepartmentDef{ID: slot, Label: slot})
		}
	}
	return depts
}

func bindDepartments(ca *bpmncore.BPMNCallActivity, cs *bpmncore.CompileState, calledProc *bpmncore.BPMNProcess, called *dsl.CompiledPlan, depts map[string]string) map[string]string {
	callerFor := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(depts)) {
		calledLane, callerLane := bpmncore.LaneNamed(calledProc, key), bpmncore.LaneNamed(cs.Proc, depts[key])
		if calledLane == nil || callerLane == nil {
			continue
		}
		callerFor[calledLane.Name] = cs.NamedLaneDepartment(callerLane.Name, bpmncore.LaneDeptID(callerLane))
	}
	if open := bpmncore.OpenSlots(calledProc); len(open) == 1 {
		if _, ok := callerFor[open[0]]; !ok {
			callerFor[open[0]] = cs.LaneDepartment(ca.ID)
		}
	}
	bound := map[string]string{}
	for lane, caller := range callerFor {
		for _, segment := range laneSegments(called, lane) {
			bound[segment] = caller
		}
	}
	if len(bound) == 0 {
		return nil
	}
	return bound
}

func laneSegments(plan *dsl.CompiledPlan, lane string) []string {
	if plan == nil {
		return []string{lane}
	}
	var ids []string
	for _, d := range plan.Departments {
		if d.Label == lane {
			ids = append(ids, d.ID)
		}
	}
	return ids
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
