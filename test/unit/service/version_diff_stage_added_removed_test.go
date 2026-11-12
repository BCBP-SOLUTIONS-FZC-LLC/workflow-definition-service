package service_test

import (
	"context"
	"errors"
	"fmt"
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

func stagesPlan(deptID string, stages ...dsl.StageDef) *dsl.CompiledPlan {
	return &dsl.CompiledPlan{
		Name:        "test",
		Departments: []dsl.DepartmentDef{{ID: deptID, Label: deptID, Stages: stages}},
	}
}

func taskStage(nodeID string) dsl.StageDef {
	return dsl.StageDef{Type: "task", NodeID: nodeID, Role: "reviewer"}
}

func TestVersionService_Diff_AStageOnlyInTheTarget_IsAStructuralChange(t *testing.T) {
	t.Parallel()

	result := w3aDiffStoredPlans(t,
		stagesPlan("a", taskStage("T1")),
		stagesPlan("a", taskStage("T1"), taskStage("T2")))

	assert.Equal(t, service.ChangeTypeStructural, result.ChangeType)
	assert.False(t, result.Changes.MetadataOnly)
	assert.Equal(t, []service.StageChange{{NodeKey: "a/T2", Description: "stage added"}}, result.Changes.StageChanges)
}

func TestVersionService_Diff_AStageOnlyInTheBase_IsAStructuralChange(t *testing.T) {
	t.Parallel()

	result := w3aDiffStoredPlans(t,
		stagesPlan("a", taskStage("T1"), taskStage("T2")),
		stagesPlan("a", taskStage("T1")))

	assert.Equal(t, service.ChangeTypeStructural, result.ChangeType)
	assert.False(t, result.Changes.MetadataOnly)
	assert.Equal(t, []service.StageChange{{NodeKey: "a/T2", Description: "stage removed"}}, result.Changes.StageChanges)
}

func TestVersionService_Diff_AReplacedStage_ListsTheAddedStageThenTheRemovedOne(t *testing.T) {
	t.Parallel()

	result := w3aDiffStoredPlans(t,
		stagesPlan("a", taskStage("T1")),
		stagesPlan("a", taskStage("T2")))

	assert.Equal(t, []service.StageChange{
		{NodeKey: "a/T2", Description: "stage added"},
		{NodeKey: "a/T1", Description: "stage removed"},
	}, result.Changes.StageChanges)
}

func TestVersionService_Diff_TheSameStageIDInAnotherDepartment_IsAnAddedStage(t *testing.T) {
	t.Parallel()
	base := &dsl.CompiledPlan{Name: "test", Departments: []dsl.DepartmentDef{{ID: "a", Stages: []dsl.StageDef{taskStage("T1")}}, {ID: "b"}}}
	target := &dsl.CompiledPlan{Name: "test", Departments: []dsl.DepartmentDef{{ID: "a", Stages: []dsl.StageDef{taskStage("T1")}}, {ID: "b", Stages: []dsl.StageDef{taskStage("T1")}}}}

	result := w3aDiffStoredPlans(t, base, target)

	assert.Equal(t, []service.StageChange{{NodeKey: "b/T1", Description: "stage added"}}, result.Changes.StageChanges)
}

func TestVersionService_Diff_SameStages_AreStillMetadataOnly(t *testing.T) {
	t.Parallel()
	plan := stagesPlan("a", taskStage("T1"), taskStage("T2"))

	result := w3aDiffStoredPlans(t, plan, plan)

	assert.Empty(t, result.Changes.StageChanges)
	assert.Equal(t, service.ChangeTypeMetadataOnly, result.ChangeType)
}

func TestVersionService_Publish_AddingOrRemovingAStage_NeedsTheForceFlag(t *testing.T) {
	cases := map[string]struct{ active, draft *dsl.CompiledPlan }{
		"a stage added":   {active: stagesPlan("a", taskStage("T1")), draft: stagesPlan("a", taskStage("T1"), taskStage("T2"))},
		"a stage removed": {active: stagesPlan("a", taskStage("T1"), taskStage("T2")), draft: stagesPlan("a", taskStage("T1"))},
	}
	for name, tc := range cases {
		for _, force := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/force=%v", name, force), func(t *testing.T) {
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
				compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(tc.draft), nil)
				compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
				wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
				vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).
					Return(&domain.WorkflowVersion{CompiledPlanJSON: marshalledPlan(t, tc.active)}, nil)
				if force {
					vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(2), nil)
					tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
						func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
					vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, gomock.Any(), int32(2), gomock.Any(), "h", gomock.Any()).Return(nil)
					assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
					wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
				}

				_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, force)

				if force {
					require.NoError(t, err)
					return
				}
				if !errors.Is(err, domain.ErrStructuralDivergence) {
					t.Fatalf("err = %v, want ErrStructuralDivergence without the force flag", err)
				}
			})
		}
	}
}
