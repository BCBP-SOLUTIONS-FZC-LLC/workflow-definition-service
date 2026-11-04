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
				fmt.Sprintf("more than one element has the id %q; every element id in a process, sub-process bodies included, must be unique", id))
		}
		seen[id] = true
	}
	noteScope(note, scopeOf(proc))
	return errs
}

type idScope struct {
	startEvents, endEvents                []bpmncore.BPMNEvent
	userTasks, genericTasks, serviceTasks []bpmncore.BPMNUserTask
	sendTasks                             []bpmncore.BPMNSendTask
	receiveTasks                          []bpmncore.BPMNReceiveTask
	parallel, exclusive, inclusive        []bpmncore.BPMNGateway
	subProcesses                          []bpmncore.BPMNSubProcess
	callActivities                        []bpmncore.BPMNCallActivity
	boundaryEvents                        []bpmncore.BPMNBoundaryEvent
	sequenceFlows                         []bpmncore.BPMNSequenceFlow
}

func scopeOf(proc *bpmncore.BPMNProcess) idScope {
	return idScope{
		startEvents: proc.StartEvents, endEvents: proc.EndEvents,
		userTasks: proc.UserTasks, genericTasks: proc.GenericTasks, serviceTasks: proc.ServiceTasks,
		sendTasks: proc.SendTasks, receiveTasks: proc.ReceiveTasks,
		parallel: proc.ParallelGateways, exclusive: proc.ExclusiveGateways, inclusive: proc.InclusiveGateways,
		subProcesses: proc.SubProcesses, callActivities: proc.CallActivities,
		boundaryEvents: proc.BoundaryEvents, sequenceFlows: proc.SequenceFlows,
	}
}

func subProcessScope(sp bpmncore.BPMNSubProcess) idScope {
	return idScope{
		startEvents: sp.StartEvents, endEvents: sp.EndEvents,
		userTasks: sp.UserTasks, genericTasks: sp.GenericTasks, serviceTasks: sp.ServiceTasks,
		sendTasks: sp.SendTasks, receiveTasks: sp.ReceiveTasks,
		parallel: sp.ParallelGateways, exclusive: sp.ExclusiveGateways, inclusive: sp.InclusiveGateways,
		subProcesses: sp.SubProcesses, callActivities: sp.CallActivities,
		boundaryEvents: sp.BoundaryEvents, sequenceFlows: sp.SequenceFlows,
	}
}

func noteScope(note func(string), s idScope) {
	for _, e := range s.startEvents {
		note(e.ID)
	}
	for _, e := range s.endEvents {
		note(e.ID)
	}
	for _, t := range s.userTasks {
		note(t.ID)
	}
	for _, t := range s.genericTasks {
		note(t.ID)
	}
	for _, t := range s.serviceTasks {
		note(t.ID)
	}
	for _, t := range s.sendTasks {
		note(t.ID)
	}
	for _, t := range s.receiveTasks {
		note(t.ID)
	}
	for _, gw := range s.parallel {
		note(gw.ID)
	}
	for _, gw := range s.exclusive {
		note(gw.ID)
	}
	for _, gw := range s.inclusive {
		note(gw.ID)
	}
	for _, ca := range s.callActivities {
		note(ca.ID)
	}
	for _, be := range s.boundaryEvents {
		note(be.ID)
	}
	for _, sf := range s.sequenceFlows {
		note(sf.ID)
	}
	for _, sp := range s.subProcesses {
		note(sp.ID)
		noteScope(note, subProcessScope(sp))
	}
}
