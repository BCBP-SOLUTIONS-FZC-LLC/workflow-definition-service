package element

import "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"

// DefaultElementHandlers returns the built-in handler registry.
func DefaultElementHandlers() map[bpmncore.FlowNodeType]bpmncore.ElementHandler {
	handlers := []bpmncore.ElementHandler{
		StartEventHandler{},
		EndEventHandler{},
		UserTaskHandler{},
		SubProcessHandler{},
		ExclusiveGatewayHandler{},
		ParallelGatewayHandler{},
		TimerBoundaryHandler{},
		ErrorBoundaryHandler{},
	}
	m := make(map[bpmncore.FlowNodeType]bpmncore.ElementHandler, len(handlers))
	for _, h := range handlers {
		m[h.NodeType()] = h
	}
	return m
}
