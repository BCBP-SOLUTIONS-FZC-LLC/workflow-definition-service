package service_test

import (
	"context"
	"errors"
	"slices"
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

type w9aScopeWire struct {
	svc            *service.ModuleService
	versions       *mocks.MockModuleVersionRepository
	globalVersions *mocks.MockModuleVersionRepository
}

func newW9aScopeWire(t *testing.T) *w9aScopeWire {
	t.Helper()
	ctrl := gomock.NewController(t)
	w := &w9aScopeWire{
		versions:       mocks.NewMockModuleVersionRepository(ctrl),
		globalVersions: mocks.NewMockModuleVersionRepository(ctrl),
	}
	w.svc = service.NewModuleService(service.ModuleDeps{
		Versions: w.versions, GlobalVersions: w.globalVersions, Compiler: bpmn_compiler.New(),
	})
	return w
}

func TestModuleContract_NestedCallsResolveFromTheRepositoryOfTheVersionsScope(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		scope domain.CatalogScope
	}{
		{name: "tenant version resolves as the caller tenant", scope: domain.ScopeTenant},
		{name: "global version resolves as the global catalog", scope: domain.ScopeGlobal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newW9aScopeWire(t)
			tenantID := uuid.New()
			v := libraryVersion(uuid.New(), "Process_Package", 3, contractPackageBPMN)
			v.Scope = tc.scope
			check := libraryVersion(uuid.New(), "Process_Check", 1, goldenCheckBPMN)
			w.versions.EXPECT().GetByID(gomock.Any(), tenantID, v.ID).Return(v, nil)
			library := w.versions
			libraryTenant := tenantID
			if tc.scope == domain.ScopeGlobal {
				library = w.globalVersions
				libraryTenant = uuid.Nil
			}
			library.EXPECT().Resolve(gomock.Any(), libraryTenant, "Process_Check", int32(0)).Return(check, nil)

			got, err := w.svc.Contract(context.Background(), tenantID, v.ModuleID, v.ID)

			require.NoError(t, err)
			keys := make([]string, len(got.Tasks))
			for i, task := range got.Tasks {
				keys[i] = task.AssigneeKey
			}
			assert.True(t, slices.Contains(keys, "Pkg_Check::Check_Task"), "the nested module's task is part of the contract: %v", keys)
		})
	}
}

func TestModuleContract_GlobalVersionCallingATenantOnlyModule_FailsWithUnresolvedCall(t *testing.T) {
	t.Parallel()

	w := newW9aScopeWire(t)
	tenantID := uuid.New()
	v := libraryVersion(uuid.New(), "Process_Package", 1, contractPackageBPMN)
	v.Scope = domain.ScopeGlobal
	w.versions.EXPECT().GetByID(gomock.Any(), tenantID, v.ID).Return(v, nil)
	w.globalVersions.EXPECT().Resolve(gomock.Any(), uuid.Nil, "Process_Check", int32(0)).Return(nil, domain.ErrNotFound)

	_, err := w.svc.Contract(context.Background(), tenantID, v.ModuleID, v.ID)

	var failed *domain.ValidationFailedError
	require.True(t, errors.As(err, &failed), "err = %v", err)
	require.Len(t, failed.Errors, 1)
	assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, failed.Errors[0].Code)
	assert.Equal(t, "Pkg_Check", failed.Errors[0].NodeID)
	assert.Contains(t, failed.Errors[0].Message, `"Process_Check"`)
}

func TestModuleService_Usages_ModuleNotFound_ReturnsNotFoundWithoutFurtherLookups(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{name: "plain not found", err: domain.ErrNotFound},
		{name: "wrapped not found", err: errors.Join(errors.New("row lookup"), domain.ErrNotFound)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			svc, m := newLibraryModuleService(ctrl)
			tenantID, moduleID := uuid.New(), uuid.New()
			m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).Return(nil, tc.err)

			usages, err := svc.Usages(context.Background(), tenantID, moduleID)

			require.ErrorIs(t, err, domain.ErrNotFound)
			assert.Nil(t, usages)
		})
	}
}
