package element

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// TimerBoundaryHandler satisfies ElementHandler for timer boundary events.
type TimerBoundaryHandler struct{}

func (TimerBoundaryHandler) NodeType() bpmncore.FlowNodeType { return bpmncore.NodeTypeBoundaryEvent }
func (TimerBoundaryHandler) EventKind() string               { return "boundary" }

func (TimerBoundaryHandler) Validate(_ string, _ *bpmncore.BPMNProcess, _ *bpmncore.Graph, _ *bpmncore.BPMNDefinitions, _ map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}

// Compile traverses the continuation path that follows this timer boundary event.
// Called from CompileTimerBoundaryPaths once per unvisited timer boundary.
func (TimerBoundaryHandler) Compile(nodeID string, cs *bpmncore.CompileState) error {
	targetID := bpmncore.OutgoingTargetOf(nodeID, cs.Proc)
	if targetID == "" {
		return nil
	}
	return cs.TraverseNode(targetID)
}

// ErrorBoundaryHandler satisfies ElementHandler for error boundary events.
// Validation is done in validator.ValidateBoundaryEvents. Compilation is handled
// entirely by SubProcessHandler, which builds ErrorPaths via FirstDeptAhead — traversing
// the target here would incorrectly add those stages to the normal execution flow.
type ErrorBoundaryHandler struct{}

func (ErrorBoundaryHandler) NodeType() bpmncore.FlowNodeType { return bpmncore.NodeTypeBoundaryEvent }
func (ErrorBoundaryHandler) EventKind() string               { return "boundary" }

func (ErrorBoundaryHandler) Validate(_ string, _ *bpmncore.BPMNProcess, _ *bpmncore.Graph, _ *bpmncore.BPMNDefinitions, _ map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}

func (ErrorBoundaryHandler) Compile(_ string, _ *bpmncore.CompileState) error {
	return nil
}
