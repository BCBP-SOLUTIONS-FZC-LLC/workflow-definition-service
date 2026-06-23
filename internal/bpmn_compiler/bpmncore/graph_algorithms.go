package bpmncore

// ClassifyBackEdges returns the set of back-edges (revert/loop flows) reachable
// from start. An edge (u → v) is a back-edge when v is an ancestor of u on the
// active DFS stack. Removing back-edges yields the forward DAG used by
// reachability, loop, gateway-matching, and compilation.
func ClassifyBackEdges(g *Graph, start string) map[[2]string]bool {
	backEdges := make(map[[2]string]bool)
	onStack := make(map[string]bool)
	visited := make(map[string]bool)

	var visit func(v string)
	visit = func(v string) {
		visited[v] = true
		onStack[v] = true
		for _, w := range g.Outgoing[v] {
			if onStack[w] {
				backEdges[[2]string{v, w}] = true
				continue
			}
			if !visited[w] {
				visit(w)
			}
		}
		onStack[v] = false
	}

	visit(start)
	return backEdges
}

func MarkReachable(start string, g *Graph, visited map[string]bool) {
	stack := []string{start}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[v] {
			continue
		}
		visited[v] = true
		stack = append(stack, g.Outgoing[v]...)
	}
}

type sccState struct {
	index   int
	lowlink int
	onStack bool
}

type tarjanRunner struct {
	nodeStates map[string]*sccState
	stack      []string
	sccs       [][]string
	nextIdx    int
}

func (r *tarjanRunner) strongconnect(g *Graph, v string) {
	s := &sccState{index: r.nextIdx, lowlink: r.nextIdx, onStack: true}
	r.nodeStates[v] = s
	r.nextIdx++
	r.stack = append(r.stack, v)

	for _, w := range g.Outgoing[v] {
		if _, seen := r.nodeStates[w]; !seen {
			r.strongconnect(g, w)
			if r.nodeStates[w].lowlink < s.lowlink {
				s.lowlink = r.nodeStates[w].lowlink
			}
		} else if r.nodeStates[w].onStack {
			if r.nodeStates[w].index < s.lowlink {
				s.lowlink = r.nodeStates[w].index
			}
		}
	}

	if s.lowlink == s.index {
		var scc []string
		for {
			w := r.stack[len(r.stack)-1]
			r.stack = r.stack[:len(r.stack)-1]
			r.nodeStates[w].onStack = false
			scc = append(scc, w)
			if w == v {
				break
			}
		}
		r.sccs = append(r.sccs, scc)
	}
}

// computes strongly connected components of g using Tarjan's algorithm.
func TarjanSCC(g *Graph) [][]string {
	r := &tarjanRunner{nodeStates: make(map[string]*sccState, len(g.NodeIDs))}
	for id := range g.NodeIDs {
		if _, seen := r.nodeStates[id]; !seen {
			r.strongconnect(g, id)
		}
	}
	return r.sccs
}
