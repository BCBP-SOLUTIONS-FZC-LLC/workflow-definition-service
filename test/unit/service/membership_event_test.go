package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
	execution *mocks.MockExecutionService,
) *service.VersionService {
	return service.NewVersionService(service.VersionDeps{
		Transactor: tx,
		Versions:   vRepo,
		Assignees:  aRepo,
		Execution:  execution,
		Log:        testkit.FakeLogger{},
	})
}

var (
	mTenantID = uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	mUserID   = uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	mEventID  = uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	mDeptID   = "018e1f2a-0000-7000-8000-000000000010"
	mVerID    = uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd")
	mVerID2   = uuid.MustParse("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")

	mEventTime = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
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

// ListByUser fails before any other side effect is reached — the consumer can
// retry the full handler.
func TestHandleDepartmentMembershipRevoked_ListByUserError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).Return(nil, errors.New("db error"))

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err == nil {
		t.Fatal("expected error from ListByUser, got nil")
	}
}

func TestHandleDepartmentMembershipRevoked_VersionDeletedMeanwhile_IsSkipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	// Version may have been concurrently deleted — handler skips it silently.
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).Return(nil, domain.ErrNotFound)
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err != nil {
		t.Fatalf("expected nil (silent skip), got %v", err)
	}
}

func TestHandleDepartmentMembershipRevoked_VersionLookupFailure_IsReturnedSoTheEventIsRedelivered(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).Return(nil, errors.New("connection reset"))

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err == nil {
		t.Fatal("a database failure while reading the version must be returned, or the revocation is lost for good")
	}
}

func TestHandleDepartmentMembershipRevoked_NoMatchingAssignees(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	// Assignees exist but in a different department — filtered out.
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", "018e1f2a-0000-7000-8000-000000000020")}, nil)
	// PauseUserTasks is still called: Execution may have active tasks for this user.
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestHandleTenantMembershipRevoked_InvalidatesEveryDepartment is the
// difference that makes IAM's tenant-level MembershipRevoked a separate
// handler rather than a call into the department-scoped one: the user has
// left the tenant entirely, so assignments in EVERY department must be
// invalidated. Reusing the department-scoped path would filter on a
// department this event does not carry and invalidate nothing at all.
func TestHandleTenantMembershipRevoked_InvalidatesEveryDepartment(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	otherVerID := uuid.New()
	otherDeptID := "018e1f2a-0000-7000-8000-000000000020"
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{
			assignee(mVerID, "task-1", mDeptID),
			assignee(otherVerID, "task-2", otherDeptID),
		}, nil)

	// Both versions invalidated — not just the one in mDeptID.
	for _, id := range []uuid.UUID{mVerID, otherVerID} {
		vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, id).
			Return(&domain.WorkflowVersion{ID: id, WorkflowID: uuid.New(), Status: domain.VersionStatusPublished}, nil)
		vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, id, gomock.Any()).Return(nil)
	}
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleTenantMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mEventTime); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleTenantMembershipRevoked_ListByUserError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).Return(nil, errors.New("db error"))

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleTenantMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mEventTime); err == nil {
		t.Fatal("expected the list failure to propagate so the delivery is retried")
	}
}

func TestHandleDepartmentMembershipRevoked_DraftVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
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

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Published versions are invalidated directly (SetInvalid) and Execution is
// asked to pause the user's tasks — no event is emitted.
func TestHandleDepartmentMembershipRevoked_PublishedVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
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

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleDepartmentMembershipRevoked_DeduplicatesVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
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

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleDepartmentMembershipRevoked_ArchivedVersionSkipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
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

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleDepartmentMembershipRevoked_SetInvalidError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
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

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestHandleDepartmentMembershipRevoked_PauseUserTasksError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
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

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err == nil {
		t.Fatal("expected error from PauseUserTasks, got nil")
	}
}

// NilExecution: when EXECUTION_SERVICE_URL is not configured, s.execution is nil.
// PauseUserTasks must be skipped (not panicked).
func TestHandleDepartmentMembershipRevoked_NilExecution(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
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

	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx,
		Versions:   vRepo,
		Assignees:  aRepo,
		Execution:  nil, // not configured
		Log:        testkit.FakeLogger{},
	})
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err != nil {
		t.Fatalf("unexpected error with nil execution: %v", err)
	}
}

func TestHandleDepartmentMembershipRevoked_MultipleVersions(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
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

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A revocation whose cache eviction fails is not done: Execution would keep
// starting instances on the plan the cache still calls valid. The user's tasks
// are still paused, and the failure reaches the consumer so the event is
// redelivered.
func TestHandleDepartmentMembershipRevoked_CacheDelError_StillPausesThenReturnsIt(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)
	cache := mocks.NewMockCacheStore(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-a", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{ID: mVerID, WorkflowID: uuid.New(), Status: domain.VersionStatusPublished}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	evictionErr := errors.New("cache unavailable")
	cache.EXPECT().Del(gomock.Any(), gomock.Any()).Return(evictionErr)
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)

	svc := service.NewVersionService(service.VersionDeps{
		Versions:  vRepo,
		Assignees: aRepo,
		Execution: exec,
		Cache:     cache,
		Log:       testkit.FakeLogger{},
	})
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); !errors.Is(err, evictionErr) {
		t.Fatalf("error = %v, want the cache eviction failure", err)
	}
}

func TestHandleDepartmentMembershipRevoked_RedeliveryConverges(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil).Times(2)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{ID: mVerID, WorkflowID: uuid.New(), Status: domain.VersionStatusPublished}, nil).Times(2)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil).Times(2)
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil).Times(2)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	for delivery := 1; delivery <= 2; delivery++ {
		if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err != nil {
			t.Fatalf("delivery %d: unexpected error: %v", delivery, err)
		}
	}
}

// A redelivered or reordered revocation that arrives after the user was added
// back and a new version was published with them as default assignee must not
// invalidate that version: its assignee row is newer than the event. The user's
// open tasks are still paused, since this service cannot tell whether they
// were re-added.
func TestHandleDepartmentMembershipRevoked_AssigneeAddedAfterTheEvent_IsLeftAlone(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	older := assignee(mVerID, "task-old", mDeptID)
	older.CreatedAt = mEventTime.Add(-time.Hour)
	newer := assignee(mVerID2, "task-new", mDeptID)
	newer.CreatedAt = mEventTime.Add(time.Hour)
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).Return([]*domain.NodeAssignee{older, newer}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{ID: mVerID, WorkflowID: uuid.New(), Status: domain.VersionStatusPublished}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleTenantMembershipRevoked_AssigneeAddedAfterTheEvent_IsLeftAlone(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	newer := assignee(mVerID, "task-new", mDeptID)
	newer.CreatedAt = mEventTime.Add(time.Minute)
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).Return([]*domain.NodeAssignee{newer}, nil)
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, exec)
	if err := svc.HandleTenantMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mEventTime); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
