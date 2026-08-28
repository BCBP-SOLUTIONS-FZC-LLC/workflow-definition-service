package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateGatewayMatching(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, gw := range proc.ParallelGateways {
		errs = append(errs, checkSplitJoin(gw.ID, "parallel", g, backEdges)...)
	}
	for _, gw := range proc.ExclusiveGateways {
		errs = append(errs, checkSplitJoin(gw.ID, "exclusive", g, backEdges)...)
	}
	for _, gw := range proc.InclusiveGateways {
		errs = append(errs, checkSplitJoin(gw.ID, "inclusive", g, backEdges)...)
	}
	return errs
}

func checkSplitJoin(gwID, kind string, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	if forwardOutCount(gwID, g, backEdges) <= 1 {
		return nil
	}
	joinID, ok := bpmncore.FindJoin(gwID, g, backEdges)
	if !ok {
		if allBranchesTerminate(gwID, g, backEdges) {
			return nil
		}
		return AppendErr(nil, domain.BPMNErrUnmatchedGateway, gwID,
			fmt.Sprintf("%s split gateway has no matching join gateway", kind))
	}
	for _, branch := range g.Outgoing[gwID] {
		if backEdges[[2]string{gwID, branch}] {
			continue
		}
		if bpmncore.ForwardReaches(branch, joinID, g, backEdges) {
			continue
		}
		if branchTerminatesAtEndEvent(branch, g, backEdges) {
			continue
		}
		return AppendErr(nil, domain.BPMNErrUnmatchedGateway, gwID,
			fmt.Sprintf("%s split gateway has a branch that does not reconverge at its join", kind))
	}
	return nil
}

func allBranchesTerminate(gwID string, g *bpmncore.Graph, backEdges map[[2]string]bool) bool {
	for _, branch := range g.Outgoing[gwID] {
		if backEdges[[2]string{gwID, branch}] {
			continue
		}
		if !branchTerminatesAtEndEvent(branch, g, backEdges) {
			return false
		}
	}
	return true
}

func branchTerminatesAtEndEvent(start string, g *bpmncore.Graph, backEdges map[[2]string]bool) bool {
	found := false
	bpmncore.IterativeDFS(g, start, bpmncore.DFSVisitor{
		OnEnter: func(v string) bool {
			if g.NodeType[v] == bpmncore.NodeTypeEndEvent {
				found = true
			}
			return !found
		},
		OnEdge: func(from, to string, _ bool) bool {
			return !backEdges[[2]string{from, to}] && !found
		},
	})
	return found
}

func forwardOutCount(node string, g *bpmncore.Graph, backEdges map[[2]string]bool) int {
	n := 0
	for _, next := range g.Outgoing[node] {
		if !backEdges[[2]string{node, next}] {
			n++
		}
	}
	return n
}
