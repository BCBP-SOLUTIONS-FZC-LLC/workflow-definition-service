package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestWorkflowService_Get_VersionsLimitOutsideOneToHundred_FallsBackToTwenty(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		requested int
		wantLimit int
	}{
		{name: "zero", requested: 0, wantLimit: 20},
		{name: "negative", requested: -1, wantLimit: 20},
		{name: "one above the maximum", requested: 101, wantLimit: 20},
		{name: "lower bound is kept", requested: 1, wantLimit: 1},
		{name: "upper bound is kept", requested: 100, wantLimit: 100},
		{name: "in-range value is kept", requested: 37, wantLimit: 37},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewWorkflowService(service.WorkflowDeps{Workflows: wfRepo, Versions: vRepo})
			tenantID, wfID := uuid.New(), uuid.New()
			stored := []*domain.WorkflowVersion{{ID: uuid.New(), WorkflowID: wfID, TenantID: tenantID}}
			wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ID: wfID}, nil)
			vRepo.EXPECT().ListByWorkflow(gomock.Any(), tenantID, wfID, 1, tc.wantLimit).Return(stored, int64(1), nil)

			wf, versions, err := svc.Get(context.Background(), tenantID, wfID, tc.requested)

			require.NoError(t, err)
			assert.Equal(t, wfID, wf.ID)
			assert.Equal(t, stored, versions)
		})
	}
}
