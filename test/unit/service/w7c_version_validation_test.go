package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func w7cCodes(errs []domain.BPMNValidationError) []domain.BPMNErrorCode {
	codes := make([]domain.BPMNErrorCode, 0, len(errs))
	for _, e := range errs {
		codes = append(codes, e.Code)
	}
	return codes
}

func w7cRef(moduleID uuid.UUID, v *domain.ModuleVersion) domain.ModuleRef {
	return domain.ModuleRef{ModuleID: moduleID, VersionID: v.ID, ProcessID: v.ProcessID, VersionNumber: *v.VersionNumber}
}

func TestValidationService_ValidateVersion_APublishedVersionCallingAModuleItNeverRecorded_IsUnresolvedWithNoLiveFallback(t *testing.T) {
	t.Parallel()
	moduleID := uuid.New()
	other := libraryVersion(moduleID, "Process_Other", 1, libraryReviewBPMN)
	cases := []struct {
		name   string
		status domain.VersionStatus
		refs   []domain.ModuleRef
	}{
		{"published with no recorded modules", domain.VersionStatusPublished, nil},
		{"published with only another module recorded", domain.VersionStatusPublished, []domain.ModuleRef{w7cRef(moduleID, other)}},
		{"archived with no recorded modules", domain.VersionStatusArchived, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			modules := mocks.NewMockModuleVersionRepository(ctrl)
			svc := service.NewValidationService(service.ValidationDeps{Compiler: bpmn_compiler.New(), ModuleVersions: modules})
			tenantID := uuid.New()

			isValid, errs, err := svc.ValidateVersion(context.Background(), tenantID,
				&domain.WorkflowVersion{TenantID: tenantID, Status: tc.status, BPMNXML: libraryCallerBPMN, ModuleRefs: tc.refs})

			require.NoError(t, err)
			assert.False(t, isValid)
			require.Len(t, errs, 2, "errors = %+v", errs)
			assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, errs[0].Code)
			assert.Equal(t, "CA_1", errs[0].NodeID)
			assert.Contains(t, errs[0].Message, `"Process_Review" version 3`)
			assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, errs[1].Code)
			assert.Equal(t, "CA_2", errs[1].NodeID)
			assert.Contains(t, errs[1].Message, `"Process_Review"`)
			assert.NotContains(t, errs[1].Message, "version")
			assert.Equal(t, domain.SeverityError, errs[0].Severity)
			assert.Equal(t, domain.SeverityError, errs[1].Severity)
		})
	}
}

func TestValidationService_ValidateVersion_AModuleProblemStopsTheCompilerCheck(t *testing.T) {
	t.Parallel()
	withOrphan := strings.Replace(libraryCallerBPMN, `<bpmn:endEvent id="End_1"/>`, `<bpmn:endEvent id="End_1"/><bpmn:userTask id="Orphan_Task"/>`, 1)

	t.Run("the compiler on its own would report a separate structural problem", func(t *testing.T) {
		t.Parallel()
		errs, err := bpmn_compiler.New().ValidateForPublish(context.Background(), withOrphan)
		require.NoError(t, err)
		assert.Contains(t, w7cCodes(errs), domain.BPMNErrorCode("DANGLING_NODE"))
	})

	for _, tc := range []struct {
		name string
		run  func(svc *service.ValidationService, tenantID uuid.UUID) (bool, []domain.BPMNValidationError, error)
	}{
		{"on a draft", func(svc *service.ValidationService, tenantID uuid.UUID) (bool, []domain.BPMNValidationError, error) {
			return svc.ValidateVersion(context.Background(), tenantID,
				&domain.WorkflowVersion{TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: withOrphan})
		}},
		{"on a published version", func(svc *service.ValidationService, tenantID uuid.UUID) (bool, []domain.BPMNValidationError, error) {
			return svc.ValidateVersion(context.Background(), tenantID,
				&domain.WorkflowVersion{TenantID: tenantID, Status: domain.VersionStatusPublished, BPMNXML: withOrphan})
		}},
		{"on submitted text", func(svc *service.ValidationService, tenantID uuid.UUID) (bool, []domain.BPMNValidationError, error) {
			return svc.Validate(context.Background(), tenantID, withOrphan)
		}},
	} {
		t.Run("the module issues are the only ones returned "+tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			modules := mocks.NewMockModuleVersionRepository(ctrl)
			tenantID := uuid.New()
			modules.EXPECT().Resolve(gomock.Any(), tenantID, "Process_Review", gomock.Any()).Return(nil, domain.ErrNotFound).AnyTimes()
			svc := service.NewValidationService(service.ValidationDeps{Compiler: bpmn_compiler.New(), ModuleVersions: modules})

			isValid, errs, err := tc.run(svc, tenantID)

			require.NoError(t, err)
			assert.False(t, isValid)
			require.Len(t, errs, 2)
			assert.Equal(t, []domain.BPMNErrorCode{domain.BPMNErrUnresolvedCalledElement, domain.BPMNErrUnresolvedCalledElement}, w7cCodes(errs))
			assert.Equal(t, []string{"CA_1", "CA_2"}, []string{errs[0].NodeID, errs[1].NodeID})
		})
	}
}

func TestValidationService_ValidateVersion_AModuleProblemNeverReachesTheCompilerCheck(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	versions := mocks.NewMockModuleVersionRepository(ctrl)
	tenantID := uuid.New()
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_Gone"}}, nil, nil)
	versions.EXPECT().Resolve(gomock.Any(), tenantID, "Process_Gone", int32(0)).Return(nil, domain.ErrNotFound)
	compiler.EXPECT().BundleModules(gomock.Any(), gomock.Any()).Times(0)
	compiler.EXPECT().ValidateForPublish(gomock.Any(), gomock.Any()).Times(0)
	compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Times(0)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler, ModuleVersions: versions})

	isValid, errs, err := svc.ValidateVersion(context.Background(), tenantID,
		&domain.WorkflowVersion{Status: domain.VersionStatusDraft, BPMNXML: "<main/>"})

	require.NoError(t, err)
	assert.False(t, isValid)
	require.Len(t, errs, 1)
	assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, errs[0].Code)
	assert.Equal(t, "CA_1", errs[0].NodeID)
}

func TestValidationService_ValidateVersion_ASnapshotCallPicksTheExactPinnedNumberAndOtherwiseTheHighestRecorded(t *testing.T) {
	t.Parallel()
	moduleID := uuid.New()
	review2 := libraryVersion(moduleID, "Process_Review", 2, libraryReviewBPMN)
	review3 := libraryVersion(moduleID, "Process_Review", 3, libraryReviewBPMN)
	review4 := libraryVersion(moduleID, "Process_Review", 4, libraryReviewCallsCheckBPMN)

	t.Run("the pinned call reads exactly its number and the unpinned call reads the highest", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		modules := mocks.NewMockModuleVersionRepository(ctrl)
		tenantID := uuid.New()
		for _, v := range []*domain.ModuleVersion{review3, review4} {
			modules.EXPECT().GetByID(gomock.Any(), tenantID, v.ID).Return(v, nil).Times(1)
		}
		svc := service.NewValidationService(service.ValidationDeps{Compiler: bpmn_compiler.New(), ModuleVersions: modules})
		published := &domain.WorkflowVersion{
			TenantID: tenantID, Status: domain.VersionStatusPublished, BPMNXML: libraryCallerBPMN,
			ModuleRefs: []domain.ModuleRef{w7cRef(moduleID, review4), w7cRef(moduleID, review2), w7cRef(moduleID, review3)},
		}

		isValid, errs, err := svc.ValidateVersion(context.Background(), tenantID, published)

		require.NoError(t, err)
		assert.False(t, isValid)
		require.Len(t, errs, 1, "errors = %+v", errs)
		assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, errs[0].Code)
		assert.Equal(t, "CA_2", errs[0].NodeID, "the problem sits in version 4, the highest recorded one, which CA_2 takes")
		assert.Contains(t, errs[0].Message, `module "Process_Review" version 4`)
		assert.Contains(t, errs[0].Message, `"Process_Check"`)
	})

	t.Run("a pinned number that was never recorded is unresolved even when a higher one was", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		modules := mocks.NewMockModuleVersionRepository(ctrl)
		tenantID := uuid.New()
		review4Clean := libraryVersion(moduleID, "Process_Review", 4, libraryReviewBPMN)
		modules.EXPECT().GetByID(gomock.Any(), tenantID, review4Clean.ID).Return(review4Clean, nil).Times(1)
		svc := service.NewValidationService(service.ValidationDeps{Compiler: bpmn_compiler.New(), ModuleVersions: modules})
		published := &domain.WorkflowVersion{
			TenantID: tenantID, Status: domain.VersionStatusPublished, BPMNXML: libraryCallerBPMN,
			ModuleRefs: []domain.ModuleRef{w7cRef(moduleID, review2), w7cRef(moduleID, review4Clean)},
		}

		isValid, errs, err := svc.ValidateVersion(context.Background(), tenantID, published)

		require.NoError(t, err)
		assert.False(t, isValid)
		require.Len(t, errs, 1, "errors = %+v", errs)
		assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, errs[0].Code)
		assert.Equal(t, "CA_1", errs[0].NodeID)
		assert.Contains(t, errs[0].Message, `"Process_Review" version 3`)
	})
}

func TestValidationService_ValidateVersion_ADraftCallingPublishedModules_IsValidWithNoIssues(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	tenantID, moduleID := uuid.New(), uuid.New()
	v3 := libraryVersion(moduleID, "Process_Review", 3, libraryReviewBPMN)
	v4 := libraryVersion(moduleID, "Process_Review", 4, libraryReviewBPMN)
	type lookup struct {
		processID string
		version   int32
	}
	var lookups []lookup
	modules.EXPECT().Resolve(gomock.Any(), tenantID, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ uuid.UUID, processID string, version int32) (*domain.ModuleVersion, error) {
			lookups = append(lookups, lookup{processID, version})
			if version == 3 {
				return v3, nil
			}
			return v4, nil
		}).Times(2)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: bpmn_compiler.New(), ModuleVersions: modules})

	isValid, errs, err := svc.ValidateVersion(context.Background(), tenantID,
		&domain.WorkflowVersion{TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: libraryCallerBPMN})

	require.NoError(t, err)
	assert.True(t, isValid)
	assert.Empty(t, errs)
	assert.Equal(t, []lookup{{"Process_Review", 3}, {"Process_Review", 0}}, lookups)
}
