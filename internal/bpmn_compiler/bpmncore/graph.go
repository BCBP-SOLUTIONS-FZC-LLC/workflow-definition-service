package bpmncore

type Graph struct {
	Outgoing map[string][]string
	Incoming map[string][]string
	NodeType map[string]FlowNodeType
	NodeIDs  map[string]struct{}
}

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
	for _, ca := range proc.CallActivities {
		register(ca.ID, NodeTypeCallActivity)
	}
	for _, be := range proc.BoundaryEvents {
		switch {
		case be.Timer != nil:
			register(be.ID, NodeTypeTimerBoundaryEvent)
		case be.Error != nil:
			register(be.ID, NodeTypeErrorBoundaryEvent)
		case be.Message != nil:
			register(be.ID, NodeTypeMessageBoundaryEvent)
		default:
			register(be.ID, NodeTypeBoundaryEvent)
		}
	}
	for _, sf := range proc.SequenceFlows {
		g.Outgoing[sf.SourceRef] = append(g.Outgoing[sf.SourceRef], sf.TargetRef)
		g.Incoming[sf.TargetRef] = append(g.Incoming[sf.TargetRef], sf.SourceRef)
	}
	return g
}

func BuildLaneLabels(proc *BPMNProcess) map[string]string {
	labels := make(map[string]string, len(proc.LaneSet.Lanes))
	for _, lane := range proc.LaneSet.Lanes {
		labels[lane.Name] = lane.Name
	}
	return labels
}

func BuildCondExprs(proc *BPMNProcess) map[[2]string]string {
	exprs := make(map[[2]string]string)
	for _, sf := range proc.SequenceFlows {
		if sf.ConditionExpression != "" {
			exprs[[2]string{sf.SourceRef, sf.TargetRef}] = sf.ConditionExpression
		}
	}
	return exprs
}

func FindJoin(splitID string, g *Graph, backEdges map[[2]string]bool) (string, bool) {
	if forwardCount(splitID, g, backEdges) < 2 {
		return "", false
	}
	join := immediatePostDominator(splitID, g, backEdges)
	return join, join != ""
}

func forwardCount(node string, g *Graph, backEdges map[[2]string]bool) int {
	n := 0
	for _, next := range g.Outgoing[node] {
		if !backEdges[[2]string{node, next}] {
			n++
		}
	}
	return n
}

func immediatePostDominator(root string, g *Graph, backEdges map[[2]string]bool) string {
	const (
		onStack = 1
		done    = 2
	)
	state := map[string]int{root: onStack}
	successors := map[string][]string{}
	var order []string
	type frame struct {
		node string
		next int
	}
	stack := []frame{{node: root}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		out := g.Outgoing[top.node]
		if top.next == len(out) {
			state[top.node] = done
			order = append(order, top.node)
			stack = stack[:len(stack)-1]
			continue
		}
		next := out[top.next]
		top.next++
		if backEdges[[2]string{top.node, next}] || state[next] == onStack {
			continue
		}
		successors[top.node] = append(successors[top.node], next)
		if state[next] == 0 {
			state[next] = onStack
			stack = append(stack, frame{node: next})
		}
	}

	parent := map[string]string{}
	depth := map[string]int{"": 0}
	for _, n := range order {
		var meet string
		for i, succ := range successors[n] {
			if i == 0 {
				meet = succ
				continue
			}
			meet = commonAncestor(meet, succ, parent, depth)
		}
		parent[n] = meet
		depth[n] = depth[meet] + 1
	}
	return parent[root]
}

func commonAncestor(a, b string, parent map[string]string, depth map[string]int) string {
	for a != b {
		if depth[a] >= depth[b] {
			a = parent[a]
		} else {
			b = parent[b]
		}
	}
	return a
}

func FirstForward(node string, g *Graph, backEdges map[[2]string]bool) (string, bool) {
	for _, n := range g.Outgoing[node] {
		if !backEdges[[2]string{node, n}] {
			return n, true
		}
	}
	return "", false
}

func BuildLaneRefSet(proc *BPMNProcess) map[string]struct{} {
	refs := make(map[string]struct{})
	for _, lane := range proc.LaneSet.Lanes {
		for _, ref := range lane.FlowNodeRefs {
			refs[ref] = struct{}{}
		}
	}
	return refs
}

func FindTask(proc *BPMNProcess, id string) *BPMNUserTask {
	for i := range proc.UserTasks {
		if proc.UserTasks[i].ID == id {
			return &proc.UserTasks[i]
		}
	}
	return nil
}

func FindSendTask(proc *BPMNProcess, id string) *BPMNSendTask {
	for i := range proc.SendTasks {
		if proc.SendTasks[i].ID == id {
			return &proc.SendTasks[i]
		}
	}
	return nil
}

func FindReceiveTask(proc *BPMNProcess, id string) *BPMNReceiveTask {
	for i := range proc.ReceiveTasks {
		if proc.ReceiveTasks[i].ID == id {
			return &proc.ReceiveTasks[i]
		}
	}
	return nil
}

func FindSubProcess(proc *BPMNProcess, id string) *BPMNSubProcess {
	for i := range proc.SubProcesses {
		if proc.SubProcesses[i].ID == id {
			return &proc.SubProcesses[i]
		}
	}
	return nil
}

func FindCallActivity(proc *BPMNProcess, id string) *BPMNCallActivity {
	for i := range proc.CallActivities {
		if proc.CallActivities[i].ID == id {
			return &proc.CallActivities[i]
		}
	}
	return nil
}

func FindProcessByID(defs *BPMNDefinitions, id string) *BPMNProcess {
	for i := range defs.Processes {
		if defs.Processes[i].ID == id {
			return &defs.Processes[i]
		}
	}
	return findProcessByParticipantID(defs, id)
}

func findProcessByParticipantID(defs *BPMNDefinitions, participantID string) *BPMNProcess {
	if defs.Collaboration == nil {
		return nil
	}
	for _, p := range defs.Collaboration.Participants {
		if p.ID != participantID {
			continue
		}
		for i := range defs.Processes {
			if defs.Processes[i].ID == p.ProcessRef {
				return &defs.Processes[i]
			}
		}
	}
	return nil
}

func LaneFor(taskID string, proc *BPMNProcess) *BPMNLane {
	for i := range proc.LaneSet.Lanes {
		lane := &proc.LaneSet.Lanes[i]
		for _, ref := range lane.FlowNodeRefs {
			if ref == taskID {
				return lane
			}
		}
	}
	return nil
}

func DeptIDFor(taskID string, proc *BPMNProcess) string {
	lane := LaneFor(taskID, proc)
	if lane == nil {
		return ""
	}
	for _, p := range lane.ExtensionElements.ZeebeProps.Items {
		if p.Name == "dept_id" {
			return p.Value
		}
	}
	return ""
}

func FindImplicitStart(proc *BPMNProcess, g *Graph) string {
	if len(proc.StartEvents) > 0 {
		return ""
	}
	var candidates []string
	for id, kind := range g.NodeType {
		if IsBoundaryEventType(kind) || kind == NodeTypeEndEvent {
			continue
		}
		if len(g.Incoming[id]) == 0 && len(g.Outgoing[id]) > 0 {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	return ""
}

func OutgoingTargetOf(nodeID string, proc *BPMNProcess) string {
	for _, sf := range proc.SequenceFlows {
		if sf.SourceRef == nodeID {
			return sf.TargetRef
		}
	}
	return ""
}

func TimerBoundaryFor(taskID string, proc *BPMNProcess) *BPMNBoundaryEvent {
	for i := range proc.BoundaryEvents {
		be := &proc.BoundaryEvents[i]
		if be.AttachedToRef == taskID && be.Timer != nil {
			return be
		}
	}
	return nil
}

func MessageBoundaryFor(taskID string, proc *BPMNProcess) *BPMNBoundaryEvent {
	for i := range proc.BoundaryEvents {
		be := &proc.BoundaryEvents[i]
		if be.AttachedToRef == taskID && be.Message != nil {
			return be
		}
	}
	return nil
}
