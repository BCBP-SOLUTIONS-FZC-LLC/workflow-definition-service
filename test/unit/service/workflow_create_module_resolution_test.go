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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestWorkflowService_Create_ModuleResolution_StopsBeforeCompilerValidation(t *testing.T) {
	t.Parallel()

	lookupDown := fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable)
	cases := []struct {
		name        string
		resolveErr  error
		wantIssue   bool
		wantWrapped error
	}{
		{name: "a call to an unpublished module is a validation failure", resolveErr: domain.ErrNotFound, wantIssue: true},
		{name: "a failing module lookup is an error, not a validation failure", resolveErr: lookupDown, wantWrapped: domain.ErrUpstreamUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			modules := mocks.NewMockModuleVersionRepository(ctrl)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewWorkflowService(service.WorkflowDeps{
				PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl),
				Workflows: wfRepo, Versions: vRepo, Compiler: compiler, ModuleVersions: modules,
			})

			tenantID := uuid.New()
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return(
				[]domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_Missing", VersionNumber: 2}}, nil, nil)
			modules.EXPECT().Resolve(gomock.Any(), tenantID, "Process_Missing", int32(2)).Return(nil, tc.resolveErr)

			wf, v, err := svc.Create(context.Background(), tenantID, uuid.New(), "key", "name", "", "<main/>", "starter")

			assert.Nil(t, wf)
			assert.Nil(t, v)
			require.Error(t, err)
			var vf *domain.ValidationFailedError
			if tc.wantIssue {
				require.ErrorAs(t, err, &vf)
				require.Len(t, vf.Errors, 1)
				assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, vf.Errors[0].Code)
				assert.Equal(t, "CA_1", vf.Errors[0].NodeID)
				assert.Contains(t, vf.Errors[0].Message, `"Process_Missing" version 2`)
				return
			}
			assert.False(t, errors.As(err, &vf), "an infrastructure failure must not be reported as a validation failure")
			assert.ErrorIs(t, err, tc.wantWrapped)
		})
	}
}
