package element

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type EndEventHandler struct{}

func (EndEventHandler) NodeType() bpmncore.FlowNodeType { return bpmncore.NodeTypeEndEvent }

func (EndEventHandler) Validate(_ string, _ *bpmncore.BPMNProcess, _ *bpmncore.Graph, _ *bpmncore.BPMNDefinitions, _ map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}

func (EndEventHandler) Compile(_ string, cs *bpmncore.CompileState) error {
	cs.FlushSeqBuf()
	return nil
}
