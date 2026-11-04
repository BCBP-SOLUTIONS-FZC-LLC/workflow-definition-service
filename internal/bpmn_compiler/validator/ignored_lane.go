package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateIgnoredLanes(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	ignored := map[string]bool{}
	for i := range proc.LaneSet.Lanes {
		if bpmncore.IsIgnoredLane(&proc.LaneSet.Lanes[i]) {
			for _, id := range proc.LaneSet.Lanes[i].FlowNodeRefs {
				ignored[id] = true
			}
		}
	}
	var errs []domain.BPMNValidationError
	for _, ca := range proc.CallActivities {
		if ignored[ca.ID] {
			errs = AppendErr(errs, domain.BPMNErrIgnoredLaneWork, ca.ID,
				"a callActivity runs a module, so it cannot be in a lane marked ignore; move it to a lane of the workflow's own")
		}
	}
	for _, sp := range proc.SubProcesses {
		if ignored[sp.ID] {
			errs = AppendErr(errs, domain.BPMNErrIgnoredLaneWork, sp.ID,
				"a subProcess runs work of the workflow's own, so it cannot be in a lane marked ignore; move it to a lane of the workflow's own")
		}
	}
	for _, t := range proc.UserTasks {
		if _, isConnector := bpmncore.ConnectorType(t.ExtensionElements); isConnector && ignored[t.ID] {
			errs = AppendErr(errs, domain.BPMNErrIgnoredLaneWork, t.ID,
				"a connector task is automatic work of the workflow's own, so it cannot be in a lane marked ignore; move it to a lane of the workflow's own")
		}
	}
	skipped := bpmncore.SkippedTasks(proc, g)
	for _, be := range proc.BoundaryEvents {
		if skipped[be.AttachedToRef] {
			errs = AppendErr(errs, domain.BPMNErrIgnoredLaneWork, be.ID,
				fmt.Sprintf("boundary event is attached to %q, a task in a lane marked ignore, which never runs", be.AttachedToRef))
		}
	}
	for _, sf := range proc.SequenceFlows {
		if backEdges[[2]string{sf.SourceRef, sf.TargetRef}] && !loopRunsWork(sf.TargetRef, sf.SourceRef, g, backEdges, skipped) {
			errs = AppendErr(errs, domain.BPMNErrIgnoredLaneWork, sf.ID,
				fmt.Sprintf("flow loops back to %q, but the way back to %q holds only tasks in lanes marked ignore, which never run; loop back to a task of the workflow's own", sf.TargetRef, sf.SourceRef))
		}
	}
	return errs
}

func loopRunsWork(target, gateway string, g *bpmncore.Graph, backEdges map[[2]string]bool, skipped map[string]bool) bool {
	seen := map[string]bool{gateway: true}
	for queue := []string{target}; len(queue) > 0; queue = queue[1:] {
		n := queue[0]
		if seen[n] {
			continue
		}
		seen[n] = true
		switch t := g.NodeType[n]; {
		case skipped[n]:
		case t == bpmncore.NodeTypeUserTask, t == bpmncore.NodeTypeSendTask, t == bpmncore.NodeTypeReceiveTask,
			t == bpmncore.NodeTypeSubProcess, t == bpmncore.NodeTypeCallActivity:
			return true
		}
		for _, next := range g.Outgoing[n] {
			if !backEdges[[2]string{n, next}] {
				queue = append(queue, next)
			}
		}
	}
	return false
}
