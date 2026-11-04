package bpmncore

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

func randomGraph(r *rand.Rand, nodes int, cyclic bool) *Graph {
	g := &Graph{Outgoing: map[string][]string{}, NodeType: map[string]FlowNodeType{}}
	name := func(i int) string { return fmt.Sprintf("n%d", i) }
	for i := 0; i < nodes; i++ {
		g.Outgoing[name(i)] = nil
		if r.IntN(4) == 0 {
			g.NodeType[name(i)] = NodeTypeEndEvent
		}
	}
	for i := 0; i < nodes; i++ {
		for k := r.IntN(3); k > 0; k-- {
			target := i + 1 + r.IntN(nodes)
			if cyclic && r.IntN(5) == 0 {
				target = r.IntN(nodes)
			}
			if target < nodes {
				g.Outgoing[name(i)] = append(g.Outgoing[name(i)], name(target))
			}
		}
	}
	return g
}

func TestJoins_AgreeWithTheSearchBelowEachSplit(t *testing.T) {
	tests := []struct {
		name   string
		cyclic bool
	}{
		{name: "forward graph without cycles", cyclic: false},
		{name: "cycles no back edge breaks", cyclic: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := rand.New(rand.NewPCG(7, 11))
			shared, fallback := 0, 0
			for round := 0; round < 200; round++ {
				g := randomGraph(r, 3+r.IntN(25), tt.cyclic)
				backEdges := ClassifyBackEdges(g, "n0")
				joins := NewJoins(g, backEdges)
				for node := range g.Outgoing {
					if joins.cyclic[node] {
						fallback++
					} else {
						shared++
					}
					wantJoin, wantOK := FindJoin(node, g, backEdges)
					gotJoin, gotOK := joins.Find(node)
					if gotJoin != wantJoin || gotOK != wantOK {
						t.Fatalf("round %d, %s: Find = (%q, %v), want (%q, %v)", round, node, gotJoin, gotOK, wantJoin, wantOK)
					}
					if got, want := joins.ReachesEndEvent(node), reachesEndEvent(node, g, backEdges); got != want {
						t.Fatalf("round %d, %s: ReachesEndEvent = %v, want %v", round, node, got, want)
					}
				}
			}
			if shared < 1000 {
				t.Errorf("the shared pass answered only %d nodes", shared)
			}
			if tt.cyclic && fallback < 100 {
				t.Errorf("the per-split search answered only %d nodes", fallback)
			}
		})
	}
}

func TestJoins_ACycleWithoutAnEndEventDoesNotReachOne(t *testing.T) {
	g := &Graph{
		Outgoing: map[string][]string{"x": {"y"}, "y": {"x"}},
		NodeType: map[string]FlowNodeType{},
	}

	if NewJoins(g, map[[2]string]bool{}).ReachesEndEvent("x") {
		t.Error("ReachesEndEvent() = true, want false: the cycle holds no end event")
	}
}
