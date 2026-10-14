package bpmncore

import "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"

type ElementHandler interface {
	NodeType() FlowNodeType
	Validate(nodeID string, proc *BPMNProcess, g *Graph, defs *BPMNDefinitions, stageTypes map[string]StageTypeHandler, elements map[FlowNodeType]ElementHandler) []domain.BPMNValidationError
	Compile(nodeID string, cs *CompileState) error
}

type StageTypeHandler interface {
	ID() string
	ActivityName() string
	ValidateProps(taskID string, props map[string]string) []domain.BPMNValidationError
}
