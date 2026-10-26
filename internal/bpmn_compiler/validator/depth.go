package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const MaxPathDepth = 2000

const maxPathDepth = MaxPathDepth

func ValidateMaxDepth(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	return validateMaxDepthFrom(proc, g, backEdges, "")
}

func validateMaxDepthFrom(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool, implicitStart string) []domain.BPMNValidationError {
	start := implicitStart
	if len(proc.StartEvents) > 0 {
		start = proc.StartEvents[0].ID
	}
	if start == "" {
		return nil
	}
	memo := make(map[string]int)
	bpmncore.IterativeDFS(g, start, bpmncore.DFSVisitor{
		OnEdge: func(from, to string, _ bool) bool {
			return !backEdges[[2]string{from, to}]
		},
		OnExit: func(v string) {
			best := 1
			for _, next := range g.Outgoing[v] {
				if backEdges[[2]string{v, next}] {
					continue
				}
				if d := 1 + memo[next]; d > best {
					best = d
				}
			}
			memo[v] = best
		},
	})
	if memo[start] > maxPathDepth {
		return AppendErr(nil, domain.BPMNErrMaxDepthExceeded, start,
			fmt.Sprintf("workflow path exceeds the maximum depth of %d nodes", maxPathDepth))
	}
	return nil
}
