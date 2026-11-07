package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestWorkflowService_Archive_ActiveVersionClearedBeforeTheTransaction_ReturnsNoActiveVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
	}{
		{name: "another request archived the workflow after the first read"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			exec := mocks.NewMockExecutionService(ctrl)
			svc := service.NewWorkflowService(service.WorkflowDeps{
				Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo, Execution: exec,
			})

			tenantID, wfID, activeVID := uuid.New(), uuid.New(), uuid.New()
			gomock.InOrder(
				wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
					Return(&domain.Workflow{ID: wfID, ActiveVersionID: &activeVID}, nil),
				wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
					Return(&domain.Workflow{ID: wfID, ActiveVersionID: nil}, nil),
			)
			exec.EXPECT().CheckActiveInstances(gomock.Any(), tenantID, wfID).Return(false, int32(0), nil)

			err := svc.Archive(context.Background(), tenantID, uuid.New(), wfID)

			if !errors.Is(err, domain.ErrNoActiveVersion) {
				t.Fatalf("err = %v, want ErrNoActiveVersion", err)
			}
		})
	}
}

func TestWorkflowService_Archive_FirstWorkflowReadFails_StopsBeforeAnyOtherCall(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		readErr error
		wantErr error
	}{
		{
			name:    "database connection error is unavailable",
			readErr: fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable),
			wantErr: domain.ErrUpstreamUnavailable,
		},
		{
			name:    "workflow of another tenant is not found",
			readErr: domain.ErrNotFound,
			wantErr: domain.ErrNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			exec := mocks.NewMockExecutionService(ctrl)
			svc := service.NewWorkflowService(service.WorkflowDeps{
				Transactor: mocks.NewMockTransactor(ctrl),
				Workflows:  wfRepo,
				Versions:   mocks.NewMockWorkflowVersionRepository(ctrl),
				Execution:  exec,
			})

			tenantID, wfID := uuid.New(), uuid.New()
			wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(nil, tc.readErr)

			err := svc.Archive(context.Background(), tenantID, uuid.New(), wfID)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
