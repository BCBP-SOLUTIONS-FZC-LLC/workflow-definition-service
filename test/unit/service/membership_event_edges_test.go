package service_test

import (
	"context"
	"encoding/json"
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

func TestHandleMembershipRevoked_ZeroEventTime_InvalidatesEveryAssignee(t *testing.T) {
	cases := []struct {
		name string
		run  func(svc *service.VersionService) error
	}{
		{
			name: "department scope",
			run: func(svc *service.VersionService) error {
				return svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, time.Time{})
			},
		},
		{
			name: "tenant scope",
			run: func(svc *service.VersionService) error {
				return svc.HandleTenantMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, time.Time{})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			aRepo := mocks.NewMockAssigneeRepository(ctrl)
			exec := mocks.NewMockExecutionService(ctrl)

			future := assignee(mVerID, "task-future", mDeptID)
			future.CreatedAt = time.Now().Add(24 * time.Hour)
			aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).Return([]*domain.NodeAssignee{future}, nil)
			vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
				Return(&domain.WorkflowVersion{ID: mVerID, WorkflowID: uuid.New(), Status: domain.VersionStatusPublished}, nil)
			var written string
			vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).
				DoAndReturn(func(_ context.Context, _, _ uuid.UUID, errorsJSON string) error {
					written = errorsJSON
					return nil
				})
			exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID, gomock.Any()).Return(nil)

			svc := newMembershipSvc(ctrl, mocks.NewMockTransactor(ctrl), vRepo, aRepo, exec)
			if err := tc.run(svc); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var got []map[string]string
			if err := json.Unmarshal([]byte(written), &got); err != nil {
				t.Fatalf("invalidation errors are not JSON: %v", err)
			}
			if len(got) != 1 || got[0]["node_id"] != "task-future" {
				t.Fatalf("with no event time the assignee row must still be invalidated, wrote %s", written)
			}
		})
	}
}

func TestHandleDepartmentMembershipRevoked_VersionDeletedBeforeTheWrite_IsReturnedSoTheEventIsRedelivered(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{ID: mVerID, WorkflowID: uuid.New(), Status: domain.VersionStatusDraft}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(domain.ErrNotFound)

	svc := newMembershipSvc(ctrl, mocks.NewMockTransactor(ctrl), vRepo, aRepo, exec)
	err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime)

	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("error = %v, want the not-found from the write, so the redelivery can skip the deleted version", err)
	}
}

func TestHandleMembershipRevoked_WithoutACacheStore_InvalidatesAndPauses(t *testing.T) {
	cases := []struct {
		name string
		run  func(svc *service.VersionService) error
	}{
		{
			name: "department scope",
			run: func(svc *service.VersionService) error {
				return svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime)
			},
		},
		{
			name: "tenant scope",
			run: func(svc *service.VersionService) error {
				return svc.HandleTenantMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mEventTime)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			aRepo := mocks.NewMockAssigneeRepository(ctrl)
			exec := mocks.NewMockExecutionService(ctrl)

			aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
				Return([]*domain.NodeAssignee{assignee(mVerID, "task-1", mDeptID)}, nil)
			vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
				Return(&domain.WorkflowVersion{ID: mVerID, WorkflowID: uuid.New(), Status: domain.VersionStatusPublished}, nil)
			vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
			exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID, gomock.Any()).Return(nil)

			svc := service.NewVersionService(service.VersionDeps{
				Versions:  vRepo,
				Assignees: aRepo,
				Execution: exec,
				Log:       testkit.FakeLogger{},
			})
			if err := tc.run(svc); err != nil {
				t.Fatalf("a missing cache store must not fail the revocation: %v", err)
			}
		})
	}
}
