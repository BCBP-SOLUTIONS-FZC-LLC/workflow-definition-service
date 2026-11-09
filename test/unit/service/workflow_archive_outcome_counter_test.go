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

var (
	archiveCounterTenant   = uuid.New()
	archiveCounterWorkflow = uuid.New()
)

// Serial on purpose: the counter is process-global, so each case reads it
// before and after its own single call.
func TestWorkflowService_Archive_ClientAndServerFailuresShareTheErrLabel(t *testing.T) {
	registerServiceMetrics(t)
	activeVersion := uuid.New()
	dbDown := errors.New("db down")

	cases := []struct {
		name       string
		setup      func(wf *mocks.MockWorkflowRepository, exec *mocks.MockExecutionService)
		wantStatus string
		wantErr    error
	}{
		{
			name: "an archived workflow counts ok",
			setup: func(wf *mocks.MockWorkflowRepository, exec *mocks.MockExecutionService) {
				wf.EXPECT().GetByID(gomock.Any(), archiveCounterTenant, archiveCounterWorkflow).
					Return(&domain.Workflow{ActiveVersionID: &activeVersion}, nil).Times(2)
				exec.EXPECT().CheckActiveInstances(gomock.Any(), archiveCounterTenant, archiveCounterWorkflow).Return(false, int32(0), nil)
				wf.EXPECT().UpdateActiveVersion(gomock.Any(), archiveCounterTenant, archiveCounterWorkflow, (*uuid.UUID)(nil)).Return(nil)
			},
			wantStatus: "ok",
		},
		{
			name: "an unknown workflow counts err",
			setup: func(wf *mocks.MockWorkflowRepository, _ *mocks.MockExecutionService) {
				wf.EXPECT().GetByID(gomock.Any(), archiveCounterTenant, archiveCounterWorkflow).Return(nil, domain.ErrNotFound)
			},
			wantStatus: "err",
			wantErr:    domain.ErrNotFound,
		},
		{
			name: "a workflow without an active version counts err",
			setup: func(wf *mocks.MockWorkflowRepository, _ *mocks.MockExecutionService) {
				wf.EXPECT().GetByID(gomock.Any(), archiveCounterTenant, archiveCounterWorkflow).Return(&domain.Workflow{}, nil)
			},
			wantStatus: "err",
			wantErr:    domain.ErrNoActiveVersion,
		},
		{
			name: "a workflow with running instances counts err",
			setup: func(wf *mocks.MockWorkflowRepository, exec *mocks.MockExecutionService) {
				wf.EXPECT().GetByID(gomock.Any(), archiveCounterTenant, archiveCounterWorkflow).
					Return(&domain.Workflow{ActiveVersionID: &activeVersion}, nil)
				exec.EXPECT().CheckActiveInstances(gomock.Any(), archiveCounterTenant, archiveCounterWorkflow).Return(true, int32(2), nil)
			},
			wantStatus: "err",
			wantErr:    domain.ErrActiveInstancesExist,
		},
		{
			name: "a failed read counts err the same as a client error",
			setup: func(wf *mocks.MockWorkflowRepository, _ *mocks.MockExecutionService) {
				wf.EXPECT().GetByID(gomock.Any(), archiveCounterTenant, archiveCounterWorkflow).Return(nil, dbDown)
			},
			wantStatus: "err",
			wantErr:    dbDown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			exec := mocks.NewMockExecutionService(ctrl)
			if tc.wantStatus == "ok" {
				vRepo.EXPECT().Archive(gomock.Any(), gomock.Any(), activeVersion).Return(nil)
			}
			tc.setup(wfRepo, exec)
			svc := service.NewWorkflowService(service.WorkflowDeps{
				Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo, Execution: exec,
			})
			okBefore := testutil.ToFloat64(observability.WFArchiveTotal.WithLabelValues("ok"))
			errBefore := testutil.ToFloat64(observability.WFArchiveTotal.WithLabelValues("err"))

			err := svc.Archive(context.Background(), archiveCounterTenant, uuid.New(), archiveCounterWorkflow)

			wantOK, wantErr := 0.0, 0.0
			if tc.wantStatus == "ok" {
				require.NoError(t, err)
				wantOK = 1
			} else {
				require.ErrorIs(t, err, tc.wantErr)
				wantErr = 1
			}
			assert.Equal(t, okBefore+wantOK, testutil.ToFloat64(observability.WFArchiveTotal.WithLabelValues("ok")))
			assert.Equal(t, errBefore+wantErr, testutil.ToFloat64(observability.WFArchiveTotal.WithLabelValues("err")))
		})
	}
}
