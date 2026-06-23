package bpmncore

// Graph is the directed graph representation of a BPMN process.
type Graph struct {
	Outgoing map[string][]string
	Incoming map[string][]string
	NodeType map[string]FlowNodeType
	NodeIDs  map[string]struct{}
}

// BuildGraph constructs a Graph from a BPMNProcess.
func BuildGraph(proc *BPMNProcess) *Graph {
	g := &Graph{
		Outgoing: make(map[string][]string),
		Incoming: make(map[string][]string),
		NodeType: make(map[string]FlowNodeType),
		NodeIDs:  make(map[string]struct{}),
	}
	register := func(id string, kind FlowNodeType) {
		g.NodeType[id] = kind
		g.NodeIDs[id] = struct{}{}
	}
	for _, e := range proc.StartEvents {
		register(e.ID, NodeTypeStartEvent)
	}
	for _, e := range proc.EndEvents {
		register(e.ID, NodeTypeEndEvent)
	}
	for _, t := range proc.UserTasks {
		register(t.ID, NodeTypeUserTask)
	}
	for _, t := range proc.SendTasks {
		register(t.ID, NodeTypeSendTask)
	}
	for _, t := range proc.ReceiveTasks {
		register(t.ID, NodeTypeReceiveTask)
	}
	for _, gw := range proc.ParallelGateways {
		register(gw.ID, NodeTypeParallelGateway)
	}
	for _, gw := range proc.ExclusiveGateways {
		register(gw.ID, NodeTypeExclusiveGateway)
	}
	for _, gw := range proc.InclusiveGateways {
		register(gw.ID, NodeTypeInclusiveGateway)
	}
	for _, sp := range proc.SubProcesses {
		register(sp.ID, NodeTypeSubProcess)
	}
	for _, be := range proc.BoundaryEvents {
		register(be.ID, NodeTypeBoundaryEvent)
	}
	for _, sf := range proc.SequenceFlows {
		g.Outgoing[sf.SourceRef] = append(g.Outgoing[sf.SourceRef], sf.TargetRef)
		g.Incoming[sf.TargetRef] = append(g.Incoming[sf.TargetRef], sf.SourceRef)
	}
	return g
}

// BuildLaneLabels maps lane name to itself (identity); the lane name is the
// authoritative department identifier used as both ID and display label.
func BuildLaneLabels(proc *BPMNProcess) map[string]string {
	labels := make(map[string]string, len(proc.LaneSet.Lanes))
	for _, lane := range proc.LaneSet.Lanes {
		labels[lane.Name] = lane.Name
	}
	return labels
}

// BuildCondExprs maps (sourceRef, targetRef) to conditionExpression for flows
// that carry a <bpmn:conditionExpression> child element.
func BuildCondExprs(proc *BPMNProcess) map[[2]string]string {
	exprs := make(map[[2]string]string)
	for _, sf := range proc.SequenceFlows {
		if sf.ConditionExpression != "" {
			exprs[[2]string{sf.SourceRef, sf.TargetRef}] = sf.ConditionExpression
		}
	}
	return exprs
}

// FindJoin walks forward from a split gateway to find the matching join gateway.
func FindJoin(splitID string, g *Graph, backEdges map[[2]string]bool) (string, bool) {
	curr, ok := FirstForward(splitID, g, backEdges)
	if !ok {
		return "", false
	}
	visited := make(map[string]bool)
	for {
		if visited[curr] {
			return "", false
		}
		visited[curr] = true
		if len(g.Incoming[curr]) > 1 {
			return curr, true
		}
		next, ok := FirstForward(curr, g, backEdges)
		if !ok {
			return "", false
		}
		curr = next
	}
}

// FirstForward returns the first outgoing non-back-edge neighbour of node.
func FirstForward(node string, g *Graph, backEdges map[[2]string]bool) (string, bool) {
	for _, n := range g.Outgoing[node] {
		if !backEdges[[2]string{node, n}] {
			return n, true
		}
	}
	return "", false
}

// ForwardReaches returns true if target is reachable from start via forward edges.
func ForwardReaches(start, target string, g *Graph, backEdges map[[2]string]bool) bool {
	visited := make(map[string]bool)
	var walk func(node string) bool
	walk = func(node string) bool {
		if node == target {
			return true
		}
		if visited[node] {
			return false
		}
		visited[node] = true
		for _, next := range g.Outgoing[node] {
			if backEdges[[2]string{node, next}] {
				continue
			}
			if walk(next) {
				return true
			}
		}
		return false
	}
	return walk(start)
}

// BuildLaneRefSet returns the set of node IDs that appear in any lane's flowNodeRef list.
func BuildLaneRefSet(proc *BPMNProcess) map[string]struct{} {
	refs := make(map[string]struct{})
	for _, lane := range proc.LaneSet.Lanes {
		for _, ref := range lane.FlowNodeRefs {
			refs[ref] = struct{}{}
		}
	}
	return refs
}

// FindTask returns the BPMNUserTask with the given ID, or nil.
func FindTask(proc *BPMNProcess, id string) *BPMNUserTask {
	for i := range proc.UserTasks {
		if proc.UserTasks[i].ID == id {
			return &proc.UserTasks[i]
		}
	}
	return nil
}

// FindSubProcess returns the BPMNSubProcess with the given ID, or nil.
func FindSubProcess(proc *BPMNProcess, id string) *BPMNSubProcess {
	for i := range proc.SubProcesses {
		if proc.SubProcesses[i].ID == id {
			return &proc.SubProcesses[i]
		}
	}
	return nil
}

// LaneNameFor returns the name of the lane containing taskID, or "".
func LaneNameFor(taskID string, proc *BPMNProcess) string {
	for _, lane := range proc.LaneSet.Lanes {
		for _, ref := range lane.FlowNodeRefs {
			if ref == taskID {
				return lane.Name
			}
		}
	}
	return ""
}

// OutgoingTargetOf returns the first sequence flow target from nodeID, or "".
func OutgoingTargetOf(nodeID string, proc *BPMNProcess) string {
	for _, sf := range proc.SequenceFlows {
		if sf.SourceRef == nodeID {
			return sf.TargetRef
		}
	}
	return ""
}

// TimerBoundaryFor returns the timer boundary event attached to taskID, or nil.
func TimerBoundaryFor(taskID string, proc *BPMNProcess) *BPMNBoundaryEvent {
	for i := range proc.BoundaryEvents {
		be := &proc.BoundaryEvents[i]
		if be.AttachedToRef == taskID && be.Timer != nil {
			return be
		}
	}
	return nil
}
