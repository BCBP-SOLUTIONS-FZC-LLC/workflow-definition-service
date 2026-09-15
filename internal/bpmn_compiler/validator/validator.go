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

// Validate runs all structural and semantic checks on a process.
// An explicit start event is required; use ValidateWithImplicitStart for
// collaboration processes where the entry point is derived from message flows.
func Validate(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, stageTypes map[string]bpmncore.StageTypeHandler, elements map[bpmncore.FlowNodeType]bpmncore.ElementHandler, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	return ValidateWithImplicitStart(proc, g, "", stageTypes, elements, defs)
}

// ValidateWithImplicitStart is like Validate but accepts a caller-supplied
// implicit start node ID. Use this when the caller has already determined the
// entry point (e.g. CompileCollaboration tracing message flows from ignored pools).
func ValidateWithImplicitStart(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, implicitStart string, stageTypes map[string]bpmncore.StageTypeHandler, elements map[bpmncore.FlowNodeType]bpmncore.ElementHandler, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	var backEdges map[[2]string]bool
	switch {
	case len(proc.StartEvents) > 0:
		backEdges = bpmncore.ClassifyBackEdges(g, proc.StartEvents[0].ID)
	case implicitStart != "":
		backEdges = bpmncore.ClassifyBackEdges(g, implicitStart)
	default:
		backEdges = map[[2]string]bool{}
	}

	var errs []domain.BPMNValidationError
	errs = append(errs, ValidateCounts(proc, implicitStart)...)
	errs = append(errs, ValidateNoNestedSubProcess(proc)...)
	errs = append(errs, ValidateSeqFlowRefs(proc, g)...)
	errs = append(errs, ValidateDanglingNodes(proc, g, implicitStart)...)
	errs = append(errs, ValidateReachability(proc, g, implicitStart)...)
	errs = append(errs, ValidateLoops(g, backEdges)...)
	errs = append(errs, ValidateMaxDepth(proc, g, backEdges)...)
	errs = append(errs, ValidateGatewayMatching(proc, g, backEdges)...)
	errs = append(errs, ValidateConditionExprs(proc, g)...)
	errs = append(errs, ValidateBoundaryEvents(proc, g)...)
	errs = append(errs, ValidateMessageBoundaryNames(proc, defs)...)
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

	if h, ok := elements[bpmncore.NodeTypeUserTask]; ok {
		for _, t := range proc.UserTasks {
			errs = append(errs, h.Validate(t.ID, proc, g, defs, stageTypes, elements)...)
		}
	}
	for _, t := range proc.SendTasks {
		errs = append(errs, validateOptionalTaskMembership(t.ID, t.ExtensionElements, proc, laneRefs)...)
	}
	for _, t := range proc.ReceiveTasks {
		errs = append(errs, validateOptionalTaskMembership(t.ID, t.ExtensionElements, proc, laneRefs)...)
	}
	for _, ca := range proc.CallActivities {
		errs = append(errs, ValidateLaneMembership(ca.ID, laneRefs)...)
	}

	if h, ok := elements[bpmncore.NodeTypeSubProcess]; ok {
		for _, sp := range proc.SubProcesses {
			errs = append(errs, h.Validate(sp.ID, proc, g, defs, stageTypes, elements)...)
		}
	}
	if h, ok := elements[bpmncore.NodeTypeCallActivity]; ok {
		for _, ca := range proc.CallActivities {
			errs = append(errs, h.Validate(ca.ID, proc, g, defs, stageTypes, elements)...)
		}
	}
	errs = append(errs, ValidateCallActivities(proc, defs)...)
	return errs
}

// validateOptionalTaskMembership runs the checks shared by sendTask/receiveTask
// nodes, whose zeebe:assignmentDefinition is optional (unlike userTask's, which
// is required).
func validateOptionalTaskMembership(taskID string, ext bpmncore.BPMNExtensionElements, proc *bpmncore.BPMNProcess, laneRefs map[string]struct{}) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	errs = append(errs, ValidateLaneMembership(taskID, laneRefs)...)
	errs = append(errs, ValidateOptionalAssignment(taskID, ext)...)
	errs = append(errs, ValidateDeptID(taskID, proc)...)
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
