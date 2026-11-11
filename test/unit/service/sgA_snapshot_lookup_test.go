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

type sgASnapshotFixture struct {
	tenantID uuid.UUID
	refs     []domain.ModuleRef
	byID     map[uuid.UUID]*domain.ModuleVersion
}

func sgANewSnapshotFixture() sgASnapshotFixture {
	f := sgASnapshotFixture{tenantID: uuid.New(), byID: map[uuid.UUID]*domain.ModuleVersion{}}
	moduleID := uuid.New()
	for _, r := range []struct {
		process string
		number  int32
	}{{"Process_M", 1}, {"Process_M", 3}, {"Process_M", 2}, {"Process_X", 9}} {
		v := libraryVersion(moduleID, r.process, r.number, "<module "+r.process+"/>")
		f.byID[v.ID] = v
		f.refs = append(f.refs, domain.ModuleRef{ModuleID: moduleID, VersionID: v.ID, ProcessID: r.process, VersionNumber: r.number})
	}
	return f
}

func (f sgASnapshotFixture) refTo(process string, number int32) domain.ModuleRef {
	for _, r := range f.refs {
		if r.ProcessID == process && r.VersionNumber == number {
			return r
		}
	}
	panic("no ref")
}

func TestValidationService_ValidateVersion_APublishedVersionPicksItsRecordedModuleVersionByID(t *testing.T) {
	cases := []struct {
		name        string
		callVersion int32
		wantNumber  int32
	}{
		{"an unpinned call takes the highest recorded version of its module", 0, 3},
		{"a pinned call takes exactly that recorded version", 2, 2},
		{"a pinned call takes the oldest recorded version when asked", 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := sgANewSnapshotFixture()
			ctrl := gomock.NewController(t)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			versions := mocks.NewMockModuleVersionRepository(ctrl)
			want := f.refTo("Process_M", tc.wantNumber)
			picked := f.byID[want.VersionID]

			compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_M", VersionNumber: tc.callVersion}}, nil, nil)
			versions.EXPECT().GetByID(gomock.Any(), f.tenantID, want.VersionID).Return(picked, nil)
			compiler.EXPECT().ModuleCalls(gomock.Any(), picked.BPMNXML).Return(nil, nil, nil)
			compiler.EXPECT().BundleModules("<main/>", []domain.ResolvedModule{{
				ProcessID: "Process_M", VersionNumber: tc.callVersion, PlanName: fmt.Sprintf("Process_M@v%d", tc.wantNumber), BPMNXML: picked.BPMNXML,
			}}).Return("<bundled/>", nil)
			compiler.EXPECT().ValidateForPublish(gomock.Any(), "<bundled/>").Return(nil, nil)
			svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler, ModuleVersions: versions})

			isValid, errs, err := svc.ValidateVersion(context.Background(), f.tenantID,
				&domain.WorkflowVersion{Status: domain.VersionStatusPublished, BPMNXML: "<main/>", ModuleRefs: f.refs})

			require.NoError(t, err)
			assert.True(t, isValid)
			assert.Empty(t, errs)
		})
	}
}

func TestValidationService_ValidateVersion_ACallWithNoRecordedRefIsUnresolvedWithoutAReadOrALiveLookup(t *testing.T) {
	cases := []struct {
		name string
		call domain.ModuleCall
		refs func(f sgASnapshotFixture) []domain.ModuleRef
	}{
		{"no ref names the process", domain.ModuleCall{CallID: "CA_1", ProcessID: "Process_Gone"}, func(f sgASnapshotFixture) []domain.ModuleRef { return f.refs }},
		{"the pinned number was never recorded", domain.ModuleCall{CallID: "CA_1", ProcessID: "Process_M", VersionNumber: 7}, func(f sgASnapshotFixture) []domain.ModuleRef { return f.refs }},
		{"the version records no refs at all", domain.ModuleCall{CallID: "CA_1", ProcessID: "Process_M"}, func(sgASnapshotFixture) []domain.ModuleRef { return nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := sgANewSnapshotFixture()
			ctrl := gomock.NewController(t)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			versions := mocks.NewMockModuleVersionRepository(ctrl)
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{tc.call}, nil, nil)
			svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler, ModuleVersions: versions})

			isValid, errs, err := svc.ValidateVersion(context.Background(), f.tenantID,
				&domain.WorkflowVersion{Status: domain.VersionStatusArchived, BPMNXML: "<main/>", ModuleRefs: tc.refs(f)})

			require.NoError(t, err)
			assert.False(t, isValid)
			require.Len(t, errs, 1, "errors = %+v", errs)
			assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, errs[0].Code)
			assert.Equal(t, "CA_1", errs[0].NodeID)
			assert.Contains(t, errs[0].Message, tc.call.ProcessID)
		})
	}
}

func TestValidationService_ValidateVersion_APublishedVersionWhoseRecordedModuleCannotBeReadFailsTheCall(t *testing.T) {
	f := sgANewSnapshotFixture()
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	versions := mocks.NewMockModuleVersionRepository(ctrl)
	want := f.refTo("Process_M", 3)
	readFailure := errors.New("connection reset")
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_M"}}, nil, nil)
	versions.EXPECT().GetByID(gomock.Any(), f.tenantID, want.VersionID).Return(nil, readFailure)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler, ModuleVersions: versions})

	isValid, errs, err := svc.ValidateVersion(context.Background(), f.tenantID,
		&domain.WorkflowVersion{Status: domain.VersionStatusPublished, BPMNXML: "<main/>", ModuleRefs: f.refs})

	require.Error(t, err)
	assert.ErrorIs(t, err, readFailure)
	assert.Contains(t, err.Error(), "get module version "+want.VersionID.String())
	assert.False(t, isValid)
	assert.Empty(t, errs)
}

func TestValidationService_ValidateVersion_ADraftResolvesItsModulesLiveForItsTenant(t *testing.T) {
	f := sgANewSnapshotFixture()
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	versions := mocks.NewMockModuleVersionRepository(ctrl)
	live := f.byID[f.refTo("Process_M", 3).VersionID]
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_M"}}, nil, nil)
	versions.EXPECT().Resolve(gomock.Any(), f.tenantID, "Process_M", int32(0)).Return(live, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), live.BPMNXML).Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<main/>", []domain.ResolvedModule{{ProcessID: "Process_M", PlanName: "Process_M@v3", BPMNXML: live.BPMNXML}}).Return("<bundled/>", nil)
	compiler.EXPECT().ValidateForPublish(gomock.Any(), "<bundled/>").Return(nil, nil)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler, ModuleVersions: versions})

	isValid, errs, err := svc.ValidateVersion(context.Background(), f.tenantID,
		&domain.WorkflowVersion{Status: domain.VersionStatusDraft, BPMNXML: "<main/>", ModuleRefs: f.refs[:1]})

	require.NoError(t, err)
	assert.True(t, isValid)
	assert.Empty(t, errs)
}
