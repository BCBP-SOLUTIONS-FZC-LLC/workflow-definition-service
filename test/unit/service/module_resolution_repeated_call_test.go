package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestValidationService_Validate_SameCallListedTwice_ResolvesTheModuleOnceAndReportsItsProblemOnce(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler, ModuleVersions: modules})

	tenantID := uuid.New()
	review := libraryVersion(uuid.New(), "Process_Review", 3, "<review/>")
	call := domain.ModuleCall{CallID: "CA_1", ProcessID: "Process_Review"}
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{call, call}, nil, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<review/>").Return(nil, []domain.BPMNValidationError{
		{Code: domain.BPMNErrNoStartEvent, NodeID: "Inner_1", Message: "missing start event", Severity: domain.SeverityError},
	}, nil)
	modules.EXPECT().Resolve(gomock.Any(), tenantID, "Process_Review", int32(0)).Return(review, nil).Times(1)

	isValid, errs, err := svc.Validate(context.Background(), tenantID, "<main/>")

	require.NoError(t, err)
	assert.False(t, isValid)
	require.Len(t, errs, 1, "the module's problem is attributed once, not once per listing of the call")
	assert.Equal(t, domain.BPMNErrNoStartEvent, errs[0].Code)
	assert.Equal(t, "CA_1", errs[0].NodeID)
	assert.Equal(t, `inside module "Process_Review" version 3: missing start event`, errs[0].Message)
}
