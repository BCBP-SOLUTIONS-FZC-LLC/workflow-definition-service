package validator

import (
	"fmt"
	"slices"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func validateRepeatedProperties(defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for i := range defs.Processes {
		p := &defs.Processes[i]
		errs = append(errs, repeatedIn(p.ID, p.ExtensionElements)...)
		errs = append(errs, repeatedInScope(p.LaneSet, p.UserTasks, p.GenericTasks, p.ServiceTasks, p.SendTasks, p.ReceiveTasks, p.CallActivities, p.SequenceFlows)...)
		errs = append(errs, repeatedInSubProcesses(p.SubProcesses)...)
	}
	return errs
}

func repeatedInSubProcesses(subs []bpmncore.BPMNSubProcess) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for i := range subs {
		sp := &subs[i]
		errs = append(errs, repeatedIn(sp.ID, sp.ExtensionElements)...)
		errs = append(errs, repeatedInScope(sp.LaneSet, sp.UserTasks, sp.GenericTasks, sp.ServiceTasks, sp.SendTasks, sp.ReceiveTasks, sp.CallActivities, sp.SequenceFlows)...)
		errs = append(errs, repeatedInSubProcesses(sp.SubProcesses)...)
	}
	return errs
}

func repeatedInScope(
	lanes bpmncore.BPMNLaneSet,
	userTasks, genericTasks, serviceTasks []bpmncore.BPMNUserTask,
	sendTasks []bpmncore.BPMNSendTask,
	receiveTasks []bpmncore.BPMNReceiveTask,
	calls []bpmncore.BPMNCallActivity,
	flows []bpmncore.BPMNSequenceFlow,
) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, l := range lanes.Lanes {
		errs = append(errs, repeatedIn(l.ID, l.ExtensionElements)...)
	}
	for _, t := range slices.Concat(userTasks, genericTasks, serviceTasks) {
		errs = append(errs, repeatedIn(t.ID, t.ExtensionElements)...)
	}
	for _, t := range sendTasks {
		errs = append(errs, repeatedIn(t.ID, t.ExtensionElements)...)
	}
	for _, t := range receiveTasks {
		errs = append(errs, repeatedIn(t.ID, t.ExtensionElements)...)
	}
	for _, c := range calls {
		errs = append(errs, repeatedIn(c.ID, c.ExtensionElements)...)
	}
	for _, f := range flows {
		errs = append(errs, repeatedIn(f.ID, f.ExtensionElements)...)
	}
	return errs
}

func repeatedIn(id string, ext bpmncore.BPMNExtensionElements) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	seen := map[string]bool{}
	reported := map[string]bool{}
	for _, p := range ext.ZeebeProps.Items {
		if seen[p.Name] && !reported[p.Name] {
			reported[p.Name] = true
			errs = AppendErr(errs, domain.BPMNErrInvalidZeebeProperty, id,
				fmt.Sprintf("the zeebe property %q is written more than once on this element; keep one, because the readers of a property do not agree on which value counts", p.Name))
		}
		seen[p.Name] = true
	}
	return errs
}
