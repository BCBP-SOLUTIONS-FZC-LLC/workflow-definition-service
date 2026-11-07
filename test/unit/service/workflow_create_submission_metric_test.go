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

var submissionMetricTenant = uuid.New()

func storedSubmittedWorkflow() gomock.Matcher {
	return gomock.Cond(func(w *domain.Workflow) bool {
		return w.TenantID == submissionMetricTenant && w.TemplateKey == "key" && w.Name == "name"
	})
}

func storedSubmittedDraft() gomock.Matcher {
	return gomock.Cond(func(v *domain.WorkflowVersion) bool {
		return v.TenantID == submissionMetricTenant && v.Status == domain.VersionStatusDraft &&
			v.BPMNXML == "<bpmn/>" && v.IsValid
	})
}

func TestWorkflowService_Create_CountsEachAttemptOnceWithItsOutcome(t *testing.T) {
	registerServiceMetrics(t)
	insertDown := errors.New("db down")

	cases := []struct {
		name       string
		planCode   string
		setup      func(c *mocks.MockPlanCompiler, wf *mocks.MockWorkflowRepository, v *mocks.MockWorkflowVersionRepository)
		wantStatus string
		wantErr    error
	}{
		{
			name:     "a created workflow counts ok",
			planCode: "starter",
			setup: func(c *mocks.MockPlanCompiler, wf *mocks.MockWorkflowRepository, v *mocks.MockWorkflowVersionRepository) {
				expectNoLibraryCalls(c)
				c.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, nil)
				wf.EXPECT().CountByTenant(gomock.Any(), submissionMetricTenant).Return(int64(0), nil)
				wf.EXPECT().Create(gomock.Any(), storedSubmittedWorkflow()).Return(nil)
				v.EXPECT().Create(gomock.Any(), storedSubmittedDraft()).Return(nil)
			},
			wantStatus: "ok",
		},
		{
			name:     "a BPMN that fails validation counts err",
			planCode: "starter",
			setup: func(c *mocks.MockPlanCompiler, _ *mocks.MockWorkflowRepository, _ *mocks.MockWorkflowVersionRepository) {
				expectNoLibraryCalls(c)
				c.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(
					[]domain.BPMNValidationError{{Code: "MISSING_START", Severity: domain.SeverityError}}, nil)
			},
			wantStatus: "err",
			wantErr:    &domain.ValidationFailedError{},
		},
		{
			name:     "an unknown plan counts err",
			planCode: "platinum",
			setup: func(c *mocks.MockPlanCompiler, _ *mocks.MockWorkflowRepository, _ *mocks.MockWorkflowVersionRepository) {
				expectNoLibraryCalls(c)
				c.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, nil)
			},
			wantStatus: "err",
			wantErr:    domain.ErrUpstreamUnavailable,
		},
		{
			name:     "a tenant at its template limit counts err",
			planCode: "starter",
			setup: func(c *mocks.MockPlanCompiler, wf *mocks.MockWorkflowRepository, _ *mocks.MockWorkflowVersionRepository) {
				expectNoLibraryCalls(c)
				c.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, nil)
				wf.EXPECT().CountByTenant(gomock.Any(), submissionMetricTenant).Return(int64(5), nil)
			},
			wantStatus: "err",
			wantErr:    domain.ErrPlanQuotaExceeded,
		},
		{
			name:     "a failed insert counts err",
			planCode: "starter",
			setup: func(c *mocks.MockPlanCompiler, wf *mocks.MockWorkflowRepository, _ *mocks.MockWorkflowVersionRepository) {
				expectNoLibraryCalls(c)
				c.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, nil)
				wf.EXPECT().CountByTenant(gomock.Any(), submissionMetricTenant).Return(int64(0), nil)
				wf.EXPECT().Create(gomock.Any(), storedSubmittedWorkflow()).Return(insertDown)
			},
			wantStatus: "err",
			wantErr:    insertDown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			tc.setup(compiler, wfRepo, vRepo)
			svc := service.NewWorkflowService(service.WorkflowDeps{
				PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl),
				Workflows: wfRepo, Versions: vRepo, Compiler: compiler,
			})
			okBefore := testutil.ToFloat64(observability.WFSubmissionsTotal.WithLabelValues("ok"))
			errBefore := testutil.ToFloat64(observability.WFSubmissionsTotal.WithLabelValues("err"))

			_, _, err := svc.Create(context.Background(), submissionMetricTenant, uuid.New(), "key", "name", "", "<bpmn/>", tc.planCode)

			wantOK, wantErr := 0.0, 0.0
			if tc.wantStatus == "ok" {
				require.NoError(t, err)
				wantOK = 1
			} else {
				require.Error(t, err)
				var validation *domain.ValidationFailedError
				if errors.As(tc.wantErr, &validation) {
					assert.ErrorAs(t, err, &validation)
				} else {
					assert.ErrorIs(t, err, tc.wantErr)
				}
				wantErr = 1
			}
			assert.Equal(t, okBefore+wantOK, testutil.ToFloat64(observability.WFSubmissionsTotal.WithLabelValues("ok")))
			assert.Equal(t, errBefore+wantErr, testutil.ToFloat64(observability.WFSubmissionsTotal.WithLabelValues("err")))
		})
	}
}
