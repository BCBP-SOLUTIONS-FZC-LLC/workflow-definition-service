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

type starterMocks struct {
	starters       *mocks.MockStarterTemplateRepository
	globalStarters *mocks.MockStarterTemplateRepository
	versions       *mocks.MockWorkflowVersionRepository
	compiler       *mocks.MockPlanCompiler
}

func newStarterService(ctrl *gomock.Controller) (*service.StarterService, starterMocks) {
	m := starterMocks{
		starters:       mocks.NewMockStarterTemplateRepository(ctrl),
		globalStarters: mocks.NewMockStarterTemplateRepository(ctrl),
		versions:       mocks.NewMockWorkflowVersionRepository(ctrl),
		compiler:       mocks.NewMockPlanCompiler(ctrl),
	}
	svc := service.NewStarterService(service.StarterDeps{
		Starters:       m.starters,
		GlobalStarters: m.globalStarters,
		Versions:       m.versions,
		Compiler:       m.compiler,
	})
	return svc, m
}

func TestStarterService_List_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	tenantID := uuid.New()
	m.starters.EXPECT().List(gomock.Any(), tenantID, port.StarterFilter{}).
		Return([]*domain.StarterTemplate{{ID: uuid.New()}}, int64(1), nil)

	got, count, err := svc.List(context.Background(), tenantID, port.StarterFilter{})
	if err != nil || len(got) != 1 || count != 1 {
		t.Fatalf("unexpected: %v %v %v", got, count, err)
	}
}

func TestStarterService_List_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	m.starters.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, int64(0), errors.New("db error"))

	_, _, err := svc.List(context.Background(), uuid.New(), port.StarterFilter{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStarterService_Create_TenantScope_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	m.compiler.EXPECT().Validate(gomock.Any(), "<bpmn/>").Return(nil, nil)
	m.starters.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	tenantID, userID := uuid.New(), uuid.New()
	st, err := svc.Create(context.Background(), tenantID, userID, service.CreateStarterReq{
		Name: "s", BPMNXML: "<bpmn/>",
	})
	if err != nil || st == nil {
		t.Fatalf("unexpected: %v %v", st, err)
	}
	if st.Scope != domain.ScopeTenant || st.TenantID == nil || *st.TenantID != tenantID {
		t.Errorf("expected tenant scope bound to caller; got %+v", st)
	}
}

func TestStarterService_Create_GlobalScope_UsesSystemPool(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.globalStarters.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	st, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateStarterReq{
		Name: "s", BPMNXML: "<bpmn/>", Scope: domain.ScopeGlobal,
	})
	if err != nil || st == nil {
		t.Fatalf("unexpected: %v %v", st, err)
	}
	if st.Scope != domain.ScopeGlobal || st.TenantID != nil {
		t.Errorf("expected global scope with nil tenant_id; got %+v", st)
	}
}

func TestStarterService_Create_ValidationErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).
		Return([]domain.BPMNValidationError{{Code: "BAD"}}, nil)

	_, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateStarterReq{BPMNXML: "<bad/>"})
	var vfe *domain.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Fatalf("expected ValidationFailedError, got %v", err)
	}
}

func TestStarterService_Create_ValidateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, errors.New("compiler error"))

	_, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateStarterReq{BPMNXML: "<bpmn/>"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStarterService_Create_RepoError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.starters.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("insert error"))

	_, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateStarterReq{BPMNXML: "<bpmn/>"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStarterService_Get_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	id := uuid.New()
	m.starters.EXPECT().GetByID(gomock.Any(), gomock.Any(), id).Return(&domain.StarterTemplate{ID: id}, nil)

	st, err := svc.Get(context.Background(), uuid.New(), id)
	if err != nil || st == nil {
		t.Fatalf("unexpected: %v %v", st, err)
	}
}

func TestStarterService_Get_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	m.starters.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, err := svc.Get(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStarterService_CreateFromWorkflowVersion_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	tenantID, userID, sourceID := uuid.New(), uuid.New(), uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), tenantID, sourceID).
		Return(&domain.WorkflowVersion{ID: sourceID, Status: domain.VersionStatusPublished, BPMNXML: "<src/>"}, nil)
	m.starters.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	st, err := svc.CreateFromWorkflowVersion(context.Background(), tenantID, userID, sourceID, service.CreateFromWorkflowVersionReq{Name: "s"})
	if err != nil || st == nil {
		t.Fatalf("unexpected: %v %v", st, err)
	}
	if st.BPMNXML != "<src/>" || st.Scope != domain.ScopeTenant || st.SourceWorkflowVersionID == nil || *st.SourceWorkflowVersionID != sourceID {
		t.Errorf("unexpected starter: %+v", st)
	}
}

func TestStarterService_CreateFromWorkflowVersion_ArchivedSourceOK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	sourceID := uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), sourceID).
		Return(&domain.WorkflowVersion{ID: sourceID, Status: domain.VersionStatusArchived, BPMNXML: "<src/>"}, nil)
	m.starters.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.CreateFromWorkflowVersion(context.Background(), uuid.New(), uuid.New(), sourceID, service.CreateFromWorkflowVersionReq{Name: "s"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStarterService_CreateFromWorkflowVersion_SourceNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, err := svc.CreateFromWorkflowVersion(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.CreateFromWorkflowVersionReq{})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStarterService_CreateFromWorkflowVersion_SourceIsDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.WorkflowVersion{Status: domain.VersionStatusDraft}, nil)

	_, err := svc.CreateFromWorkflowVersion(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.CreateFromWorkflowVersionReq{})
	if !errors.Is(err, domain.ErrVersionNotPublished) {
		t.Fatalf("expected ErrVersionNotPublished, got %v", err)
	}
}

func TestStarterService_CreateFromWorkflowVersion_CreateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.WorkflowVersion{Status: domain.VersionStatusPublished}, nil)
	m.starters.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("insert error"))

	_, err := svc.CreateFromWorkflowVersion(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.CreateFromWorkflowVersionReq{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStarterService_Delete_TenantScope_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	id := uuid.New()
	m.starters.EXPECT().GetByID(gomock.Any(), gomock.Any(), id).Return(&domain.StarterTemplate{ID: id, Scope: domain.ScopeTenant}, nil)
	m.starters.EXPECT().Delete(gomock.Any(), id).Return(nil)

	if err := svc.Delete(context.Background(), uuid.New(), id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStarterService_Delete_GlobalScope_UsesSystemPool(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	id := uuid.New()
	m.starters.EXPECT().GetByID(gomock.Any(), gomock.Any(), id).Return(&domain.StarterTemplate{ID: id, Scope: domain.ScopeGlobal}, nil)
	m.globalStarters.EXPECT().Delete(gomock.Any(), id).Return(nil)

	if err := svc.Delete(context.Background(), uuid.New(), id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStarterService_Delete_GetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	m.starters.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	if err := svc.Delete(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStarterService_Delete_DeleteError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)

	m.starters.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(&domain.StarterTemplate{Scope: domain.ScopeTenant}, nil)
	m.starters.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(errors.New("db error"))

	if err := svc.Delete(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error")
	}
}
