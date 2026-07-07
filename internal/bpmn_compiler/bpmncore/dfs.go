package bpmncore

// optional callbacks
type DFSVisitor struct {
	// Return false to prune this node's children and suppress OnExit.
	OnEnter func(nodeID string) bool
	// Return false to skip this edge; return true to push if not already scheduled.
	OnEdge func(from, to string, alreadyScheduled bool) bool
	// Not called when OnEnter returned false for this node.
	OnExit func(nodeID string)
}

func IterativeDFS(g *Graph, start string, v DFSVisitor) {
	type frame struct {
		id   string
		done bool // true = all children pushed; OnExit pending
	}
	scheduled := make(map[string]bool)
	scheduled[start] = true
	stack := []frame{{id: start}}

	for len(stack) > 0 {
		n := len(stack) - 1
		id, done := stack[n].id, stack[n].done

		if done {
			stack = stack[:n]
			if v.OnExit != nil {
				v.OnExit(id)
			}
			continue
		}

		stack[n].done = true // mark pending exit before pushing children

		if v.OnEnter != nil && !v.OnEnter(id) {
			stack = stack[:n] // prune: skip children and OnExit
			continue
		}

		for _, next := range g.Outgoing[id] {
			alreadySeen := scheduled[next]
			push := !alreadySeen
			if v.OnEdge != nil {
				push = v.OnEdge(id, next, alreadySeen)
			}
			if push && !alreadySeen {
				scheduled[next] = true
				stack = append(stack, frame{id: next})
			}
		}
	}
}
