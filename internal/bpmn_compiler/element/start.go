package element

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type StartEventHandler struct{}

func (StartEventHandler) NodeType() bpmncore.FlowNodeType { return bpmncore.NodeTypeStartEvent }
func (StartEventHandler) EventKind() string               { return "start" }

func (StartEventHandler) Validate(_ string, _ *bpmncore.BPMNProcess, _ *bpmncore.Graph, _ *bpmncore.BPMNDefinitions, _ map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}

func (StartEventHandler) Compile(_ string, _ *bpmncore.CompileState) error {
	return nil
}
