package validator

import (
	"fmt"
	"slices"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

var singleOutgoingKinds = map[bpmncore.FlowNodeType]string{
	bpmncore.NodeTypeStartEvent:   "start event",
	bpmncore.NodeTypeUserTask:     "task",
	bpmncore.NodeTypeSendTask:     "send task",
	bpmncore.NodeTypeReceiveTask:  "receive task",
	bpmncore.NodeTypeSubProcess:   "sub-process",
	bpmncore.NodeTypeCallActivity: "call activity",
}

func ValidateSingleOutgoing(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	errs := singleOutgoingErrors(g, backEdges)
	for i := range proc.SubProcesses {
		body := bpmncore.SubProcToProcess(&proc.SubProcesses[i])
		bodyGraph := bpmncore.BuildGraph(body)
		bodyBackEdges := map[[2]string]bool{}
		if len(body.StartEvents) > 0 {
			bodyBackEdges = bpmncore.ProcessBackEdges(body, bodyGraph, body.StartEvents[0].ID)
		}
		errs = append(errs, singleOutgoingErrors(bodyGraph, bodyBackEdges)...)
	}
	return errs
}

func singleOutgoingErrors(g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	ids := make([]string, 0, len(g.NodeType))
	for id, kind := range g.NodeType {
		if _, ok := singleOutgoingKinds[kind]; ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)

	var errs []domain.BPMNValidationError
	for _, id := range ids {
		if n := forwardOutCount(id, g, backEdges); n > 1 {
			errs = AppendErr(errs, domain.BPMNErrSplitWithoutGateway, id,
				fmt.Sprintf("%s has %d outgoing flows; a flow splits only at a gateway, so give it one outgoing flow and branch with a parallel, exclusive or inclusive gateway", singleOutgoingKinds[g.NodeType[id]], n))
		}
	}
	return errs
}
