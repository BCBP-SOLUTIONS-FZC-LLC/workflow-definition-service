package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/events"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func buildPlan(deptIDs ...string) *dsl.CompiledPlan {
	depts := make([]dsl.DepartmentDef, 0, len(deptIDs))
	for _, id := range deptIDs {
		depts = append(depts, dsl.DepartmentDef{ID: id, Label: id})
	}
	return &dsl.CompiledPlan{Name: "test", Departments: depts}
}

func marshalledPlan(t *testing.T, plan *dsl.CompiledPlan) *string {
	t.Helper()
	b, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	s := string(b)
	return &s
}

func TestVersionService_List_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID := uuid.New(), uuid.New()
	versions := []*domain.WorkflowVersion{{ID: uuid.New()}}
	vRepo.EXPECT().ListByWorkflow(gomock.Any(), tenantID, wfID, 1, 20).Return(versions, int64(1), nil)

	got, count, err := svc.List(context.Background(), tenantID, wfID, 1, 20)
	if err != nil || len(got) != 1 || count != 1 {
		t.Fatalf("unexpected: %v %v %v", got, count, err)
	}
}

func TestVersionService_List_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().ListByWorkflow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, int64(0), errors.New("db error"))

	_, _, err := svc.List(context.Background(), uuid.New(), uuid.New(), 1, 10)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Get_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).
		Return(&domain.WorkflowVersion{ID: vID, WorkflowID: wfID}, nil)

	got, err := svc.Get(context.Background(), tenantID, wfID, vID)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
}

func TestVersionService_Get_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, err := svc.Get(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Get_WorkflowMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).
		Return(&domain.WorkflowVersion{ID: vID, WorkflowID: uuid.New()}, nil)

	_, err := svc.Get(context.Background(), tenantID, wfID, vID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Publish_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Outbox: outbox, Compiler: compiler,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	// Pre-flight (outside transaction)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan(), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("abc123", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil) // divergence check: first publish
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)

	// Transaction
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, int32(1), gomock.Any(), "abc123").Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	// No BulkInsert — empty plan produces no assignees
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
	if got.Status != domain.VersionStatusPublished {
		t.Errorf("expected published status, got %s", got.Status)
	}
}

// Publishing a draft whose compiled plan structurally diverges from the active
// version must be rejected unless force is set.
func TestVersionService_Publish_CheckDivergence_WorkflowGetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Workflows: wfRepo, Versions: vRepo, Compiler: compiler,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan("a"), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(nil, errors.New("db error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error from workflow GetByID failure")
	}
}

func TestVersionService_Publish_CheckDivergence_ActiveVersionGetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Workflows: wfRepo, Versions: vRepo, Compiler: compiler,
	})

	tenantID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan("a"), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).Return(nil, errors.New("db error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error from active version GetByID failure")
	}
}

func TestVersionService_Publish_StructuralDivergence(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Workflows: wfRepo, Versions: vRepo, Compiler: compiler,
	})

	tenantID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan("a"), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
	// Active baseline has a different department set → structural divergence.
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).
		Return(&domain.WorkflowVersion{CompiledPlanJSON: marshalledPlan(t, buildPlan("b"))}, nil)

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if !errors.Is(err, domain.ErrStructuralDivergence) {
		t.Fatalf("expected ErrStructuralDivergence, got %v", err)
	}
}

// force=true publishes through a structural divergence.
func TestVersionService_Publish_ForceStructural(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Outbox: outbox, Compiler: compiler,
	})

	tenantID, userID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan("a"), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).
		Return(&domain.WorkflowVersion{CompiledPlanJSON: marshalledPlan(t, buildPlan("b"))}, nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(2), nil)

	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, int32(2), gomock.Any(), "h").Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, true)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
	if got.Status != domain.VersionStatusPublished {
		t.Errorf("expected published status, got %s", got.Status)
	}
}

// An active version with no stored compiled plan (nil baseline) cannot diverge —
// publish proceeds. Also exercises an empty/unreadable baseline being treated as
// "allow" rather than a hard block.
func TestVersionService_Publish_NilBaselineAllowsPublish(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Outbox: outbox, Compiler: compiler,
	})

	tenantID, userID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan("a"), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
	// Active version exists but has no compiled plan stored → no baseline to compare.
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).
		Return(&domain.WorkflowVersion{CompiledPlanJSON: nil}, nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(2), nil)

	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, int32(2), gomock.Any(), "h").Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	if _, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// An unreadable stored baseline (invalid JSON) is treated as "allow publish",
// not a hard failure.
func TestVersionService_Publish_UnreadableBaselineAllowsPublish(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Outbox: outbox, Compiler: compiler,
	})

	tenantID, userID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}
	badJSON := "{not-json"

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan("a"), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).
		Return(&domain.WorkflowVersion{CompiledPlanJSON: &badJSON}, nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(2), nil)

	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, int32(2), gomock.Any(), "h").Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	if _, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVersionService_Publish_GetVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(nil, errors.New("db failure"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error from GetByID failure")
	}
}

func TestVersionService_Publish_VersionNotDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if !errors.Is(err, domain.ErrVersionNotDraft) {
		t.Fatalf("expected ErrVersionNotDraft, got %v", err)
	}
}

func TestVersionService_Publish_WorkflowMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: uuid.New(), Status: domain.VersionStatusDraft}, nil)

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Publish_BundleError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bad/>"}, nil)
	compiler.EXPECT().Bundle("<bad/>", gomock.Any()).Return("", errors.New("malformed module BPMN"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_CompileError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bad/>"}, nil)
	compiler.EXPECT().Bundle("<bad/>", gomock.Any()).Return("<bad/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bad/>").Return(nil, errors.New("parse error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_EligibilityFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	membership := mocks.NewMockMembershipService(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Versions: vRepo, Compiler: compiler, Membership: membership,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	assigneeID := uuid.Must(uuid.NewV7())
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID:              "finance",
			IAMDepartmentID: "finance",
			Stages: []dsl.StageDef{{
				Type:             "review",
				Role:             "reviewer",
				DefaultAssignees: []string{assigneeID.String()},
			}},
		}},
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("hash", nil)
	membership.EXPECT().CheckEligibility(gomock.Any(), tenantID, assigneeID, "finance", "reviewer").
		Return(false, nil)

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if !errors.Is(err, domain.ErrAssigneeIneligible) {
		t.Fatalf("expected ErrAssigneeIneligible, got %v", err)
	}
}

func TestVersionService_Publish_SkipEligibilityCheck(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	membership := mocks.NewMockMembershipService(ctrl) // set but must not be called
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Outbox: outbox, Compiler: compiler, Membership: membership,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan(), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil) // divergence check: first publish
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(2), nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	// forcePublishStructural=true — structural divergence bypassed; eligibility still runs (empty plan, no assignees)
	got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, true)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
}

func TestVersionService_Publish_WithAssignees_BulkInsert(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Outbox: outbox, Compiler: compiler,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	assigneeID := uuid.Must(uuid.NewV7())
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID:              "ops",
			IAMDepartmentID: uuid.New().String(),
			Stages: []dsl.StageDef{{
				Type:             "approve",
				Role:             "manager",
				DefaultAssignees: []string{assigneeID.String()},
			}},
		}},
	}
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("hash", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil) // divergence check: first publish
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, int32(1), gomock.Any(), "hash").Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	assignees.EXPECT().BulkInsert(gomock.Any(), tenantID, vID, gomock.Any()).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
}

func TestVersionService_Clone_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	source := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, BPMNXML: "<bpmn/>",
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(source, nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	newWF, newV, err := svc.Clone(context.Background(), tenantID, userID, wfID, vID, "enterprise",
		service.CloneReq{NewKey: "new-key", NewName: "New", NewDescription: "Copy"})
	if err != nil || newWF == nil || newV == nil {
		t.Fatalf("unexpected: %v %v %v", newWF, newV, err)
	}
	if newV.BPMNXML != "<bpmn/>" {
		t.Errorf("expected BPMN copied from source")
	}
}

func TestVersionService_Clone_SourceIsDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft}, nil)

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise", service.CloneReq{})
	if !errors.Is(err, domain.ErrVersionNotPublished) {
		t.Fatalf("expected ErrVersionNotPublished, got %v", err)
	}
}

func TestVersionService_Clone_WorkflowMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: uuid.New(), Status: domain.VersionStatusPublished}, nil)

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise", service.CloneReq{})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Clone_GetSourceError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, _, err := svc.Clone(context.Background(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), "enterprise", service.CloneReq{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Promote_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo, Outbox: outbox,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	prevID := uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{ID: wfID, ActiveVersionID: &prevID}, nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVersionService_Promote_AlreadyActive(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	// Target is already active → early exit: no transactor, no UpdateActiveVersion, no outbox.
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{ID: wfID, ActiveVersionID: &vID}, nil)

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVersionService_Promote_NotPublished(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft}, nil)

	_, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID)
	if !errors.Is(err, domain.ErrVersionNotPublished) {
		t.Fatalf("expected ErrVersionNotPublished, got %v", err)
	}
}

func TestVersionService_Promote_WorkflowMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: uuid.New(), Status: domain.VersionStatusPublished}, nil)

	_, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Promote_CodecEncodeError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	codec := mocks.NewMockGlueCodec(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Workflows: wfRepo, Versions: vRepo, GlueCodec: codec,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{ID: wfID}, nil)
	codec.EXPECT().Encode(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("glue unavailable"))

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID); err == nil {
		t.Fatal("expected error from codec.Encode failure in buildEnvelope")
	}
}

func TestVersionService_Promote_WorkflowGetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(nil, errors.New("db error"))

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID); err == nil {
		t.Fatal("expected error from workflow GetByID failure")
	}
}

func TestVersionService_Promote_GetVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	if _, err := svc.Promote(context.Background(), uuid.New(), uuid.New(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Promote_UpdateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo, Outbox: outbox,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{ID: wfID}, nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(errors.New("db error"))

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID); err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Promote_WithVersionNumberAndPlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo, Outbox: outbox,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vNum := int32(5)
	plan := `{"name":"p"}`
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(&domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished,
		VersionNumber: &vNum, CompiledPlanJSON: &plan,
	}, nil)
	// No active version → promotedFrom stays nil.
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{ID: wfID}, nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVersionService_Export_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vNum := int32(3)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, BPMNXML: "<bpmn/>", VersionNumber: &vNum}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{BusinessKey: "tender-review"}, nil)

	xml, filename, err := svc.Export(context.Background(), tenantID, wfID, vID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if xml != "<bpmn/>" {
		t.Errorf("unexpected xml: %s", xml)
	}
	if filename != "tender-review-v3.bpmn" {
		t.Errorf("unexpected filename: %s", filename)
	}
}

func TestVersionService_Export_Draft_Filename(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, BPMNXML: "<bpmn/>", VersionNumber: nil}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{BusinessKey: "my-wf"}, nil)

	_, filename, err := svc.Export(context.Background(), tenantID, wfID, vID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filename != "my-wf-vdraft.bpmn" {
		t.Errorf("unexpected filename: %s", filename)
	}
}

func TestVersionService_Export_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, _, err := svc.Export(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Export_WorkflowMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(
		&domain.WorkflowVersion{WorkflowID: uuid.New()}, nil)

	_, _, err := svc.Export(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Export_WorkflowGetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(nil, errors.New("db error"))

	_, _, err := svc.Export(context.Background(), tenantID, wfID, vID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Diff_UsesStoredPlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID := uuid.New(), uuid.New()
	baseID, targetID := uuid.New(), uuid.New()

	basePlan := buildPlan("finance")
	targetPlan := buildPlan("finance", "operations")

	// compiler.Compile must NOT be called — stored JSON used
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, CompiledPlanJSON: marshalledPlan(t, basePlan)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, CompiledPlanJSON: marshalledPlan(t, targetPlan)}, nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ChangeType != "STRUCTURAL" {
		t.Errorf("expected STRUCTURAL, got %s", result.ChangeType)
	}
	if len(result.Changes.AddedDepartments) != 1 || result.Changes.AddedDepartments[0] != "operations" {
		t.Errorf("expected operations added, got %v", result.Changes.AddedDepartments)
	}
}

func TestVersionService_Diff_FallsBackToCompile(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID := uuid.New(), uuid.New()
	baseID, targetID := uuid.New(), uuid.New()

	// No stored compiled plan — compiler.Compile must be called for both
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<base/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<target/>"}, nil)
	compiler.EXPECT().Bundle("<base/>", gomock.Any()).Return("<base/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<base/>").Return(buildPlan("a"), nil)
	compiler.EXPECT().Bundle("<target/>", gomock.Any()).Return("<target/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<target/>").Return(buildPlan("a"), nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ChangeType != "METADATA_ONLY" {
		t.Errorf("expected METADATA_ONLY, got %s", result.ChangeType)
	}
}

// TestVersionService_Diff_UnmarshalFailureFallsBackToCompile covers resolvePlan's
// other fallback trigger: CompiledPlanJSON is present (non-empty) but fails to
// unmarshal, as opposed to TestVersionService_Diff_FallsBackToCompile's case of
// CompiledPlanJSON being entirely absent. Both must fall through to
// compiler.Bundle + compiler.Compile.
func TestVersionService_Diff_UnmarshalFailureFallsBackToCompile(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID := uuid.New(), uuid.New()
	baseID, targetID := uuid.New(), uuid.New()
	garbage := "not valid json"

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<base/>", CompiledPlanJSON: &garbage}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<target/>", CompiledPlanJSON: &garbage}, nil)
	compiler.EXPECT().Bundle("<base/>", gomock.Any()).Return("<base/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<base/>").Return(buildPlan("a"), nil)
	compiler.EXPECT().Bundle("<target/>", gomock.Any()).Return("<target/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<target/>").Return(buildPlan("a"), nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ChangeType != "METADATA_ONLY" {
		t.Errorf("expected METADATA_ONLY, got %s", result.ChangeType)
	}
}

func TestVersionService_Diff_WorkflowMismatch_Base(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID := uuid.New(), uuid.New()
	baseID, targetID := uuid.New(), uuid.New()

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: uuid.New()}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID}, nil)

	_, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Diff_GetBaseError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(nil, domain.ErrNotFound)

	_, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Diff_GetTargetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(nil, domain.ErrNotFound)

	_, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Diff_CompileBaseError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<base/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<target/>"}, nil)
	compiler.EXPECT().Bundle("<base/>", gomock.Any()).Return("<base/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<base/>").Return(nil, errors.New("compile error"))

	_, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Diff_CompileTargetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<base/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<target/>"}, nil)
	compiler.EXPECT().Bundle("<base/>", gomock.Any()).Return("<base/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<base/>").Return(buildPlan(), nil)
	compiler.EXPECT().Bundle("<target/>", gomock.Any()).Return("<target/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<target/>").Return(nil, errors.New("compile error"))

	_, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Diff_WithStepChanges(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	basePlan := &dsl.CompiledPlan{
		Execution: dsl.ExecutionPlan{
			Steps: []dsl.ExecutionStep{{Sequential: []string{"step-a"}}},
		},
	}
	targetPlan := &dsl.CompiledPlan{
		Execution: dsl.ExecutionPlan{
			Steps: []dsl.ExecutionStep{
				{Sequential: []string{"step-a"}},
				{Sequential: []string{"step-b"}},
			},
		},
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<base/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<target/>"}, nil)
	compiler.EXPECT().Bundle("<base/>", gomock.Any()).Return("<base/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<base/>").Return(basePlan, nil)
	compiler.EXPECT().Bundle("<target/>", gomock.Any()).Return("<target/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<target/>").Return(targetPlan, nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ChangeType != "STRUCTURAL" {
		t.Errorf("expected STRUCTURAL (step added), got %s", result.ChangeType)
	}
	if len(result.Changes.StepChanges) != 1 {
		t.Errorf("expected 1 step change, got %d", len(result.Changes.StepChanges))
	}
}

func TestVersionService_Diff_StepRemoved(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	// Base has 2 steps, target has 1 — step removed
	basePlan := &dsl.CompiledPlan{
		Execution: dsl.ExecutionPlan{
			Steps: []dsl.ExecutionStep{
				{Sequential: []string{"a"}},
				{Sequential: []string{"b"}},
			},
		},
	}
	targetPlan := &dsl.CompiledPlan{
		Execution: dsl.ExecutionPlan{
			Steps: []dsl.ExecutionStep{{Sequential: []string{"a"}}},
		},
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<b/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<t/>"}, nil)
	compiler.EXPECT().Bundle("<b/>", gomock.Any()).Return("<b/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<b/>").Return(basePlan, nil)
	compiler.EXPECT().Bundle("<t/>", gomock.Any()).Return("<t/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<t/>").Return(targetPlan, nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Changes.StepChanges) != 1 {
		t.Errorf("expected 1 step change (removal), got %d", len(result.Changes.StepChanges))
	}
}

func publishPreFlightMocks(
	t *testing.T,
	vRepo *mocks.MockWorkflowVersionRepository,
	compiler *mocks.MockPlanCompiler,
	tenantID, wfID, vID uuid.UUID,
	plan *dsl.CompiledPlan,
) {
	t.Helper()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
}

func TestVersionService_Publish_CodecEncodeError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	codec := mocks.NewMockGlueCodec(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Versions: vRepo, Compiler: compiler, GlueCodec: codec,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan(), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
	codec.EXPECT().Encode(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("glue unavailable"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error from codec.Encode failure")
	}
}

func TestVersionService_Publish_HashError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan(), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("", errors.New("hash error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_NextVersionNumberError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Transactor: tx, Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan(), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(0), errors.New("db error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_DeleteByVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Versions: vRepo, Assignees: assignees, Compiler: compiler,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	publishPreFlightMocks(t, vRepo, compiler, tenantID, wfID, vID, buildPlan())
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("delete error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_BulkInsertError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Versions: vRepo, Assignees: assignees, Compiler: compiler,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	assigneeID := uuid.Must(uuid.NewV7())
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID:              "hr",
			IAMDepartmentID: uuid.New().String(),
			Stages: []dsl.StageDef{{
				Type:             "approve",
				Role:             "manager",
				DefaultAssignees: []string{assigneeID.String()},
			}},
		}},
	}
	publishPreFlightMocks(t, vRepo, compiler, tenantID, wfID, vID, plan)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	assignees.EXPECT().BulkInsert(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("bulk error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_UpdateActiveVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo, Assignees: assignees, Compiler: compiler,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	publishPreFlightMocks(t, vRepo, compiler, tenantID, wfID, vID, buildPlan())
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil) // divergence check: first publish
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("update error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_EnqueueError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Outbox: outbox, Compiler: compiler,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	publishPreFlightMocks(t, vRepo, compiler, tenantID, wfID, vID, buildPlan())
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil) // divergence check: first publish
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(errors.New("enqueue error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_InvalidAssigneeUUID(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	membership := mocks.NewMockMembershipService(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Versions: vRepo, Compiler: compiler, Membership: membership,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID: "legal",
			Stages: []dsl.StageDef{{
				Type:             "review",
				Role:             "approver",
				DefaultAssignees: []string{"not-a-valid-uuid"},
			}},
		}},
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if !errors.Is(err, domain.ErrAssigneeIneligible) {
		t.Fatalf("expected ErrAssigneeIneligible for invalid UUID, got %v", err)
	}
}

func TestVersionService_Publish_EligibilityCheckError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	membership := mocks.NewMockMembershipService(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Versions: vRepo, Compiler: compiler, Membership: membership,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	assigneeID := uuid.Must(uuid.NewV7())
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID:              "ops",
			IAMDepartmentID: "ops",
			Stages: []dsl.StageDef{{
				Type:             "approve",
				Role:             "manager",
				DefaultAssignees: []string{assigneeID.String()},
			}},
		}},
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	membership.EXPECT().CheckEligibility(gomock.Any(), tenantID, assigneeID, "ops", "manager").
		Return(false, errors.New("membership service error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Clone_CreateWorkflowError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusPublished, BPMNXML: "<bpmn/>"}, nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("duplicate key"))

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise", service.CloneReq{NewKey: "copy"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Clone_CreateVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusPublished, BPMNXML: "<bpmn/>"}, nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("version error"))

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise", service.CloneReq{NewKey: "copy"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_PublishVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Versions: vRepo, Assignees: assignees, Compiler: compiler,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	publishPreFlightMocks(t, vRepo, compiler, tenantID, wfID, vID, buildPlan())
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("publish db error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_EligibleAssignees_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	membership := mocks.NewMockMembershipService(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Outbox: outbox, Compiler: compiler, Membership: membership,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	assigneeID := uuid.Must(uuid.NewV7())
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID:              "finance",
			IAMDepartmentID: "018e1f2a-0000-7000-8000-000000000030",
			Stages: []dsl.StageDef{{
				Type:             "approve",
				Role:             "manager",
				DefaultAssignees: []string{assigneeID.String()},
			}},
		}},
	}
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil) // divergence check: first publish
	// All assignees eligible → checkAssigneeEligibility returns nil
	membership.EXPECT().CheckEligibility(gomock.Any(), tenantID, assigneeID, "018e1f2a-0000-7000-8000-000000000030", "manager").
		Return(true, nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, int32(1), gomock.Any(), "h").Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	assignees.EXPECT().BulkInsert(gomock.Any(), tenantID, vID, gomock.Any()).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
}

func TestVersionService_Publish_ExtractAssignees_InvalidUUID_Skipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Outbox: outbox, Compiler: compiler,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID:              "ops",
			IAMDepartmentID: uuid.New().String(),
			Stages: []dsl.StageDef{{
				Type:             "review",
				Role:             "reviewer",
				DefaultAssignees: []string{"not-a-uuid"}, // invalid UUID skipped in extractAssignees
			}},
		}},
	}
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil) // divergence check: first publish
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, int32(1), gomock.Any(), "h").Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	// BulkInsert NOT called — invalid UUID skipped by extractAssignees → empty slice
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	// forcePublishStructural=true; no membership dep → eligibility skipped
	got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, true)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
}

func TestVersionService_Diff_DepartmentRemoved(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	// Base has 2 depts, target has 1 — "old-dept" is removed
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<b/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<t/>"}, nil)
	compiler.EXPECT().Bundle("<b/>", gomock.Any()).Return("<b/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<b/>").Return(buildPlan("finance", "old-dept"), nil)
	compiler.EXPECT().Bundle("<t/>", gomock.Any()).Return("<t/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<t/>").Return(buildPlan("finance"), nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ChangeType != "STRUCTURAL" {
		t.Errorf("expected STRUCTURAL, got %s", result.ChangeType)
	}
	if len(result.Changes.RemovedDepartments) != 1 || result.Changes.RemovedDepartments[0] != "old-dept" {
		t.Errorf("expected old-dept removed, got %v", result.Changes.RemovedDepartments)
	}
}

func TestVersionService_Clone_QuotaNotExceeded(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := txPassthrough(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()

	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
	})

	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(3), nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	newWF, newV, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "starter",
		service.CloneReq{NewKey: "k", NewName: "n"})
	if err != nil || newWF == nil || newV == nil {
		t.Fatalf("unexpected: err=%v wf=%v v=%v", err, newWF, newV)
	}
}

func TestVersionService_Clone_QuotaExceeded(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := txPassthrough(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()

	svc := service.NewVersionService(service.VersionDeps{Transactor: tx, Workflows: wfRepo, Versions: vRepo})

	// Source is fetched before the (serializable) tx; the quota COUNT now runs inside it.
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(5), nil)

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "starter",
		service.CloneReq{NewKey: "k", NewName: "n"})
	if !errors.Is(err, domain.ErrPlanQuotaExceeded) {
		t.Fatalf("expected ErrPlanQuotaExceeded, got %v", err)
	}
}

func TestVersionService_Clone_QuotaCountError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := txPassthrough(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()

	svc := service.NewVersionService(service.VersionDeps{Transactor: tx, Workflows: wfRepo, Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(0), errors.New("db error"))

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "pro",
		service.CloneReq{NewKey: "k", NewName: "n"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestVersionService_Publish_EventPayload asserts the outbox envelope carries the
// correct fields (WorkflowKey, ArtifactHash, VersionID, PublishedBy) and that
// CompiledPlanJSON is NOT present in the payload (LLD §10.7 — SNS 256 KB limit).
func TestVersionService_Publish_EventPayload(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Outbox: outbox, Compiler: compiler,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	const businessKey = "invoice-approval-v2"
	const artifactHash = "sha256-deadbeef"

	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Bundle("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan(), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return(artifactHash, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{ID: wfID, BusinessKey: businessKey}, nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(3), nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, int32(3), gomock.Any(), artifactHash).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	var capturedPayload events.TemplatePublishedPayload
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, env any) error {
			// env is events.Envelope[json.RawMessage]; Payload holds the raw JSON.
			type envelopeWithPayload struct {
				Payload json.RawMessage `json:"data"`
			}
			b, err := json.Marshal(env)
			if err != nil {
				t.Fatalf("marshal envelope: %v", err)
			}
			var wrapper envelopeWithPayload
			if err := json.Unmarshal(b, &wrapper); err != nil {
				t.Fatalf("unmarshal envelope wrapper: %v", err)
			}
			if err := json.Unmarshal(wrapper.Payload, &capturedPayload); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}
			return nil
		})

	_, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedPayload.WorkflowKey != businessKey {
		t.Errorf("WorkflowKey = %q, want %q", capturedPayload.WorkflowKey, businessKey)
	}
	if capturedPayload.ArtifactHash != artifactHash {
		t.Errorf("ArtifactHash = %q, want %q", capturedPayload.ArtifactHash, artifactHash)
	}
	if capturedPayload.VersionID != vID.String() {
		t.Errorf("VersionID = %q, want %q", capturedPayload.VersionID, vID.String())
	}
	if capturedPayload.PublishedBy != userID.String() {
		t.Errorf("PublishedBy = %q, want %q", capturedPayload.PublishedBy, userID.String())
	}
	if capturedPayload.VersionNumber != 3 {
		t.Errorf("VersionNumber = %d, want 3", capturedPayload.VersionNumber)
	}
	if capturedPayload.PromotedFromVersionID != nil {
		t.Errorf("PromotedFromVersionID should be nil for first publish, got %v", *capturedPayload.PromotedFromVersionID)
	}

	// Verify CompiledPlanJSON is NOT in the payload (LLD §10.7 — SNS 256 KB limit).
	rawPayload, _ := json.Marshal(capturedPayload)
	assertNotContains(t, string(rawPayload), "compiled_plan_json")
}

func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			t.Errorf("payload must not contain %q (SNS size limit): %s", substr, s)
			return
		}
	}
}
