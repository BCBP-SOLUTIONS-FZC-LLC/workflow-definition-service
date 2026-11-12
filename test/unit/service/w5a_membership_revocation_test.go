package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

func w5aNodeIDs(t *testing.T, errorsJSON string) []string {
	t.Helper()
	var entries []struct {
		NodeID string `json:"node_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(errorsJSON), &entries))
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.NodeID
	}
	return ids
}

func TestMembershipRevocation_AZeroEventTime_KeepsEveryRowAndGroupsNodesPerVersion(t *testing.T) {
	otherDept := "018e1f2a-0000-7000-8000-000000000020"
	future := time.Now().AddDate(1, 0, 0)
	cases := []struct {
		name         string
		revoke       func(svc *service.VersionService) error
		wantVersions map[uuid.UUID][]string
	}{
		{
			name: "department event keeps only that department",
			revoke: func(svc *service.VersionService) error {
				return svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, time.Time{})
			},
			wantVersions: map[uuid.UUID][]string{mVerID: {"task-1", "task-2"}, mVerID2: {"task-3"}},
		},
		{
			name: "tenant event keeps every department",
			revoke: func(svc *service.VersionService) error {
				return svc.HandleTenantMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, time.Time{})
			},
			wantVersions: map[uuid.UUID][]string{mVerID: {"task-1", "task-2"}, mVerID2: {"task-3", "task-4"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			aRepo := mocks.NewMockAssigneeRepository(ctrl)
			exec := mocks.NewMockExecutionService(ctrl)
			rows := []*domain.NodeAssignee{
				assignee(mVerID, "task-1", mDeptID),
				assignee(mVerID, "task-2", mDeptID),
				assignee(mVerID2, "task-3", mDeptID),
				assignee(mVerID2, "task-4", otherDept),
			}
			for _, r := range rows {
				r.CreatedAt = future
			}
			aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).Return(rows, nil)
			vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, gomock.Any()).DoAndReturn(
				func(_ context.Context, _, id uuid.UUID) (*domain.WorkflowVersion, error) {
					return &domain.WorkflowVersion{ID: id, WorkflowID: uuid.New(), Status: domain.VersionStatusDraft}, nil
				}).Times(len(tc.wantVersions))
			got := map[uuid.UUID][]string{}
			vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _, id uuid.UUID, errorsJSON string) error {
					got[id] = w5aNodeIDs(t, errorsJSON)
					return nil
				}).Times(len(tc.wantVersions))
			exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)
			svc := service.NewVersionService(service.VersionDeps{
				Versions: vRepo, Assignees: aRepo, Execution: exec, Log: testkit.FakeLogger{},
			})

			require.NoError(t, tc.revoke(svc))

			assert.Equal(t, tc.wantVersions, got, "rows created after now are kept because a zero event time disables the cut-off")
		})
	}
}

func TestMembershipRevocation_ACacheDeleteFailure_IsReturnedWithTheCauseAndNamesTheEvictedVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)
	cache := mocks.NewMockCacheStore(ctrl)
	cause := errors.New("cache unavailable")

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-a", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{ID: mVerID, WorkflowID: uuid.New(), Status: domain.VersionStatusPublished}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	var deleted []string
	cache.EXPECT().Del(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, keys ...string) error {
			deleted = append(deleted, keys...)
			return cause
		})
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)
	svc := service.NewVersionService(service.VersionDeps{
		Versions: vRepo, Assignees: aRepo, Execution: exec, Cache: cache, Log: testkit.FakeLogger{},
	})

	err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime)

	require.Error(t, err)
	assert.ErrorIs(t, err, cause, "the cause is reachable through the wrapper")
	assert.Equal(t, "evict compiled plan "+mVerID.String()+": "+cause.Error(), err.Error())
	assert.Equal(t, []string{domain.CompiledPlanCacheKey(mTenantID, mVerID)}, deleted)
}

func TestMembershipRevocation_WithoutACache_InvalidatesTheVersionAndReturnsNoError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	aRepo := mocks.NewMockAssigneeRepository(ctrl)
	exec := mocks.NewMockExecutionService(ctrl)

	aRepo.EXPECT().ListByUser(gomock.Any(), mTenantID, mUserID).
		Return([]*domain.NodeAssignee{assignee(mVerID, "task-a", mDeptID)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), mTenantID, mVerID).
		Return(&domain.WorkflowVersion{ID: mVerID, WorkflowID: uuid.New(), Status: domain.VersionStatusPublished}, nil)
	vRepo.EXPECT().SetInvalid(gomock.Any(), mTenantID, mVerID, gomock.Any()).Return(nil)
	exec.EXPECT().PauseUserTasks(gomock.Any(), mTenantID, mUserID).Return(nil)
	svc := service.NewVersionService(service.VersionDeps{
		Versions: vRepo, Assignees: aRepo, Execution: exec, Log: testkit.FakeLogger{},
	})

	err := svc.HandleDepartmentMembershipRevoked(context.Background(), mEventID, mTenantID, mUserID, mDeptID, mEventTime)

	assert.NoError(t, err, "no cache means nothing to evict and no eviction error")
}
