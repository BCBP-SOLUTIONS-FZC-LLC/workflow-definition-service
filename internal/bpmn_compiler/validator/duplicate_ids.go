package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateDuplicateIDs(proc *bpmncore.BPMNProcess) []domain.BPMNValidationError {
	seen := make(map[string]bool)
	reported := make(map[string]bool)
	var errs []domain.BPMNValidationError
	note := func(id string) {
		if id == "" {
			return
		}
		if seen[id] && !reported[id] {
			reported[id] = true
			errs = AppendErr(errs, domain.BPMNErrDuplicateElementID, id,
				fmt.Sprintf("more than one element has the id %q; every element id in a process must be unique", id))
		}
		seen[id] = true
	}
	for _, e := range proc.StartEvents {
		note(e.ID)
	}
	for _, e := range proc.EndEvents {
		note(e.ID)
	}
	for _, t := range proc.UserTasks {
		note(t.ID)
	}
	for _, t := range proc.GenericTasks {
		note(t.ID)
	}
	for _, t := range proc.ServiceTasks {
		note(t.ID)
	}
	for _, t := range proc.SendTasks {
		note(t.ID)
	}
	for _, t := range proc.ReceiveTasks {
		note(t.ID)
	}
	for _, gw := range proc.ParallelGateways {
		note(gw.ID)
	}
	for _, gw := range proc.ExclusiveGateways {
		note(gw.ID)
	}
	for _, gw := range proc.InclusiveGateways {
		note(gw.ID)
	}
	for _, sp := range proc.SubProcesses {
		note(sp.ID)
	}
	for _, ca := range proc.CallActivities {
		note(ca.ID)
	}
	for _, be := range proc.BoundaryEvents {
		note(be.ID)
	}
	for _, sf := range proc.SequenceFlows {
		note(sf.ID)
	}
	return errs
}
