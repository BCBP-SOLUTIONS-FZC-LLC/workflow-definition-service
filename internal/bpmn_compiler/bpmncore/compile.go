package bpmncore

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func Compile(proc *BPMNProcess, g *Graph, stageTypes map[string]StageTypeHandler, elements map[FlowNodeType]ElementHandler, defs *BPMNDefinitions) (*domain.CompiledPlan, error) {
	if len(proc.StartEvents) == 0 {
		return nil, fmt.Errorf("compile: no start event")
	}
	laneLabels := BuildLaneLabels(proc)
	condExprs := BuildCondExprs(proc)
	backEdges := ClassifyBackEdges(g, proc.StartEvents[0].ID)

	state := NewCompileState(proc, g, laneLabels, condExprs, backEdges, stageTypes, elements, defs)
	nexts := state.ForwardNexts(proc.StartEvents[0].ID)
	if len(nexts) > 0 {
		if err := state.TraverseNode(nexts[0]); err != nil {
			return nil, fmt.Errorf("compile: %w", err)
		}
	}
	if err := state.CompileTimerBoundaryPaths(); err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}

	return &domain.CompiledPlan{
		Name:        proc.Name,
		TaskQueue:   defaultTaskQueue,
		Departments: state.depts,
		Execution:   domain.ExecutionPlan{Steps: state.steps},
	}, nil
}

func BuildStageDef(task *BPMNUserTask, stageTypes map[string]StageTypeHandler, proc *BPMNProcess) (domain.StageDef, error) {
	ext := task.ExtensionElements

	stageType := ""
	if ext.TaskDefinition != nil {
		stageType = ext.TaskDefinition.Type
	}
	h, ok := stageTypes[stageType]
	if !ok {
		return domain.StageDef{},
			fmt.Errorf("task %q: unknown taskDefinition type %q", task.ID, stageType)
	}

	role := ""
	var assignees []string
	if ext.AssignmentDefinition != nil {
		role = ext.AssignmentDefinition.CandidateGroups
		if cu := strings.TrimSpace(ext.AssignmentDefinition.CandidateUsers); cu != "" {
			assignees = []string{cu}
		}
	}

	requiresComment := false
	for _, p := range ext.ZeebeProps.Items {
		if p.Name == "requires_comment" {
			requiresComment, _ = strconv.ParseBool(p.Value)
			break
		}
	}

	stage := domain.StageDef{
		Type:             stageType,
		Activity:         h.ActivityName(),
		Role:             role,
		DefaultAssignees: assignees,
		RequiresComment:  requiresComment,
	}

	if be := TimerBoundaryFor(task.ID, proc); be != nil {
		stage.BoundaryTimer = &domain.BoundaryTimer{
			Duration:     be.Timer.Duration,
			Interrupting: be.CancelActivity != "false",
		}
	}

	return stage, nil
}
