package bpmn_compiler

const defaultTaskQueue = "wf-queue-default"

var stageActivities = map[string]string{
	"prep":    "PrepActivity",
	"review":  "ReviewActivity",
	"approve": "ApproveActivity",
}

type graph struct {
	outgoing map[string][]string
	incoming map[string][]string
	nodeType map[string]FlowNodeType
	nodeIDs  map[string]struct{}
}

// buildGraph constructs the graph representation of sequence flows from a BPMN process.
func buildGraph(proc *bpmnProcess) *graph {
	g := &graph{
		outgoing: make(map[string][]string),
		incoming: make(map[string][]string),
		nodeType: make(map[string]FlowNodeType),
		nodeIDs:  make(map[string]struct{}),
	}
	register := func(id string, kind FlowNodeType) {
		g.nodeType[id] = kind
		g.nodeIDs[id] = struct{}{}
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
	for _, gw := range proc.ParallelGateways {
		register(gw.ID, NodeTypeParallelGateway)
	}
	for _, gw := range proc.ExclusiveGateways {
		register(gw.ID, NodeTypeExclusiveGateway)
	}
	for _, sf := range proc.SequenceFlows {
		g.outgoing[sf.SourceRef] = append(g.outgoing[sf.SourceRef], sf.TargetRef)
		g.incoming[sf.TargetRef] = append(g.incoming[sf.TargetRef], sf.SourceRef)
	}
	return g
}

func buildLaneIndex(proc *bpmnProcess) map[string]bpmnLane {
	idx := make(map[string]bpmnLane)
	for _, lane := range proc.LaneSet.Lanes {
		for _, ref := range lane.FlowNodeRefs {
			idx[ref] = lane
		}
	}
	return idx
}

func buildCondExprs(proc *bpmnProcess) map[[2]string]string {
	exprs := make(map[[2]string]string)
	for _, sf := range proc.SequenceFlows {
		props := propsMap(sf.ExtensionElements)
		if cond, ok := props["condition_expression"]; ok && cond != "" {
			exprs[[2]string{sf.SourceRef, sf.TargetRef}] = cond
		}
	}
	return exprs
}

// findJoin walks forward from a split gateway to find the matching join gateway.
func findJoin(splitID string, g *graph) (string, bool) {
	branches := g.outgoing[splitID]
	if len(branches) == 0 {
		return "", false
	}
	curr := branches[0]
	for {
		if len(g.incoming[curr]) > 1 {
			return curr, true
		}
		nexts := g.outgoing[curr]
		if len(nexts) == 0 {
			return "", false
		}
		curr = nexts[0]
	}
}
