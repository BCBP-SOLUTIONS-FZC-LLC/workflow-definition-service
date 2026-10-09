package bpmncore

type DFSVisitor struct {
	OnEnter func(nodeID string) bool
	OnEdge  func(from, to string, alreadyScheduled bool) bool
	OnExit  func(nodeID string)
}

func IterativeDFS(g *Graph, start string, v DFSVisitor) {
	type frame struct {
		id          string
		pendingExit bool
	}
	scheduled := make(map[string]bool)
	scheduled[start] = true
	stack := []frame{{id: start}}

	for len(stack) > 0 {
		n := len(stack) - 1
		id, pendingExit := stack[n].id, stack[n].pendingExit

		if pendingExit {
			stack = stack[:n]
			if v.OnExit != nil {
				v.OnExit(id)
			}
			continue
		}

		stack[n].pendingExit = true

		if v.OnEnter != nil && !v.OnEnter(id) {
			stack = stack[:n]
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
