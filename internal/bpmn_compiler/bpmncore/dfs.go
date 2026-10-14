package bpmncore

type DFSVisitor struct {
	OnEnter func(nodeID string) bool
	OnEdge  func(from, to string, alreadyScheduled bool) bool
	OnExit  func(nodeID string)
}

func IterativeDFS(g *Graph, start string, v DFSVisitor) {
	type frame struct {
		id      string
		entered bool
		next    int
	}
	visited := map[string]bool{start: true}
	stack := []frame{{id: start}}

	for len(stack) > 0 {
		top := &stack[len(stack)-1]

		if !top.entered {
			top.entered = true
			if v.OnEnter != nil && !v.OnEnter(top.id) {
				stack = stack[:len(stack)-1]
				continue
			}
		}

		children := g.Outgoing[top.id]
		if top.next >= len(children) {
			id := top.id
			stack = stack[:len(stack)-1]
			if v.OnExit != nil {
				v.OnExit(id)
			}
			continue
		}

		from, next := top.id, children[top.next]
		top.next++
		alreadySeen := visited[next]
		descend := !alreadySeen
		if v.OnEdge != nil {
			descend = v.OnEdge(from, next, alreadySeen)
		}
		if descend && !alreadySeen {
			visited[next] = true
			stack = append(stack, frame{id: next})
		}
	}
}
