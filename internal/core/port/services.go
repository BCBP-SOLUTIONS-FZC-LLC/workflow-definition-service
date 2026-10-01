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
	// CompileForPublish returns the shape stored as compiled_plan_json.
	CompileForPublish(ctx context.Context, bpmnXML string) (*dsl.CompiledCollaboration, error)
	// CompileDraft returns the same shape without the rules only publish
	// enforces, for comparing a version that need not be publishable.
	CompileDraft(ctx context.Context, bpmnXML string) (*dsl.CompiledCollaboration, error)
	Validate(ctx context.Context, bpmnXML string) ([]domain.BPMNValidationError, error)
	// ValidateForPublish is Validate plus the rules only the expanded plan
	// shows, so a document it accepts is one publish accepts.
	ValidateForPublish(ctx context.Context, bpmnXML string) ([]domain.BPMNValidationError, error)
	// ValidateModule validates a bundled module document with the module's own
	// process as the root, its open lanes allowed; global forbids naming users.
	ValidateModule(ctx context.Context, bpmnXML, moduleProcessID string, global bool) ([]domain.BPMNValidationError, error)
	// CompileModule compiles a bundled module document with the module's own
	// process as the root, judged as ValidateModule judges it.
	CompileModule(ctx context.Context, bpmnXML, moduleProcessID string) (*dsl.CompiledCollaboration, domain.ModuleDeclarations, error)
	Hash(ctx context.Context, bpmnXML string) (string, error)
	// ModuleCalls lists the calls in bpmnXML that name a library module (a
	// versionTag pin, or a target not in the document), plus issues with calls
	// that cannot name one.
	ModuleCalls(ctx context.Context, bpmnXML string) ([]domain.ModuleCall, []domain.BPMNValidationError, error)
	// BundleModules merges resolved library module versions into mainXML, each
	// under its plan name, and points every call at the version it resolved.
	BundleModules(mainXML string, modules []domain.ResolvedModule) (string, error)
	// ProcessID returns bpmnXML's single top-level <bpmn:process> id, erroring
	// if it contains zero or more than one.
	ProcessID(ctx context.Context, bpmnXML string) (string, error)
}

type SecretsClient interface {
	Write(ctx context.Context, path string, data map[string]string) error
	// Delete permanently destroys every version of the secret at path (OpenBao
	// KV-v2's metadata-delete, not the data-delete soft-delete which leaves it
	// recoverable) — a genuine revoke, not a reversible mark.
	Delete(ctx context.Context, path string) error
}
