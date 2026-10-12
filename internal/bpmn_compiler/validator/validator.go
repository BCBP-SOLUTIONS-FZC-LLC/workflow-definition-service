package validator

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	maxUserTasks = bpmncore.StageBudget
	maxLanes     = 100
	maxCondExpr  = 4096

	MaxUserTasks = maxUserTasks
	MaxLanes     = maxLanes
	MaxCondExpr  = maxCondExpr
)

func Validate(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, stageTypes map[string]bpmncore.StageTypeHandler, elements map[bpmncore.FlowNodeType]bpmncore.ElementHandler, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	return ValidateWithImplicitStart(proc, g, "", stageTypes, elements, defs)
}

func ValidateWithImplicitStart(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, implicitStart string, stageTypes map[string]bpmncore.StageTypeHandler, elements map[bpmncore.FlowNodeType]bpmncore.ElementHandler, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	var backEdges map[[2]string]bool
	switch {
	case len(proc.StartEvents) > 0:
		backEdges = bpmncore.ProcessBackEdges(proc, g, proc.StartEvents[0].ID)
	case implicitStart != "":
		backEdges = bpmncore.ProcessBackEdges(proc, g, implicitStart)
	default:
		backEdges = map[[2]string]bool{}
	}

	var errs []domain.BPMNValidationError
	errs = append(errs, ValidateCounts(proc, implicitStart)...)
	if depth := ValidateMaxDepth(proc, g, backEdges); len(depth) > 0 {
		return append(errs, depth...)
	}
	errs = append(errs, ValidateLaneNames(proc)...)
	errs = append(errs, ValidateNoNestedSubProcess(proc)...)
	errs = append(errs, ValidateDuplicateIDs(proc)...)
	errs = append(errs, ValidateSeqFlowRefs(proc, g)...)
	errs = append(errs, ValidateDanglingNodes(proc, g, implicitStart)...)
	errs = append(errs, ValidateReachability(proc, g, implicitStart)...)
	errs = append(errs, ValidateLoops(g, backEdges)...)
	errs = append(errs, ValidateGatewayMatching(proc, g, backEdges)...)
	errs = append(errs, ValidateSingleOutgoing(proc, g, backEdges)...)
	errs = append(errs, ValidateConditionExprs(proc, g, backEdges)...)
	errs = append(errs, ValidateBoundaryEvents(proc, g, backEdges)...)
	errs = append(errs, ValidateMessageBoundaryNames(proc, defs)...)
	errs = append(errs, ValidateErrorBoundaryRefs(proc, defs)...)
	errs = append(errs, ValidateIgnoredLanes(proc, g, backEdges)...)
	errs = append(errs, validatePerElement(proc, g, stageTypes, elements, defs)...)

	return errs
}

func validatePerElement(
	proc *bpmncore.BPMNProcess,
	g *bpmncore.Graph,
	stageTypes map[string]bpmncore.StageTypeHandler,
	elements map[bpmncore.FlowNodeType]bpmncore.ElementHandler,
	defs *bpmncore.BPMNDefinitions,
) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	laneRefs := bpmncore.BuildLaneRefSet(proc)
	skipped := bpmncore.SkippedTasks(proc, g)

	if h, ok := elements[bpmncore.NodeTypeUserTask]; ok {
		for _, t := range proc.UserTasks {
			if skipped[t.ID] {
				continue
			}
			errs = append(errs, h.Validate(t.ID, proc, g, defs, stageTypes, elements)...)
		}
	}
	for _, t := range proc.SendTasks {
		if skipped[t.ID] {
			continue
		}
		errs = append(errs, validateOptionalTaskMembership(t.ID, t.ExtensionElements, proc, laneRefs, defs)...)
	}
	for _, t := range proc.ReceiveTasks {
		if skipped[t.ID] {
			continue
		}
		errs = append(errs, validateOptionalTaskMembership(t.ID, t.ExtensionElements, proc, laneRefs, defs)...)
	}
	for _, ca := range proc.CallActivities {
		errs = append(errs, ValidateLaneMembership(ca.ID, laneRefs)...)
	}

	if h, ok := elements[bpmncore.NodeTypeSubProcess]; ok {
		for _, sp := range proc.SubProcesses {
			errs = append(errs, ValidateLaneMembership(sp.ID, laneRefs)...)
			errs = append(errs, h.Validate(sp.ID, proc, g, defs, stageTypes, elements)...)
		}
	}
	if h, ok := elements[bpmncore.NodeTypeCallActivity]; ok {
		for _, ca := range proc.CallActivities {
			errs = append(errs, h.Validate(ca.ID, proc, g, defs, stageTypes, elements)...)
		}
	}
	return errs
}

func validateOptionalTaskMembership(taskID string, ext bpmncore.BPMNExtensionElements, proc *bpmncore.BPMNProcess, laneRefs map[string]struct{}, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	errs = append(errs, ValidateLaneMembership(taskID, laneRefs)...)
	errs = append(errs, ValidateOptionalAssignment(taskID, ext)...)
	errs = append(errs, ValidateDeptID(taskID, proc, defs)...)
	return errs
}

func AppendErr(
	errs []domain.BPMNValidationError,
	code domain.BPMNErrorCode,
	nodeID, msg string,
) []domain.BPMNValidationError {
	return append(errs, domain.BPMNValidationError{
		Code:     code,
		NodeID:   nodeID,
		Message:  msg,
		Severity: domain.SeverityError,
	})
}

func AppendWarn(
	errs []domain.BPMNValidationError,
	code domain.BPMNErrorCode,
	nodeID, msg string,
) []domain.BPMNValidationError {
	return append(errs, domain.BPMNValidationError{
		Code:     code,
		NodeID:   nodeID,
		Message:  msg,
		Severity: domain.SeverityWarning,
	})
}
