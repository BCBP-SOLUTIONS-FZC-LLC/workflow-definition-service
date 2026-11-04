package bpmncore

import "sort"

type Joins struct {
	g          *Graph
	backEdges  map[[2]string]bool
	cyclic     map[string]bool
	ipdom      map[string]string
	reachesEnd map[string]bool
}

func NewJoins(g *Graph, backEdges map[[2]string]bool) *Joins {
	w := newForwardWalk(g, backEdges)
	w.walk()
	j := &Joins{
		g:          g,
		backEdges:  backEdges,
		cyclic:     w.cyclic,
		ipdom:      map[string]string{},
		reachesEnd: map[string]bool{},
	}
	j.index(w.order, w.successors)
	return j
}

type lazyJoins struct {
	joins *Joins
}

func (l *lazyJoins) get(g *Graph, backEdges map[[2]string]bool) *Joins {
	if l.joins == nil {
		l.joins = NewJoins(g, backEdges)
	}
	return l.joins
}

func (j *Joins) ReachesUnbrokenCycle(node string) bool {
	return j.cyclic[node]
}

func (j *Joins) Find(splitID string) (string, bool) {
	if j.cyclic[splitID] {
		return FindJoin(splitID, j.g, j.backEdges)
	}
	if forwardCount(splitID, j.g, j.backEdges) < 2 {
		return "", false
	}
	join := j.ipdom[splitID]
	return join, join != ""
}

func (j *Joins) ReachesEndEvent(node string) bool {
	if j.cyclic[node] {
		return reachesEndEvent(node, j.g, j.backEdges)
	}
	return j.reachesEnd[node]
}

func (j *Joins) index(order []string, successors map[string][]string) {
	depth := map[string]int{"": 0}
	for _, n := range order {
		if j.cyclic[n] {
			continue
		}
		var meet string
		reaches := j.g.NodeType[n] == NodeTypeEndEvent
		for i, succ := range successors[n] {
			reaches = reaches || j.reachesEnd[succ]
			if i == 0 {
				meet = succ
				continue
			}
			meet = commonAncestor(meet, succ, j.ipdom, depth)
		}
		j.ipdom[n] = meet
		depth[n] = depth[meet] + 1
		j.reachesEnd[n] = reaches
	}
}

type forwardWalk struct {
	g          *Graph
	backEdges  map[[2]string]bool
	state      map[string]int
	successors map[string][]string
	order      []string
	cyclic     map[string]bool
}

const (
	walkOnStack = 1
	walkDone    = 2
)

func newForwardWalk(g *Graph, backEdges map[[2]string]bool) *forwardWalk {
	return &forwardWalk{
		g:          g,
		backEdges:  backEdges,
		state:      map[string]int{},
		successors: map[string][]string{},
		cyclic:     map[string]bool{},
	}
}

func (w *forwardWalk) walk() {
	roots := make([]string, 0, len(w.g.Outgoing))
	for n := range w.g.Outgoing {
		roots = append(roots, n)
	}
	sort.Strings(roots)
	for _, root := range roots {
		if w.state[root] == 0 {
			w.from(root)
		}
	}
}

func (w *forwardWalk) from(root string) {
	type frame struct {
		node string
		next int
	}
	w.state[root] = walkOnStack
	stack := []frame{{node: root}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		out := w.g.Outgoing[top.node]
		if top.next == len(out) {
			w.finish(top.node)
			stack = stack[:len(stack)-1]
			continue
		}
		next := out[top.next]
		top.next++
		if w.backEdges[[2]string{top.node, next}] {
			continue
		}
		if w.state[next] == walkOnStack {
			w.cyclic[top.node] = true
			continue
		}
		w.successors[top.node] = append(w.successors[top.node], next)
		if w.state[next] == 0 {
			w.state[next] = walkOnStack
			stack = append(stack, frame{node: next})
		}
	}
}

func (w *forwardWalk) finish(node string) {
	w.state[node] = walkDone
	w.order = append(w.order, node)
	for _, succ := range w.successors[node] {
		if w.cyclic[succ] {
			w.cyclic[node] = true
			return
		}
	}
}

func reachesEndEvent(start string, g *Graph, backEdges map[[2]string]bool) bool {
	found := false
	IterativeDFS(g, start, DFSVisitor{
		OnEnter: func(v string) bool {
			if g.NodeType[v] == NodeTypeEndEvent {
				found = true
			}
			return !found
		},
		OnEdge: func(from, to string, _ bool) bool {
			return !backEdges[[2]string{from, to}] && !found
		},
	})
	return found
}
