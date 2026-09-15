package validator

import (
	"fmt"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateLoops(g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError

	for edge := range backEdges {
		src, dst := edge[0], edge[1]
		if src == dst {
			errs = AppendErr(errs, domain.BPMNErrUnguardedLoop, src,
				"self-loop is not allowed; model a loop via an exclusive gateway")
			continue
		}
		switch t := g.NodeType[src]; {
		case t == bpmncore.NodeTypeExclusiveGateway:
			if _, ok := bpmncore.FirstForward(src, g, backEdges); !ok {
				errs = AppendErr(errs, domain.BPMNErrUnguardedLoop, src,
					"exclusive gateway has no forward exit branch; loop cannot terminate")
			}
		case isExternallyGuardedLoopSource(t):
		default:
			errs = AppendErr(errs, domain.BPMNErrUnguardedLoop, src,
				"back-edge (revert flow) must originate at an exclusive gateway")
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

// isExternallyGuardedLoopSource reports whether a back-edge sourced at this
// node type is gated by something outside the sequence-flow graph (a receive
// task's inbound message, or an external participant's message back into an
// ignored pool), so it needs no exclusive gateway to be considered terminable.
func isExternallyGuardedLoopSource(t bpmncore.FlowNodeType) bool {
	return t == bpmncore.NodeTypeReceiveTask || t == bpmncore.NodeTypeExternalParticipant
}

func sccHasGuardedExit(scc []string, g *bpmncore.Graph) bool {
	inSCC := make(map[string]bool, len(scc))
	for _, n := range scc {
		inSCC[n] = true
	}
	for _, n := range scc {
		t := g.NodeType[n]
		if t == bpmncore.NodeTypeExternalParticipant {
			return true
		}
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

func canExitSCCViaOutgoingEdge(t bpmncore.FlowNodeType) bool {
	switch t { //nolint:exhaustive // deliberate allowlist: every other node type can't exit an SCC via an outgoing edge
	case bpmncore.NodeTypeExclusiveGateway, bpmncore.NodeTypeInclusiveGateway,
		bpmncore.NodeTypeParallelGateway, bpmncore.NodeTypeReceiveTask:
		return true
	default:
		return false
	}
}
