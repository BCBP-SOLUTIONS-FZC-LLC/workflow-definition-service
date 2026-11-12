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

func TestVersionService_Promote_AVersionThatIsNotThere_IsNotFoundAndNothingElseRuns(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Transactor: tx, Workflows: wfRepo, Versions: vRepo})
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(nil, domain.ErrNotFound)

	got, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise")

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.EqualError(t, err, "get version: resource not found")
	assert.Nil(t, got)
}

func TestVersionService_Promote_AWorkflowRowThatCannotBeFoundAfterTheVersionCheck_IsNotFoundAndNothingChanges(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Transactor: tx, Workflows: wfRepo, Versions: vRepo})
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusPublished, IsValid: true}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(nil, domain.ErrNotFound)

	got, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise")

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.EqualError(t, err, "get workflow: resource not found")
	assert.Nil(t, got)
}

func TestVersionService_Promote_AVersionThatIsNotPublished_IsRefusedBeforeTheWorkflowIsRead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status domain.VersionStatus
	}{
		{"draft", domain.VersionStatusDraft},
		{"archived", domain.VersionStatusArchived},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			tx := mocks.NewMockTransactor(ctrl)
			svc := service.NewVersionService(service.VersionDeps{Transactor: tx, Workflows: wfRepo, Versions: vRepo})
			tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
				&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, TenantID: tenantID, Status: tc.status, IsValid: true}, nil)

			got, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise")

			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrVersionNotPublished)
			assert.False(t, errors.Is(err, domain.ErrInvalidVersionStatus), "the refusal is the not-published one")
			assert.Nil(t, got)
		})
	}
}
