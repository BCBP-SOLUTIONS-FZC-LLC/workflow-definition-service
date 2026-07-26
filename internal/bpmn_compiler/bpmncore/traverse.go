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

func (s *CompileState) DeptOf(taskID string) (id, label, iamDeptID string) {
	name := LaneNameFor(taskID, s.Proc)
	return name, name, DeptIDFor(taskID, s.Proc)
}

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
}

func (s *CompileState) AddToSeqBuf(deptID string) {
	if !slices.Contains(s.seqBuf, deptID) {
		s.seqBuf = append(s.seqBuf, deptID)
	}
}

func (s *CompileState) FlushSeqBuf() {
	if len(s.seqBuf) == 0 {
		return
	}
	buf := make([]string, len(s.seqBuf))
	copy(buf, s.seqBuf)
	s.steps = append(s.steps, dsl.ExecutionStep{Sequential: buf})
	s.seqBuf = s.seqBuf[:0]
}

func (s *CompileState) TraverseNode(nodeID string) error {
	// StopAt is set by branch states to halt at the parallel join gateway.
	if s.StopAt != "" && nodeID == s.StopAt {
		return nil
	}
	if s.visited[nodeID] {
		return nil
	}
	s.visited[nodeID] = true

	if s.G.NodeType[nodeID] == NodeTypeExternalParticipant {
		s.FlushSeqBuf()
		if pool, ok := s.ExternalNodes[nodeID]; ok {
			s.steps = append(s.steps, dsl.ExecutionStep{
				CallPool: &dsl.CallPoolStep{Pool: pool},
			})
		}
		for _, next := range s.ForwardNexts(nodeID) {
			if err := s.TraverseNode(next); err != nil {
				return err
			}
		}
		return nil
	}

	if h, ok := s.Elements[s.G.NodeType[nodeID]]; ok {
		return h.Compile(nodeID, s)
	}
	// No handler (e.g. inclusiveGateway): pass through to successors without emitting a stage.
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
	return s.handleSplitWithJoin(nodeID, fwd, backTargets, joinID, kind)
}

func (s *CompileState) handleSingleForwardWithReverts(
	nodeID string, fwd, backTargets []string,
) error {
	step := dsl.ExecutionStep{}
	if len(fwd) == 1 {
		dept, stage := s.firstTaskAhead(fwd[0])
		terminates := s.G.NodeType[fwd[0]] == NodeTypeEndEvent
		nodeID0, name0 := "", ""
		if !terminates && (stage == "sub_workflow" || stage == "") {
			nodeID0, name0 = s.firstTaskNodeAhead(fwd[0])
		}
		step.Exclusive = append(step.Exclusive, dsl.ExclusiveBranch{
			Target:              dept,
			TargetStage:         stage,
			TargetNodeID:        nodeID0,
			TargetName:          name0,
			ConditionExpression: s.condExprs[[2]string{nodeID, fwd[0]}],
			Terminates:          terminates,
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
	var continuationNode string
	for _, branchStart := range fwd {
		dept, stage := s.firstTaskAhead(branchStart)
		nodeID0, name0 := "", ""
		if stage == "sub_workflow" || stage == "" {
			nodeID0, name0 = s.firstTaskNodeAhead(branchStart)
		}
		step.Exclusive = append(step.Exclusive, dsl.ExclusiveBranch{
			Target:              dept,
			TargetStage:         stage,
			TargetNodeID:        nodeID0,
			TargetName:          name0,
			ConditionExpression: s.condExprs[[2]string{nodeID, branchStart}],
			Terminates:          true,
		})
		if s.G.NodeType[branchStart] != NodeTypeEndEvent {
			continuationNode = branchStart
		}
	}
	step.Exclusive = append(step.Exclusive, s.revertBranches(nodeID, backTargets)...)
	s.steps = append(s.steps, step)
	if continuationNode != "" {
		return s.TraverseNode(continuationNode)
	}
	return nil
}

func (s *CompileState) handleSplitWithJoin(
	nodeID string, fwd, backTargets []string, joinID string, kind GatewayKind,
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
	if kind == ExclusiveGateway {
		if err := s.compileSubprocBranches(fwd, joinID); err != nil {
			return err
		}
	}
	return s.TraverseNode(joinID)
}

func (s *CompileState) compileSubprocBranches(fwd []string, joinID string) error {
	for _, branchStart := range fwd {
		if branchStart == joinID || s.visited[branchStart] {
			continue
		}
		if s.G.NodeType[branchStart] != NodeTypeSubProcess {
			continue
		}
		bs := NewBranchState(s, joinID)
		if err := bs.TraverseNode(branchStart); err != nil {
			return err
		}
		bs.FlushSeqBuf()
		s.steps = append(s.steps, bs.CollectedSteps()...)
	}
	return nil
}

func (s *CompileState) FillBoundaryTimerTarget(taskID string, stage *dsl.StageDef) {
	if stage.BoundaryTimer == nil {
		return
	}
	be := TimerBoundaryFor(taskID, s.Proc)
	if be == nil {
		return
	}
	targetID := OutgoingTargetOf(be.ID, s.Proc)
	if targetID != "" {
		stage.BoundaryTimer.TargetDept = s.FirstDeptAhead(targetID)
	}
}

func (s *CompileState) FillBoundaryMessageTarget(taskID string, stage *dsl.StageDef) {
	if stage.BoundaryMessage == nil {
		return
	}
	be := MessageBoundaryFor(taskID, s.Proc)
	if be == nil {
		return
	}
	targetID := OutgoingTargetOf(be.ID, s.Proc)
	if targetID != "" {
		stage.BoundaryMessage.TargetDept = s.FirstDeptAhead(targetID)
	}
}

func (s *CompileState) CompileBoundaryPaths() error {
	for _, be := range s.Proc.BoundaryEvents {
		if (be.Timer == nil && be.Message == nil) || s.visited[be.ID] {
			continue
		}
		if err := s.TraverseNode(be.ID); err != nil {
			return fmt.Errorf("boundary continuation from %q: %w", be.ID, err)
		}
	}
	return nil
}

func (s *CompileState) FirstDeptAhead(node string) string {
	dept, _ := s.firstTaskAhead(node)
	return dept
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
		dept, stage := s.firstTaskAhead(backTarget)
		nodeID0, name0 := "", ""
		if stage == "sub_workflow" || stage == "" {
			nodeID0, name0 = s.firstTaskNodeAhead(backTarget)
		}
		branches = append(branches, dsl.ExclusiveBranch{
			ConditionExpression: s.condExprs[[2]string{gatewayID, backTarget}],
			RevertToDept:        dept,
			RevertToStage:       stage,
			RevertToNodeID:      nodeID0,
			RevertToName:        name0,
		})
	}
	return branches
}

func (s *CompileState) firstTaskAhead(node string) (dept, stageType string) {
	visited := make(map[string]bool)
	curr := node
	for {
		if visited[curr] {
			return "", ""
		}
		visited[curr] = true
		if s.G.NodeType[curr] == NodeTypeUserTask {
			return s.resolveUserTaskDept(curr)
		}
		if s.G.NodeType[curr] == NodeTypeSubProcess {
			return LaneNameFor(curr, s.Proc), "sub_workflow"
		}
		fwd := s.ForwardNexts(curr)
		if len(fwd) == 0 {
			return "", ""
		}
		curr = fwd[0]
	}
}

func (s *CompileState) firstTaskNodeAhead(node string) (id, name string) {
	visited := make(map[string]bool)
	curr := node
	for {
		if visited[curr] {
			return "", ""
		}
		visited[curr] = true
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
			NodeTypeMessageBoundaryEvent, NodeTypeExternalParticipant:
			// gateway, boundary, or structural node — keep walking forward
		}
		fwd := s.ForwardNexts(curr)
		if len(fwd) == 0 {
			return "", ""
		}
		curr = fwd[0]
	}
}

func (s *CompileState) resolveUserTaskDept(curr string) (dept, stageType string) {
	task := FindTask(s.Proc, curr)
	if task == nil {
		return "", ""
	}
	deptName := LaneNameFor(curr, s.Proc)
	if task.ExtensionElements.TaskDefinition != nil {
		return deptName, task.ExtensionElements.TaskDefinition.Type
	}
	return deptName, ""
}

func reachesEndEvent(g *Graph, backEdges map[[2]string]bool, from, stop string) bool {
	queue := []string{from}
	seen := map[string]bool{from: true}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if curr == stop {
			continue
		}
		if g.NodeType[curr] == NodeTypeEndEvent {
			return true
		}
		for _, next := range g.Outgoing[curr] {
			if !backEdges[[2]string{curr, next}] && !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

func (s *CompileState) traverseBranches(splitID, joinID string) ([]dsl.ParallelBranch, error) {
	var branches []dsl.ParallelBranch

	for _, branchStart := range s.ForwardNexts(splitID) {
		leadDept, _ := s.firstTaskAhead(branchStart)

		bs := NewBranchState(s, joinID)
		if err := bs.TraverseNode(branchStart); err != nil {
			return nil, err
		}
		bs.FlushSeqBuf()

		branches = append(branches, dsl.ParallelBranch{
			DeptID: leadDept,
			Steps:  bs.CollectedSteps(),
		})
	}
	return branches, nil
}

func (s *CompileState) traverseExclusiveBranches(
	splitID, joinID string,
) ([]dsl.ExclusiveBranch, error) {
	var branches []dsl.ExclusiveBranch
	for _, branchStart := range s.ForwardNexts(splitID) {
		b, err := s.compileExclusiveBranch(splitID, branchStart, joinID)
		if err != nil {
			return nil, err
		}
		branches = append(branches, b)
	}
	return branches, nil
}

func (s *CompileState) compileExclusiveBranch(
	splitID, branchStart, joinID string,
) (dsl.ExclusiveBranch, error) {
	condExpr := s.condExprs[[2]string{splitID, branchStart}]
	var branchDept, branchStage string
	curr := branchStart
	walked := make(map[string]bool)

	for curr != joinID {
		if walked[curr] {
			break
		}
		walked[curr] = true

		if err := s.processExclusiveNode(curr, &branchDept, &branchStage); err != nil {
			return dsl.ExclusiveBranch{}, err
		}

		fwd := s.ForwardNexts(curr)
		if len(fwd) == 0 {
			break
		}
		curr = fwd[0]
	}

	terminates := branchDept == "" &&
		reachesEndEvent(s.G, s.backEdges, branchStart, joinID)
	if branchDept == "" && !terminates {
		branchDept, branchStage = s.firstTaskAhead(joinID)
	}
	targetNodeID, targetName := "", ""
	if branchStage == "sub_workflow" || branchStage == "" ||
		branchStage == "send_task" || branchStage == "receive_task" {
		targetNodeID, targetName = s.firstTaskNodeAhead(branchStart)
		if targetNodeID == "" && branchDept != "" {
			targetNodeID, targetName = s.firstTaskNodeAhead(joinID)
		}
	}
	return dsl.ExclusiveBranch{
		Target:              branchDept,
		TargetStage:         branchStage,
		TargetNodeID:        targetNodeID,
		TargetName:          targetName,
		ConditionExpression: condExpr,
		Terminates:          terminates,
	}, nil
}

func (s *CompileState) processExclusiveNode(
	curr string,
	branchDept, branchStage *string,
) error {
	switch s.G.NodeType[curr] {
	case NodeTypeUserTask:
		return s.processExclusiveUserTask(curr, branchDept, branchStage)
	case NodeTypeSendTask:
		return s.processExclusiveMessageTask(curr, "send_task", branchDept, branchStage)
	case NodeTypeReceiveTask:
		return s.processExclusiveMessageTask(curr, "receive_task", branchDept, branchStage)
	case NodeTypeSubProcess, NodeTypeCallActivity:
		if *branchDept == "" {
			*branchDept = LaneNameFor(curr, s.Proc)
			*branchStage = "sub_workflow"
		}
	case NodeTypeStartEvent, NodeTypeEndEvent,
		NodeTypeParallelGateway, NodeTypeExclusiveGateway, NodeTypeInclusiveGateway,
		NodeTypeBoundaryEvent, NodeTypeTimerBoundaryEvent, NodeTypeErrorBoundaryEvent,
		NodeTypeMessageBoundaryEvent, NodeTypeExternalParticipant:
		// No stage recording needed in an exclusive branch walk.
	}
	return nil
}

func (s *CompileState) processExclusiveUserTask(
	curr string,
	branchDept, branchStage *string,
) error {
	task := FindTask(s.Proc, curr)
	if task == nil {
		return fmt.Errorf(errTaskNotFound, curr)
	}
	deptID, label, iamDeptID := s.DeptOf(curr)
	if *branchDept == "" {
		*branchDept = deptID
		if task.ExtensionElements.TaskDefinition != nil {
			*branchStage = task.ExtensionElements.TaskDefinition.Type
		}
	}
	stage, err := BuildStageDef(task, s.StageTypes, s.Proc, s.Defs)
	if err != nil {
		return err
	}
	s.FillBoundaryTimerTarget(task.ID, &stage)
	s.FillBoundaryMessageTarget(task.ID, &stage)
	s.EnsureDept(deptID, label, iamDeptID)
	s.AppendStage(deptID, stage)
	s.visited[curr] = true
	return nil
}

func (s *CompileState) processExclusiveMessageTask(
	curr, stageType string,
	branchDept, branchStage *string,
) error {
	deptID, label, iamDeptID := s.DeptOf(curr)
	if *branchDept == "" {
		*branchDept = deptID
		*branchStage = stageType
	}
	var (
		taskID, taskName, msgName string
		ext                       BPMNExtensionElements
	)
	if stageType == "send_task" {
		t := FindSendTask(s.Proc, curr)
		if t == nil {
			return fmt.Errorf("sendTask %q not found in process", curr)
		}
		taskID, taskName, msgName = t.ID, t.Name, ResolveMessageName(t.MessageRef, s.Defs)
		ext = t.ExtensionElements
	} else {
		t := FindReceiveTask(s.Proc, curr)
		if t == nil {
			return fmt.Errorf("receiveTask %q not found in process", curr)
		}
		taskID, taskName, msgName = t.ID, t.Name, ResolveMessageName(t.MessageRef, s.Defs)
		ext = t.ExtensionElements
	}
	stage := BuildMessageStageDef(taskID, taskName, stageType, msgName, ext)
	s.EnsureDept(deptID, label, iamDeptID)
	s.AppendStage(deptID, stage)
	s.visited[curr] = true
	return nil
}
