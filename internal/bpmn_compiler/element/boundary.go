package element

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type TimerBoundaryHandler struct{}

func (TimerBoundaryHandler) NodeType() bpmncore.FlowNodeType {
	return bpmncore.NodeTypeTimerBoundaryEvent
}

func (TimerBoundaryHandler) Validate(_ string, _ *bpmncore.BPMNProcess, _ *bpmncore.Graph, _ *bpmncore.BPMNDefinitions, _ map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}

// Compile traverses the continuation path that follows this timer boundary event.
// Called from CompileBoundaryPaths once per unvisited timer boundary.
func (TimerBoundaryHandler) Compile(nodeID string, cs *bpmncore.CompileState) error {
	targetID := bpmncore.OutgoingTargetOf(nodeID, cs.Proc)
	if targetID == "" {
		return nil
	}
	return cs.TraverseNode(targetID)
}

type MessageBoundaryHandler struct{}

func (MessageBoundaryHandler) NodeType() bpmncore.FlowNodeType {
	return bpmncore.NodeTypeMessageBoundaryEvent
}

func (MessageBoundaryHandler) Validate(_ string, _ *bpmncore.BPMNProcess, _ *bpmncore.Graph, _ *bpmncore.BPMNDefinitions, _ map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}

// Compile traverses the continuation path that follows this message boundary
// event. Called from CompileBoundaryPaths once per unvisited message boundary.
// A message boundary event with no outgoing flow is a valid terminal
// interrupt notification — targetID is empty and this is a no-op.
func (MessageBoundaryHandler) Compile(nodeID string, cs *bpmncore.CompileState) error {
	targetID := bpmncore.OutgoingTargetOf(nodeID, cs.Proc)
	if targetID == "" {
		return nil
	}
	return cs.TraverseNode(targetID)
}

type ErrorBoundaryHandler struct{}

func (ErrorBoundaryHandler) NodeType() bpmncore.FlowNodeType {
	return bpmncore.NodeTypeErrorBoundaryEvent
}

func (ErrorBoundaryHandler) Validate(_ string, _ *bpmncore.BPMNProcess, _ *bpmncore.Graph, _ *bpmncore.BPMNDefinitions, _ map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}

func (ErrorBoundaryHandler) Compile(_ string, _ *bpmncore.CompileState) error {
	return nil
}
