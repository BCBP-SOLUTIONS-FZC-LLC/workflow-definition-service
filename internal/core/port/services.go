package port

import (
	"context"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type MembershipService interface {
	CheckEligibility(ctx context.Context, tenantID, userID uuid.UUID, departmentID, role string) (bool, error)
}

type ExecutionService interface {
	CheckActiveInstances(ctx context.Context, tenantID, workflowID uuid.UUID) (hasActive bool, count int32, err error)
	PauseUserTasks(ctx context.Context, tenantID, userID uuid.UUID) error
}

type PlanCompiler interface {
	Compile(ctx context.Context, bpmnXML string) (*dsl.CompiledPlan, error)
	CompileCollaboration(ctx context.Context, bpmnXML string) (*dsl.CompiledCollaboration, error)
	Validate(ctx context.Context, bpmnXML string) ([]domain.BPMNValidationError, error)
	Hash(ctx context.Context, bpmnXML string) (string, error)
	// Bundle merges module BPMNs into mainXML before compile/validate.
	Bundle(mainXML string, moduleXMLs []string) (string, error)
	// ProcessID returns bpmnXML's single top-level <bpmn:process> id, erroring
	// if it contains zero or more than one.
	ProcessID(ctx context.Context, bpmnXML string) (string, error)
}

type Authorizer interface {
	Authorize(userID string, roles []string, action string, resource string) error
}

type SecretsClient interface {
	Write(ctx context.Context, path string, data map[string]string) error
}
