package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

func newMembershipSvc(
	ctrl *gomock.Controller,
	tx *mocks.MockTransactor,
	vRepo *mocks.MockWorkflowVersionRepository,
	aRepo *mocks.MockAssigneeRepository,
	outbox *mocks.MockOutboxRepository,
	pe *mocks.MockProcessedEventRepository,
	execution *mocks.MockExecutionService,
) *service.VersionService {
	return service.NewVersionService(service.VersionDeps{
		Transactor:      tx,
		Versions:        vRepo,
		Assignees:       aRepo,
		Outbox:          outbox,
		ProcessedEvents: pe,
		Execution:       execution,
		Log:             testkit.FakeLogger{},
	})
}

var (
	mTenantID = uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	mUserID   = uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	mEventID  = uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	mDeptID   = "018e1f2a-0000-7000-8000-000000000010"
	mVerID    = uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd")
	mVerID2   = uuid.MustParse("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")
)

func assignee(versionID uuid.UUID, nodeKey, deptID string) *domain.NodeAssignee {
	return &domain.NodeAssignee{
		WorkflowVersionID: versionID,
		NodeKey:           nodeKey,
		UserID:            mUserID,
		DepartmentID:      uuid.MustParse(deptID),
		Role:              "reviewer",
	}
}

// RecordIfNew now runs after all side effects — a failure at the end returns an
// error so the consumer retries (at-least-once guarantee preserved).
func TestHandleMembershipRevoked_RecordIfNewError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	// No matching assignees; PauseUserTasks succeeds; then RecordIfNew fails.
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", "018e1f2a-0000-7000-8000-000000000020")}, nil)
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)
	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, "membership-wf-q", "department.membership.revoked").Return(false, errors.New("db error"))

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe, exec)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err == nil {
		t.Fatal("expected error from RecordIfNew, got nil")
	}
}

// ListByUser fails before RecordIfNew is reached — no dedup record is written so
// the consumer can retry the full handler.
func TestHandleMembershipRevoked_ListByUserError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).Return(nil, errors.New("db error"))

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe, exec)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err == nil {
		t.Fatal("expected error from ListByUser, got nil")
	}
}

func TestHandleMembershipRevoked_VersionGetByIDError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	// Version may have been concurrently deleted — handler skips it silently.
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).Return(nil, errors.New("not found"))
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)
	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, "membership-wf-q", "department.membership.revoked").Return(true, nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe, exec)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("expected nil (silent skip), got %v", err)
	}
}

// AlreadyProcessed: a concurrent delivery ran simultaneously, completed all work,
// and recorded the event first. This delivery runs all idempotent side effects
// safely, then finds isNew=false at RecordIfNew and returns nil.
func TestHandleMembershipRevoked_AlreadyProcessed(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	// No matching assignees for this department; PauseUserTasks still called.
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", "018e1f2a-0000-7000-8000-000000000020")}, nil)
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)
	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, "membership-wf-q", "department.membership.revoked").Return(false, nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe, exec)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestHandleMembershipRevoked_NoMatchingAssignees(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	// Assignees exist but in a different department — filtered out.
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", "018e1f2a-0000-7000-8000-000000000020")}, nil)
	// PauseUserTasks is still called: Execution may have active tasks for this user.
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)
	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, "membership-wf-q", "department.membership.revoked").Return(true, nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe, exec)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestHandleMembershipRevoked_DraftVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{
			ID:         mVerID,
			WorkflowID: uuid.New(),
			Status:     domain.VersionStatusDraft,
		}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)
	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, "membership-wf-q", "department.membership.revoked").Return(true, nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe, exec)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Published versions are invalidated directly (SetInvalid) and Execution is
// notified via gRPC PauseUserTasks — no outbox event is emitted.
func TestHandleMembershipRevoked_PublishedVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{
			ID:         mVerID,
			WorkflowID: uuid.New(),
			Status:     domain.VersionStatusPublished,
		}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)
	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, "membership-wf-q", "department.membership.revoked").Return(true, nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe, exec)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleMembershipRevoked_DeduplicatesVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{
			assignee(mVerID, "task-1", mDeptID),
			assignee(mVerID, "task-2", mDeptID), // same version, different node
		}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{
			ID:         mVerID,
			WorkflowID: uuid.New(),
			Status:     domain.VersionStatusDraft,
		}, nil)
	// SetInvalid called once despite two assignees on same version.
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)
	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, "membership-wf-q", "department.membership.revoked").Return(true, nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe, exec)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleMembershipRevoked_ArchivedVersionSkipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{
			ID:         mVerID,
			WorkflowID: uuid.New(),
			Status:     domain.VersionStatusArchived,
		}, nil)
	// SetInvalid must NOT be called for archived versions.
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)
	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, "membership-wf-q", "department.membership.revoked").Return(true, nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe, exec)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// SetInvalid fails before RecordIfNew is reached — consumer retries the full handler.
func TestHandleMembershipRevoked_SetInvalidError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{
			ID:         mVerID,
			WorkflowID: uuid.New(),
			Status:     domain.VersionStatusDraft,
		}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(errors.New("db error"))

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe, exec)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// PauseUserTasks fails before RecordIfNew is reached — consumer retries so
// PauseUserTasks will eventually succeed (at-least-once guarantee).
func TestHandleMembershipRevoked_PauseUserTasksError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{
			ID:         mVerID,
			WorkflowID: uuid.New(),
			Status:     domain.VersionStatusPublished,
		}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(domain.ErrUpstreamUnavailable)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe, exec)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err == nil {
		t.Fatal("expected error from PauseUserTasks, got nil")
	}
}

// NilExecution: when ExecutionServiceAddr is not configured, s.execution is nil.
// PauseUserTasks must be skipped (not panicked) and the event recorded.
func TestHandleMembershipRevoked_NilExecution(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{
			ID:         mVerID,
			WorkflowID: uuid.New(),
			Status:     domain.VersionStatusPublished,
		}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	// PauseUserTasks must NOT be called when execution is nil.
	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, "membership-wf-q", "department.membership.revoked").Return(true, nil)

	svc := service.NewVersionService(service.VersionDeps{
		Transactor:      tx,
		Versions:        vRepo,
		Assignees:       aRepo,
		Outbox:          outbox,
		ProcessedEvents: pe,
		Execution:       nil, // not configured
		Log:             testkit.FakeLogger{},
	})
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("unexpected error with nil execution: %v", err)
	}
}

func TestHandleMembershipRevoked_MultipleVersions(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{
			assignee(mVerID, "task-1", mDeptID),
			assignee(mVerID2, "task-2", mDeptID),
		}, nil)

	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{ID: mVerID, WorkflowID: uuid.New(), Status: domain.VersionStatusDraft}, nil).
		AnyTimes()
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID2).
		Return(&domain.WorkflowVersion{ID: mVerID2, WorkflowID: uuid.New(), Status: domain.VersionStatusPublished}, nil).
		AnyTimes()

	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil).AnyTimes()
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID2, gomock.Any()).Return(nil).AnyTimes()
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)
	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, "membership-wf-q", "department.membership.revoked").Return(true, nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe, exec)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleMembershipRevoked_CacheDelError_LogsAndContinues(t *testing.T) {
	// invalidatePlanCache: when cache.Del fails the error is logged only, not returned.
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)
	cache := mocks.NewMockCacheStore(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-a", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{ID: mVerID, WorkflowID: uuid.New(), Status: domain.VersionStatusPublished}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	cache.EXPECT().Del(gomock.Any(), gomock.Any()).Return(errors.New("cache unavailable"))
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)
	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, "membership-wf-q", "department.membership.revoked").Return(true, nil)

	svc := service.NewVersionService(service.VersionDeps{
		Versions:        vRepo,
		Assignees:       aRepo,
		ProcessedEvents: pe,
		Execution:       exec,
		Cache:           cache,
		Log:             testkit.FakeLogger{},
	})
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("cache Del error must not propagate: %v", err)
	}
}
