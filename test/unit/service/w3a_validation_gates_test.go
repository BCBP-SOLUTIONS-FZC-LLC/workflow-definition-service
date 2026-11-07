package service_test

import (
	"context"
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

func w3aMixedFindings() (warnings, blocking []domain.BPMNValidationError) {
	warnings = []domain.BPMNValidationError{
		{Code: "WARN_ONE", NodeID: "n1", Message: "first warning", Severity: domain.SeverityWarning},
		{Code: "WARN_TWO", NodeID: "n2", Message: "second warning", Severity: domain.SeverityWarning},
	}
	blocking = []domain.BPMNValidationError{
		{Code: "BLOCK_ONE", NodeID: "n3", Message: "first blocker", Severity: domain.SeverityError},
		{Code: "BLOCK_TWO", NodeID: "n4", Message: "second blocker", Severity: domain.SeverityError},
	}
	return warnings, blocking
}

func TestModuleService_Create_BlockingFindingsRefuseTheModuleAndStoreNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	warnings, blocking := w3aMixedFindings()

	expectModuleValidation(m.compiler, "P1", []domain.BPMNValidationError{warnings[0], blocking[0], warnings[1], blocking[1]}, nil)

	mod, ver, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateModuleReq{Name: "m", BPMNXML: "<bad/>"})

	var vfe *domain.ValidationFailedError
	require.ErrorAs(t, err, &vfe)
	assert.Nil(t, mod)
	assert.Nil(t, ver)
}

func TestModuleService_Create_WarningsOnlyPassesAndStoresTheModuleForTheCaller(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)
	warnings, _ := w3aMixedFindings()
	tenantID, userID := uuid.New(), uuid.New()

	expectModuleValidation(m.compiler, "P1", warnings, nil)
	m.modules.EXPECT().ProcessIDInUse(gomock.Any(), gomock.Any(), "P1").DoAndReturn(
		func(_ context.Context, scopeTenant *uuid.UUID, _ string) (bool, error) {
			require.NotNil(t, scopeTenant)
			assert.Equal(t, tenantID, *scopeTenant)
			return false, nil
		})
	var storedModule *domain.Module
	m.modules.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, mod *domain.Module) error { storedModule = mod; return nil })
	var storedVersion *domain.ModuleVersion
	m.versions.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, v *domain.ModuleVersion) error { storedVersion = v; return nil })

	mod, ver, err := svc.Create(context.Background(), tenantID, userID, service.CreateModuleReq{Name: "m", BPMNXML: "<warn/>"})

	require.NoError(t, err)
	require.NotNil(t, storedModule)
	require.NotNil(t, storedVersion)
	assert.Same(t, storedModule, mod)
	assert.Same(t, storedVersion, ver)
	require.NotNil(t, mod.TenantID)
	assert.Equal(t, tenantID, *mod.TenantID)
	require.NotNil(t, mod.CreatedByUserID)
	assert.Equal(t, userID, *mod.CreatedByUserID)
	assert.Equal(t, "<warn/>", ver.BPMNXML)
	assert.Equal(t, mod.ID, ver.ModuleID)
}

func TestStarterService_Create_BlockingFindingsRefuseTheStarterAndStoreNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)
	warnings, blocking := w3aMixedFindings()
	all := []domain.BPMNValidationError{warnings[0], blocking[0], warnings[1], blocking[1]}

	expectNoLibraryCalls(m.compiler)
	m.compiler.EXPECT().Validate(gomock.Any(), "<bad/>").Return(all, nil)

	st, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateStarterReq{Name: "s", BPMNXML: "<bad/>"})

	var vfe *domain.ValidationFailedError
	require.ErrorAs(t, err, &vfe)
	assert.Nil(t, st)
}

func TestStarterService_Create_WarningsOnlyIsCreatedForTheCaller(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)
	warnings, _ := w3aMixedFindings()
	tenantID, userID := uuid.New(), uuid.New()

	expectNoLibraryCalls(m.compiler)
	m.compiler.EXPECT().Validate(gomock.Any(), "<warn/>").Return(warnings, nil)
	var stored *domain.StarterTemplate
	m.starters.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, st *domain.StarterTemplate) error { stored = st; return nil })

	st, err := svc.Create(context.Background(), tenantID, userID, service.CreateStarterReq{Name: "s", BPMNXML: "<warn/>"})

	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Same(t, stored, st)
	require.NotNil(t, st.TenantID)
	assert.Equal(t, tenantID, *st.TenantID)
	require.NotNil(t, st.CreatedByUserID)
	assert.Equal(t, userID, *st.CreatedByUserID)
	assert.Equal(t, "<warn/>", st.BPMNXML)
}

func TestValidationService_ValidateVersion_DraftsResolveModulesLiveAndOtherStatusesUseTheirPinnedSnapshot(t *testing.T) {
	fullLibrary := func(moduleID uuid.UUID) map[libraryKey]*domain.ModuleVersion {
		return map[libraryKey]*domain.ModuleVersion{
			{"Process_Review", 3}: libraryVersion(moduleID, "Process_Review", 3, libraryReviewBPMN),
			{"Process_Review", 0}: libraryVersion(moduleID, "Process_Review", 4, libraryReviewBPMN),
		}
	}
	cases := []struct {
		name         string
		status       domain.VersionStatus
		liveLibrary  bool
		wantValid    bool
		wantLive     bool
		wantSnapshot bool
	}{
		{name: "draft with the modules in the live library", status: domain.VersionStatusDraft, liveLibrary: true, wantValid: true, wantLive: true},
		{name: "draft whose modules are gone from the live library", status: domain.VersionStatusDraft, liveLibrary: false, wantValid: false, wantLive: true},
		{name: "published with the live library empty", status: domain.VersionStatusPublished, liveLibrary: false, wantValid: true, wantSnapshot: true},
		{name: "archived with the live library empty", status: domain.VersionStatusArchived, liveLibrary: false, wantValid: true, wantSnapshot: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			modules := mocks.NewMockModuleVersionRepository(ctrl)
			svc := service.NewValidationService(service.ValidationDeps{Compiler: bpmn_compiler.New(), ModuleVersions: modules})
			tenantID, moduleID := uuid.New(), uuid.New()
			library := fullLibrary(moduleID)
			pinned3, pinned4 := library[libraryKey{"Process_Review", 3}], library[libraryKey{"Process_Review", 0}]

			liveCalls, snapshotCalls := 0, 0
			modules.EXPECT().Resolve(gomock.Any(), tenantID, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ uuid.UUID, processID string, version int32) (*domain.ModuleVersion, error) {
					liveCalls++
					if v, ok := library[libraryKey{processID, version}]; ok && tc.liveLibrary {
						return v, nil
					}
					return nil, domain.ErrNotFound
				}).AnyTimes()
			modules.EXPECT().GetByID(gomock.Any(), tenantID, gomock.Any()).DoAndReturn(
				func(_ context.Context, _, id uuid.UUID) (*domain.ModuleVersion, error) {
					snapshotCalls++
					switch id {
					case pinned3.ID:
						return pinned3, nil
					case pinned4.ID:
						return pinned4, nil
					}
					return nil, domain.ErrNotFound
				}).AnyTimes()
			version := &domain.WorkflowVersion{
				TenantID: tenantID, Status: tc.status, BPMNXML: libraryCallerBPMN,
				ModuleRefs: []domain.ModuleRef{
					{ModuleID: moduleID, VersionID: pinned3.ID, ProcessID: "Process_Review", VersionNumber: 3},
					{ModuleID: moduleID, VersionID: pinned4.ID, ProcessID: "Process_Review", VersionNumber: 4},
				},
			}

			isValid, errs, err := svc.ValidateVersion(context.Background(), tenantID, version)

			require.NoError(t, err)
			assert.Equal(t, tc.wantValid, isValid, "errors: %+v", errs)
			if !tc.wantValid {
				var codes []domain.BPMNErrorCode
				for _, e := range errs {
					codes = append(codes, e.Code)
				}
				assert.Contains(t, codes, domain.BPMNErrUnresolvedCalledElement)
			}
			assert.Equal(t, tc.wantLive, liveCalls > 0, "live resolutions: %d", liveCalls)
			assert.Equal(t, tc.wantSnapshot, snapshotCalls > 0, "snapshot reads: %d", snapshotCalls)
		})
	}
}
