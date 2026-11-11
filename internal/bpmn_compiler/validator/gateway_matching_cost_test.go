package validator

import (
	"fmt"
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
)

const (
	bestOfRuns    = 5
	maxGrowth     = 16
	maxTimeGrowth = 24
)

type chainShape int

const (
	chainReachable chainShape = iota
	chainBesideACycle
	chainIntoACycle
)

func exclusiveChainWithExits(gateways int, shape chainShape) (*bpmncore.BPMNProcess, *bpmncore.Graph, map[[2]string]bool) {
	proc := &bpmncore.BPMNProcess{StartEvents: []bpmncore.BPMNEvent{{ID: "S"}}}
	flow := 0
	link := func(from, to string) {
		flow++
		proc.SequenceFlows = append(proc.SequenceFlows,
			bpmncore.BPMNSequenceFlow{ID: fmt.Sprintf("F%d", flow), SourceRef: from, TargetRef: to})
	}
	prev := "S"
	if shape == chainIntoACycle {
		proc.EndEvents = append(proc.EndEvents, bpmncore.BPMNEvent{ID: "SE"})
		link("S", "SE")
		prev = "U"
		proc.ExclusiveGateways = append(proc.ExclusiveGateways, bpmncore.BPMNGateway{ID: "U"})
	}
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
	if shape != chainReachable {
		proc.ExclusiveGateways = append(proc.ExclusiveGateways, bpmncore.BPMNGateway{ID: "UX"}, bpmncore.BPMNGateway{ID: "UY"})
		link("UX", "UY")
		link("UY", "UX")
	}
	if shape == chainIntoACycle {
		link(prev, "UX")
	}
	g := bpmncore.BuildGraph(proc)
	return proc, g, bpmncore.ProcessBackEdges(proc, g, "S")
}

// The bound is on growth, not wall time, so it holds on a slow runner and
// under the race detector. A chain eight times longer allocates about eight
// times the bytes and takes about eight times as long; a search below every
// gateway would be about sixty-four times. Bytes allocated do not depend on
// machine load; the time bound is a loose second check with a wide margin.
func TestValidateGatewayMatching_CostGrowsLinearlyWithTheChain(t *testing.T) {
	tests := []struct {
		name  string
		shape chainShape
	}{
		{name: "every node reachable", shape: chainReachable},
		{name: "beside a cycle no start reaches", shape: chainBesideACycle},
		{name: "unreachable and leading into a cycle", shape: chainIntoACycle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			measure := func(gateways int) (time.Duration, uint64) {
				t.Helper()
				best := time.Duration(math.MaxInt64)
				var bytes uint64
				for attempt := 0; attempt < bestOfRuns; attempt++ {
					proc, g, backEdges := exclusiveChainWithExits(gateways, tt.shape)
					var before, after runtime.MemStats
					runtime.ReadMemStats(&before)
					start := time.Now()
					errs := ValidateGatewayMatching(proc, g, backEdges)
					elapsed := time.Since(start)
					runtime.ReadMemStats(&after)
					if tt.shape == chainReachable && len(errs) != 0 {
						t.Fatalf("a chain of %d exclusive gateways whose branches all end: errors %v", gateways, errs)
					}
					best = min(best, elapsed)
					bytes = after.TotalAlloc - before.TotalAlloc
				}
				return best, bytes
			}

			measure(500)
			shortTime, shortBytes := measure(2000)
			longTime, longBytes := measure(16000)
			if longBytes > maxGrowth*shortBytes {
				t.Errorf("a 16000-gateway chain allocated %d bytes, more than %d times the %d bytes of a 2000-gateway chain",
					longBytes, maxGrowth, shortBytes)
			}
			if longTime > maxTimeGrowth*shortTime {
				t.Errorf("a 16000-gateway chain took %s, more than %d times the %s of a 2000-gateway chain",
					longTime, maxTimeGrowth, shortTime)
			}
		})
	}
}
