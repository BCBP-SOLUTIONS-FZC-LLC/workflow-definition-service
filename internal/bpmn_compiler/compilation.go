package bpmn_compiler

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type compileState struct {
	proc      *bpmnProcess
	g         *graph
	laneIdx   map[string]bpmnLane
	condExprs map[[2]string]string

	depts   []domain.DepartmentDef
	deptIdx map[string]int
	steps   []domain.ExecutionStep
	seqBuf  []string
	visited map[string]bool
}

func newCompileState(
	proc *bpmnProcess,
	g *graph,
	laneIdx map[string]bpmnLane,
	condExprs map[[2]string]string,
) *compileState {
	return &compileState{
		proc:      proc,
		g:         g,
		laneIdx:   laneIdx,
		condExprs: condExprs,
		deptIdx:   make(map[string]int),
		visited:   make(map[string]bool),
	}
}

func (s *compileState) ensureDept(id, label string) {
	if _, ok := s.deptIdx[id]; ok {
		return
	}
	s.deptIdx[id] = len(s.depts)
	s.depts = append(s.depts, domain.DepartmentDef{ID: id, Label: label})
}

func (s *compileState) appendStage(deptID string, stage domain.StageDef) {
	i := s.deptIdx[deptID]
	s.depts[i].Stages = append(s.depts[i].Stages, stage)
}

func (s *compileState) addToSeqBuf(deptID string) {
	if !slices.Contains(s.seqBuf, deptID) {
		s.seqBuf = append(s.seqBuf, deptID)
	}
}

func (s *compileState) flushSeqBuf() {
	if len(s.seqBuf) == 0 {
		return
	}
	buf := make([]string, len(s.seqBuf))
	copy(buf, s.seqBuf)
	s.steps = append(s.steps, domain.ExecutionStep{Sequential: buf})
	s.seqBuf = s.seqBuf[:0]
}

func (s *compileState) traverseNode(nodeID string) error {
	if s.visited[nodeID] {
		return nil
	}
	s.visited[nodeID] = true

	switch s.g.nodeType[nodeID] {
	case NodeTypeUserTask:
		return s.handleUserTask(nodeID)
	case NodeTypeParallelGateway:
		return s.handleGateway(nodeID, false)
	case NodeTypeExclusiveGateway:
		return s.handleGateway(nodeID, true)
	case NodeTypeEndEvent:
		s.flushSeqBuf()
	}
	return nil
}

func (s *compileState) handleUserTask(nodeID string) error {
	task := findTask(s.proc, nodeID)
	if task == nil {
		return fmt.Errorf("task %q not found in process", nodeID)
	}
	lane := s.laneIdx[nodeID]
	deptID := normalizeLaneName(lane.Name)

	stage, err := buildStageDef(task)
	if err != nil {
		return err
	}
	s.ensureDept(deptID, lane.Name)
	s.appendStage(deptID, stage)
	s.addToSeqBuf(deptID)

	if nexts := s.g.outgoing[nodeID]; len(nexts) > 0 {
		return s.traverseNode(nexts[0])
	}
	return nil
}

func (s *compileState) handleGateway(nodeID string, isExclusive bool) error {
	isSplit := len(s.g.outgoing[nodeID]) > 1

	if !isSplit {
		if nexts := s.g.outgoing[nodeID]; len(nexts) > 0 {
			return s.traverseNode(nexts[0])
		}
		return nil
	}

	s.flushSeqBuf()

	joinID, ok := findJoin(nodeID, s.g)
	if !ok {
		return fmt.Errorf("split gateway %q has no matching join", nodeID)
	}

	var step domain.ExecutionStep
	var err error

	if isExclusive {
		step.Exclusive, err = s.traverseExclusiveBranches(nodeID, joinID)
	} else {
		step.Parallel, err = s.traverseBranches(nodeID, joinID)
	}
	if err != nil {
		return err
	}
	s.steps = append(s.steps, step)

	s.visited[joinID] = true
	if nexts := s.g.outgoing[joinID]; len(nexts) > 0 {
		return s.traverseNode(nexts[0])
	}
	return nil
}

func (s *compileState) traverseBranches(splitID, joinID string) ([]string, error) {
	seen := make(map[string]bool)
	var depts []string

	for _, branchStart := range s.g.outgoing[splitID] {
		curr := branchStart
		for curr != joinID {
			if s.g.nodeType[curr] == NodeTypeUserTask {
				lane := s.laneIdx[curr]
				deptID := normalizeLaneName(lane.Name)
				task := findTask(s.proc, curr)
				if task == nil {
					return nil, fmt.Errorf("task %q not found in process", curr)
				}
				stage, err := buildStageDef(task)
				if err != nil {
					return nil, err
				}
				s.ensureDept(deptID, lane.Name)
				s.appendStage(deptID, stage)
				s.visited[curr] = true
				if !seen[deptID] {
					seen[deptID] = true
					depts = append(depts, deptID)
				}
			}
			nexts := s.g.outgoing[curr]
			if len(nexts) == 0 {
				break
			}
			curr = nexts[0]
		}
	}
	return depts, nil
}

func (s *compileState) traverseExclusiveBranches(
	splitID, joinID string,
) ([]domain.ExclusiveBranch, error) {
	var branches []domain.ExclusiveBranch

	for _, branchStart := range s.g.outgoing[splitID] {
		condExpr := s.condExprs[[2]string{splitID, branchStart}]
		var branchDept string
		curr := branchStart
		for curr != joinID {
			if s.g.nodeType[curr] == NodeTypeUserTask {
				lane := s.laneIdx[curr]
				deptID := normalizeLaneName(lane.Name)
				if branchDept == "" {
					branchDept = deptID
				}
				task := findTask(s.proc, curr)
				if task == nil {
					return nil, fmt.Errorf("task %q not found in process", curr)
				}
				stage, err := buildStageDef(task)
				if err != nil {
					return nil, err
				}
				s.ensureDept(deptID, lane.Name)
				s.appendStage(deptID, stage)
				s.visited[curr] = true
			}
			nexts := s.g.outgoing[curr]
			if len(nexts) == 0 {
				break
			}
			curr = nexts[0]
		}
		branches = append(branches, domain.ExclusiveBranch{
			Target:              branchDept,
			ConditionExpression: condExpr,
		})
	}
	return branches, nil
}

func compile(proc *bpmnProcess, g *graph) (*domain.CompiledPlan, error) {
	if len(proc.StartEvents) == 0 {
		return nil, fmt.Errorf("compile: no start event")
	}
	laneIdx := buildLaneIndex(proc)
	condExprs := buildCondExprs(proc)

	state := newCompileState(proc, g, laneIdx, condExprs)
	nexts := g.outgoing[proc.StartEvents[0].ID]
	if len(nexts) > 0 {
		if err := state.traverseNode(nexts[0]); err != nil {
			return nil, fmt.Errorf("compile: %w", err)
		}
	}

	return &domain.CompiledPlan{
		Name:        proc.Name,
		TaskQueue:   defaultTaskQueue,
		Departments: state.depts,
		Execution:   domain.ExecutionPlan{Steps: state.steps},
	}, nil
}

func buildStageDef(task *bpmnUserTask) (domain.StageDef, error) {
	props := propsMap(task.ExtensionElements)

	stageType := props["stage_type"]
	activity, ok := stageActivities[stageType]
	if !ok {
		return domain.StageDef{},
			fmt.Errorf("task %q: unknown stage_type %q", task.ID, stageType)
	}

	rawIDs := strings.Split(props["default_user_ids"], ",")
	assignees := make([]string, 0, len(rawIDs))
	for _, id := range rawIDs {
		if id = strings.TrimSpace(id); id != "" {
			assignees = append(assignees, id)
		}
	}

	requiresComment, _ := strconv.ParseBool(props["requires_comment"])

	stage := domain.StageDef{
		Type:             stageType,
		Activity:         activity,
		Role:             props["role"],
		DefaultAssignees: assignees,
		RequiresComment:  requiresComment,
	}
	if mode := props["assignee_mode"]; mode != "" {
		stage.AssigneeMode = mode
	}
	if sla := props["sla_duration"]; sla != "" {
		v := sla
		stage.SLADuration = &v
	}
	return stage, nil
}

func findTask(proc *bpmnProcess, id string) *bpmnUserTask {
	for i := range proc.UserTasks {
		if proc.UserTasks[i].ID == id {
			return &proc.UserTasks[i]
		}
	}
	return nil
}
