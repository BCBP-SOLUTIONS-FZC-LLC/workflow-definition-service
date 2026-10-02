package bpmncore

import (
	"maps"
	"slices"
)

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

// FindJoin returns the join of a split: its immediate post-dominator in the
// forward graph, the nearest node every forward path from the split passes
// through. Paths that end at different end events share none, and a node
// with fewer than two forward flows is no split, so neither has a join.
func FindJoin(splitID string, g *Graph, backEdges map[[2]string]bool) (string, bool) {
	forward := func(n string) []string {
		return slices.DeleteFunc(slices.Clone(g.Outgoing[n]), func(next string) bool { return backEdges[[2]string{n, next}] })
	}
	if len(forward(splitID)) < 2 {
		return "", false
	}
	pdom := map[string]map[string]bool{}
	var postDominators func(n string) map[string]bool
	postDominators = func(n string) map[string]bool {
		if set, seen := pdom[n]; seen {
			return set
		}
		pdom[n] = map[string]bool{}
		var common map[string]bool
		for _, next := range forward(n) {
			nextSet := postDominators(next)
			if common == nil {
				common = maps.Clone(nextSet)
				continue
			}
			maps.DeleteFunc(common, func(k string, _ bool) bool { return !nextSet[k] })
		}
		if common == nil {
			common = map[string]bool{}
		}
		common[n] = true
		pdom[n] = common
		return common
	}
	candidates := maps.Clone(postDominators(splitID))
	delete(candidates, splitID)
	for c := range candidates {
		if len(postDominators(c)) == len(candidates) {
			return c, true
		}
	}
	return "", false
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

// OutgoingTargetOf returns the first sequence flow target from nodeID, or "".
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
