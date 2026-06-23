package bpmncore

import "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"

const errTaskNotFound = "task %q not found in process"
const defaultTaskQueue = "wf-queue-default"

type CompileState struct {
	Proc       *BPMNProcess
	G          *Graph
	laneLabels map[string]string
	condExprs  map[[2]string]string
	backEdges  map[[2]string]bool
	StageTypes map[string]StageTypeHandler
	Elements   map[FlowNodeType]ElementHandler
	Defs       *BPMNDefinitions

	depts   []domain.DepartmentDef
	deptIdx map[string]int
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
		Proc:       proc,
		G:          g,
		laneLabels: laneLabels,
		condExprs:  condExprs,
		backEdges:  backEdges,
		StageTypes: stageTypes,
		Elements:   elements,
		Defs:       defs,
		deptIdx:    make(map[string]int),
		visited:    make(map[string]bool),
	}
}

func (s *CompileState) CollectedDepts() []domain.DepartmentDef { return s.depts }

func (s *CompileState) CollectedSteps() []domain.ExecutionStep { return s.steps }

func (s *CompileState) AppendStep(step domain.ExecutionStep) {
	s.steps = append(s.steps, step)
}

func (s *CompileState) IsVisited(nodeID string) bool { return s.visited[nodeID] }

func (s *CompileState) SeqBufCount() int { return len(s.seqBuf) }

func (s *CompileState) RevertBranches(gatewayID string, backTargets []string) []domain.ExclusiveBranch {
	return s.revertBranches(gatewayID, backTargets)
}

func (s *CompileState) TraverseBranches(splitID, joinID string) ([]string, error) {
	return s.traverseBranches(splitID, joinID)
}

func (s *CompileState) TraverseExclusiveBranches(splitID, joinID string) ([]domain.ExclusiveBranch, error) {
	return s.traverseExclusiveBranches(splitID, joinID)
}
