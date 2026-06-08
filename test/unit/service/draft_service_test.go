package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func newDraftSvc(ctrl *gomock.Controller,
	cache *mocks.MockCacheStore,
	wfRepo *mocks.MockWorkflowRepository,
	vRepo *mocks.MockWorkflowVersionRepository,
) *service.DraftService {
	// Avoid typed-nil: a nil *MockCacheStore still satisfies port.CacheStore
	// as a non-nil interface, which would bypass the nil-guard in DraftService.
	var cacheStore port.CacheStore
	if cache != nil {
		cacheStore = cache
	}
	return service.NewDraftService(service.DraftDeps{
		Cache:     cacheStore,
		Workflows: wfRepo,
		Versions:  vRepo,
		Assignees: mocks.NewMockAssigneeRepository(ctrl),
	})
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestDraftService_Get_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, nil, vRepo)

	tenantID, wfID := uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{ID: uuid.New(), Status: domain.VersionStatusDraft}
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(draft, nil)

	got, err := svc.Get(context.Background(), tenantID, wfID)
	if err != nil || got.ID != draft.ID {
		t.Fatalf("unexpected: %v %v", got, err)
	}
}

func TestDraftService_Get_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, nil, vRepo)

	vRepo.EXPECT().GetDraft(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNoDraftExists)

	_, err := svc.Get(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNoDraftExists) {
		t.Fatalf("expected ErrNoDraftExists, got %v", err)
	}
}

// ── Init ──────────────────────────────────────────────────────────────────────

func TestDraftService_Init_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, wfRepo, vRepo)

	tenantID, userID, wfID, activeVID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ActiveVersionID: &activeVID}, nil)
	active := &domain.WorkflowVersion{ID: activeVID, BPMNXML: "<bpmn/>"}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeVID).Return(active, nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	draft, err := svc.Init(context.Background(), tenantID, userID, wfID)
	if err != nil || draft == nil {
		t.Fatalf("unexpected: %v %v", draft, err)
	}
	if draft.BPMNXML != "<bpmn/>" {
		t.Errorf("expected BPMN copied from active version")
	}
	if draft.Status != domain.VersionStatusDraft {
		t.Errorf("expected draft status")
	}
}

func TestDraftService_Init_NoActiveVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, wfRepo, vRepo)

	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Workflow{ActiveVersionID: nil}, nil)

	_, err := svc.Init(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNoActiveVersion) {
		t.Fatalf("expected ErrNoActiveVersion, got %v", err)
	}
}

func TestDraftService_Init_GetWorkflowError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, wfRepo, vRepo)

	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, err := svc.Init(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDraftService_Init_GetActiveVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, wfRepo, vRepo)

	activeVID := uuid.New()
	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Workflow{ActiveVersionID: &activeVID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), activeVID).Return(nil, errors.New("db error"))

	_, err := svc.Init(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDraftService_Init_CreateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, wfRepo, vRepo)

	activeVID := uuid.New()
	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Workflow{ActiveVersionID: &activeVID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), activeVID).
		Return(&domain.WorkflowVersion{BPMNXML: "<bpmn/>"}, nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(domain.ErrDraftAlreadyExists)

	_, err := svc.Init(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrDraftAlreadyExists) {
		t.Fatalf("expected ErrDraftAlreadyExists, got %v", err)
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestDraftService_Update_OK_BPMNOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	cache := mocks.NewMockCacheStore(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, cache, wfRepo, vRepo)

	tenantID, userID, wfID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	draft := &domain.WorkflowVersion{ID: uuid.New(), UpdatedAt: now}

	cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), "1", 30*time.Second).Return(true, nil)
	cache.EXPECT().Del(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(draft, nil)
	vRepo.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).Return(nil)

	newXML := "<bpmn2/>"
	got, err := svc.Update(context.Background(), tenantID, userID, wfID, service.UpdateDraftReq{
		BPMNXML:       &newXML,
		LastUpdatedAt: &now,
	})
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
	if got.BPMNXML != newXML {
		t.Errorf("expected BPMNXML to be updated, got %s", got.BPMNXML)
	}
}

func TestDraftService_Update_OK_MetadataOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	cache := mocks.NewMockCacheStore(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, cache, wfRepo, vRepo)

	tenantID, userID, wfID := uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{ID: uuid.New()}

	cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), "1", 30*time.Second).Return(true, nil)
	cache.EXPECT().Del(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(draft, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{Name: "old", Description: "old-desc"}, nil)
	wfRepo.EXPECT().UpdateMetadata(gomock.Any(), tenantID, wfID, "new-name", "old-desc").Return(nil)
	vRepo.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).Return(nil)

	newName := "new-name"
	_, err := svc.Update(context.Background(), tenantID, userID, wfID, service.UpdateDraftReq{
		Name: &newName,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDraftService_Update_OK_DescriptionOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	cache := mocks.NewMockCacheStore(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, cache, wfRepo, vRepo)

	tenantID, userID, wfID := uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{ID: uuid.New()}

	cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), "1", 30*time.Second).Return(true, nil)
	cache.EXPECT().Del(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(draft, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{Name: "kept-name", Description: "old"}, nil)
	newDesc := "new-desc"
	wfRepo.EXPECT().UpdateMetadata(gomock.Any(), tenantID, wfID, "kept-name", newDesc).Return(nil)
	vRepo.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.Update(context.Background(), tenantID, userID, wfID, service.UpdateDraftReq{
		Description: &newDesc,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDraftService_Update_LockNotAcquired(t *testing.T) {
	ctrl := gomock.NewController(t)
	cache := mocks.NewMockCacheStore(ctrl)
	svc := newDraftSvc(ctrl, cache, nil, nil)

	cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), "1", 30*time.Second).Return(false, nil)

	_, err := svc.Update(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.UpdateDraftReq{})
	if !errors.Is(err, domain.ErrDraftConcurrency) {
		t.Fatalf("expected ErrDraftConcurrency, got %v", err)
	}
}

func TestDraftService_Update_LockError(t *testing.T) {
	ctrl := gomock.NewController(t)
	cache := mocks.NewMockCacheStore(ctrl)
	svc := newDraftSvc(ctrl, cache, nil, nil)

	cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), "1", 30*time.Second).
		Return(false, errors.New("cache error"))

	_, err := svc.Update(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.UpdateDraftReq{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDraftService_Update_StaleTimestamp(t *testing.T) {
	ctrl := gomock.NewController(t)
	cache := mocks.NewMockCacheStore(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, cache, nil, vRepo)

	tenantID, wfID := uuid.New(), uuid.New()
	dbTime := time.Now()
	clientTime := dbTime.Add(-5 * time.Minute) // client has stale timestamp

	cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), "1", 30*time.Second).Return(true, nil)
	cache.EXPECT().Del(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).
		Return(&domain.WorkflowVersion{UpdatedAt: dbTime}, nil)

	_, err := svc.Update(context.Background(), tenantID, uuid.New(), wfID, service.UpdateDraftReq{
		LastUpdatedAt: &clientTime,
	})
	if !errors.Is(err, domain.ErrDraftConcurrency) {
		t.Fatalf("expected ErrDraftConcurrency, got %v", err)
	}
}

func TestDraftService_Update_GetDraftError(t *testing.T) {
	ctrl := gomock.NewController(t)
	cache := mocks.NewMockCacheStore(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, cache, nil, vRepo)

	cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), "1", 30*time.Second).Return(true, nil)
	cache.EXPECT().Del(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	vRepo.EXPECT().GetDraft(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNoDraftExists)

	_, err := svc.Update(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.UpdateDraftReq{})
	if !errors.Is(err, domain.ErrNoDraftExists) {
		t.Fatalf("expected ErrNoDraftExists, got %v", err)
	}
}

func TestDraftService_Update_NilCache_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	// No cache configured — should fail-open and proceed without locking
	svc := newDraftSvc(ctrl, nil, nil, vRepo)

	vRepo.EXPECT().GetDraft(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.WorkflowVersion{}, nil)
	vRepo.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.Update(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.UpdateDraftReq{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDraftService_Update_UpdateMetadataError(t *testing.T) {
	ctrl := gomock.NewController(t)
	cache := mocks.NewMockCacheStore(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, cache, wfRepo, vRepo)

	cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), "1", 30*time.Second).Return(true, nil)
	cache.EXPECT().Del(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	vRepo.EXPECT().GetDraft(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.WorkflowVersion{}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Workflow{Name: "n", Description: "d"}, nil)
	wfRepo.EXPECT().UpdateMetadata(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("metadata error"))

	newName := "x"
	_, err := svc.Update(context.Background(), uuid.New(), uuid.New(), uuid.New(),
		service.UpdateDraftReq{Name: &newName})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDraftService_Update_UpdateDraftError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, nil, vRepo)

	vRepo.EXPECT().GetDraft(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.WorkflowVersion{}, nil)
	vRepo.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).Return(errors.New("update error"))

	_, err := svc.Update(context.Background(), uuid.New(), uuid.New(), uuid.New(), service.UpdateDraftReq{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDraftService_Update_GetWorkflowForMetaError(t *testing.T) {
	ctrl := gomock.NewController(t)
	cache := mocks.NewMockCacheStore(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, cache, wfRepo, vRepo)

	cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), "1", 30*time.Second).Return(true, nil)
	cache.EXPECT().Del(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	vRepo.EXPECT().GetDraft(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.WorkflowVersion{}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	newDesc := "desc"
	_, err := svc.Update(context.Background(), uuid.New(), uuid.New(), uuid.New(),
		service.UpdateDraftReq{Description: &newDesc})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// ── Discard ───────────────────────────────────────────────────────────────────

func TestDraftService_Discard_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, nil, vRepo)

	tenantID, wfID, draftID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).
		Return(&domain.WorkflowVersion{ID: draftID}, nil)
	vRepo.EXPECT().DeleteDraft(gomock.Any(), tenantID, draftID).Return(nil)

	if err := svc.Discard(context.Background(), tenantID, wfID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDraftService_Discard_GetDraftError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, nil, vRepo)

	vRepo.EXPECT().GetDraft(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNoDraftExists)

	err := svc.Discard(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNoDraftExists) {
		t.Fatalf("expected ErrNoDraftExists, got %v", err)
	}
}

func TestDraftService_Discard_DeleteError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, nil, vRepo)

	vRepo.EXPECT().GetDraft(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.WorkflowVersion{ID: uuid.New()}, nil)
	vRepo.EXPECT().DeleteDraft(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("delete error"))

	if err := svc.Discard(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error")
	}
}
