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

func TestWorkflowService_Create_TwoCallsToTheSameMissingModule_AskTheLibraryOnceAndReportBoth(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl), Compiler: compiler, ModuleVersions: modules,
	})
	tenantID := uuid.New()
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{
		{CallID: "CA_1", ProcessID: "Process_Missing"},
		{CallID: "CA_2", ProcessID: "Process_Missing"},
		{CallID: "CA_3", ProcessID: "Process_Missing", VersionNumber: 4},
	}, nil, nil)
	modules.EXPECT().Resolve(gomock.Any(), tenantID, "Process_Missing", int32(0)).Return(nil, domain.ErrNotFound).Times(1)
	modules.EXPECT().Resolve(gomock.Any(), tenantID, "Process_Missing", int32(4)).Return(nil, domain.ErrNotFound).Times(1)

	_, _, err := svc.Create(context.Background(), tenantID, uuid.New(), "key", "name", "", "<main/>", "starter")

	var failed *domain.ValidationFailedError
	require.ErrorAs(t, err, &failed)
	require.Len(t, failed.Errors, 3, "errors = %+v", failed.Errors)
	for i, node := range []string{"CA_1", "CA_2", "CA_3"} {
		assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, failed.Errors[i].Code)
		assert.Equal(t, node, failed.Errors[i].NodeID)
	}
	assert.Contains(t, failed.Errors[2].Message, "version 4")
}

func TestWorkflowService_Create_ALookupFailureNamesTheModuleAndKeepsItsCause(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl), Compiler: compiler, ModuleVersions: modules,
	})
	tenantID := uuid.New()
	outage := errors.New("database outage")
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{
		{CallID: "CA_1", ProcessID: "Process_Down"}, {CallID: "CA_2", ProcessID: "Process_After"},
	}, nil, nil)
	modules.EXPECT().Resolve(gomock.Any(), tenantID, "Process_Down", int32(0)).Return(nil, outage)

	_, _, err := svc.Create(context.Background(), tenantID, uuid.New(), "key", "name", "", "<main/>", "starter")

	require.Error(t, err)
	assert.ErrorIs(t, err, outage)
	assert.Contains(t, err.Error(), `resolve module "Process_Down"`)
	var failed *domain.ValidationFailedError
	assert.False(t, errors.As(err, &failed))
}

func TestWorkflowService_Create_ValidatesTheBundleWithTheSaveRulesNotThePublishRules(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		compilerErr error
		issues      []domain.BPMNValidationError
		wantWrapped bool
	}{
		{name: "a compiler failure is wrapped", compilerErr: errors.New("compiler exploded"), wantWrapped: true},
		{name: "a blocking issue is a validation failure", issues: []domain.BPMNValidationError{{Code: domain.BPMNErrNoStartEvent, NodeID: "n1", Severity: domain.SeverityError}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			svc := service.NewWorkflowService(service.WorkflowDeps{
				PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl), Compiler: compiler, ModuleVersions: mocks.NewMockModuleVersionRepository(ctrl),
			})
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return(nil, nil, nil)
			compiler.EXPECT().BundleModules("<main/>", gomock.Len(0)).Return("<bundled/>", nil)
			compiler.EXPECT().Validate(gomock.Any(), "<bundled/>").Return(tc.issues, tc.compilerErr)

			wf, v, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "key", "name", "", "<main/>", "starter")

			assert.Nil(t, wf)
			assert.Nil(t, v)
			require.Error(t, err)
			if tc.wantWrapped {
				assert.ErrorIs(t, err, tc.compilerErr)
				assert.Contains(t, err.Error(), "validate bpmn: ")
				return
			}
			var failed *domain.ValidationFailedError
			require.ErrorAs(t, err, &failed)
			assert.Equal(t, tc.issues, failed.Errors)
		})
	}
}

func TestValidationService_Validate_ARejectedCompilerCallIsWrappedAndAnswersNoIssues(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler, ModuleVersions: mocks.NewMockModuleVersionRepository(ctrl)})
	failure := errors.New("compiler exploded")
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<main/>", gomock.Len(0)).Return("<bundled/>", nil)
	compiler.EXPECT().ValidateForPublish(gomock.Any(), "<bundled/>").Return(nil, failure)

	isValid, errs, err := svc.Validate(context.Background(), uuid.New(), "<main/>")

	require.Error(t, err)
	assert.ErrorIs(t, err, failure)
	assert.Contains(t, err.Error(), "validate bpmn: ")
	assert.False(t, isValid)
	assert.Empty(t, errs)
}
