package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestWorkflowService_Archive_ReReadInsideTransactionFails_ReturnsItAndWritesNothing(t *testing.T) {
	t.Parallel()

	dbDown := fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable)
	cases := []struct {
		name    string
		readErr error
	}{
		{name: "workflow deleted after the first read is not found", readErr: domain.ErrNotFound},
		{name: "database failure keeps its cause", readErr: dbDown},
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
				wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(nil, tc.readErr),
			)
			exec.EXPECT().CheckActiveInstances(gomock.Any(), tenantID, wfID).Return(false, int32(0), nil)
			// No Archive on the version repository and no UpdateActiveVersion on the
			// workflow repository are expected: either call fails the test.

			err := svc.Archive(context.Background(), tenantID, uuid.New(), wfID)

			assert.ErrorIs(t, err, tc.readErr)
		})
	}
}
