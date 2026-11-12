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

func TestValidationService_Validate_CountsAFailureOnlyForABlockingResult(t *testing.T) {
	registerServiceMetrics(t)
	blocking := domain.BPMNValidationError{Code: domain.BPMNErrNoStartEvent, NodeID: "n1", Severity: domain.SeverityError}
	warning := domain.BPMNValidationError{Code: domain.BPMNWarnUnknownStageType, NodeID: "n2", Severity: domain.SeverityWarning}
	cases := []struct {
		name      string
		issues    []domain.BPMNValidationError
		wantValid bool
		wantDelta float64
	}{
		{"no issues leaves the counter alone", nil, true, 0},
		{"warnings only leave the counter alone", []domain.BPMNValidationError{warning, warning}, true, 0},
		{"one blocking error counts once", []domain.BPMNValidationError{blocking}, false, 1},
		{"a blocking error among warnings counts once", []domain.BPMNValidationError{warning, blocking, blocking}, false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compiler := mocks.NewMockPlanCompiler(gomock.NewController(t))
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
			compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
			compiler.EXPECT().ValidateForPublish(gomock.Any(), "<bpmn/>").Return(tc.issues, nil)
			svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler})
			before := testutil.ToFloat64(observability.WFValidationFailuresTotal)

			isValid, errs, err := svc.Validate(context.Background(), uuid.New(), "<bpmn/>")

			require.NoError(t, err)
			assert.Equal(t, tc.wantValid, isValid)
			assert.Equal(t, tc.issues, errs)
			assert.Equal(t, before+tc.wantDelta, testutil.ToFloat64(observability.WFValidationFailuresTotal))
		})
	}
}

func TestValidationService_Validate_AnInfrastructureErrorDoesNotCountAsAValidationFailure(t *testing.T) {
	registerServiceMetrics(t)
	cases := []struct {
		name    string
		arrange func(c *mocks.MockPlanCompiler, cause error)
	}{
		{"the module bundling fails", func(c *mocks.MockPlanCompiler, cause error) {
			c.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
			c.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("", cause)
		}},
		{"the validator itself fails", func(c *mocks.MockPlanCompiler, cause error) {
			c.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
			c.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
			c.EXPECT().ValidateForPublish(gomock.Any(), "<bpmn/>").Return(nil, cause)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cause := errors.New("compiler unavailable")
			compiler := mocks.NewMockPlanCompiler(gomock.NewController(t))
			tc.arrange(compiler, cause)
			svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler})
			before := testutil.ToFloat64(observability.WFValidationFailuresTotal)

			isValid, errs, err := svc.Validate(context.Background(), uuid.New(), "<bpmn/>")

			require.ErrorIs(t, err, cause)
			assert.False(t, isValid)
			assert.Nil(t, errs)
			assert.Equal(t, before, testutil.ToFloat64(observability.WFValidationFailuresTotal))
		})
	}
}
