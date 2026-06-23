package validator

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	maxUserTasks = 1000
	maxLanes     = 100
	maxCondExpr  = 4096

	// exported for white-box tests.
	MaxUserTasks = maxUserTasks
	MaxLanes     = maxLanes
	MaxCondExpr  = maxCondExpr
)

func Validate(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, stageTypes map[string]bpmncore.StageTypeHandler, elements map[bpmncore.FlowNodeType]bpmncore.ElementHandler, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	// Classify back-edges from the start event; the forward graph (edges minus
	// back-edges) is what reachability, loop, and gateway checks reason about.
	var backEdges map[[2]string]bool
	if len(proc.StartEvents) > 0 {
		backEdges = bpmncore.ClassifyBackEdges(g, proc.StartEvents[0].ID)
	} else {
		backEdges = map[[2]string]bool{}
	}

	var errs []domain.BPMNValidationError
	errs = append(errs, ValidateCounts(proc)...)
	errs = append(errs, ValidateSeqFlowRefs(proc, g)...)
	errs = append(errs, ValidateDanglingNodes(proc, g)...)
	errs = append(errs, ValidateReachability(proc, g)...)
	errs = append(errs, ValidateLoops(g, backEdges)...)
	errs = append(errs, ValidateMaxDepth(proc, g, backEdges)...)
	errs = append(errs, ValidateGatewayMatching(proc, g, backEdges)...)
	errs = append(errs, ValidateConditionExprs(proc, g)...)
	errs = append(errs, ValidateBoundaryEvents(proc, g)...)

	// Per-element validation via handler registry.
	if proc.IsExecutable {
		laneRefs := bpmncore.BuildLaneRefSet(proc)
		if h, ok := elements[bpmncore.NodeTypeUserTask]; ok {
			for _, t := range proc.UserTasks {
				errs = append(errs, h.Validate(t.ID, proc, g, defs, stageTypes, elements)...)
			}
		}
		for _, t := range proc.SendTasks {
			errs = append(errs, ValidateTaskDef(t.ID, t.ExtensionElements, stageTypes)...)
			errs = append(errs, ValidateLaneMembership(t.ID, laneRefs)...)
			errs = append(errs, ValidateRequiresComment(t.ID, t.ExtensionElements)...)
		}
		for _, t := range proc.ReceiveTasks {
			errs = append(errs, ValidateTaskDef(t.ID, t.ExtensionElements, stageTypes)...)
			errs = append(errs, ValidateLaneMembership(t.ID, laneRefs)...)
			errs = append(errs, ValidateRequiresComment(t.ID, t.ExtensionElements)...)
		}
	}

	if h, ok := elements[bpmncore.NodeTypeSubProcess]; ok {
		for _, sp := range proc.SubProcesses {
			errs = append(errs, h.Validate(sp.ID, proc, g, defs, stageTypes, elements)...)
		}
	}

	return errs
}

// AppendErr appends a BPMNValidationError to errs and returns the result.
func AppendErr(
	errs []domain.BPMNValidationError,
	code domain.BPMNErrorCode,
	nodeID, msg string,
) []domain.BPMNValidationError {
	return append(errs, domain.BPMNValidationError{
		Code:    code,
		NodeID:  nodeID,
		Message: msg,
	})
}
