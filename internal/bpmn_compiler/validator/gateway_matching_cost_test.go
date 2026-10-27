package validator

import (
	"fmt"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
)

func exclusiveChainWithExits(gateways int) (*bpmncore.BPMNProcess, *bpmncore.Graph, map[[2]string]bool) {
	proc := &bpmncore.BPMNProcess{StartEvents: []bpmncore.BPMNEvent{{ID: "S"}}}
	flow := 0
	link := func(from, to string) {
		flow++
		proc.SequenceFlows = append(proc.SequenceFlows,
			bpmncore.BPMNSequenceFlow{ID: fmt.Sprintf("F%d", flow), SourceRef: from, TargetRef: to})
	}
	prev := "S"
	for i := 0; i < gateways; i++ {
		gw, exit := fmt.Sprintf("G%d", i), fmt.Sprintf("X%d", i)
		proc.ExclusiveGateways = append(proc.ExclusiveGateways, bpmncore.BPMNGateway{ID: gw})
		proc.EndEvents = append(proc.EndEvents, bpmncore.BPMNEvent{ID: exit})
		link(prev, gw)
		link(gw, exit)
		prev = gw
	}
	proc.EndEvents = append(proc.EndEvents, bpmncore.BPMNEvent{ID: "E"})
	link(prev, "E")
	g := bpmncore.BuildGraph(proc)
	return proc, g, bpmncore.ProcessBackEdges(proc, g, "S")
}

// The bound is on growth, not wall time, so it holds on a slow runner and
// under the race detector: a chain four times longer takes about four times
// as long, where a search below every gateway takes sixteen.
func TestValidateGatewayMatching_CostGrowsLinearlyWithTheChain(t *testing.T) {
	measure := func(gateways int) time.Duration {
		t.Helper()
		proc, g, backEdges := exclusiveChainWithExits(gateways)
		start := time.Now()
		errs := ValidateGatewayMatching(proc, g, backEdges)
		elapsed := time.Since(start)
		if len(errs) != 0 {
			t.Fatalf("a chain of %d exclusive gateways whose branches all end: errors %v", gateways, errs)
		}
		return elapsed
	}

	measure(500)
	short := measure(2000)
	long := measure(8000)
	if long > 8*short {
		t.Errorf("an 8000-gateway chain took %s, more than 8 times the %s of a 2000-gateway chain", long, short)
	}
}
