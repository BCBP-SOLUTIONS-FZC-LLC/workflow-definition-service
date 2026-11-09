package service_test

import (
	"context"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestVersionService_Diff_ModuleIssuesInAVersionThatMustBeRecompiled_AreReturned(t *testing.T) {
	issue := domain.BPMNValidationError{
		Code: domain.BPMNErrUnresolvedCalledElement, NodeID: "CA_1", Message: "module Process_Review version 3 does not exist",
	}
	cases := []struct {
		name         string
		baseStored   bool
		wantInChain  string
		wantNotInErr string
	}{
		{"the base cannot be resolved", false, "resolve base plan", "resolve target plan"},
		{"the target cannot be resolved", true, "resolve target plan", "resolve base plan"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})
			tenantID, wfID := uuid.New(), uuid.New()
			n1, n2 := int32(1), int32(2)
			base := &domain.WorkflowVersion{
				ID: uuid.New(), WorkflowID: wfID, TenantID: tenantID,
				Status: domain.VersionStatusPublished, VersionNumber: &n1, BPMNXML: "<base/>",
			}
			target := &domain.WorkflowVersion{
				ID: uuid.New(), WorkflowID: wfID, TenantID: tenantID,
				Status: domain.VersionStatusPublished, VersionNumber: &n2, BPMNXML: "<target/>",
			}
			unresolved := "<target/>"
			if tc.baseStored {
				base.CompiledPlanJSON = marshalledPlan(t, buildPlan("a"))
			} else {
				unresolved = "<base/>"
			}
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, base.ID).Return(base, nil)
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, target.ID).Return(target, nil)
			compiler.EXPECT().ModuleCalls(gomock.Any(), unresolved).
				Return(nil, []domain.BPMNValidationError{issue}, nil).Times(1)

			got, err := svc.Diff(context.Background(), tenantID, wfID, base.ID, target.ID)

			assert.Nil(t, got)
			var vf *domain.ValidationFailedError
			require.ErrorAs(t, err, &vf)
			assert.Equal(t, []domain.BPMNValidationError{issue}, vf.Errors)
			assert.Contains(t, err.Error(), tc.wantInChain)
			assert.NotContains(t, err.Error(), tc.wantNotInErr)
		})
	}
}

func TestVersionService_Diff_VersionAgainstItself_IsMetadataOnlyWithNoChanges(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})
	tenantID, wfID := uuid.New(), uuid.New()
	plan := &dsl.CompiledPlan{
		Name: "main",
		Departments: []dsl.DepartmentDef{
			{ID: "a", Label: "a", Stages: []dsl.StageDef{{Type: "review", NodeID: "R1", Role: "reviewer"}}},
			{ID: "b", Label: "b"},
		},
		Execution: dsl.ExecutionPlan{Steps: []dsl.ExecutionStep{{Sequential: []string{"a"}}, {Sequential: []string{"b"}}}},
	}
	n := int32(1)
	v := &domain.WorkflowVersion{
		ID: uuid.New(), WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusPublished,
		VersionNumber: &n, CompiledPlanJSON: marshalledPlan(t, plan),
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, v.ID).Return(v, nil).Times(2)

	got, err := svc.Diff(context.Background(), tenantID, wfID, v.ID, v.ID)

	require.NoError(t, err)
	assert.Equal(t, service.ChangeTypeMetadataOnly, got.ChangeType)
	assert.Equal(t, wfID, got.WorkflowID)
	assert.Equal(t, v.ID, got.BaseVersionID)
	assert.Equal(t, v.ID, got.TargetVersionID)
	assert.True(t, got.Changes.MetadataOnly)
	assert.Empty(t, got.Changes.AddedDepartments)
	assert.Empty(t, got.Changes.RemovedDepartments)
	assert.Empty(t, got.Changes.StepChanges)
	assert.Empty(t, got.Changes.StageChanges)
}
