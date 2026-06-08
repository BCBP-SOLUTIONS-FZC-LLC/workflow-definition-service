package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

// txPassthrough returns a MockTransactor whose RunInTx immediately invokes fn.
func txPassthrough(ctrl *gomock.Controller) *mocks.MockTransactor {
	tx := mocks.NewMockTransactor(ctrl)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	return tx
}

// ── List ─────────────────────────────────────────────────────────────────────

func TestWorkflowService_List_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{Workflows: wfRepo})

	tenantID := uuid.New()
	expected := []*domain.Workflow{{ID: uuid.New()}}
	wfRepo.EXPECT().List(gomock.Any(), tenantID, port.WorkflowFilter{}).Return(expected, int64(1), nil)

	got, count, err := svc.List(context.Background(), tenantID, port.WorkflowFilter{})
	if err != nil || len(got) != 1 || count != 1 {
		t.Fatalf("unexpected result: %v %v %v", got, count, err)
	}
}

func TestWorkflowService_List_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{Workflows: wfRepo})

	wfRepo.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, int64(0), errors.New("db error"))

	_, _, err := svc.List(context.Background(), uuid.New(), port.WorkflowFilter{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestWorkflowService_Create_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := txPassthrough(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
	})

	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	wf, v, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "key", "name", "desc", "<bpmn/>")
	if err != nil || wf == nil || v == nil {
		t.Fatalf("unexpected: %v %v %v", wf, v, err)
	}
	if v.Status != domain.VersionStatusDraft {
		t.Errorf("expected draft status, got %s", v.Status)
	}
}

func TestWorkflowService_Create_WorkflowCreateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := txPassthrough(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: tx, Workflows: wfRepo,
	})

	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(domain.ErrDuplicateBusinessKey)

	_, _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "key", "name", "", "<bpmn/>")
	if !errors.Is(err, domain.ErrDuplicateBusinessKey) {
		t.Fatalf("expected ErrDuplicateBusinessKey, got %v", err)
	}
}

func TestWorkflowService_Create_VersionCreateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := txPassthrough(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
	})

	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("version error"))

	_, _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "key", "name", "", "<bpmn/>")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestWorkflowService_Get_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID := uuid.New(), uuid.New()
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ID: wfID}, nil)
	vRepo.EXPECT().ListByWorkflow(gomock.Any(), tenantID, wfID, 1, 20).
		Return([]*domain.WorkflowVersion{{ID: uuid.New()}}, int64(1), nil)

	wf, versions, err := svc.Get(context.Background(), tenantID, wfID)
	if err != nil || wf == nil || len(versions) != 1 {
		t.Fatalf("unexpected result: %v %v %v", wf, versions, err)
	}
}

func TestWorkflowService_Get_WorkflowNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{Workflows: wfRepo})

	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, _, err := svc.Get(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestWorkflowService_Get_VersionListError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{Workflows: wfRepo, Versions: vRepo})

	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(&domain.Workflow{}, nil)
	vRepo.EXPECT().ListByWorkflow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, int64(0), errors.New("list error"))

	_, _, err := svc.Get(context.Background(), uuid.New(), uuid.New())
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── Archive ───────────────────────────────────────────────────────────────────

func TestWorkflowService_Archive_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo, Outbox: outbox, Execution: exec,
	})

	tenantID, userID, wfID, activeVID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ID: wfID, ActiveVersionID: &activeVID}, nil)
	exec.EXPECT().CheckActiveInstances(gomock.Any(), tenantID, wfID).Return(false, int32(0), nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Archive(gomock.Any(), tenantID, activeVID).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, (*uuid.UUID)(nil)).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	if err := svc.Archive(context.Background(), tenantID, userID, wfID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkflowService_Archive_NoActiveVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{Workflows: wfRepo})

	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Workflow{ActiveVersionID: nil}, nil)

	err := svc.Archive(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNoActiveVersion) {
		t.Fatalf("expected ErrNoActiveVersion, got %v", err)
	}
}

func TestWorkflowService_Archive_ActiveInstancesExist(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{Workflows: wfRepo, Execution: exec})

	activeVID := uuid.New()
	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Workflow{ActiveVersionID: &activeVID}, nil)
	exec.EXPECT().CheckActiveInstances(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(true, int32(3), nil)

	err := svc.Archive(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrActiveInstancesExist) {
		t.Fatalf("expected ErrActiveInstancesExist, got %v", err)
	}
}

func TestWorkflowService_Archive_ExecutionCheckError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{Workflows: wfRepo, Execution: exec})

	activeVID := uuid.New()
	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Workflow{ActiveVersionID: &activeVID}, nil)
	exec.EXPECT().CheckActiveInstances(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(false, int32(0), errors.New("grpc error"))

	if err := svc.Archive(context.Background(), uuid.New(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error")
	}
}

func TestWorkflowService_Archive_SkipsExecutionWhenNil(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo, Outbox: outbox,
	})

	tenantID, userID, wfID, activeVID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ActiveVersionID: &activeVID}, nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Archive(gomock.Any(), tenantID, activeVID).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, (*uuid.UUID)(nil)).Return(nil)
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	if err := svc.Archive(context.Background(), tenantID, userID, wfID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkflowService_Archive_GetWorkflowError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{Workflows: wfRepo})

	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	if err := svc.Archive(context.Background(), uuid.New(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error")
	}
}

func TestWorkflowService_Archive_VersionArchiveError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
	})

	activeVID := uuid.New()
	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Workflow{ActiveVersionID: &activeVID}, nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Archive(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("archive error"))

	if err := svc.Archive(context.Background(), uuid.New(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error")
	}
}

func TestWorkflowService_Archive_UpdateActiveVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
	})

	activeVID := uuid.New()
	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Workflow{ActiveVersionID: &activeVID}, nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Archive(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("update error"))

	if err := svc.Archive(context.Background(), uuid.New(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error")
	}
}
