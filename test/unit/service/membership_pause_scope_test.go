package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

func TestMembershipRevocation_PauseIsScopedToTheRevokedDepartmentOnly(t *testing.T) {
	deptID := uuid.MustParse(mDeptID)
	cases := []struct {
		name     string
		revoke   func(svc *service.VersionService) error
		wantDept *uuid.UUID
	}{
		{
			name: "a department revocation asks Execution to pause only that department's tasks",
			revoke: func(svc *service.VersionService) error {
				return svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime)
			},
			wantDept: &deptID,
		},
		{
			name: "a full membership revocation asks Execution to pause every task of the user",
			revoke: func(svc *service.VersionService) error {
				return svc.HandleTenantMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mEventTime)
			},
			wantDept: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			aRepo := mocks.NewMockAssigneeRepository(ctrl)
			exec := mocks.NewMockExecutionService(ctrl)
			aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).Return(nil, nil)
			exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID, tc.wantDept).Return(nil).Times(1)
			svc := service.NewVersionService(service.VersionDeps{
				Assignees: aRepo, Execution: exec, Log: testkit.FakeLogger{},
			})

			require.NoError(t, tc.revoke(svc))
		})
	}
}

func TestMembershipRevocation_PauseCarriesTheRevokedDepartmentWhenOtherDepartmentsHoldAssignments(t *testing.T) {
	deptID := uuid.MustParse(mDeptID)
	otherDept := uuid.MustParse("018e1f2a-0000-7000-8000-000000000020")
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)
	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).Return([]*domain.NodeAssignee{
		assignee(mVerID, "task-1", mDeptID),
		assignee(mVerID2, "task-2", otherDept.String()),
	}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{ID: mVerID, WorkflowID: uuid.New(), Status: domain.VersionStatusDraft}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID, &deptID).Return(nil).Times(1)
	svc := service.NewVersionService(service.VersionDeps{
		Versions: vRepo, Assignees: aRepo, Execution: exec, Log: testkit.FakeLogger{},
	})

	require.NoError(t, svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime))
}
