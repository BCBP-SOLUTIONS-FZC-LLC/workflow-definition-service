package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func buildPlan(deptIDs ...string) *domain.CompiledPlan {
	depts := make([]domain.DepartmentDef, 0, len(deptIDs))
	for _, id := range deptIDs {
		depts = append(depts, domain.DepartmentDef{ID: id, Label: id})
	}
	return &domain.CompiledPlan{Name: "test", Departments: depts}
}

func marshalledPlan(t *testing.T, plan *domain.CompiledPlan) *string {
	t.Helper()
	b, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	s := string(b)
	return &s
}

// ── List ─────────────────────────────────────────────────────────────────────

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

// ── Get ───────────────────────────────────────────────────────────────────────

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

// ── Publish ───────────────────────────────────────────────────────────────────

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
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan(), nil)
	compiler.EXPECT().Hash("<bpmn/>").Return("abc123", nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)

	// Transaction
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
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

func TestVersionService_Publish_CompileError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bad/>"}, nil)
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
	assigneeID := uuid.New()
	plan := &domain.CompiledPlan{
		Departments: []domain.DepartmentDef{{
			ID: "finance",
			Stages: []domain.StageDef{{
				Type:             "review",
				Role:             "reviewer",
				DefaultAssignees: []string{assigneeID.String()},
			}},
		}},
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash("<bpmn/>").Return("hash", nil)
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
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan(), nil)
	compiler.EXPECT().Hash("<bpmn/>").Return("h", nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(2), nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	// skipEligibilityCheck=true — membership.CheckEligibility must NOT be called
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
	assigneeID := uuid.New()
	plan := &domain.CompiledPlan{
		Departments: []domain.DepartmentDef{{
			ID: "ops",
			Stages: []domain.StageDef{{
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
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash("<bpmn/>").Return("hash", nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
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

// ── Clone ─────────────────────────────────────────────────────────────────────

func TestVersionService_Clone_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo, Outbox: outbox,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	source := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, BPMNXML: "<bpmn/>",
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(source, nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	newWF, newV, err := svc.Clone(context.Background(), tenantID, userID, wfID, vID,
		service.CloneReq{NewKey: "new-key", NewName: "New", NewDescription: "Copy"})
	if err != nil || newWF == nil || newV == nil {
		t.Fatalf("unexpected: %v %v %v", newWF, newV, err)
	}
	if newV.BPMNXML != "<bpmn/>" {
		t.Errorf("expected BPMN copied from source")
	}
}

func TestVersionService_Clone_OutboxEnqueueError(t *testing.T) {
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
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusPublished, BPMNXML: "<bpmn/>"}, nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(errors.New("outbox error"))

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, service.CloneReq{NewKey: "copy"})
	if err == nil {
		t.Fatal("expected error from outbox.Enqueue")
	}
}

func TestVersionService_Clone_SourceIsDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft}, nil)

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, service.CloneReq{})
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

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, service.CloneReq{})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Clone_GetSourceError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, _, err := svc.Clone(context.Background(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), service.CloneReq{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── Promote ───────────────────────────────────────────────────────────────────

func TestVersionService_Promote_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	if err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID); err != nil {
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

	err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID)
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

	err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Promote_GetVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	if err := svc.Promote(context.Background(), uuid.New(), uuid.New(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Promote_UpdateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(errors.New("db error"))

	if err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID); err == nil {
		t.Fatal("expected error")
	}
}

// ── Export ────────────────────────────────────────────────────────────────────

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

// ── Diff ──────────────────────────────────────────────────────────────────────

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
	compiler.EXPECT().Compile(gomock.Any(), "<base/>").Return(buildPlan("a"), nil)
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
	compiler.EXPECT().Compile(gomock.Any(), "<base/>").Return(buildPlan(), nil)
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

	basePlan := &domain.CompiledPlan{
		Execution: domain.ExecutionPlan{
			Steps: []domain.ExecutionStep{{Sequential: []string{"step-a"}}},
		},
	}
	targetPlan := &domain.CompiledPlan{
		Execution: domain.ExecutionPlan{
			Steps: []domain.ExecutionStep{
				{Sequential: []string{"step-a"}},
				{Sequential: []string{"step-b"}},
			},
		},
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<base/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<target/>"}, nil)
	compiler.EXPECT().Compile(gomock.Any(), "<base/>").Return(basePlan, nil)
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
	basePlan := &domain.CompiledPlan{
		Execution: domain.ExecutionPlan{
			Steps: []domain.ExecutionStep{
				{Sequential: []string{"a"}},
				{Sequential: []string{"b"}},
			},
		},
	}
	targetPlan := &domain.CompiledPlan{
		Execution: domain.ExecutionPlan{
			Steps: []domain.ExecutionStep{{Sequential: []string{"a"}}},
		},
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<b/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<t/>"}, nil)
	compiler.EXPECT().Compile(gomock.Any(), "<b/>").Return(basePlan, nil)
	compiler.EXPECT().Compile(gomock.Any(), "<t/>").Return(targetPlan, nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Changes.StepChanges) != 1 {
		t.Errorf("expected 1 step change (removal), got %d", len(result.Changes.StepChanges))
	}
}

// ── Publish error paths (inner tx failures) ──────────────────────────────────

func publishPreFlightMocks(
	t *testing.T,
	vRepo *mocks.MockWorkflowVersionRepository,
	compiler *mocks.MockPlanCompiler,
	tenantID, wfID, vID uuid.UUID,
	plan *domain.CompiledPlan,
) {
	t.Helper()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash("<bpmn/>").Return("h", nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
}

func TestVersionService_Publish_HashError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan(), nil)
	compiler.EXPECT().Hash("<bpmn/>").Return("", errors.New("hash error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_NextVersionNumberError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(buildPlan(), nil)
	compiler.EXPECT().Hash("<bpmn/>").Return("h", nil)
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
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
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
	assigneeID := uuid.New()
	plan := &domain.CompiledPlan{
		Departments: []domain.DepartmentDef{{
			ID: "hr",
			Stages: []domain.StageDef{{
				Type:             "approve",
				Role:             "manager",
				DefaultAssignees: []string{assigneeID.String()},
			}},
		}},
	}
	publishPreFlightMocks(t, vRepo, compiler, tenantID, wfID, vID, plan)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
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
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
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
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
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
	plan := &domain.CompiledPlan{
		Departments: []domain.DepartmentDef{{
			ID: "legal",
			Stages: []domain.StageDef{{
				Type:             "review",
				Role:             "approver",
				DefaultAssignees: []string{"not-a-valid-uuid"},
			}},
		}},
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash("<bpmn/>").Return("h", nil)

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
	assigneeID := uuid.New()
	plan := &domain.CompiledPlan{
		Departments: []domain.DepartmentDef{{
			ID: "ops",
			Stages: []domain.StageDef{{
				Type:             "approve",
				Role:             "manager",
				DefaultAssignees: []string{assigneeID.String()},
			}},
		}},
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash("<bpmn/>").Return("h", nil)
	membership.EXPECT().CheckEligibility(gomock.Any(), tenantID, assigneeID, "ops", "manager").
		Return(false, errors.New("membership service error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── Clone error paths ────────────────────────────────────────────────────────

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
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("duplicate key"))

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, service.CloneReq{NewKey: "copy"})
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
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("version error"))

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, service.CloneReq{NewKey: "copy"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── Publish pre-flight: inner tx Publish error ────────────────────────────────

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
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("publish db error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── Publish with eligible assignees: covers checkAssigneeEligibility return nil ──

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
	assigneeID := uuid.New()
	plan := &domain.CompiledPlan{
		Departments: []domain.DepartmentDef{{
			ID: "finance",
			Stages: []domain.StageDef{{
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
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash("<bpmn/>").Return("h", nil)
	// All assignees eligible → checkAssigneeEligibility returns nil
	membership.EXPECT().CheckEligibility(gomock.Any(), tenantID, assigneeID, "finance", "manager").
		Return(true, nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
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

// ── extractAssignees: invalid UUID with skipEligibilityCheck ─────────────────

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
	plan := &domain.CompiledPlan{
		Departments: []domain.DepartmentDef{{
			ID: "ops",
			Stages: []domain.StageDef{{
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
	compiler.EXPECT().Compile(gomock.Any(), "<bpmn/>").Return(plan, nil)
	compiler.EXPECT().Hash("<bpmn/>").Return("h", nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, int32(1), gomock.Any(), "h").Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	// BulkInsert NOT called — invalid UUID skipped by extractAssignees → empty slice
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	// skipEligibilityCheck=true skips UUID validation in checkStageEligibility
	got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, true)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
}

// ── computeDiff: removed department ──────────────────────────────────────────

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
	compiler.EXPECT().Compile(gomock.Any(), "<b/>").Return(buildPlan("finance", "old-dept"), nil)
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
