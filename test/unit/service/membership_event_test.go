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
) *service.VersionService {
	return service.NewVersionService(service.VersionDeps{
		Transactor:      tx,
		Versions:        vRepo,
		Assignees:       aRepo,
		Outbox:          outbox,
		ProcessedEvents: pe,
		Log:             testkit.FakeLogger{},
	})
}

var (
	mTenantID = uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	mUserID   = uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	mEventID  = uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	mDeptID   = "dept-finance"
	mVerID    = uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd")
	mVerID2   = uuid.MustParse("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")
)

func assignee(versionID uuid.UUID, nodeKey, deptID string) *domain.NodeAssignee {
	return &domain.NodeAssignee{
		WorkflowVersionID: versionID,
		NodeKey:           nodeKey,
		UserID:            mUserID,
		DepartmentID:      deptID,
		Role:              "reviewer",
	}
}

func TestHandleMembershipRevoked_RecordIfNewError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)

	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, mTenantID, "iam-membership").Return(false, errors.New("db error"))

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err == nil {
		t.Fatal("expected error from RecordIfNew, got nil")
	}
}

func TestHandleMembershipRevoked_ListByUserError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)

	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, mTenantID, "iam-membership").Return(true, nil)
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).Return(nil, errors.New("db error"))

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe)
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

	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, mTenantID, "iam-membership").Return(true, nil)
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	// Version may have been concurrently deleted — handler skips it and returns nil.
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).Return(nil, errors.New("not found"))

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("expected nil (silent skip), got %v", err)
	}
}

func TestHandleMembershipRevoked_AlreadyProcessed(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)

	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, mTenantID, "iam-membership").Return(false, nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe)
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

	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, mTenantID, "iam-membership").Return(true, nil)
	// Assignees exist but in a different department — should be filtered out.
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", "dept-other")}, nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe)
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

	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, mTenantID, "iam-membership").Return(true, nil)
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{
			ID:         mVerID,
			WorkflowID: uuid.New(),
			Status:     domain.VersionStatusDraft,
		}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleMembershipRevoked_PublishedVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outboxRepo := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)

	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, mTenantID, "iam-membership").Return(true, nil)
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{
			ID:         mVerID,
			WorkflowID: uuid.New(),
			Status:     domain.VersionStatusPublished,
		}, nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	outboxRepo.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outboxRepo, pe)
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

	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, mTenantID, "iam-membership").Return(true, nil)
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

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe)
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

	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, mTenantID, "iam-membership").Return(true, nil)
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{
			ID:         mVerID,
			WorkflowID: uuid.New(),
			Status:     domain.VersionStatusArchived,
		}, nil)
	// SetInvalid must NOT be called for archived versions.

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleMembershipRevoked_SetInvalidError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outbox := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)

	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, mTenantID, "iam-membership").Return(true, nil)
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{
			ID:         mVerID,
			WorkflowID: uuid.New(),
			Status:     domain.VersionStatusDraft,
		}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(errors.New("db error"))

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outbox, pe)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestHandleMembershipRevoked_OutboxError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outboxRepo := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)

	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, mTenantID, "iam-membership").Return(true, nil)
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{
			ID:         mVerID,
			WorkflowID: uuid.New(),
			Status:     domain.VersionStatusPublished,
		}, nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	outboxRepo.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(errors.New("sns down"))

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outboxRepo, pe)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestHandleMembershipRevoked_PublishedVersionNumber(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outboxRepo := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)

	vNum := int32(7)

	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, mTenantID, "iam-membership").Return(true, nil)
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{
			ID:            mVerID,
			WorkflowID:    uuid.New(),
			Status:        domain.VersionStatusPublished,
			VersionNumber: &vNum,
		}, nil)
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	outboxRepo.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outboxRepo, pe)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleMembershipRevoked_MultipleVersions(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	outboxRepo := mocks.NewMockOutboxRepository(ctrl)
	pe := mocks.NewMockProcessedEventRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)

	pe.EXPECT().RecordIfNew(gomock.Any(), mEventID, mTenantID, "iam-membership").Return(true, nil)
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
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }).AnyTimes()
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID2, gomock.Any()).Return(nil).AnyTimes()
	outboxRepo.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	svc := newMembershipSvc(ctrl, tx, vRepo, aRepo, outboxRepo, pe)
	if err := svc.HandleMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
