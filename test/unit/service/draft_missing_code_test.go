package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestDraftService_MissingDraft_NamesWhetherTheWorkflowExists(t *testing.T) {
	storeDown := errors.New("workflow read: connection reset")
	cases := []struct {
		name        string
		workflowErr error
		wantIs      error
		wantNotIs   error
	}{
		{name: "workflow exists without a draft", wantIs: domain.ErrNoDraftExists, wantNotIs: domain.ErrNotFound},
		{name: "workflow missing or in another tenant", workflowErr: domain.ErrNotFound, wantIs: domain.ErrNotFound, wantNotIs: domain.ErrNoDraftExists},
		{name: "workflow read fails", workflowErr: storeDown, wantIs: storeDown, wantNotIs: domain.ErrNoDraftExists},
	}
	for _, tc := range cases {
		for _, op := range []string{"get", "update", "discard"} {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				tenantID, userID, wfID := uuid.New(), uuid.New(), uuid.New()
				ctrl := gomock.NewController(t)
				wfRepo := mocks.NewMockWorkflowRepository(ctrl)
				vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
				svc := newDraftSvc(ctrl, nil, wfRepo, vRepo)
				vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(nil, domain.ErrNotFound)
				var wf *domain.Workflow
				if tc.workflowErr == nil {
					wf = &domain.Workflow{ID: wfID, TenantID: tenantID}
				}
				wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(wf, tc.workflowErr)

				var err error
				switch op {
				case "get":
					_, err = svc.Get(context.Background(), tenantID, wfID)
				case "update":
					_, err = svc.Update(context.Background(), tenantID, userID, wfID, service.UpdateDraftReq{RecordVersion: 3})
				case "discard":
					err = svc.Discard(context.Background(), tenantID, wfID)
				}

				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantIs)
				assert.NotErrorIs(t, err, tc.wantNotIs)
			})
		}
	}
}

func TestDraftService_ExistingDraft_DoesNotReadTheWorkflow(t *testing.T) {
	tenantID, wfID := uuid.New(), uuid.New()
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, wfRepo, vRepo)
	draft := &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft}
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(draft, nil)

	got, err := svc.Get(context.Background(), tenantID, wfID)

	require.NoError(t, err)
	assert.Same(t, draft, got)
}

func TestDraftService_DraftReadFailure_DoesNotReadTheWorkflow(t *testing.T) {
	tenantID, wfID := uuid.New(), uuid.New()
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, wfRepo, vRepo)
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(nil, domain.ErrUpstreamUnavailable)

	_, err := svc.Get(context.Background(), tenantID, wfID)

	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.NotErrorIs(t, err, domain.ErrNoDraftExists)
}
