package bpmncore

import (
	"fmt"
	"slices"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"
)

type GatewayKind bool

const (
	ParallelGateway  GatewayKind = false
	ExclusiveGateway GatewayKind = true
)

func (s *CompileState) ForwardNexts(node string) []string {
	outs := s.G.Outgoing[node]
	fwd := make([]string, 0, len(outs))
	for _, n := range outs {
		if !s.backEdges[[2]string{node, n}] {
			fwd = append(fwd, n)
		}
	}
	return fwd
}

func (s *CompileState) EnsureDept(id, label, iamDeptID string) {
	if _, ok := s.sd.deptIdx[id]; ok {
		return
	}
	s.sd.deptIdx[id] = len(s.sd.depts)
	s.sd.depts = append(s.sd.depts, dsl.DepartmentDef{ID: id, Label: label, IAMDepartmentID: iamDeptID})
}

func (s *CompileState) AppendStage(deptID string, stage dsl.StageDef) {
	i := s.sd.deptIdx[deptID]
	s.sd.depts[i].Stages = append(s.sd.depts[i].Stages, stage)
	s.sd.stages++
}

func (s *CompileState) AddToSeqBuf(deptID string) {
	if !slices.Contains(s.seqBuf, deptID) {
		s.seqBuf = append(s.seqBuf, deptID)
	}
}

func (s *CompileState) FlushSeqBuf() {
	s.openLane, s.openSegment = "", ""
	if len(s.seqBuf) == 0 {
		return
	}
	buf := make([]string, len(s.seqBuf))
	copy(buf, s.seqBuf)
	s.steps = append(s.steps, dsl.ExecutionStep{Sequential: buf})
	s.seqBuf = s.seqBuf[:0]
}

func (s *CompileState) TraverseNode(nodeID string) error {
	if s.StopAt != "" && nodeID == s.StopAt {
		return nil
	}
	if s.stopAtCompiled && s.sd.visited[nodeID] {
		if s.rejoined != nil && s.G.NodeType[nodeID] != NodeTypeEndEvent {
			*s.rejoined = true
		}
		return nil
	}
	if s.visited[nodeID] {
		return nil
	}
	s.sd.nodes++
	if s.sd.stages > StageBudget || s.sd.nodes > nodeBudget {
		return ErrStageBudget
	}
	s.visited[nodeID] = true
	s.sd.visited[nodeID] = true

	if s.skipped[nodeID] {
		return s.traverseForwardNexts(nodeID)
	}
	if h, ok := s.Elements[s.G.NodeType[nodeID]]; ok {
		return h.Compile(nodeID, s)
	}
	return s.traverseForwardNexts(nodeID)
}

func (s *CompileState) traverseForwardNexts(nodeID string) error {
	for _, next := range s.ForwardNexts(nodeID) {
		if err := s.TraverseNode(next); err != nil {
			return err
		}
	}
	return nil
}

func (s *CompileState) HandleGateway(nodeID string, kind GatewayKind) error {
	fwd := s.ForwardNexts(nodeID)
	backTargets := s.backNexts(nodeID)

	if len(fwd) <= 1 && len(backTargets) == 0 {
		if len(fwd) == 1 {
			return s.TraverseNode(fwd[0])
		}
		return nil
	}

	s.FlushSeqBuf()

	if len(fwd) <= 1 {
		return s.handleSingleForwardWithReverts(nodeID, fwd, backTargets)
	}

	joinID, ok := FindJoin(nodeID, s.G, s.backEdges)
	if !ok {
		return s.handleSplitNoJoin(nodeID, fwd, backTargets, kind)
	}
	return s.handleSplitWithJoin(nodeID, backTargets, joinID, kind)
}

func (s *CompileState) handleSingleForwardWithReverts(
	nodeID string, fwd, backTargets []string,
) error {
	step := dsl.ExecutionStep{}
	if len(fwd) == 1 {
		step.Exclusive = append(step.Exclusive, dsl.ExclusiveBranch{
			ConditionExpression: s.condExprs[[2]string{nodeID, fwd[0]}],
			Terminates:          s.G.NodeType[fwd[0]] == NodeTypeEndEvent,
		})
	}
	step.Exclusive = append(step.Exclusive, s.revertBranches(nodeID, backTargets)...)
	s.steps = append(s.steps, step)
	if len(fwd) == 1 {
		return s.TraverseNode(fwd[0])
	}
	return nil
}

func (s *CompileState) handleSplitNoJoin(
	nodeID string, fwd, backTargets []string, kind GatewayKind,
) error {
	if kind != ExclusiveGateway {
		return fmt.Errorf("split gateway %q has no matching join", nodeID)
	}
	step := dsl.ExecutionStep{}
	for _, branchStart := range fwd {
		b, err := s.exclusiveBranch(nodeID, branchStart, "")
		if err != nil {
			return err
		}
		b.Terminates = true
		step.Exclusive = append(step.Exclusive, b)
	}
	step.Exclusive = append(step.Exclusive, s.revertBranches(nodeID, backTargets)...)
	s.steps = append(s.steps, step)
	return nil
}

func (s *CompileState) handleSplitWithJoin(
	nodeID string, backTargets []string, joinID string, kind GatewayKind,
) error {
	var step dsl.ExecutionStep
	var err error
	if kind == ExclusiveGateway {
		step.Exclusive, err = s.traverseExclusiveBranches(nodeID, joinID)
	} else {
		step.Parallel, err = s.traverseBranches(nodeID, joinID)
	}
	if err != nil {
		return err
	}
	step.Exclusive = append(step.Exclusive, s.revertBranches(nodeID, backTargets)...)
	s.steps = append(s.steps, step)
	return s.TraverseNode(joinID)
}

func (s *CompileState) FillBoundaryTimerTarget(taskID string, stage *dsl.StageDef) {
	if stage.BoundaryTimer == nil {
		return
	}
	if be := TimerBoundaryFor(taskID, s.Proc); be != nil {
		stage.BoundaryTimer.BoundaryID = be.ID
		stage.BoundaryTimer.TargetDept, stage.BoundaryTimer.Terminates = s.BoundaryTarget(be.ID)
	}
}

func (s *CompileState) FillBoundaryMessageTarget(taskID string, stage *dsl.StageDef) {
	if stage.BoundaryMessage == nil {
		return
	}
	if be := MessageBoundaryFor(taskID, s.Proc); be != nil {
		stage.BoundaryMessage.TargetDept, stage.BoundaryMessage.Terminates = s.BoundaryTarget(be.ID)
	}
}

func (s *CompileState) BoundaryTarget(boundaryID string) (dept string, terminates bool) {
	node := s.taskAhead(OutgoingTargetOf(boundaryID, s.Proc))
	if node == "" {
		return "", true
	}
	return segmentOfPending(node), false
}

func (s *CompileState) CompileBoundaryPaths() error {
	for _, be := range s.Proc.BoundaryEvents {
		node := s.taskAhead(OutgoingTargetOf(be.ID, s.Proc))
		if node == "" {
			continue
		}
		if _, compiled := s.sd.nodeSegment[node]; compiled {
			continue
		}
		path := NewBranchState(s, "")
		path.stopAtCompiled = true
		path.rejoined = new(bool)
		if err := path.TraverseNode(OutgoingTargetOf(be.ID, s.Proc)); err != nil {
			return fmt.Errorf("boundary continuation from %q: %w", be.ID, err)
		}
		if !*path.rejoined {
			path.FlushSeqBuf()
			s.sd.boundarySteps[be.ID] = path.CollectedSteps()
		}
	}
	return nil
}

func (s *CompileState) ResolveErrorCode(errorRef string) string {
	if errorRef == "" || s.Defs == nil {
		return ""
	}
	for _, e := range s.Defs.Errors {
		if e.ID == errorRef {
			return e.ErrorCode
		}
	}
	return ""
}

func (s *CompileState) backNexts(node string) []string {
	var targets []string
	for _, n := range s.G.Outgoing[node] {
		if s.backEdges[[2]string{node, n}] {
			targets = append(targets, n)
		}
	}
	return targets
}

func (s *CompileState) revertBranches(
	gatewayID string,
	backTargets []string,
) []dsl.ExclusiveBranch {
	branches := make([]dsl.ExclusiveBranch, 0, len(backTargets))
	for _, backTarget := range backTargets {
		nodeID, name := s.firstTaskNodeAhead(backTarget)
		dept, onPath := s.pathSegment[nodeID]
		if !onPath {
			dept = segmentOfPending(nodeID)
		}
		branches = append(branches, dsl.ExclusiveBranch{
			ConditionExpression: s.condExprs[[2]string{gatewayID, backTarget}],
			RevertToDept:        dept,
			RevertToStage:       s.stageTypeOf(nodeID),
			RevertToNodeID:      nodeID,
			RevertToName:        name,
		})
	}
	return branches
}

func (s *CompileState) taskAhead(node string) string {
	visited := make(map[string]bool)
	for curr := node; curr != "" && !visited[curr]; {
		visited[curr] = true
		if t := s.G.NodeType[curr]; !s.skipped[curr] && (t == NodeTypeUserTask || t == NodeTypeSubProcess) {
			return curr
		}
		fwd := s.ForwardNexts(curr)
		if len(fwd) == 0 {
			return ""
		}
		curr = fwd[0]
	}
	return ""
}

func (s *CompileState) firstTaskNodeAhead(node string) (id, name string) {
	visited := make(map[string]bool)
	curr := node
	for {
		if visited[curr] {
			return "", ""
		}
		visited[curr] = true
		if !s.skipped[curr] {
			switch s.G.NodeType[curr] {
			case NodeTypeUserTask:
				task := FindTask(s.Proc, curr)
				if task != nil {
					return task.ID, task.Name
				}
				return "", ""
			case NodeTypeSendTask:
				task := FindSendTask(s.Proc, curr)
				if task != nil {
					return task.ID, task.Name
				}
				return "", ""
			case NodeTypeReceiveTask:
				task := FindReceiveTask(s.Proc, curr)
				if task != nil {
					return task.ID, task.Name
				}
				return "", ""
			case NodeTypeSubProcess:
				sp := FindSubProcess(s.Proc, curr)
				if sp != nil {
					return sp.ID, sp.Name
				}
				return "", ""
			case NodeTypeCallActivity:
				ca := FindCallActivity(s.Proc, curr)
				if ca != nil {
					return ca.ID, ca.Name
				}
				return "", ""
			case NodeTypeStartEvent, NodeTypeEndEvent,
				NodeTypeParallelGateway, NodeTypeExclusiveGateway, NodeTypeInclusiveGateway,
				NodeTypeBoundaryEvent, NodeTypeTimerBoundaryEvent, NodeTypeErrorBoundaryEvent,
				NodeTypeMessageBoundaryEvent:
			}
		}
		fwd := s.ForwardNexts(curr)
		if len(fwd) == 0 {
			return "", ""
		}
		curr = fwd[0]
	}
}

func (s *CompileState) stageTypeOf(nodeID string) string {
	switch s.G.NodeType[nodeID] {
	case NodeTypeUserTask:
		if task := FindTask(s.Proc, nodeID); task != nil && task.ExtensionElements.TaskDefinition != nil {
			return task.ExtensionElements.TaskDefinition.Type
		}
	case NodeTypeSendTask:
		return "send_task"
	case NodeTypeReceiveTask:
		return "receive_task"
	case NodeTypeSubProcess, NodeTypeCallActivity:
		return "sub_workflow"
	case NodeTypeStartEvent, NodeTypeEndEvent,
		NodeTypeParallelGateway, NodeTypeExclusiveGateway, NodeTypeInclusiveGateway,
		NodeTypeBoundaryEvent, NodeTypeTimerBoundaryEvent, NodeTypeErrorBoundaryEvent,
		NodeTypeMessageBoundaryEvent:
	}
	return ""
}

func (s *CompileState) traverseBranches(splitID, joinID string) ([]dsl.ParallelBranch, error) {
	var branches []dsl.ParallelBranch
	for _, branchStart := range s.ForwardNexts(splitID) {
		steps, err := s.branchSteps(branchStart, joinID)
		if err != nil {
			return nil, err
		}
		branches = append(branches, dsl.ParallelBranch{DeptID: FirstDepartment(steps), Steps: steps})
	}
	return branches, nil
}

func (s *CompileState) traverseExclusiveBranches(splitID, joinID string) ([]dsl.ExclusiveBranch, error) {
	var branches []dsl.ExclusiveBranch
	for _, branchStart := range s.ForwardNexts(splitID) {
		b, err := s.exclusiveBranch(splitID, branchStart, joinID)
		if err != nil {
			return nil, err
		}
		branches = append(branches, b)
	}
	return branches, nil
}

func (s *CompileState) exclusiveBranch(splitID, branchStart, stopAt string) (dsl.ExclusiveBranch, error) {
	b := dsl.ExclusiveBranch{ConditionExpression: s.condExprs[[2]string{splitID, branchStart}]}
	steps, err := s.branchSteps(branchStart, stopAt)
	if err != nil {
		return b, err
	}
	if len(steps) == 0 {
		return b, nil
	}
	b.Steps = steps
	b.Target = FirstDepartment(steps)
	b.TargetNodeID, b.TargetName = s.firstTaskNodeAhead(branchStart)
	b.TargetStage = s.stageTypeOf(b.TargetNodeID)
	return b, nil
}

func (s *CompileState) branchSteps(branchStart, stopAt string) ([]dsl.ExecutionStep, error) {
	bs := NewBranchState(s, stopAt)
	if err := bs.TraverseNode(branchStart); err != nil {
		return nil, err
	}
	bs.FlushSeqBuf()
	return bs.CollectedSteps(), nil
}
