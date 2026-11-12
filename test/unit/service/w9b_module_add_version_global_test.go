package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestModuleService_AddVersion_GlobalModule_ValidatesAndWritesThroughTheGlobalRepository(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	tenantID, userID, moduleID := uuid.New(), uuid.New(), uuid.New()
	m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
		Return(&domain.Module{ID: moduleID, Scope: domain.ScopeGlobal, ProcessID: "Process_Global"}, nil)
	m.compiler.EXPECT().ProcessID(gomock.Any(), "<global/>").Return("Process_Global", nil)
	m.compiler.EXPECT().ModuleCalls(gomock.Any(), "<global/>").
		Return([]domain.ModuleCall{{CallID: "Call_1", ProcessID: "Process_Lib"}}, nil, nil)
	number := int32(2)
	lib := &domain.ModuleVersion{ID: uuid.New(), ModuleID: uuid.New(), ProcessID: "Process_Lib", VersionNumber: &number, BPMNXML: "<lib/>"}
	m.globalVersions.EXPECT().Resolve(gomock.Any(), uuid.Nil, "Process_Lib", int32(0)).Return(lib, nil)
	m.compiler.EXPECT().ModuleCalls(gomock.Any(), "<lib/>").Return(nil, nil, nil)
	m.compiler.EXPECT().BundleModules("<global/>", []domain.ResolvedModule{
		{ProcessID: "Process_Lib", PlanName: "Process_Lib@v2", BPMNXML: "<lib/>"},
	}).Return("<bundled/>", nil)
	m.compiler.EXPECT().ValidateModule(gomock.Any(), "<bundled/>", "Process_Global", true).Return(nil, nil)
	var stored *domain.ModuleVersion
	m.globalVersions.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, v *domain.ModuleVersion) error {
			stored = v
			return nil
		})

	xml := "<global/>"
	got, err := svc.AddVersion(context.Background(), tenantID, userID, moduleID, service.AddModuleVersionReq{BPMNXML: &xml})

	require.NoError(t, err)
	require.NotNil(t, stored, "the version is written through the global repository, not the tenant one")
	assert.Same(t, stored, got)
	assert.Equal(t, domain.ScopeGlobal, stored.Scope)
	assert.Nil(t, stored.TenantID, "a global module's version belongs to no tenant")
	assert.Equal(t, domain.VersionStatusDraft, stored.Status)
	assert.Equal(t, moduleID, stored.ModuleID)
	assert.Equal(t, "Process_Global", stored.ProcessID)
	assert.Equal(t, "<global/>", stored.BPMNXML)
	assert.True(t, stored.IsValid)
	require.NotNil(t, stored.CreatedByUserID)
	assert.Equal(t, userID, *stored.CreatedByUserID)
}
