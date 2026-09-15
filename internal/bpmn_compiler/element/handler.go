package element

import "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"

func DefaultElementHandlers() map[bpmncore.FlowNodeType]bpmncore.ElementHandler {
	handlers := []bpmncore.ElementHandler{
		StartEventHandler{},
		EndEventHandler{},
		UserTaskHandler{},
		SendTaskHandler{},
		ReceiveTaskHandler{},
		SubProcessHandler{},
		CallActivityHandler{},
		ExclusiveGatewayHandler{},
		ParallelGatewayHandler{},
		ErrorBoundaryHandler{},
		TimerBoundaryHandler{},
		MessageBoundaryHandler{},
	}
	m := make(map[bpmncore.FlowNodeType]bpmncore.ElementHandler, len(handlers))
	for _, h := range handlers {
		m[h.NodeType()] = h
	}
	return m
}
