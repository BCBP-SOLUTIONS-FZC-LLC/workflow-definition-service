package validator

import (
	"fmt"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// MaxPathDepth is exported for white-box tests.
const MaxPathDepth = 2000

const maxPathDepth = MaxPathDepth

// ValidateReachability is exported for white-box tests.
func ValidateReachability(proc *bpmncore.BPMNProcess, g *bpmncore.Graph) []domain.BPMNValidationError {
	if len(proc.StartEvents) == 0 {
		return nil
	}
	visited := make(map[string]bool)
	bpmncore.MarkReachable(proc.StartEvents[0].ID, g, visited)

	for _, be := range proc.BoundaryEvents {
		bpmncore.MarkReachable(be.ID, g, visited)
	}

	var errs []domain.BPMNValidationError
	for id := range g.NodeIDs {
		if g.NodeType[id] == bpmncore.NodeTypeBoundaryEvent {
			continue
		}
		if !visited[id] {
			errs = AppendErr(errs, domain.BPMNErrUnreachableNode, id,
				"node is not reachable from the start event")
		}
	}
	return errs
}

// ValidateLoops permits guarded rework loops and rejects unguarded ones.
// Exported for white-box tests.
func ValidateLoops(g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError

	for edge := range backEdges {
		src, dst := edge[0], edge[1]
		if src == dst {
			errs = AppendErr(errs, domain.BPMNErrUnguardedLoop, src,
				"self-loop is not allowed; model a loop via an exclusive gateway")
			continue
		}
		if g.NodeType[src] != bpmncore.NodeTypeExclusiveGateway {
			errs = AppendErr(errs, domain.BPMNErrUnguardedLoop, src,
				"back-edge (revert flow) must originate at an exclusive gateway")
			continue
		}
		if _, ok := bpmncore.FirstForward(src, g, backEdges); !ok {
			errs = AppendErr(errs, domain.BPMNErrUnguardedLoop, src,
				"exclusive gateway has no forward exit branch; loop cannot terminate")
		}
	}

	for _, scc := range bpmncore.TarjanSCC(g) {
		if len(scc) <= 1 {
			continue
		}
		if !sccHasGuardedExit(scc, g) {
			errs = AppendErr(errs, domain.BPMNErrCycleDetected, strings.Join(scc, ","),
				fmt.Sprintf("unguarded loop among nodes: %s", strings.Join(scc, ", ")))
		}
	}
	return errs
}

func sccHasGuardedExit(scc []string, g *bpmncore.Graph) bool {
	inSCC := make(map[string]bool, len(scc))
	for _, n := range scc {
		inSCC[n] = true
	}
	for _, n := range scc {
		if g.NodeType[n] != bpmncore.NodeTypeExclusiveGateway {
			continue
		}
		for _, next := range g.Outgoing[n] {
			if !inSCC[next] {
				return true
			}
		}
	}
	return false
}

// ValidateMaxDepth checks that the longest forward path does not exceed MaxPathDepth.
// Exported for white-box tests.
func ValidateMaxDepth(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	if len(proc.StartEvents) == 0 {
		return nil
	}
	memo := make(map[string]int)
	var longest func(node string) int
	longest = func(node string) int {
		if d, ok := memo[node]; ok {
			return d
		}
		memo[node] = 1
		best := 1
		for _, next := range g.Outgoing[node] {
			if backEdges[[2]string{node, next}] {
				continue
			}
			if d := 1 + longest(next); d > best {
				best = d
			}
		}
		memo[node] = best
		return best
	}
	if longest(proc.StartEvents[0].ID) > maxPathDepth {
		return AppendErr(nil, domain.BPMNErrMaxDepthExceeded, proc.StartEvents[0].ID,
			fmt.Sprintf("workflow path exceeds the maximum depth of %d nodes", maxPathDepth))
	}
	return nil
}

// ValidateGatewayMatching is exported for white-box tests.
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
		if kind == "exclusive" && allBranchesTerminate(gwID, g, backEdges) {
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
		if kind == "exclusive" && branchTerminatesAtEndEvent(branch, g, backEdges) {
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
	visited := make(map[string]bool)
	var walk func(node string) bool
	walk = func(node string) bool {
		if visited[node] {
			return false
		}
		visited[node] = true
		if g.NodeType[node] == bpmncore.NodeTypeEndEvent {
			return true
		}
		for _, next := range g.Outgoing[node] {
			if backEdges[[2]string{node, next}] {
				continue
			}
			if walk(next) {
				return true
			}
		}
		return false
	}
	return walk(start)
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

// ValidateConditionExprs is exported for white-box tests.
func ValidateConditionExprs(proc *bpmncore.BPMNProcess, g *bpmncore.Graph) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, sf := range proc.SequenceFlows {
		if len(g.Outgoing[sf.SourceRef]) < 2 {
			continue
		}
		cond := sf.ConditionExpression
		if cond == "" {
			continue
		}
		if len(cond) > maxCondExpr {
			errs = AppendErr(errs, domain.BPMNErrInvalidConditionExpression, sf.ID,
				fmt.Sprintf("conditionExpression exceeds %d character limit", maxCondExpr))
		}
	}
	return errs
}
