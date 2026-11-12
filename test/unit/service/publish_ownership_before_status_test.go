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

func TestVersionService_Publish_ChecksTheVersionBelongsToTheWorkflowBeforeItsStatus(t *testing.T) {
	cases := []struct {
		name         string
		ownWorkflow  bool
		status       domain.VersionStatus
		wantNotFound bool
		wantNotDraft bool
	}{
		{name: "published version of another workflow", status: domain.VersionStatusPublished, wantNotFound: true},
		{name: "archived version of another workflow", status: domain.VersionStatusArchived, wantNotFound: true},
		{name: "draft of another workflow", status: domain.VersionStatusDraft, wantNotFound: true},
		{name: "published version of this workflow", ownWorkflow: true, status: domain.VersionStatusPublished, wantNotDraft: true},
		{name: "archived version of this workflow", ownWorkflow: true, status: domain.VersionStatusArchived, wantNotDraft: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, wfID, otherWfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			ctrl := gomock.NewController(t)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})
			owner := otherWfID
			if tc.ownWorkflow {
				owner = wfID
			}
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
				&domain.WorkflowVersion{ID: vID, WorkflowID: owner, TenantID: tenantID, Status: tc.status}, nil)

			got, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)

			require.Error(t, err)
			assert.Nil(t, got)
			if tc.wantNotFound {
				assert.ErrorIs(t, err, domain.ErrNotFound)
				assert.NotErrorIs(t, err, domain.ErrVersionNotDraft)
			}
			if tc.wantNotDraft {
				assert.ErrorIs(t, err, domain.ErrVersionNotDraft)
				assert.NotErrorIs(t, err, domain.ErrNotFound)
			}
		})
	}
}
