package bpmn_compiler

// dfs performs a depth-first search to find all reachable nodes.
func dfs(id string, g *graph, visited map[string]bool) {
	if visited[id] {
		return
	}
	visited[id] = true
	for _, next := range g.outgoing[id] {
		dfs(next, g, visited)
	}
}

// tarjanSCC returns the strongly connected components of the graph.
func tarjanSCC(g *graph) [][]string {
	type state struct {
		index   int
		lowlink int
		onStack bool
	}
	states := make(map[string]*state, len(g.nodeIDs))
	var stack []string
	var sccs [][]string
	idx := 0

	var strongconnect func(v string)
	strongconnect = func(v string) {
		s := &state{index: idx, lowlink: idx, onStack: true}
		states[v] = s
		idx++
		stack = append(stack, v)

		for _, w := range g.outgoing[v] {
			if _, visited := states[w]; !visited {
				strongconnect(w)
				if states[w].lowlink < s.lowlink {
					s.lowlink = states[w].lowlink
				}
			} else if states[w].onStack {
				if states[w].index < s.lowlink {
					s.lowlink = states[w].index
				}
			}
		}

		if s.lowlink == s.index {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				states[w].onStack = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}

	for id := range g.nodeIDs {
		if _, visited := states[id]; !visited {
			strongconnect(id)
		}
	}
	return sccs
}
