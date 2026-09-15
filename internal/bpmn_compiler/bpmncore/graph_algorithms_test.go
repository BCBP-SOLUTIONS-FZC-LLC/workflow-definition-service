package bpmncore

import "testing"

func TestClassifyBackEdges(t *testing.T) {
	g := &Graph{
		Outgoing: map[string][]string{
			"a": {"b"},
			"b": {"c"},
			"c": {"a"}, // closes the cycle -> back edge
		},
	}
	backEdges := ClassifyBackEdges(g, "a")
	if !backEdges[[2]string{"c", "a"}] {
		t.Error("expected c->a to be classified as a back edge")
	}
	if backEdges[[2]string{"a", "b"}] {
		t.Error("a->b should not be a back edge")
	}
}

func TestClassifyBackEdges_DiamondNoBackEdges(t *testing.T) {
	// Diamond: a->b, a->c, b->d, c->d. d is reached twice but never while its
	// producer is on the DFS stack, so no back edge should be recorded.
	g := &Graph{
		Outgoing: map[string][]string{
			"a": {"b", "c"},
			"b": {"d"},
			"c": {"d"},
		},
	}
	backEdges := ClassifyBackEdges(g, "a")
	if len(backEdges) != 0 {
		t.Errorf("expected no back edges in a DAG; got %v", backEdges)
	}
}

func TestMarkReachable(t *testing.T) {
	g := &Graph{Outgoing: map[string][]string{"a": {"b", "c"}, "b": {"c"}}}
	visited := map[string]bool{}
	MarkReachable("a", g, visited)
	for _, id := range []string{"a", "b", "c"} {
		if !visited[id] {
			t.Errorf("expected %q to be marked reachable", id)
		}
	}
}

func TestMarkReachable_SelfLoopNoInfiniteLoop(t *testing.T) {
	g := &Graph{Outgoing: map[string][]string{"a": {"a"}}}
	visited := map[string]bool{}
	MarkReachable("a", g, visited)
	if !visited["a"] {
		t.Error("expected a to be visited")
	}
}

func TestTarjanSCC_SimpleCycle(t *testing.T) {
	g := &Graph{
		NodeIDs:  map[string]struct{}{"a": {}, "b": {}, "c": {}},
		Outgoing: map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a"}},
	}
	sccs := TarjanSCC(g)
	if len(sccs) != 1 || len(sccs[0]) != 3 {
		t.Fatalf("expected 1 SCC of size 3; got %v", sccs)
	}
}

func TestTarjanSCC_NoCycle(t *testing.T) {
	g := &Graph{
		NodeIDs:  map[string]struct{}{"a": {}, "b": {}},
		Outgoing: map[string][]string{"a": {"b"}},
	}
	sccs := TarjanSCC(g)
	if len(sccs) != 2 {
		t.Fatalf("expected 2 singleton SCCs; got %v", sccs)
	}
}
