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

func w3aDiffStoredPlans(t *testing.T, base, target *dsl.CompiledPlan) *service.DiffResult {
	t.Helper()
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(&domain.WorkflowVersion{
		ID: baseID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusPublished,
		CompiledPlanJSON: marshalledPlan(t, base),
	}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(&domain.WorkflowVersion{
		ID: targetID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusPublished,
		CompiledPlanJSON: marshalledPlan(t, target),
	}, nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	require.NoError(t, err)
	return result
}

func w3aStepPlan(steps ...string) *dsl.CompiledPlan {
	plan := &dsl.CompiledPlan{Name: "test"}
	for _, s := range steps {
		plan.Execution.Steps = append(plan.Execution.Steps, dsl.ExecutionStep{Sequential: []string{s}})
	}
	return plan
}

func TestVersionService_Diff_AStepOnlyInTheTarget_IsOneChangeWithNothingBefore(t *testing.T) {
	result := w3aDiffStoredPlans(t, w3aStepPlan("step-a"), w3aStepPlan("step-a", "step-b"))

	assert.Equal(t, service.ChangeTypeStructural, result.ChangeType)
	require.Len(t, result.Changes.StepChanges, 1)
	change := result.Changes.StepChanges[0]
	assert.Equal(t, "step 2 changed", change.Description)
	assert.Nil(t, change.Before)
	require.NotNil(t, change.After)
	assert.Equal(t, []string{"step-b"}, change.After.Sequential)
	assert.Empty(t, result.Changes.StageChanges)
}

func TestVersionService_Diff_AStepOnlyInTheBase_IsOneChangeWithNothingAfter(t *testing.T) {
	result := w3aDiffStoredPlans(t, w3aStepPlan("step-a", "step-b"), w3aStepPlan("step-a"))

	assert.Equal(t, service.ChangeTypeStructural, result.ChangeType)
	require.Len(t, result.Changes.StepChanges, 1)
	change := result.Changes.StepChanges[0]
	assert.Equal(t, "step 2 changed", change.Description)
	require.NotNil(t, change.Before)
	assert.Equal(t, []string{"step-b"}, change.Before.Sequential)
	assert.Nil(t, change.After)
}

func TestVersionService_Diff_PlansWithoutStepsHaveNoStepChanges(t *testing.T) {
	result := w3aDiffStoredPlans(t, w3aStepPlan(), w3aStepPlan())

	assert.Nil(t, result.Changes.StepChanges)
	assert.Equal(t, service.ChangeTypeMetadataOnly, result.ChangeType)
	assert.True(t, result.Changes.MetadataOnly)
}

func w3aStagePlan(deptStages map[string]dsl.StageDef) *dsl.CompiledPlan {
	plan := &dsl.CompiledPlan{Name: "test"}
	for _, id := range []string{"a", "b"} {
		if stage, ok := deptStages[id]; ok {
			plan.Departments = append(plan.Departments, dsl.DepartmentDef{ID: id, Label: id, Stages: []dsl.StageDef{stage}})
		}
	}
	return plan
}

func TestVersionService_Diff_OneStageChange_IsNamedByTheKindOfBoundaryOrConnectorThatChanged(t *testing.T) {
	plain := dsl.StageDef{Type: "task", NodeID: "T1", Role: "reviewer"}
	withMessage := plain
	withMessage.BoundaryMessage = &dsl.MessagePath{MessageName: "stop", BoundaryID: "B2", Interrupting: true, Terminates: true}
	withTimer := plain
	withTimer.BoundaryTimer = &dsl.BoundaryTimer{BoundaryID: "B1", Duration: "P1D", Interrupting: true, Terminates: true}
	otherConnector := plain
	otherConnector.ConnectorType = "send-email"

	cases := []struct {
		name            string
		target          dsl.StageDef
		wantDescription string
	}{
		{name: "message boundary", target: withMessage, wantDescription: "message boundary changed"},
		{name: "timer boundary", target: withTimer, wantDescription: "timer boundary changed"},
		{name: "connector type", target: otherConnector, wantDescription: "connector type changed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := w3aDiffStoredPlans(t, w3aStagePlan(map[string]dsl.StageDef{"a": plain}), w3aStagePlan(map[string]dsl.StageDef{"a": tc.target}))

			assert.Equal(t, service.ChangeTypeStructural, result.ChangeType)
			assert.Equal(t, []service.StageChange{{NodeKey: "a/T1", Description: tc.wantDescription}}, result.Changes.StageChanges)
			assert.Empty(t, result.Changes.StepChanges)
		})
	}
}

func TestVersionService_Diff_TheSameStageIDInTwoDepartments_IsTrackedPerDepartment(t *testing.T) {
	timed := dsl.StageDef{Type: "task", NodeID: "T1", Role: "reviewer",
		BoundaryTimer: &dsl.BoundaryTimer{BoundaryID: "B1", Duration: "P1D", Interrupting: true, Terminates: true}}
	plain := dsl.StageDef{Type: "task", NodeID: "T1", Role: "reviewer"}
	retimed := timed
	retimed.BoundaryTimer = &dsl.BoundaryTimer{BoundaryID: "B1", Duration: "P2D", Interrupting: true, Terminates: true}

	t.Run("unchanged stages with different boundaries do not read as changes", func(t *testing.T) {
		t.Parallel()
		plans := map[string]dsl.StageDef{"a": timed, "b": plain}

		result := w3aDiffStoredPlans(t, w3aStagePlan(plans), w3aStagePlan(plans))

		assert.Empty(t, result.Changes.StageChanges)
		assert.Equal(t, service.ChangeTypeMetadataOnly, result.ChangeType)
	})

	t.Run("a change in one department names only that department's stage", func(t *testing.T) {
		t.Parallel()

		result := w3aDiffStoredPlans(t,
			w3aStagePlan(map[string]dsl.StageDef{"a": timed, "b": plain}),
			w3aStagePlan(map[string]dsl.StageDef{"a": retimed, "b": plain}))

		assert.Equal(t, []service.StageChange{{NodeKey: "a/T1", Description: "timer boundary changed"}}, result.Changes.StageChanges)
	})

	t.Run("a change in both departments gives two distinct keys", func(t *testing.T) {
		t.Parallel()
		withMessage := plain
		withMessage.BoundaryMessage = &dsl.MessagePath{MessageName: "stop", BoundaryID: "B2", Interrupting: true, Terminates: true}

		result := w3aDiffStoredPlans(t,
			w3aStagePlan(map[string]dsl.StageDef{"a": timed, "b": plain}),
			w3aStagePlan(map[string]dsl.StageDef{"a": retimed, "b": withMessage}))

		assert.ElementsMatch(t, []service.StageChange{
			{NodeKey: "a/T1", Description: "timer boundary changed"},
			{NodeKey: "b/T1", Description: "message boundary changed"},
		}, result.Changes.StageChanges)
	})
}
