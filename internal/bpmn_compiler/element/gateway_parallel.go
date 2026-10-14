package element

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type ParallelGatewayHandler struct{}

func (ParallelGatewayHandler) NodeType() bpmncore.FlowNodeType {
	return bpmncore.NodeTypeParallelGateway
}

func (ParallelGatewayHandler) Validate(_ string, _ *bpmncore.BPMNProcess, _ *bpmncore.Graph, _ *bpmncore.BPMNDefinitions, _ map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}

func (ParallelGatewayHandler) Compile(nodeID string, cs *bpmncore.CompileState) error {
	return cs.HandleGateway(nodeID, bpmncore.ParallelGateway)
}
