package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestVersionService_List_ReadsTheWorkflowFirst(t *testing.T) {
	cases := []struct {
		name         string
		workflowErr  error
		wantListed   bool
		wantErr      error
		wantErrWraps string
	}{
		{name: "workflow missing or in another tenant", workflowErr: domain.ErrNotFound, wantErr: domain.ErrNotFound, wantErrWraps: "get workflow: "},
		{name: "workflow read fails", workflowErr: domain.ErrUpstreamUnavailable, wantErr: domain.ErrUpstreamUnavailable, wantErrWraps: "get workflow: "},
		{name: "workflow exists", wantListed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, wfID := uuid.New(), uuid.New()
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

			var wf *domain.Workflow
			if tc.workflowErr == nil {
				wf = &domain.Workflow{ID: wfID, TenantID: tenantID}
			}
			wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(wf, tc.workflowErr)
			listed := []*domain.WorkflowVersion{{ID: uuid.New(), WorkflowID: wfID, TenantID: tenantID}}
			if tc.wantListed {
				vRepo.EXPECT().ListByWorkflow(gomock.Any(), tenantID, wfID, 2, 15).Return(listed, int64(16), nil)
			}

			got, total, err := svc.List(context.Background(), tenantID, wfID, 2, 15)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.True(t, strings.HasPrefix(err.Error(), tc.wantErrWraps), "err = %q", err)
				assert.Nil(t, got)
				assert.Zero(t, total)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, listed, got)
			assert.Equal(t, int64(16), total)
		})
	}
}

func TestModuleService_ListVersions_ReadsTheModuleFirst(t *testing.T) {
	cases := []struct {
		name       string
		moduleErr  error
		wantListed bool
		wantErr    error
	}{
		{name: "module missing or in another tenant", moduleErr: domain.ErrNotFound, wantErr: domain.ErrNotFound},
		{name: "module read fails", moduleErr: errors.New("connection reset"), wantErr: errors.New("connection reset")},
		{name: "module exists", wantListed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, moduleID := uuid.New(), uuid.New()
			ctrl := gomock.NewController(t)
			svc, m := newModuleService(ctrl)

			var mod *domain.Module
			if tc.moduleErr == nil {
				mod = &domain.Module{ID: moduleID}
			}
			m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).Return(mod, tc.moduleErr)
			listed := []*domain.ModuleVersion{{ID: uuid.New(), ModuleID: moduleID}}
			if tc.wantListed {
				m.versions.EXPECT().ListByModule(gomock.Any(), tenantID, moduleID, 3, 10).Return(listed, int64(21), nil)
			}

			got, total, err := svc.ListVersions(context.Background(), tenantID, moduleID, 3, 10)

			if tc.wantErr != nil {
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.wantErr.Error())
				if errors.Is(tc.wantErr, domain.ErrNotFound) {
					assert.ErrorIs(t, err, domain.ErrNotFound)
				}
				assert.Nil(t, got)
				assert.Zero(t, total)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, listed, got)
			assert.Equal(t, int64(21), total)
		})
	}
}
