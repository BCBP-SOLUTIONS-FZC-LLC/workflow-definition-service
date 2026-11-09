package service_test

import (
	"context"
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

func TestValidationService_ValidateVersion_CountsEachBlockingResponseOnce(t *testing.T) {
	registerServiceMetrics(t)
	blocking := domain.BPMNValidationError{Code: domain.BPMNErrNoStartEvent, NodeID: "n1", Severity: domain.SeverityError}
	warning := domain.BPMNValidationError{Code: domain.BPMNWarnUnknownStageType, NodeID: "n2", Severity: domain.SeverityWarning}
	cases := []struct {
		name      string
		issues    []domain.BPMNValidationError
		wantValid bool
		wantDelta float64
	}{
		{"a valid result does not count", nil, true, 0},
		{"a warning-only result does not count", []domain.BPMNValidationError{warning}, true, 0},
		{"one blocking issue counts once", []domain.BPMNValidationError{blocking}, false, 1},
		{"several blocking issues in one response count once", []domain.BPMNValidationError{blocking, blocking, warning}, false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compiler := mocks.NewMockPlanCompiler(gomock.NewController(t))
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
			compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
			compiler.EXPECT().ValidateForPublish(gomock.Any(), "<bpmn/>").Return(tc.issues, nil)
			svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler})
			before := testutil.ToFloat64(observability.WFValidationFailuresTotal)

			isValid, errs, err := svc.ValidateVersion(context.Background(), uuid.New(),
				&domain.WorkflowVersion{Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"})

			require.NoError(t, err)
			assert.Equal(t, tc.wantValid, isValid)
			assert.Equal(t, tc.issues, errs)
			assert.Equal(t, before+tc.wantDelta, testutil.ToFloat64(observability.WFValidationFailuresTotal))
		})
	}
}
