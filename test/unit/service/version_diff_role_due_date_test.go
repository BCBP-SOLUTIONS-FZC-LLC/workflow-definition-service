package service_test

import (
	"context"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestVersionService_Diff_ATaskRoleChange_IsAStructuralChange(t *testing.T) {
	t.Parallel()
	before := dsl.StageDef{Type: "task", NodeID: "T1", Role: "reviewer"}
	after := dsl.StageDef{Type: "task", NodeID: "T1", Role: "approver"}

	result := w3aDiffStoredPlans(t, stagesPlan("a", before), stagesPlan("a", after))

	assert.Equal(t, service.ChangeTypeStructural, result.ChangeType)
	assert.False(t, result.Changes.MetadataOnly)
	assert.Equal(t, []service.StageChange{{NodeKey: "a/T1", Description: "role changed"}}, result.Changes.StageChanges)
	assert.Empty(t, result.Changes.MetadataChanges)
}

func TestVersionService_Diff_ADueDateChange_IsAMetadataChange(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, base, target string }{
		{"a date set", "", "P3D"},
		{"a date removed", "P3D", ""},
		{"a date moved", "P3D", "P5D"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := dsl.StageDef{Type: "task", NodeID: "T1", Role: "reviewer", DueDate: tc.base}
			after := dsl.StageDef{Type: "task", NodeID: "T1", Role: "reviewer", DueDate: tc.target}

			result := w3aDiffStoredPlans(t, stagesPlan("a", before), stagesPlan("a", after))

			assert.Equal(t, service.ChangeTypeMetadataOnly, result.ChangeType)
			assert.True(t, result.Changes.MetadataOnly)
			assert.Empty(t, result.Changes.StageChanges)
			assert.Equal(t, []service.StageChange{{NodeKey: "a/T1", Description: "due date changed"}}, result.Changes.MetadataChanges)
		})
	}
}

func TestVersionService_Diff_RoleAndDueDateChangedTogether_ListsEachInItsOwnList(t *testing.T) {
	t.Parallel()
	before := dsl.StageDef{Type: "task", NodeID: "T1", Role: "reviewer", DueDate: "P3D"}
	after := dsl.StageDef{Type: "task", NodeID: "T1", Role: "approver", DueDate: "P5D"}

	result := w3aDiffStoredPlans(t, stagesPlan("a", before), stagesPlan("a", after))

	assert.Equal(t, service.ChangeTypeStructural, result.ChangeType)
	assert.Equal(t, []service.StageChange{{NodeKey: "a/T1", Description: "role changed"}}, result.Changes.StageChanges)
	assert.Equal(t, []service.StageChange{{NodeKey: "a/T1", Description: "due date changed"}}, result.Changes.MetadataChanges)
}

func TestVersionService_Diff_ADueDateChangeBesideATimerChange_ListsBoth(t *testing.T) {
	t.Parallel()
	before := dsl.StageDef{Type: "task", NodeID: "T1", Role: "reviewer", DueDate: "P3D"}
	after := before
	after.DueDate = "P5D"
	after.BoundaryTimer = &dsl.BoundaryTimer{BoundaryID: "B1", Duration: "P1D", Interrupting: true, Terminates: true}

	result := w3aDiffStoredPlans(t, stagesPlan("a", before), stagesPlan("a", after))

	assert.Equal(t, []service.StageChange{{NodeKey: "a/T1", Description: "timer boundary changed"}}, result.Changes.StageChanges)
	assert.Equal(t, []service.StageChange{{NodeKey: "a/T1", Description: "due date changed"}}, result.Changes.MetadataChanges)
}

func TestVersionService_Diff_UnchangedRoleAndDueDate_ReportNothing(t *testing.T) {
	t.Parallel()
	stage := dsl.StageDef{Type: "task", NodeID: "T1", Role: "reviewer", DueDate: "P3D"}

	result := w3aDiffStoredPlans(t, stagesPlan("a", stage), stagesPlan("a", stage))

	assert.Empty(t, result.Changes.StageChanges)
	assert.Empty(t, result.Changes.MetadataChanges)
	assert.True(t, result.Changes.MetadataOnly)
}

func TestVersionService_Publish_ARoleChangeNeedsTheForceFlagButADueDateChangeDoesNot(t *testing.T) {
	reviewer := dsl.StageDef{Type: "task", NodeID: "T1", Role: "reviewer", DueDate: "P3D"}
	approver := reviewer
	approver.Role = "approver"
	later := reviewer
	later.DueDate = "P5D"
	cases := []struct {
		name           string
		draft          dsl.StageDef
		force          bool
		wantDivergence bool
	}{
		{"role changed, no force", approver, false, true},
		{"role changed, forced", approver, true, false},
		{"due date changed, no force", later, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			tx := mocks.NewMockTransactor(ctrl)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			assignees := mocks.NewMockAssigneeRepository(ctrl)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			svc := service.NewVersionService(service.VersionDeps{
				Transactor: tx, Workflows: wfRepo, Versions: vRepo, Assignees: assignees, Compiler: compiler,
			})
			tenantID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(&domain.WorkflowVersion{
				ID: vID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
			}, nil)
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
			compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
			compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(stagesPlan("a", tc.draft)), nil)
			compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
			wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).
				Return(&domain.WorkflowVersion{CompiledPlanJSON: marshalledPlan(t, stagesPlan("a", reviewer))}, nil)
			if !tc.wantDivergence {
				vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(2), nil)
				tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
				vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, gomock.Any(), int32(2), gomock.Any(), "h", gomock.Any()).Return(nil)
				assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
				wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
			}

			_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, tc.force)

			if tc.wantDivergence {
				assert.ErrorIs(t, err, domain.ErrStructuralDivergence)
				return
			}
			assert.NoError(t, err)
		})
	}
}
