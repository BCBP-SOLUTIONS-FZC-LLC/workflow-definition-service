package bpmncore

import (
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"testing"
	"time"
)

func referenceFindJoin(splitID string, g *Graph, backEdges map[[2]string]bool) (string, bool) {
	forward := func(n string) []string {
		return slices.DeleteFunc(slices.Clone(g.Outgoing[n]), func(next string) bool { return backEdges[[2]string{n, next}] })
	}
	if len(forward(splitID)) < 2 {
		return "", false
	}
	pdom := map[string]map[string]bool{}
	var postDominators func(n string) map[string]bool
	postDominators = func(n string) map[string]bool {
		if set, seen := pdom[n]; seen {
			return set
		}
		pdom[n] = map[string]bool{}
		var common map[string]bool
		for _, next := range forward(n) {
			nextSet := postDominators(next)
			if common == nil {
				common = maps.Clone(nextSet)
				continue
			}
			maps.DeleteFunc(common, func(k string, _ bool) bool { return !nextSet[k] })
		}
		if common == nil {
			common = map[string]bool{}
		}
		common[n] = true
		pdom[n] = common
		return common
	}
	candidates := maps.Clone(postDominators(splitID))
	delete(candidates, splitID)
	for c := range candidates {
		if len(postDominators(c)) == len(candidates) {
			return c, true
		}
	}
	return "", false
}

func randomForwardDAG(r *rand.Rand, nodes int) *Graph {
	g := &Graph{Outgoing: map[string][]string{}}
	for i := 0; i < nodes; i++ {
		from := fmt.Sprintf("n%02d", i)
		out := r.Intn(4)
		seen := map[int]bool{}
		for k := 0; k < out && i < nodes-1; k++ {
			to := i + 1 + r.Intn(nodes-i-1)
			if seen[to] {
				continue
			}
			seen[to] = true
			g.Outgoing[from] = append(g.Outgoing[from], fmt.Sprintf("n%02d", to))
		}
	}
	return g
}

func TestFindJoin_AgreesWithTheReferenceOnRandomForwardGraphs(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 3000; trial++ {
		g := randomForwardDAG(r, 3+r.Intn(14))
		for i := 0; i < len(g.Outgoing)+1; i++ {
			split := fmt.Sprintf("n%02d", i)
			wantJoin, wantOK := referenceFindJoin(split, g, nil)
			gotJoin, gotOK := FindJoin(split, g, nil)
			if gotJoin != wantJoin || gotOK != wantOK {
				t.Fatalf("trial %d split %s on %v: got (%q, %v), want (%q, %v)", trial, split, g.Outgoing, gotJoin, gotOK, wantJoin, wantOK)
			}
		}
	}
}

func TestFindJoin_IgnoresBackEdges(t *testing.T) {
	g := &Graph{Outgoing: map[string][]string{
		"S": {"A", "B"}, "A": {"J"}, "B": {"J"}, "J": {"X"}, "X": {"E", "S"},
	}}
	back := map[[2]string]bool{{"X", "S"}: true}

	join, ok := FindJoin("S", g, back)
	wantJoin, wantOK := referenceFindJoin("S", g, back)
	if join != wantJoin || ok != wantOK || join != "J" {
		t.Errorf("FindJoin = (%q, %v), reference (%q, %v), want J", join, ok, wantJoin, wantOK)
	}
}

func TestFindJoin_ACycleDoesNotHang(t *testing.T) {
	g := &Graph{Outgoing: map[string][]string{"S": {"A", "B"}, "A": {"B"}, "B": {"A"}}}

	done := make(chan struct{})
	go func() {
		FindJoin("S", g, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("FindJoin did not return on a cyclic graph")
	}
}

func chainAfterSplit(length int) *Graph {
	g := &Graph{Outgoing: map[string][]string{"S": {"A", "B"}, "A": {"c0"}, "B": {"c0"}}}
	for i := 0; i < length; i++ {
		next := fmt.Sprintf("c%d", i+1)
		g.Outgoing[fmt.Sprintf("c%d", i)] = []string{next}
	}
	return g
}

func TestFindJoin_ALongChainIsLinear(t *testing.T) {
	g := chainAfterSplit(30000)

	start := time.Now()
	join, ok := FindJoin("S", g, nil)
	elapsed := time.Since(start)

	if !ok || join != "c0" {
		t.Fatalf("FindJoin = (%q, %v), want c0", join, ok)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("a chain of 30000 nodes took %s; the join must not cost more than the nodes after the split", elapsed)
	}
}
