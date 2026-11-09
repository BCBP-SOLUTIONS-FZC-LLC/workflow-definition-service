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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func registerServiceMetrics(t *testing.T) {
	t.Helper()
	log, err := logger.NewLogger("dev")
	require.NoError(t, err)
	_ = gincommon.ObservabilityMiddlewares(gincommon.Config{
		Logger: log, ServiceName: "workflow-definition-service", BuildVersion: "test",
		Domain: observability.Domain, Environment: "test",
	})
	observability.Register()
}

func TestVersionService_Clone_RecordsTheOutcomeAndLogsOnlyOnSuccess(t *testing.T) {
	registerServiceMetrics(t)
	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	sourceDown := errors.New("db down")
	cases := []struct {
		name       string
		source     *domain.WorkflowVersion
		sourceErr  error
		wantStatus string
		wantErr    error
		wantLogged bool
	}{
		{
			name:       "a clone of a published version counts ok and is logged",
			source:     &domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, BPMNXML: "<bpmn/>"},
			wantStatus: "ok",
			wantLogged: true,
		},
		{
			name:       "a clone of a draft counts err and is not logged",
			source:     &domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusDraft},
			wantStatus: "err",
			wantErr:    domain.ErrVersionNotPublished,
		},
		{
			name:       "a source that cannot be read counts err and is not logged",
			sourceErr:  sourceDown,
			wantStatus: "err",
			wantErr:    sourceDown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(tc.source, tc.sourceErr)
			if tc.wantLogged {
				wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
				vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
			}
			log := &recordingLogger{}
			svc := service.NewVersionService(service.VersionDeps{
				PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl),
				Workflows: wfRepo, Versions: vRepo, Log: log,
			})
			okBefore := testutil.ToFloat64(observability.WFCloneTotal.WithLabelValues("ok"))
			errBefore := testutil.ToFloat64(observability.WFCloneTotal.WithLabelValues("err"))

			newWF, newV, err := svc.Clone(context.Background(), tenantID, userID, wfID, vID, "enterprise",
				service.CloneReq{NewKey: "copy", NewName: "Copy"})

			wantOK, wantErr := 0.0, 0.0
			if tc.wantStatus == "ok" {
				require.NoError(t, err)
				wantOK = 1
			} else {
				require.ErrorIs(t, err, tc.wantErr)
				wantErr = 1
			}
			assert.Equal(t, okBefore+wantOK, testutil.ToFloat64(observability.WFCloneTotal.WithLabelValues("ok")))
			assert.Equal(t, errBefore+wantErr, testutil.ToFloat64(observability.WFCloneTotal.WithLabelValues("err")))

			if !tc.wantLogged {
				assert.Empty(t, log.infos)
				return
			}
			line := log.only(t)
			assert.Equal(t, "version cloned", line.msg)
			assert.Equal(t, tenantID.String(), line.fields["tenant_id"])
			assert.Equal(t, userID.String(), line.fields["user_id"])
			assert.Equal(t, wfID.String(), line.fields["source_workflow_id"])
			assert.Equal(t, vID.String(), line.fields["source_version_id"])
			assert.Equal(t, newWF.ID.String(), line.fields["new_workflow_id"])
			assert.Equal(t, newV.ID.String(), line.fields["new_version_id"])
		})
	}
}
