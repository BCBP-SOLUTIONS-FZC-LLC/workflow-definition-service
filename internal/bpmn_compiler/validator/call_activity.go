package validator

import (
	"encoding/json"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateCallActivities(proc *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, ca := range proc.CallActivities {
		errs = append(errs, validateCalledElement(ca, defs)...)
	}
	return errs
}

func validateCalledElement(ca bpmncore.BPMNCallActivity, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
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
	var errs []domain.BPMNValidationError
	errs = append(errs, validateDeptsMapping(ca, calledProc)...)
	return errs
}

func validateDeptsMapping(ca bpmncore.BPMNCallActivity, calledProc *bpmncore.BPMNProcess) []domain.BPMNValidationError {
	m := ca.ExtensionElements.IOMapping
	if len(calledProc.LaneSet.Lanes) == 0 && !hasDeptIDInput(m) {
		return AppendErr(nil, domain.BPMNErrMissingDeptInputForModule, ca.ID,
			fmt.Sprintf("callActivity references processId %q which has no lanes; a dept_id input mapping is required (add <zeebe:ioMapping><zeebe:input source=\"<dept>\" target=\"dept_id\"/></zeebe:ioMapping>)", calledProc.ID))
	}
	if m == nil {
		return nil
	}
	for _, in := range m.Inputs {
		if in.Target != "Depts" {
			continue
		}
		var check map[string]string
		if err := json.Unmarshal([]byte(in.Source), &check); err != nil {
			return AppendWarn(nil, domain.BPMNErrInvalidZeebeProperty, ca.ID,
				fmt.Sprintf("callActivity Depts mapping source is not valid JSON (map[string]string): %v", err))
		}
	}
	return nil
}

func hasDeptIDInput(m *bpmncore.ZeebeIOMapping) bool {
	if m == nil {
		return false
	}
	for _, in := range m.Inputs {
		if in.Target == "dept_id" {
			return true
		}
	}
	return false
}
