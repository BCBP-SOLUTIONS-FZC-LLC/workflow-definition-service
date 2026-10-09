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
	CompileForPublish(ctx context.Context, bpmnXML string) (*dsl.CompiledCollaboration, error)
	CompileDraft(ctx context.Context, bpmnXML string) (*dsl.CompiledCollaboration, error)
	Validate(ctx context.Context, bpmnXML string) ([]domain.BPMNValidationError, error)
	ValidateForPublish(ctx context.Context, bpmnXML string) ([]domain.BPMNValidationError, error)
	ValidateModule(ctx context.Context, bpmnXML, moduleProcessID string, global bool) ([]domain.BPMNValidationError, error)
	CompileModule(ctx context.Context, bpmnXML, moduleProcessID string) (*dsl.CompiledCollaboration, domain.ModuleDeclarations, error)
	Hash(ctx context.Context, bpmnXML string) (string, error)
	ModuleCalls(ctx context.Context, bpmnXML string) ([]domain.ModuleCall, []domain.BPMNValidationError, error)
	BundleModules(mainXML string, modules []domain.ResolvedModule) (string, error)
	ProcessID(ctx context.Context, bpmnXML string) (string, error)
}

type SecretsClient interface {
	Write(ctx context.Context, path string, data map[string]string) error
	Delete(ctx context.Context, path string) error
}
