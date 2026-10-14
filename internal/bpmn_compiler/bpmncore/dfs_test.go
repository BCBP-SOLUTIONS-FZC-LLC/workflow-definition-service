package bpmncore

import (
	"fmt"
	"testing"
	"time"
)

func TestIterativeDFS_NilCallbacks(t *testing.T) {
	g := &Graph{Outgoing: map[string][]string{"a": {"b"}, "b": {}}}
	// Must not panic when all visitor callbacks are nil.
	IterativeDFS(g, "a", DFSVisitor{})
}

func TestIterativeDFS_PruneOnEnter(t *testing.T) {
	g := &Graph{Outgoing: map[string][]string{"a": {"b"}, "b": {"c"}}}
	var visited []string
	IterativeDFS(g, "a", DFSVisitor{
		OnEnter: func(id string) bool {
			visited = append(visited, id)
			return id != "b" // prune b's children
		},
	})
	for _, id := range visited {
		if id == "c" {
			t.Error("expected traversal to be pruned at b, never reaching c")
		}
	}
}

func TestIterativeDFS_SkipEdge(t *testing.T) {
	g := &Graph{Outgoing: map[string][]string{"a": {"b", "c"}}}
	var entered []string
	IterativeDFS(g, "a", DFSVisitor{
		OnEnter: func(id string) bool { entered = append(entered, id); return true },
		OnEdge: func(from, to string, alreadyScheduled bool) bool {
			return to != "b" // skip pushing b
		},
	})
	for _, id := range entered {
		if id == "b" {
			t.Error("expected edge to b to be skipped")
		}
	}
}

func TestIterativeDFS_OnExitCalled(t *testing.T) {
	g := &Graph{Outgoing: map[string][]string{"a": {"b"}}}
	var exited []string
	IterativeDFS(g, "a", DFSVisitor{
		OnExit: func(id string) { exited = append(exited, id) },
	})
	if len(exited) != 2 {
		t.Fatalf("expected OnExit called for both nodes; got %v", exited)
	}
}

func TestIterativeDFS_AlreadyScheduledEdge(t *testing.T) {
	// Diamond shape: a->b, a->c, b->d, c->d. d is scheduled once via b, and the
	// edge c->d must be recognised as "already scheduled" on the second visit.
	g := &Graph{Outgoing: map[string][]string{"a": {"b", "c"}, "b": {"d"}, "c": {"d"}}}
	var alreadySeen []bool
	IterativeDFS(g, "a", DFSVisitor{
		OnEdge: func(from, to string, alreadyScheduled bool) bool {
			if to == "d" {
				alreadySeen = append(alreadySeen, alreadyScheduled)
			}
			return true
		},
	})
	if len(alreadySeen) != 2 || alreadySeen[0] == alreadySeen[1] {
		t.Errorf("expected exactly one already-scheduled and one fresh edge into d; got %v", alreadySeen)
	}
}

func TestIterativeDFS_EntersANodeWhenItIsReached(t *testing.T) {
	g := &Graph{Outgoing: map[string][]string{"a": {"b", "c"}, "b": {"c"}}}
	var order []string
	IterativeDFS(g, "a", DFSVisitor{
		OnEnter: func(id string) bool { order = append(order, "in:"+id); return true },
		OnExit:  func(id string) { order = append(order, "out:"+id) },
	})
	want := []string{"in:a", "in:b", "in:c", "out:c", "out:b", "out:a"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Errorf("order = %v, want %v: a depth-first walk reaches c through b before it returns to a", order, want)
	}
}

func TestIterativeDFS_LongChainAndWideFanOutAreLinear(t *testing.T) {
	chain := &Graph{Outgoing: map[string][]string{}}
	for i := 0; i < 50000; i++ {
		chain.Outgoing[fmt.Sprintf("n%d", i)] = []string{fmt.Sprintf("n%d", i+1)}
	}
	fan := &Graph{Outgoing: map[string][]string{"root": nil}}
	for i := 0; i < 50000; i++ {
		leaf := fmt.Sprintf("l%d", i)
		fan.Outgoing["root"] = append(fan.Outgoing["root"], leaf)
		fan.Outgoing[leaf] = []string{"sink"}
	}
	for name, tc := range map[string]struct {
		g     *Graph
		start string
	}{"chain": {chain, "n0"}, "fan-out": {fan, "root"}} {
		t.Run(name, func(t *testing.T) {
			entered := 0
			begin := time.Now()
			IterativeDFS(tc.g, tc.start, DFSVisitor{OnEnter: func(string) bool { entered++; return true }})
			if elapsed := time.Since(begin); elapsed > 2*time.Second {
				t.Errorf("walking %d nodes took %s", entered, elapsed)
			}
			if entered < 50000 {
				t.Errorf("entered %d nodes, want every node", entered)
			}
		})
	}
}
