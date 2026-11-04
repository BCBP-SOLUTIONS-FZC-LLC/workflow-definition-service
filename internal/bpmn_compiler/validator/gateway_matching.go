package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateGatewayMatching(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	joins := bpmncore.NewJoins(g, backEdges)
	for _, gw := range proc.ParallelGateways {
		errs = append(errs, checkSplitJoin(gw.ID, "parallel", g, backEdges, joins)...)
	}
	for _, gw := range proc.ExclusiveGateways {
		errs = append(errs, checkSplitJoin(gw.ID, "exclusive", g, backEdges, joins)...)
	}
	for _, gw := range proc.InclusiveGateways {
		errs = append(errs, checkSplitJoin(gw.ID, "inclusive", g, backEdges, joins)...)
	}
	return errs
}

func checkSplitJoin(
	gwID, kind string, g *bpmncore.Graph, backEdges map[[2]string]bool, joins *bpmncore.Joins,
) []domain.BPMNValidationError {
	if forwardOutCount(gwID, g, backEdges) <= 1 || joins.ReachesUnbrokenCycle(gwID) {
		return nil
	}
	if _, ok := joins.Find(gwID); ok {
		return nil
	}
	if kind == "exclusive" && allBranchesTerminate(gwID, g, backEdges, joins) {
		return nil
	}
	return AppendErr(nil, domain.BPMNErrUnmatchedGateway, gwID,
		fmt.Sprintf("%s split gateway has no matching join gateway", kind))
}

func allBranchesTerminate(gwID string, g *bpmncore.Graph, backEdges map[[2]string]bool, joins *bpmncore.Joins) bool {
	for _, branch := range g.Outgoing[gwID] {
		if backEdges[[2]string{gwID, branch}] {
			continue
		}
		if !joins.ReachesEndEvent(branch) {
			return false
		}
	}
	return true
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
