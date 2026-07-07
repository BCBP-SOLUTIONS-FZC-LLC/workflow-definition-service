package bpmncore

import "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"

const errTaskNotFound = "task %q not found in process"
const defaultTaskQueue = "wf-queue-default"

type sharedDeptsStore struct {
	depts   []domain.DepartmentDef
	deptIdx map[string]int
}

type CompileState struct {
	Proc          *BPMNProcess
	G             *Graph
	laneLabels    map[string]string
	condExprs     map[[2]string]string
	backEdges     map[[2]string]bool
	StageTypes    map[string]StageTypeHandler
	Elements      map[FlowNodeType]ElementHandler
	Defs          *BPMNDefinitions
	ExternalNodes map[string]string
	// StopAt, when non-empty, causes TraverseNode to return immediately without
	// processing the node. Used by branch states to stop at the parallel join.
	StopAt string

	sd      *sharedDeptsStore
	steps   []domain.ExecutionStep
	seqBuf  []string
	visited map[string]bool
}

func NewCompileState(
	proc *BPMNProcess,
	g *Graph,
	laneLabels map[string]string,
	condExprs map[[2]string]string,
	backEdges map[[2]string]bool,
	stageTypes map[string]StageTypeHandler,
	elements map[FlowNodeType]ElementHandler,
	defs *BPMNDefinitions,
) *CompileState {
	return &CompileState{
		Proc:          proc,
		G:             g,
		laneLabels:    laneLabels,
		condExprs:     condExprs,
		backEdges:     backEdges,
		StageTypes:    stageTypes,
		Elements:      elements,
		Defs:          defs,
		ExternalNodes: make(map[string]string),
		sd:            &sharedDeptsStore{deptIdx: make(map[string]int)},
		visited:       make(map[string]bool),
	}
}

func NewBranchState(parent *CompileState, stopAt string) *CompileState {
	return &CompileState{
		Proc:          parent.Proc,
		G:             parent.G,
		laneLabels:    parent.laneLabels,
		condExprs:     parent.condExprs,
		backEdges:     parent.backEdges,
		StageTypes:    parent.StageTypes,
		Elements:      parent.Elements,
		Defs:          parent.Defs,
		ExternalNodes: parent.ExternalNodes,
		StopAt:        stopAt,
		sd:            parent.sd,
		visited:       make(map[string]bool),
	}
}

func (s *CompileState) CollectedDepts() []domain.DepartmentDef { return s.sd.depts }

func (s *CompileState) CollectedSteps() []domain.ExecutionStep { return s.steps }

func (s *CompileState) AppendStep(step domain.ExecutionStep) {
	s.steps = append(s.steps, step)
}

func (s *CompileState) StepsCount() int { return len(s.steps) }

func (s *CompileState) PatchStep(idx int, f func(*domain.ExecutionStep)) {
	if idx >= 0 && idx < len(s.steps) {
		f(&s.steps[idx])
	}
}

func (s *CompileState) SetDeptMeta(deptID string, ignore bool, props map[string]string) {
	idx, ok := s.sd.deptIdx[deptID]
	if !ok {
		return
	}
	s.sd.depts[idx].Ignore = ignore
	for k, v := range props {
		if k == "ignore" {
			continue
		}
		if s.sd.depts[idx].Props == nil {
			s.sd.depts[idx].Props = make(map[string]string)
		}
		s.sd.depts[idx].Props[k] = v
	}
}

func (s *CompileState) IsVisited(nodeID string) bool { return s.visited[nodeID] }

func (s *CompileState) SeqBufCount() int { return len(s.seqBuf) }

func (s *CompileState) RevertBranches(gatewayID string, backTargets []string) []domain.ExclusiveBranch {
	return s.revertBranches(gatewayID, backTargets)
}

func (s *CompileState) TraverseBranches(splitID, joinID string) ([]domain.ParallelBranch, error) {
	return s.traverseBranches(splitID, joinID)
}

func (s *CompileState) TraverseExclusiveBranches(splitID, joinID string) ([]domain.ExclusiveBranch, error) {
	return s.traverseExclusiveBranches(splitID, joinID)
}
