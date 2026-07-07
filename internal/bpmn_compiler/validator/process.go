package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateCounts(proc *bpmncore.BPMNProcess, implicitStart string) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError

	switch n := len(proc.StartEvents); {
	case n == 0:
		// If there is a natural root node (no incoming sequence flows) the
		// process is triggered by a message from another pool — don't error.
		if implicitStart == "" {
			errs = AppendErr(errs, domain.BPMNErrNoStartEvent, "", "process has no start event")
		}
	case n > 1:
		for _, e := range proc.StartEvents[1:] {
			errs = AppendErr(errs, domain.BPMNErrMultipleStartEvents, e.ID,
				"only one start event is allowed")
		}
	}

	if len(proc.EndEvents) == 0 && implicitStart == "" {
		errs = AppendErr(errs, domain.BPMNErrNoEndEvent, "", "process has no end event")
	}

	if len(proc.UserTasks) > maxUserTasks {
		errs = AppendErr(errs, domain.BPMNErrTaskLimitExceeded, "",
			fmt.Sprintf("process exceeds %d user task limit (%d found)",
				maxUserTasks, len(proc.UserTasks)))
	}

	if len(proc.LaneSet.Lanes) > maxLanes {
		errs = AppendErr(errs, domain.BPMNErrLaneLimitExceeded, "",
			fmt.Sprintf("laneSet exceeds %d lane limit (%d found)",
				maxLanes, len(proc.LaneSet.Lanes)))
	}

	return errs
}

func ValidateNoNestedSubProcess(proc *bpmncore.BPMNProcess) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, sp := range proc.SubProcesses {
		for _, nested := range sp.SubProcesses {
			errs = AppendErr(errs, domain.BPMNErrNestedSubProcessNotSupported, nested.ID,
				fmt.Sprintf("subProcess %q contains a nested subProcess %q which is not supported", sp.ID, nested.ID))
		}
	}
	return errs
}

func ValidateSeqFlowRefs(proc *bpmncore.BPMNProcess, g *bpmncore.Graph) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, sf := range proc.SequenceFlows {
		if _, ok := g.NodeIDs[sf.SourceRef]; !ok {
			errs = AppendErr(errs, domain.BPMNErrInvalidSequenceFlowRef, sf.ID,
				fmt.Sprintf("sourceRef %q does not reference a known node", sf.SourceRef))
		}
		if _, ok := g.NodeIDs[sf.TargetRef]; !ok {
			errs = AppendErr(errs, domain.BPMNErrInvalidSequenceFlowRef, sf.ID,
				fmt.Sprintf("targetRef %q does not reference a known node", sf.TargetRef))
		}
	}
	return errs
}

// ValidateDanglingNodes checks that all flow nodes are properly connected.
// implicitStart is exempted from the "no incoming sequence flow" check because
// it is intentionally triggered by a message flow rather than a sequence flow.
func ValidateDanglingNodes(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, implicitStart string) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError

	for _, t := range proc.UserTasks {
		errs = append(errs, checkDanglingWithImplicit(t.ID, g, implicitStart)...)
	}
	for _, t := range proc.SendTasks {
		errs = append(errs, checkDanglingWithImplicit(t.ID, g, implicitStart)...)
	}
	for _, t := range proc.ReceiveTasks {
		errs = append(errs, checkDanglingWithImplicit(t.ID, g, implicitStart)...)
	}
	for _, gw := range proc.ParallelGateways {
		errs = append(errs, CheckDangling(gw.ID, g)...)
	}
	for _, gw := range proc.ExclusiveGateways {
		errs = append(errs, CheckDangling(gw.ID, g)...)
	}
	for _, gw := range proc.InclusiveGateways {
		errs = append(errs, CheckDangling(gw.ID, g)...)
	}
	for _, sp := range proc.SubProcesses {
		errs = append(errs, checkDanglingWithImplicit(sp.ID, g, implicitStart)...)
	}
	for _, ca := range proc.CallActivities {
		errs = append(errs, checkDanglingWithImplicit(ca.ID, g, implicitStart)...)
	}
	errs = append(errs, checkEventDangling(proc, g)...)
	return errs
}

func checkEventDangling(proc *bpmncore.BPMNProcess, g *bpmncore.Graph) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, e := range proc.StartEvents {
		if len(g.Outgoing[e.ID]) == 0 {
			errs = AppendErr(errs, domain.BPMNErrDanglingNode, e.ID,
				"start event has no outgoing sequence flow")
		}
	}
	for _, e := range proc.EndEvents {
		if len(g.Incoming[e.ID]) == 0 {
			errs = AppendErr(errs, domain.BPMNErrDanglingNode, e.ID,
				"end event has no incoming sequence flow")
		}
	}
	for _, be := range proc.BoundaryEvents {
		if len(g.Outgoing[be.ID]) == 0 && be.Message == nil {
			errs = AppendErr(errs, domain.BPMNErrDanglingNode, be.ID,
				"boundary event has no outgoing sequence flow")
		}
	}
	return errs
}

func CheckDangling(id string, g *bpmncore.Graph) []domain.BPMNValidationError {
	return checkDanglingWithImplicit(id, g, "")
}

func checkDanglingWithImplicit(id string, g *bpmncore.Graph, implicitStart string) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	if id != implicitStart && len(g.Incoming[id]) == 0 {
		errs = AppendErr(errs, domain.BPMNErrDanglingNode, id, "node has no incoming sequence flow")
	}
	if len(g.Outgoing[id]) == 0 {
		errs = AppendErr(errs, domain.BPMNErrDanglingNode, id, "node has no outgoing sequence flow")
	}
	return errs
}
