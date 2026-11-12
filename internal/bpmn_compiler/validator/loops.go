package validator

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateLoops(g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError

	for _, edge := range sortedBackEdges(backEdges) {
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

	for _, scc := range sortedSCCs(g) {
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

func sortedBackEdges(backEdges map[[2]string]bool) [][2]string {
	edges := slices.Collect(maps.Keys(backEdges))
	slices.SortFunc(edges, func(a, b [2]string) int { return slices.Compare(a[:], b[:]) })
	return edges
}

func sortedSCCs(g *bpmncore.Graph) [][]string {
	sccs := bpmncore.TarjanSCC(g)
	for _, scc := range sccs {
		slices.Sort(scc)
	}
	slices.SortFunc(sccs, slices.Compare[[]string])
	return sccs
}

func sccHasGuardedExit(scc []string, g *bpmncore.Graph) bool {
	inSCC := make(map[string]bool, len(scc))
	for _, n := range scc {
		inSCC[n] = true
	}
	for _, n := range scc {
		t := g.NodeType[n]
		if !canExitSCCViaOutgoingEdge(t) {
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

var sccExitNodeTypes = []bpmncore.FlowNodeType{
	bpmncore.NodeTypeExclusiveGateway,
	bpmncore.NodeTypeInclusiveGateway,
	bpmncore.NodeTypeParallelGateway,
	bpmncore.NodeTypeReceiveTask,
}

func canExitSCCViaOutgoingEdge(t bpmncore.FlowNodeType) bool {
	return slices.Contains(sccExitNodeTypes, t)
}

var revertWorkNodes = []bpmncore.FlowNodeType{
	bpmncore.NodeTypeUserTask, bpmncore.NodeTypeSendTask, bpmncore.NodeTypeReceiveTask,
	bpmncore.NodeTypeSubProcess, bpmncore.NodeTypeCallActivity,
}

func ValidateRevertTargets(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	skipped := bpmncore.SkippedTasks(proc, g)
	for _, edge := range sortedBackEdges(backEdges) {
		if g.NodeType[edge[0]] != bpmncore.NodeTypeExclusiveGateway {
			continue
		}
		unsupported := false
		for _, target := range revertReach(edge[1], edge[0], g, backEdges, skipped) {
			if t := g.NodeType[target]; t == bpmncore.NodeTypeSubProcess || t == bpmncore.NodeTypeCallActivity {
				unsupported = true
				errs = AppendErr(errs, domain.BPMNErrUnsupportedRevertTarget, edge[0],
					fmt.Sprintf("revert flow leads back to %s %q; a revert runs its target's department again, so put a userTask before it and loop back to that", t, target))
			}
		}
		if !unsupported && revertTarget(edge[1], g, backEdges, skipped) == "" {
			errs = AppendErr(errs, domain.BPMNErrUnsupportedRevertTarget, edge[0],
				fmt.Sprintf("revert flow to %q reaches no task along its first path; a revert runs its target's department again, so loop back to a task, or to a gateway whose first branch leads to one", edge[1]))
		}
	}
	return errs
}

func revertReach(start, source string, g *bpmncore.Graph, backEdges map[[2]string]bool, skipped map[string]bool) []string {
	var reached []string
	seen := map[string]bool{source: true}
	for stack := []string{start}; len(stack) > 0; {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[node] {
			continue
		}
		seen[node] = true
		if !skipped[node] && slices.Contains(revertWorkNodes, g.NodeType[node]) {
			reached = append(reached, node)
			continue
		}
		next := g.Outgoing[node]
		for i := len(next) - 1; i >= 0; i-- {
			if !backEdges[[2]string{node, next[i]}] {
				stack = append(stack, next[i])
			}
		}
	}
	return reached
}

func revertTarget(node string, g *bpmncore.Graph, backEdges map[[2]string]bool, skipped map[string]bool) string {
	visited := map[string]bool{}
	for curr := node; curr != "" && !visited[curr]; {
		visited[curr] = true
		if !skipped[curr] && slices.Contains(revertWorkNodes, g.NodeType[curr]) {
			return curr
		}
		next, ok := bpmncore.FirstForward(curr, g, backEdges)
		if !ok {
			return ""
		}
		curr = next
	}
	return ""
}
