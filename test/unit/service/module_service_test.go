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

type moduleMocks struct {
	tx             *mocks.MockTransactor
	globalTx       *mocks.MockTransactor
	modules        *mocks.MockModuleRepository
	globalModules  *mocks.MockModuleRepository
	versions       *mocks.MockModuleVersionRepository
	globalVersions *mocks.MockModuleVersionRepository
	compiler       *mocks.MockPlanCompiler
}

func newModuleService(ctrl *gomock.Controller) (*service.ModuleService, moduleMocks) {
	m := moduleMocks{
		tx:             mocks.NewMockTransactor(ctrl),
		globalTx:       mocks.NewMockTransactor(ctrl),
		modules:        mocks.NewMockModuleRepository(ctrl),
		globalModules:  mocks.NewMockModuleRepository(ctrl),
		versions:       mocks.NewMockModuleVersionRepository(ctrl),
		globalVersions: mocks.NewMockModuleVersionRepository(ctrl),
		compiler:       mocks.NewMockPlanCompiler(ctrl),
	}
	svc := service.NewModuleService(service.ModuleDeps{
		Transactor:       m.tx,
		GlobalTransactor: m.globalTx,
		Modules:          m.modules,
		GlobalModules:    m.globalModules,
		Versions:         m.versions,
		GlobalVersions:   m.globalVersions,
		Compiler:         m.compiler,
	})
	return svc, m
}

func passthrough(tx *mocks.MockTransactor) {
	fn := func(ctx context.Context, f func(context.Context) error) error { return f(ctx) }
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(fn)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(fn)
}

func TestModuleService_List_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	tenantID := uuid.New()
	expected := []*domain.Module{{ID: uuid.New()}}
	m.modules.EXPECT().List(gomock.Any(), tenantID, port.ModuleFilter{}).Return(expected, int64(1), nil)

	got, count, err := svc.List(context.Background(), tenantID, port.ModuleFilter{})
	if err != nil || len(got) != 1 || count != 1 {
		t.Fatalf("unexpected result: %v %v %v", got, count, err)
	}
}

func TestModuleService_List_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.modules.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, int64(0), errors.New("db error"))

	_, _, err := svc.List(context.Background(), uuid.New(), port.ModuleFilter{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_Create_TenantScope_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)

	m.compiler.EXPECT().Validate(gomock.Any(), "<bpmn/>").Return(nil, nil)
	m.compiler.EXPECT().ProcessID(gomock.Any(), "<bpmn/>").Return("Process_1", nil)
	m.modules.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	m.versions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	tenantID, userID := uuid.New(), uuid.New()
	mod, v, err := svc.Create(context.Background(), tenantID, userID, service.CreateModuleReq{
		Name: "m", BPMNXML: "<bpmn/>",
	})
	if err != nil || mod == nil || v == nil {
		t.Fatalf("unexpected: %v %v %v", mod, v, err)
	}
	if mod.Scope != domain.ScopeTenant || mod.TenantID == nil || *mod.TenantID != tenantID {
		t.Errorf("expected tenant scope bound to caller; got %+v", mod)
	}
	if v.ProcessID != "Process_1" || v.Status != domain.VersionStatusDraft {
		t.Errorf("unexpected version: %+v", v)
	}
}

func TestModuleService_Create_GlobalScope_UsesSystemPool(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.globalTx)

	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.compiler.EXPECT().ProcessID(gomock.Any(), gomock.Any()).Return("Process_1", nil)
	m.globalModules.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	m.globalVersions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	mod, _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateModuleReq{
		Name: "m", BPMNXML: "<bpmn/>", Scope: domain.ScopeGlobal,
	})
	if err != nil || mod == nil {
		t.Fatalf("unexpected: %v %v", mod, err)
	}
	if mod.Scope != domain.ScopeGlobal || mod.TenantID != nil {
		t.Errorf("expected global scope with nil tenant_id; got %+v", mod)
	}
}

func TestModuleService_Create_ValidationErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).
		Return([]domain.BPMNValidationError{{Code: "MISSING_START"}}, nil)

	_, _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateModuleReq{BPMNXML: "<bad/>"})
	var vfe *domain.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Fatalf("expected ValidationFailedError, got %v", err)
	}
}

func TestModuleService_Create_ValidateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, errors.New("compiler error"))

	_, _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateModuleReq{BPMNXML: "<bpmn/>"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_Create_ProcessIDError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.compiler.EXPECT().ProcessID(gomock.Any(), gomock.Any()).Return("", errors.New("multiple processes"))

	_, _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateModuleReq{BPMNXML: "<bpmn/>"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_Create_ModuleCreateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)

	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.compiler.EXPECT().ProcessID(gomock.Any(), gomock.Any()).Return("P1", nil)
	m.modules.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("insert error"))

	_, _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateModuleReq{BPMNXML: "<bpmn/>"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_Create_VersionCreateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)

	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.compiler.EXPECT().ProcessID(gomock.Any(), gomock.Any()).Return("P1", nil)
	m.modules.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	m.versions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("insert error"))

	_, _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateModuleReq{BPMNXML: "<bpmn/>"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_Get_WithActiveVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	tenantID, moduleID, versionID := uuid.New(), uuid.New(), uuid.New()
	m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
		Return(&domain.Module{ID: moduleID, ActiveVersionID: &versionID}, nil)
	m.versions.EXPECT().GetByID(gomock.Any(), tenantID, versionID).
		Return(&domain.ModuleVersion{ID: versionID}, nil)

	mod, v, err := svc.Get(context.Background(), tenantID, moduleID)
	if err != nil || mod == nil || v == nil {
		t.Fatalf("unexpected: %v %v %v", mod, v, err)
	}
}

func TestModuleService_Get_NoActiveVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Module{ActiveVersionID: nil}, nil)

	mod, v, err := svc.Get(context.Background(), uuid.New(), uuid.New())
	if err != nil || mod == nil || v != nil {
		t.Fatalf("expected module with nil active version, got %v %v %v", mod, v, err)
	}
}

func TestModuleService_Get_ModuleNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, _, err := svc.Get(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestModuleService_Get_VersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	versionID := uuid.New()
	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Module{ActiveVersionID: &versionID}, nil)
	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("db error"))

	_, _, err := svc.Get(context.Background(), uuid.New(), uuid.New())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_ListVersions_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	tenantID, moduleID := uuid.New(), uuid.New()
	m.versions.EXPECT().ListByModule(gomock.Any(), tenantID, moduleID, 1, 20).
		Return([]*domain.ModuleVersion{{ID: uuid.New()}}, int64(1), nil)

	got, count, err := svc.ListVersions(context.Background(), tenantID, moduleID, 1, 20)
	if err != nil || len(got) != 1 || count != 1 {
		t.Fatalf("unexpected: %v %v %v", got, count, err)
	}
}

func TestModuleService_ListVersions_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.versions.EXPECT().ListByModule(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, int64(0), errors.New("db error"))

	_, _, err := svc.ListVersions(context.Background(), uuid.New(), uuid.New(), 1, 20)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_GetVersion_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	moduleID, versionID := uuid.New(), uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), versionID).
		Return(&domain.ModuleVersion{ID: versionID, ModuleID: moduleID}, nil)

	v, err := svc.GetVersion(context.Background(), uuid.New(), moduleID, versionID)
	if err != nil || v == nil {
		t.Fatalf("unexpected: %v %v", v, err)
	}
}

func TestModuleService_GetVersion_WrongModule(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	versionID := uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), versionID).
		Return(&domain.ModuleVersion{ID: versionID, ModuleID: uuid.New()}, nil)

	_, err := svc.GetVersion(context.Background(), uuid.New(), uuid.New(), versionID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestModuleService_GetVersion_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, err := svc.GetVersion(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestModuleService_AddVersion_ExplicitXML_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	tenantID, userID, moduleID := uuid.New(), uuid.New(), uuid.New()
	m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
		Return(&domain.Module{ID: moduleID, Scope: domain.ScopeTenant}, nil)
	m.compiler.EXPECT().Validate(gomock.Any(), "<new/>").Return(nil, nil)
	m.compiler.EXPECT().ProcessID(gomock.Any(), "<new/>").Return("P2", nil)
	m.versions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	xml := "<new/>"
	v, err := svc.AddVersion(context.Background(), tenantID, userID, moduleID, service.AddModuleVersionReq{BPMNXML: &xml})
	if err != nil || v == nil || v.ProcessID != "P2" {
		t.Fatalf("unexpected: %v %v", v, err)
	}
}

func TestModuleService_AddVersion_CopyForward_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	tenantID, moduleID, activeID := uuid.New(), uuid.New(), uuid.New()
	m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
		Return(&domain.Module{ID: moduleID, Scope: domain.ScopeTenant, ActiveVersionID: &activeID}, nil)
	m.versions.EXPECT().GetByID(gomock.Any(), tenantID, activeID).
		Return(&domain.ModuleVersion{ID: activeID, BPMNXML: "<active/>"}, nil)
	m.compiler.EXPECT().Validate(gomock.Any(), "<active/>").Return(nil, nil)
	m.compiler.EXPECT().ProcessID(gomock.Any(), "<active/>").Return("P1", nil)
	m.versions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	v, err := svc.AddVersion(context.Background(), tenantID, uuid.New(), moduleID, service.AddModuleVersionReq{})
	if err != nil || v == nil || v.BPMNXML != "<active/>" {
		t.Fatalf("unexpected: %v %v", v, err)
	}
}

func TestModuleService_AddVersion_ModuleNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, err := svc.AddVersion(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.AddModuleVersionReq{})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestModuleService_AddVersion_NoActiveVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Module{ActiveVersionID: nil}, nil)

	_, err := svc.AddVersion(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.AddModuleVersionReq{})
	if !errors.Is(err, domain.ErrNoActiveVersion) {
		t.Fatalf("expected ErrNoActiveVersion, got %v", err)
	}
}

func TestModuleService_AddVersion_ActiveVersionFetchError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	activeID := uuid.New()
	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Module{ActiveVersionID: &activeID}, nil)
	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), activeID).Return(nil, errors.New("db error"))

	_, err := svc.AddVersion(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.AddModuleVersionReq{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_AddVersion_ValidationErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(&domain.Module{}, nil)
	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).
		Return([]domain.BPMNValidationError{{Code: "BAD"}}, nil)

	xml := "<bad/>"
	_, err := svc.AddVersion(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.AddModuleVersionReq{BPMNXML: &xml})
	var vfe *domain.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Fatalf("expected ValidationFailedError, got %v", err)
	}
}

func TestModuleService_AddVersion_ValidateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(&domain.Module{}, nil)
	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, errors.New("compiler error"))

	xml := "<x/>"
	_, err := svc.AddVersion(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.AddModuleVersionReq{BPMNXML: &xml})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_AddVersion_ProcessIDError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(&domain.Module{}, nil)
	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.compiler.EXPECT().ProcessID(gomock.Any(), gomock.Any()).Return("", errors.New("multiple processes"))

	xml := "<x/>"
	_, err := svc.AddVersion(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.AddModuleVersionReq{BPMNXML: &xml})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_AddVersion_CreateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(&domain.Module{}, nil)
	m.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.compiler.EXPECT().ProcessID(gomock.Any(), gomock.Any()).Return("P1", nil)
	m.versions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(domain.ErrDraftAlreadyExists)

	xml := "<x/>"
	_, err := svc.AddVersion(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.AddModuleVersionReq{BPMNXML: &xml})
	if !errors.Is(err, domain.ErrDraftAlreadyExists) {
		t.Fatalf("expected ErrDraftAlreadyExists, got %v", err)
	}
}

func TestModuleService_Publish_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)

	tenantID, userID, moduleID, versionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), tenantID, versionID).
		Return(&domain.ModuleVersion{ID: versionID, ModuleID: moduleID, Status: domain.VersionStatusDraft, Scope: domain.ScopeTenant}, nil)
	m.compiler.EXPECT().Compile(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.versions.EXPECT().NextVersionNumber(gomock.Any(), moduleID).Return(int32(1), nil)
	m.versions.EXPECT().Publish(gomock.Any(), versionID, int32(1)).Return(nil)
	m.modules.EXPECT().UpdateActiveVersion(gomock.Any(), moduleID, &versionID).Return(nil)

	v, err := svc.Publish(context.Background(), tenantID, userID, moduleID, versionID)
	if err != nil || v == nil || v.Status != domain.VersionStatusPublished {
		t.Fatalf("unexpected: %v %v", v, err)
	}
}

func TestModuleService_Publish_GlobalScope_UsesSystemPool(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.globalTx)

	moduleID, versionID := uuid.New(), uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), versionID).
		Return(&domain.ModuleVersion{ID: versionID, ModuleID: moduleID, Status: domain.VersionStatusDraft, Scope: domain.ScopeGlobal}, nil)
	m.compiler.EXPECT().Compile(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.globalVersions.EXPECT().NextVersionNumber(gomock.Any(), moduleID).Return(int32(1), nil)
	m.globalVersions.EXPECT().Publish(gomock.Any(), versionID, int32(1)).Return(nil)
	m.globalModules.EXPECT().UpdateActiveVersion(gomock.Any(), moduleID, &versionID).Return(nil)

	_, err := svc.Publish(context.Background(), uuid.New(), uuid.New(), moduleID, versionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModuleService_Publish_GetVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, err := svc.Publish(context.Background(), uuid.New(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestModuleService_Publish_WrongModule(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	versionID := uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), versionID).
		Return(&domain.ModuleVersion{ID: versionID, ModuleID: uuid.New(), Status: domain.VersionStatusDraft}, nil)

	_, err := svc.Publish(context.Background(), uuid.New(), uuid.New(), uuid.New(), versionID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestModuleService_Publish_NotDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	moduleID, versionID := uuid.New(), uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), versionID).
		Return(&domain.ModuleVersion{ID: versionID, ModuleID: moduleID, Status: domain.VersionStatusPublished}, nil)

	_, err := svc.Publish(context.Background(), uuid.New(), uuid.New(), moduleID, versionID)
	if !errors.Is(err, domain.ErrInvalidVersionStatus) {
		t.Fatalf("expected ErrInvalidVersionStatus, got %v", err)
	}
}

func TestModuleService_Publish_CompileError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	moduleID, versionID := uuid.New(), uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), versionID).
		Return(&domain.ModuleVersion{ID: versionID, ModuleID: moduleID, Status: domain.VersionStatusDraft}, nil)
	m.compiler.EXPECT().Compile(gomock.Any(), gomock.Any()).Return(nil, errors.New("compile error"))

	_, err := svc.Publish(context.Background(), uuid.New(), uuid.New(), moduleID, versionID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_Publish_NextVersionNumberError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)

	moduleID, versionID := uuid.New(), uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), versionID).
		Return(&domain.ModuleVersion{ID: versionID, ModuleID: moduleID, Status: domain.VersionStatusDraft}, nil)
	m.compiler.EXPECT().Compile(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.versions.EXPECT().NextVersionNumber(gomock.Any(), moduleID).Return(int32(0), errors.New("db error"))

	_, err := svc.Publish(context.Background(), uuid.New(), uuid.New(), moduleID, versionID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_Publish_PublishError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)

	moduleID, versionID := uuid.New(), uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), versionID).
		Return(&domain.ModuleVersion{ID: versionID, ModuleID: moduleID, Status: domain.VersionStatusDraft}, nil)
	m.compiler.EXPECT().Compile(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.versions.EXPECT().NextVersionNumber(gomock.Any(), moduleID).Return(int32(1), nil)
	m.versions.EXPECT().Publish(gomock.Any(), versionID, int32(1)).Return(domain.ErrVersionNotDraft)

	_, err := svc.Publish(context.Background(), uuid.New(), uuid.New(), moduleID, versionID)
	if !errors.Is(err, domain.ErrVersionNotDraft) {
		t.Fatalf("expected ErrVersionNotDraft, got %v", err)
	}
}

func TestModuleService_Publish_UpdateActiveVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)

	moduleID, versionID := uuid.New(), uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), gomock.Any(), versionID).
		Return(&domain.ModuleVersion{ID: versionID, ModuleID: moduleID, Status: domain.VersionStatusDraft}, nil)
	m.compiler.EXPECT().Compile(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.versions.EXPECT().NextVersionNumber(gomock.Any(), moduleID).Return(int32(1), nil)
	m.versions.EXPECT().Publish(gomock.Any(), versionID, int32(1)).Return(nil)
	m.modules.EXPECT().UpdateActiveVersion(gomock.Any(), moduleID, &versionID).Return(errors.New("db error"))

	_, err := svc.Publish(context.Background(), uuid.New(), uuid.New(), moduleID, versionID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_Archive_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)

	tenantID, userID, moduleID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
		Return(&domain.Module{ID: moduleID, Scope: domain.ScopeTenant, ActiveVersionID: &activeID}, nil)
	m.versions.EXPECT().Archive(gomock.Any(), activeID).Return(nil)
	m.modules.EXPECT().UpdateActiveVersion(gomock.Any(), moduleID, (*uuid.UUID)(nil)).Return(nil)

	if err := svc.Archive(context.Background(), tenantID, userID, moduleID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModuleService_Archive_GlobalScope_UsesSystemPool(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.globalTx)

	moduleID, activeID := uuid.New(), uuid.New()
	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), moduleID).
		Return(&domain.Module{ID: moduleID, Scope: domain.ScopeGlobal, ActiveVersionID: &activeID}, nil)
	m.globalVersions.EXPECT().Archive(gomock.Any(), activeID).Return(nil)
	m.globalModules.EXPECT().UpdateActiveVersion(gomock.Any(), moduleID, (*uuid.UUID)(nil)).Return(nil)

	if err := svc.Archive(context.Background(), uuid.New(), uuid.New(), moduleID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModuleService_Archive_GetModuleError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	if err := svc.Archive(context.Background(), uuid.New(), uuid.New(), uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestModuleService_Archive_NoActiveVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)

	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Module{ActiveVersionID: nil}, nil)

	err := svc.Archive(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNoActiveVersion) {
		t.Fatalf("expected ErrNoActiveVersion, got %v", err)
	}
}

func TestModuleService_Archive_ArchiveError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)

	activeID := uuid.New()
	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Module{ActiveVersionID: &activeID}, nil)
	m.versions.EXPECT().Archive(gomock.Any(), activeID).Return(errors.New("db error"))

	if err := svc.Archive(context.Background(), uuid.New(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleService_Archive_UpdateActiveVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)

	activeID := uuid.New()
	m.modules.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Module{ActiveVersionID: &activeID}, nil)
	m.versions.EXPECT().Archive(gomock.Any(), activeID).Return(nil)
	m.modules.EXPECT().UpdateActiveVersion(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("db error"))

	if err := svc.Archive(context.Background(), uuid.New(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error")
	}
}
