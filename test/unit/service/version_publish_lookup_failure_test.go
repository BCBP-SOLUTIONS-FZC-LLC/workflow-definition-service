package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestVersionService_Publish_VersionOfAnotherTenant_IsNotFoundAndWritesNothing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
	}{
		{name: "the draft lookup finds nothing under the caller's tenant"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewVersionService(service.VersionDeps{
				Transactor: mocks.NewMockTransactor(ctrl),
				Workflows:  mocks.NewMockWorkflowRepository(ctrl),
				Versions:   vRepo,
				Assignees:  mocks.NewMockAssigneeRepository(ctrl),
				Compiler:   bpmn_compiler.New(),
			})
			callerTenant, wfID, versionOfOtherTenant := uuid.New(), uuid.New(), uuid.New()
			vRepo.EXPECT().GetByID(gomock.Any(), callerTenant, versionOfOtherTenant).
				Return(nil, domain.ErrNotFound).Times(1)

			got, err := svc.Publish(context.Background(), callerTenant, uuid.New(), wfID, versionOfOtherTenant, false)

			require.ErrorIs(t, err, domain.ErrNotFound)
			assert.Nil(t, got)
		})
	}
}

func TestVersionService_Publish_ModuleLookupFailure_IsNotAValidationIssueAndWritesNothing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		lookupErr       error
		wantUnavailable bool
	}{
		{
			name:            "the module store is unreachable",
			lookupErr:       fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable),
			wantUnavailable: true,
		},
		{
			name:      "the module store fails for another reason",
			lookupErr: errors.New("scan module version: unexpected column type"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			modules := mocks.NewMockModuleVersionRepository(ctrl)
			svc := service.NewVersionService(service.VersionDeps{
				Transactor:     mocks.NewMockTransactor(ctrl),
				Workflows:      mocks.NewMockWorkflowRepository(ctrl),
				Versions:       vRepo,
				Assignees:      mocks.NewMockAssigneeRepository(ctrl),
				Compiler:       bpmn_compiler.New(),
				ModuleVersions: modules,
			})
			tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(&domain.WorkflowVersion{
				ID: vID, WorkflowID: wfID, TenantID: tenantID,
				Status: domain.VersionStatusDraft, BPMNXML: libraryCallerBPMN,
			}, nil)
			modules.EXPECT().Resolve(gomock.Any(), tenantID, gomock.Any(), gomock.Any()).
				Return(nil, tc.lookupErr).MinTimes(1)

			got, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)

			require.ErrorIs(t, err, tc.lookupErr)
			assert.Nil(t, got)
			assert.Equal(t, tc.wantUnavailable, errors.Is(err, domain.ErrUpstreamUnavailable))
			var invalid *domain.ValidationFailedError
			assert.False(t, errors.As(err, &invalid), "a lookup failure must not be reported as an unresolved call")
			assert.NotErrorIs(t, err, domain.ErrNotFound)
		})
	}
}
