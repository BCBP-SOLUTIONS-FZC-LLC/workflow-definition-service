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
)

func TestDraftService_Discard_RepeatedAfterSuccess_ReportsNoDraftWhileTheWorkflowExists(t *testing.T) {
	ctrl := gomock.NewController(t)
	tenantID, wfID, draftID := uuid.New(), uuid.New(), uuid.New()
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, wfRepo, vRepo)

	stored := &domain.WorkflowVersion{ID: draftID, WorkflowID: wfID}
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).DoAndReturn(
		func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
			if stored == nil {
				return nil, domain.ErrNotFound
			}
			return stored, nil
		}).Times(2)
	vRepo.EXPECT().DeleteDraft(gomock.Any(), tenantID, draftID).DoAndReturn(
		func(context.Context, uuid.UUID, uuid.UUID) error {
			stored = nil
			return nil
		}).Times(1)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ID: wfID, TenantID: tenantID}, nil).Times(1)

	first := svc.Discard(context.Background(), tenantID, wfID)
	second := svc.Discard(context.Background(), tenantID, wfID)

	require.NoError(t, first)
	require.Error(t, second)
	assert.ErrorIs(t, second, domain.ErrNoDraftExists)
	assert.NotErrorIs(t, second, domain.ErrNotFound)
}
