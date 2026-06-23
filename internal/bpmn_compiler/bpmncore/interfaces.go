package bpmncore

import "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"

// ElementHandler is the base interface for all BPMN flow-node handlers.
// Adding a new element type means implementing this interface and registering
// it via WithElementHandler.
type ElementHandler interface {
	NodeType() FlowNodeType
	// Validate is called once per node ID of this type. stageTypes and elements
	// are passed so container handlers (e.g. SubProcessHandler) can recurse.
	Validate(nodeID string, proc *BPMNProcess, g *Graph, defs *BPMNDefinitions, stageTypes map[string]StageTypeHandler, elements map[FlowNodeType]ElementHandler) []domain.BPMNValidationError
	// Compile is called once per node ID of this type during plan traversal.
	Compile(nodeID string, cs *CompileState) error
}

type EventHandler interface {
	ElementHandler
	EventKind() string // "start" | "end" | "boundary"
}

type ActivityHandler interface {
	ElementHandler
	ActivityKind() string // "userTask" | "subProcess"
}

type GatewayHandler interface {
	ElementHandler
	GatewayKind() string // "exclusive" | "parallel" | "inclusive"
}

// Custom stage types are registered via WithStageType on the Compiler.
type StageTypeHandler interface {
	ID() string           // taskDefinition type attribute value: "prep", "review", "approve"
	ActivityName() string // Temporal activity name registered by the workers
	// ValidateProps validates Zeebe properties specific to this stage type.
	ValidateProps(taskID string, props map[string]string) []domain.BPMNValidationError
}
