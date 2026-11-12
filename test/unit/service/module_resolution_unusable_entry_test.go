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

func moduleDraftWithoutNumber(moduleID uuid.UUID, processID string) *domain.ModuleVersion {
	return &domain.ModuleVersion{ID: uuid.New(), ModuleID: moduleID, ProcessID: processID, Status: domain.VersionStatusDraft, BPMNXML: "<draft/>"}
}

func TestValidationService_Validate_AResolvedModuleVersionWithNoNumber_IsAnUnresolvedCall(t *testing.T) {
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler, ModuleVersions: modules})
	tenantID := uuid.New()
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_M"}}, nil, nil)
	modules.EXPECT().Resolve(gomock.Any(), tenantID, "Process_M", int32(0)).Return(moduleDraftWithoutNumber(uuid.New(), "Process_M"), nil)

	var (
		isValid bool
		errs    []domain.BPMNValidationError
		err     error
	)
	require.NotPanics(t, func() { isValid, errs, err = svc.Validate(context.Background(), tenantID, "<main/>") })

	require.NoError(t, err)
	assert.False(t, isValid)
	require.Len(t, errs, 1)
	assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, errs[0].Code)
	assert.Equal(t, "CA_1", errs[0].NodeID)
}

func TestValidationService_Validate_ANilModuleVersionFromTheLookup_IsAnUnresolvedCall(t *testing.T) {
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler, ModuleVersions: modules})
	tenantID := uuid.New()
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_M", VersionNumber: 2}}, nil, nil)
	modules.EXPECT().Resolve(gomock.Any(), tenantID, "Process_M", int32(2)).Return(nil, nil)

	var (
		errs []domain.BPMNValidationError
		err  error
	)
	require.NotPanics(t, func() { _, errs, err = svc.Validate(context.Background(), tenantID, "<main/>") })

	require.NoError(t, err)
	require.Len(t, errs, 1)
	assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, errs[0].Code)
}

func TestValidationService_ValidateVersion_ASnapshotEntryPointingAtADraftModuleVersion_IsAnUnresolvedCall(t *testing.T) {
	f := sgANewSnapshotFixture()
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	versions := mocks.NewMockModuleVersionRepository(ctrl)
	want := f.refTo("Process_M", 3)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_M"}}, nil, nil)
	versions.EXPECT().GetByID(gomock.Any(), f.tenantID, want.VersionID).Return(moduleDraftWithoutNumber(want.ModuleID, "Process_M"), nil)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler, ModuleVersions: versions})

	var (
		isValid bool
		errs    []domain.BPMNValidationError
		err     error
	)
	require.NotPanics(t, func() {
		isValid, errs, err = svc.ValidateVersion(context.Background(), f.tenantID,
			&domain.WorkflowVersion{Status: domain.VersionStatusPublished, BPMNXML: "<main/>", ModuleRefs: f.refs})
	})

	require.NoError(t, err)
	assert.False(t, isValid)
	require.Len(t, errs, 1)
	assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, errs[0].Code)
	assert.Equal(t, "CA_1", errs[0].NodeID)
}
