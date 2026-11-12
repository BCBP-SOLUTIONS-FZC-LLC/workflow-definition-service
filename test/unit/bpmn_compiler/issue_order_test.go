package bpmn_compiler_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const orderRepeats = 100

func orderGraph(types map[string]bpmncore.FlowNodeType, edges [][2]string) *bpmncore.Graph {
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{},
		Incoming: map[string][]string{},
		NodeType: types,
		NodeIDs:  map[string]struct{}{},
	}
	for id := range types {
		g.NodeIDs[id] = struct{}{}
	}
	for _, e := range edges {
		g.Outgoing[e[0]] = append(g.Outgoing[e[0]], e[1])
		g.Incoming[e[1]] = append(g.Incoming[e[1]], e[0])
	}
	return g
}

func issueSignature(issues []domain.BPMNValidationError) []string {
	out := make([]string, len(issues))
	for i, issue := range issues {
		out[i] = fmt.Sprintf("%s@%s|%s", issue.Code, issue.NodeID, issue.Message)
	}
	return out
}

func requireSameOrderEveryTime(t *testing.T, run func() []domain.BPMNValidationError, wantSignature []string) {
	t.Helper()
	for i := 0; i < orderRepeats; i++ {
		require.Equal(t, wantSignature, issueSignature(run()), "run %d", i)
	}
}

func TestValidateLoops_BackEdgeAndCycleIssues_AreReportedInSortedOrder(t *testing.T) {
	types := map[string]bpmncore.FlowNodeType{"T": bpmncore.NodeTypeUserTask}
	backEdges := map[[2]string]bool{}
	var wantBackEdges []string
	for _, id := range []string{"P1", "P2", "P3", "P4", "P5", "P6"} {
		types[id] = bpmncore.NodeTypeParallelGateway
		backEdges[[2]string{id, "T"}] = true
		wantBackEdges = append(wantBackEdges, fmt.Sprintf("%s@%s|back-edge (revert flow) must originate at an exclusive gateway", domain.BPMNErrUnguardedLoop, id))
	}
	var edges [][2]string
	var wantCycles []string
	for _, pair := range [][2]string{{"C1", "C2"}, {"A1", "A2"}, {"D1", "D2"}, {"B1", "B2"}} {
		types[pair[0]] = bpmncore.NodeTypeUserTask
		types[pair[1]] = bpmncore.NodeTypeUserTask
		edges = append(edges, [2]string{pair[0], pair[1]}, [2]string{pair[1], pair[0]})
	}
	for _, key := range [][2]string{{"A1", "A2"}, {"B1", "B2"}, {"C1", "C2"}, {"D1", "D2"}} {
		wantCycles = append(wantCycles, fmt.Sprintf("%s@%s,%s|unguarded loop among nodes: %s, %s", domain.BPMNErrCycleDetected, key[0], key[1], key[0], key[1]))
	}
	g := orderGraph(types, edges)

	requireSameOrderEveryTime(t, func() []domain.BPMNValidationError {
		return validator.ValidateLoops(g, backEdges)
	}, append(wantBackEdges, wantCycles...))
}

func TestValidateRevertTargets_Issues_AreReportedInSortedOrder(t *testing.T) {
	types := map[string]bpmncore.FlowNodeType{}
	backEdges := map[[2]string]bool{}
	var want []string
	for _, n := range []string{"1", "2", "3", "4", "5", "6"} {
		gateway, sub := "X"+n, "S"+n
		types[gateway] = bpmncore.NodeTypeExclusiveGateway
		types[sub] = bpmncore.NodeTypeSubProcess
		backEdges[[2]string{gateway, sub}] = true
		want = append(want, fmt.Sprintf("%s@%s|revert flow leads back to %s %q; a revert runs its target's department again, so put a userTask before it and loop back to that",
			domain.BPMNErrUnsupportedRevertTarget, gateway, bpmncore.NodeTypeSubProcess, sub))
	}
	g := orderGraph(types, nil)

	requireSameOrderEveryTime(t, func() []domain.BPMNValidationError {
		return validator.ValidateRevertTargets(&bpmncore.BPMNProcess{}, g, backEdges)
	}, want)
}

func TestValidateReachability_UnreachableNodes_AreReportedInSortedOrder(t *testing.T) {
	types := map[string]bpmncore.FlowNodeType{"Start": bpmncore.NodeTypeStartEvent}
	var want []string
	for _, id := range []string{"U1", "U2", "U3", "U4", "U5", "U6", "U7"} {
		types[id] = bpmncore.NodeTypeUserTask
		want = append(want, fmt.Sprintf("%s@%s|node is not reachable from the start event", domain.BPMNErrUnreachableNode, id))
	}
	g := orderGraph(types, nil)
	proc := &bpmncore.BPMNProcess{StartEvents: []bpmncore.BPMNEvent{{ID: "Start"}}}

	requireSameOrderEveryTime(t, func() []domain.BPMNValidationError {
		return validator.ValidateReachability(proc, g, "")
	}, want)
}
