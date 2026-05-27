package port

import (
	"context"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type MembershipService interface {
	CheckEligibility(ctx context.Context, tenantID, userID uuid.UUID, departmentID, role string) (bool, error)
}

type ExecutionService interface {
	CheckActiveInstances(ctx context.Context, tenantID, workflowID uuid.UUID) (hasActive bool, count int32, err error)
}

type PlanCompiler interface {
	Compile(ctx context.Context, bpmnXML string) (*domain.CompiledPlan, error)
	Validate(ctx context.Context, bpmnXML string) ([]domain.BPMNValidationError, error)
	Hash(bpmnXML string) (string, error)
}

type Authorizer interface {
	Authorize(userID string, roles []string, action string, resource string) error
}
