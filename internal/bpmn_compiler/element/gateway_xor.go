package element

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type ExclusiveGatewayHandler struct{}

func (ExclusiveGatewayHandler) NodeType() bpmncore.FlowNodeType {
	return bpmncore.NodeTypeExclusiveGateway
}

func (ExclusiveGatewayHandler) Validate(_ string, _ *bpmncore.BPMNProcess, _ *bpmncore.Graph, _ *bpmncore.BPMNDefinitions, _ map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}

func (ExclusiveGatewayHandler) Compile(nodeID string, cs *bpmncore.CompileState) error {
	return cs.HandleGateway(nodeID, bpmncore.ExclusiveGateway)
}
