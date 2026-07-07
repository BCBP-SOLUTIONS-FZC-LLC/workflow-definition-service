package validator

import (
	"fmt"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// exported for white-box tests.
const MaxPathDepth = 2000

const maxPathDepth = MaxPathDepth

func ValidateReachability(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, implicitStart string) []domain.BPMNValidationError {
	startID := ""
	if len(proc.StartEvents) > 0 {
		startID = proc.StartEvents[0].ID
	} else if implicitStart != "" {
		startID = implicitStart
	}
	if startID == "" {
		return nil
	}
	visited := make(map[string]bool)
	bpmncore.MarkReachable(startID, g, visited)

	for _, be := range proc.BoundaryEvents {
		bpmncore.MarkReachable(be.ID, g, visited)
	}

	var errs []domain.BPMNValidationError
	for id := range g.NodeIDs {
		if bpmncore.IsBoundaryEventType(g.NodeType[id]) {
			continue
		}
		if !visited[id] {
			errs = AppendErr(errs, domain.BPMNErrUnreachableNode, id,
				"node is not reachable from the start event")
		}
	}
	return errs
}

func ValidateLoops(g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError

	for edge := range backEdges {
		src, dst := edge[0], edge[1]
		if src == dst {
			errs = AppendErr(errs, domain.BPMNErrUnguardedLoop, src,
				"self-loop is not allowed; model a loop via an exclusive gateway")
			continue
		}
		switch g.NodeType[src] {
		case bpmncore.NodeTypeExclusiveGateway:
			if _, ok := bpmncore.FirstForward(src, g, backEdges); !ok {
				errs = AppendErr(errs, domain.BPMNErrUnguardedLoop, src,
					"exclusive gateway has no forward exit branch; loop cannot terminate")
			}
		case bpmncore.NodeTypeReceiveTask:
			// A receive task as a loop-back source is externally guarded: the
			// process only continues when an inbound message is received. This is
			// a valid rework pattern in multi-pool BPMN (e.g. "wait for missing
			// docs, then retry").
		case bpmncore.NodeTypeExternalParticipant:
			// An external participant node is a synthetic bridge for an ignored pool.
			// The loop is guarded by the external system — it only continues when
			// the external participant sends a message back.
		case bpmncore.NodeTypeStartEvent, bpmncore.NodeTypeEndEvent,
			bpmncore.NodeTypeUserTask, bpmncore.NodeTypeSendTask,
			bpmncore.NodeTypeParallelGateway, bpmncore.NodeTypeInclusiveGateway,
			bpmncore.NodeTypeSubProcess, bpmncore.NodeTypeCallActivity,
			bpmncore.NodeTypeBoundaryEvent, bpmncore.NodeTypeTimerBoundaryEvent,
			bpmncore.NodeTypeErrorBoundaryEvent, bpmncore.NodeTypeMessageBoundaryEvent:
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

func sccHasGuardedExit(scc []string, g *bpmncore.Graph) bool {
	inSCC := make(map[string]bool, len(scc))
	for _, n := range scc {
		inSCC[n] = true
	}
	for _, n := range scc {
		switch g.NodeType[n] {
		case bpmncore.NodeTypeExclusiveGateway,
			bpmncore.NodeTypeInclusiveGateway,
			bpmncore.NodeTypeParallelGateway:
			// Any gateway with at least one branch leaving the cycle is a
			// sufficient guard — the process can exit the loop on that branch.
		case bpmncore.NodeTypeReceiveTask:
			// A receive task in the SCC guards the loop via an external message
			// trigger; the cycle can only continue when a message arrives, and
			// any branch out of the SCC counts as an exit.
		case bpmncore.NodeTypeExternalParticipant:
			// A synthetic external participant bridge in the SCC means the cycle
			// crosses an ignored pool boundary. The external system guards the
			// loop — it only resumes when a message is sent back to this process.
			return true
		case bpmncore.NodeTypeStartEvent, bpmncore.NodeTypeEndEvent,
			bpmncore.NodeTypeUserTask, bpmncore.NodeTypeSendTask,
			bpmncore.NodeTypeSubProcess, bpmncore.NodeTypeCallActivity,
			bpmncore.NodeTypeBoundaryEvent, bpmncore.NodeTypeTimerBoundaryEvent,
			bpmncore.NodeTypeErrorBoundaryEvent, bpmncore.NodeTypeMessageBoundaryEvent:
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

func ValidateMaxDepth(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	if len(proc.StartEvents) == 0 {
		return nil
	}
	memo := make(map[string]int)
	bpmncore.IterativeDFS(g, proc.StartEvents[0].ID, bpmncore.DFSVisitor{
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
	if memo[proc.StartEvents[0].ID] > maxPathDepth {
		return AppendErr(nil, domain.BPMNErrMaxDepthExceeded, proc.StartEvents[0].ID,
			fmt.Sprintf("workflow path exceeds the maximum depth of %d nodes", maxPathDepth))
	}
	return nil
}

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
