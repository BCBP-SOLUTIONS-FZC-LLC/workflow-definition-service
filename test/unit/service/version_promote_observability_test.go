package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func TestVersionService_Promote_CountsEachCallOnceWithItsOutcome(t *testing.T) {
	registerServiceMetrics(t)
	tenantID, userID, wfID, vID, otherActive := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	published := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, IsValid: true,
	}

	updateDown := errors.New("db down")
	cases := []struct {
		name       string
		setup      func(wf *mocks.MockWorkflowRepository, v *mocks.MockWorkflowVersionRepository)
		planCode   string
		wantStatus string
		wantErr    error
	}{
		{
			name: "a promote that switches the active version counts ok",
			setup: func(wf *mocks.MockWorkflowRepository, v *mocks.MockWorkflowVersionRepository) {
				v.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(published, nil)
				wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
					Return(&domain.Workflow{ID: wfID, ActiveVersionID: &otherActive}, nil).MinTimes(1)
				wf.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
			},
			planCode:   "pro",
			wantStatus: "ok",
		},
		{
			name: "a promote of the version that is already active counts ok",
			setup: func(wf *mocks.MockWorkflowRepository, v *mocks.MockWorkflowVersionRepository) {
				v.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(published, nil)
				wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
					Return(&domain.Workflow{ID: wfID, ActiveVersionID: &vID}, nil)
			},
			planCode:   "pro",
			wantStatus: "ok",
		},
		{
			name: "a version that cannot be read counts err",
			setup: func(_ *mocks.MockWorkflowRepository, v *mocks.MockWorkflowVersionRepository) {
				v.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(nil, domain.ErrNotFound)
			},
			planCode:   "pro",
			wantStatus: "err",
			wantErr:    domain.ErrNotFound,
		},
		{
			name: "a version of another workflow counts err",
			setup: func(_ *mocks.MockWorkflowRepository, v *mocks.MockWorkflowVersionRepository) {
				v.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
					&domain.WorkflowVersion{ID: vID, WorkflowID: uuid.New(), Status: domain.VersionStatusPublished, IsValid: true}, nil)
			},
			planCode:   "pro",
			wantStatus: "err",
			wantErr:    domain.ErrNotFound,
		},
		{
			name: "a version that is not published counts err",
			setup: func(_ *mocks.MockWorkflowRepository, v *mocks.MockWorkflowVersionRepository) {
				v.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
					&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusDraft, IsValid: true}, nil)
			},
			planCode:   "pro",
			wantStatus: "err",
			wantErr:    domain.ErrVersionNotPublished,
		},
		{
			name: "a first activation on an unknown plan counts err",
			setup: func(wf *mocks.MockWorkflowRepository, v *mocks.MockWorkflowVersionRepository) {
				v.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(published, nil)
				wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ID: wfID}, nil)
			},
			planCode:   "platinum",
			wantStatus: "err",
			wantErr:    domain.ErrUpstreamUnavailable,
		},
		{
			name: "a failed update counts err",
			setup: func(wf *mocks.MockWorkflowRepository, v *mocks.MockWorkflowVersionRepository) {
				v.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(published, nil)
				wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
					Return(&domain.Workflow{ID: wfID, ActiveVersionID: &otherActive}, nil).MinTimes(1)
				wf.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(updateDown)
			},
			planCode:   "pro",
			wantStatus: "err",
			wantErr:    updateDown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			tc.setup(wfRepo, vRepo)
			svc := service.NewVersionService(service.VersionDeps{
				PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl),
				Workflows: wfRepo, Versions: vRepo, Log: &recordingLogger{},
			})
			okBefore := testutil.ToFloat64(observability.WFPromoteTotal.WithLabelValues("ok"))
			errBefore := testutil.ToFloat64(observability.WFPromoteTotal.WithLabelValues("err"))

			_, err := svc.Promote(context.Background(), tenantID, userID, wfID, vID, tc.planCode)

			wantOK, wantErr := 0.0, 0.0
			if tc.wantStatus == "ok" {
				require.NoError(t, err)
				wantOK = 1
			} else {
				require.ErrorIs(t, err, tc.wantErr)
				wantErr = 1
			}
			assert.Equal(t, okBefore+wantOK, testutil.ToFloat64(observability.WFPromoteTotal.WithLabelValues("ok")))
			assert.Equal(t, errBefore+wantErr, testutil.ToFloat64(observability.WFPromoteTotal.WithLabelValues("err")))
		})
	}
}

func TestVersionService_Promote_ActiveVersionClearedBeforeTheTransactionWithoutAPlan_IsUnavailable(t *testing.T) {
	tenantID, wfID, vID, firstReadActive := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	cases := []struct {
		name     string
		planCode string
	}{
		{name: "no plan code", planCode: ""},
		{name: "blank plan code", planCode: "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewVersionService(service.VersionDeps{
				PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl),
				Workflows: wfRepo, Versions: vRepo, Log: &recordingLogger{},
			})
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
				&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, IsValid: true}, nil)
			gomock.InOrder(
				wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
					Return(&domain.Workflow{ID: wfID, ActiveVersionID: &firstReadActive}, nil),
				wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
					Return(&domain.Workflow{ID: wfID}, nil),
			)
			wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			wfRepo.EXPECT().CountByTenant(gomock.Any(), gomock.Any()).Times(0)

			_, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, tc.planCode)

			require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
		})
	}
}
