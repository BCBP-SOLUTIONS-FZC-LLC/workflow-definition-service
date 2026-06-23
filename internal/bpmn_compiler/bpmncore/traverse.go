package bpmncore

import (
	"fmt"
	"slices"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type GatewayKind bool

const (
	ParallelGateway  GatewayKind = false
	ExclusiveGateway GatewayKind = true
)

func (s *CompileState) DeptOf(taskID string) (id, label string) {
	name := LaneNameFor(taskID, s.Proc)
	return name, name
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

func (s *CompileState) EnsureDept(id, label string) {
	if _, ok := s.deptIdx[id]; ok {
		return
	}
	s.deptIdx[id] = len(s.depts)
	s.depts = append(s.depts, domain.DepartmentDef{ID: id, Label: label})
}

func (s *CompileState) AppendStage(deptID string, stage domain.StageDef) {
	i := s.deptIdx[deptID]
	s.depts[i].Stages = append(s.depts[i].Stages, stage)
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
	s.steps = append(s.steps, domain.ExecutionStep{Sequential: buf})
	s.seqBuf = s.seqBuf[:0]
}

func (s *CompileState) TraverseNode(nodeID string) error {
	if s.visited[nodeID] {
		return nil
	}
	s.visited[nodeID] = true

	if h, ok := s.Elements[s.G.NodeType[nodeID]]; ok {
		return h.Compile(nodeID, s)
	}
	// Send/receive tasks and inclusive gateways traverse without producing a stage.
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
		step := domain.ExecutionStep{}
		if len(fwd) == 1 {
			step.Exclusive = append(step.Exclusive, domain.ExclusiveBranch{
				Target:              s.FirstDeptAhead(fwd[0]),
				ConditionExpression: s.condExprs[[2]string{nodeID, fwd[0]}],
			})
		}
		step.Exclusive = append(step.Exclusive, s.revertBranches(nodeID, backTargets)...)
		s.steps = append(s.steps, step)
		if len(fwd) == 1 {
			return s.TraverseNode(fwd[0])
		}
		return nil
	}

	joinID, ok := FindJoin(nodeID, s.G, s.backEdges)
	if !ok {
		// Validator guarantees this only happens for exclusive gateways whose every
		// forward branch eventually reaches an end event (all-branches-terminate).
		if kind != ExclusiveGateway {
			return fmt.Errorf("split gateway %q has no matching join", nodeID)
		}
		step := domain.ExecutionStep{}
		var continuationNode string
		for _, branchStart := range fwd {
			step.Exclusive = append(step.Exclusive, domain.ExclusiveBranch{
				Target:              s.FirstDeptAhead(branchStart),
				ConditionExpression: s.condExprs[[2]string{nodeID, branchStart}],
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

	var step domain.ExecutionStep
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

	s.visited[joinID] = true
	if nexts := s.ForwardNexts(joinID); len(nexts) > 0 {
		return s.TraverseNode(nexts[0])
	}
	return nil
}

func (s *CompileState) FillBoundaryTimerTarget(taskID string, stage *domain.StageDef) {
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

func (s *CompileState) CompileTimerBoundaryPaths() error {
	for _, be := range s.Proc.BoundaryEvents {
		if be.Timer == nil || s.visited[be.ID] {
			continue
		}
		if err := s.TraverseNode(be.ID); err != nil {
			return fmt.Errorf("timer boundary continuation from %q: %w", be.ID, err)
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

func (s *CompileState) revertBranches(gatewayID string, backTargets []string) []domain.ExclusiveBranch {
	branches := make([]domain.ExclusiveBranch, 0, len(backTargets))
	for _, backTarget := range backTargets {
		dept, stage := s.firstTaskAhead(backTarget)
		branches = append(branches, domain.ExclusiveBranch{
			ConditionExpression: s.condExprs[[2]string{gatewayID, backTarget}],
			RevertToDept:        dept,
			RevertToStage:       stage,
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
			if task := FindTask(s.Proc, curr); task != nil {
				deptName := LaneNameFor(curr, s.Proc)
				taskType := ""
				if task.ExtensionElements.TaskDefinition != nil {
					taskType = task.ExtensionElements.TaskDefinition.Type
				}
				return deptName, taskType
			}
			return "", ""
		}
		fwd := s.ForwardNexts(curr)
		if len(fwd) == 0 {
			return "", ""
		}
		curr = fwd[0]
	}
}

func (s *CompileState) traverseBranches(splitID, joinID string) ([]string, error) {
	seen := make(map[string]bool)
	var depts []string

	for _, branchStart := range s.ForwardNexts(splitID) {
		curr := branchStart
		walked := make(map[string]bool)
		for curr != joinID {
			if walked[curr] {
				break
			}
			walked[curr] = true
			if s.G.NodeType[curr] == NodeTypeUserTask {
				task := FindTask(s.Proc, curr)
				if task == nil {
					return nil, fmt.Errorf(errTaskNotFound, curr)
				}
				deptID, label := s.DeptOf(curr)
				stage, err := BuildStageDef(task, s.StageTypes, s.Proc)
				if err != nil {
					return nil, err
				}
				s.FillBoundaryTimerTarget(task.ID, &stage)
				s.EnsureDept(deptID, label)
				s.AppendStage(deptID, stage)
				s.visited[curr] = true
				if !seen[deptID] {
					seen[deptID] = true
					depts = append(depts, deptID)
				}
			}
			fwd := s.ForwardNexts(curr)
			if len(fwd) == 0 {
				break
			}
			curr = fwd[0]
		}
	}
	return depts, nil
}

func (s *CompileState) traverseExclusiveBranches(splitID, joinID string) ([]domain.ExclusiveBranch, error) {
	var branches []domain.ExclusiveBranch

	for _, branchStart := range s.ForwardNexts(splitID) {
		condExpr := s.condExprs[[2]string{splitID, branchStart}]
		var branchDept string
		curr := branchStart
		walked := make(map[string]bool)
		for curr != joinID {
			if walked[curr] {
				break
			}
			walked[curr] = true
			if s.G.NodeType[curr] == NodeTypeUserTask {
				task := FindTask(s.Proc, curr)
				if task == nil {
					return nil, fmt.Errorf(errTaskNotFound, curr)
				}
				deptID, label := s.DeptOf(curr)
				if branchDept == "" {
					branchDept = deptID
				}
				stage, err := BuildStageDef(task, s.StageTypes, s.Proc)
				if err != nil {
					return nil, err
				}
				s.FillBoundaryTimerTarget(task.ID, &stage)
				s.EnsureDept(deptID, label)
				s.AppendStage(deptID, stage)
				s.visited[curr] = true
			}
			fwd := s.ForwardNexts(curr)
			if len(fwd) == 0 {
				break
			}
			curr = fwd[0]
		}
		branches = append(branches, domain.ExclusiveBranch{
			Target:              branchDept,
			ConditionExpression: condExpr,
		})
	}
	return branches, nil
}
