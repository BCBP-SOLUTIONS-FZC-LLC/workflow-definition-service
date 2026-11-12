package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func w7cSeq(names ...string) *dsl.ExecutionStep {
	return &dsl.ExecutionStep{Sequential: names}
}

func w7cStepsPlan(steps ...dsl.ExecutionStep) *dsl.CompiledPlan {
	return &dsl.CompiledPlan{Name: "test", Execution: dsl.ExecutionPlan{Steps: steps}}
}

func TestVersionService_Diff_StepChanges_AreOnePerDifferingPositionWithTheirBeforeAndAfter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		base   []dsl.ExecutionStep
		target []dsl.ExecutionStep
		want   []service.StepChange
	}{
		{
			name:   "a step changed in place",
			base:   []dsl.ExecutionStep{*w7cSeq("a"), *w7cSeq("b")},
			target: []dsl.ExecutionStep{*w7cSeq("a"), *w7cSeq("c")},
			want:   []service.StepChange{{Description: "step 2 changed", Before: w7cSeq("b"), After: w7cSeq("c")}},
		},
		{
			name:   "current behaviour: two steps swapped read as a change at each position",
			base:   []dsl.ExecutionStep{*w7cSeq("a"), *w7cSeq("b")},
			target: []dsl.ExecutionStep{*w7cSeq("b"), *w7cSeq("a")},
			want: []service.StepChange{
				{Description: "step 1 changed", Before: w7cSeq("a"), After: w7cSeq("b")},
				{Description: "step 2 changed", Before: w7cSeq("b"), After: w7cSeq("a")},
			},
		},
		{
			name:   "current behaviour: two added steps are each described as changed",
			base:   []dsl.ExecutionStep{*w7cSeq("a")},
			target: []dsl.ExecutionStep{*w7cSeq("a"), *w7cSeq("b"), *w7cSeq("c")},
			want: []service.StepChange{
				{Description: "step 2 changed", After: w7cSeq("b")},
				{Description: "step 3 changed", After: w7cSeq("c")},
			},
		},
		{
			name:   "current behaviour: two removed steps are each described as changed",
			base:   []dsl.ExecutionStep{*w7cSeq("a"), *w7cSeq("b"), *w7cSeq("c")},
			target: []dsl.ExecutionStep{*w7cSeq("a")},
			want: []service.StepChange{
				{Description: "step 2 changed", Before: w7cSeq("b")},
				{Description: "step 3 changed", Before: w7cSeq("c")},
			},
		},
		{
			name:   "only the differing positions are listed, in order",
			base:   []dsl.ExecutionStep{*w7cSeq("a"), *w7cSeq("b"), *w7cSeq("c")},
			target: []dsl.ExecutionStep{*w7cSeq("x"), *w7cSeq("b"), *w7cSeq("z")},
			want: []service.StepChange{
				{Description: "step 1 changed", Before: w7cSeq("a"), After: w7cSeq("x")},
				{Description: "step 3 changed", Before: w7cSeq("c"), After: w7cSeq("z")},
			},
		},
		{
			name:   "a step that keeps its names but gains one",
			base:   []dsl.ExecutionStep{*w7cSeq("a")},
			target: []dsl.ExecutionStep{*w7cSeq("a", "b")},
			want:   []service.StepChange{{Description: "step 1 changed", Before: w7cSeq("a"), After: w7cSeq("a", "b")}},
		},
		{
			name:   "a step that gains an extra",
			base:   []dsl.ExecutionStep{*w7cSeq("a")},
			target: []dsl.ExecutionStep{{Sequential: []string{"a"}, Extras: map[string]string{"zzq_key": "v"}}},
			want: []service.StepChange{{
				Description: "step 1 changed",
				Before:      w7cSeq("a"),
				After:       &dsl.ExecutionStep{Sequential: []string{"a"}, Extras: map[string]string{"zzq_key": "v"}},
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := w3aDiffStoredPlans(t, w7cStepsPlan(tc.base...), w7cStepsPlan(tc.target...))

			assert.Equal(t, service.ChangeTypeStructural, result.ChangeType)
			assert.False(t, result.Changes.MetadataOnly)
			assert.Equal(t, tc.want, result.Changes.StepChanges)
			assert.Empty(t, result.Changes.StageChanges)
			assert.Empty(t, result.Changes.AddedDepartments)
			assert.Empty(t, result.Changes.RemovedDepartments)
		})
	}
}

func TestVersionService_Diff_StepsThatAreTheSameAtEveryPosition_AreNotChanges(t *testing.T) {
	t.Parallel()
	steps := []dsl.ExecutionStep{*w7cSeq("a"), *w7cSeq("b", "c")}

	result := w3aDiffStoredPlans(t, w7cStepsPlan(steps...), w7cStepsPlan(*w7cSeq("a"), *w7cSeq("b", "c")))

	assert.Empty(t, result.Changes.StepChanges)
	assert.Equal(t, service.ChangeTypeMetadataOnly, result.ChangeType)
	assert.True(t, result.Changes.MetadataOnly)
}

func w7cDept(id string, stages ...dsl.StageDef) dsl.DepartmentDef {
	return dsl.DepartmentDef{ID: id, Label: id, Stages: stages}
}

func w7cDeptPlan(depts ...dsl.DepartmentDef) *dsl.CompiledPlan {
	return &dsl.CompiledPlan{Name: "test", Departments: depts}
}

func TestVersionService_Diff_StagesAreMatchedAcrossVersionsByDepartmentAndStage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		base            *dsl.CompiledPlan
		target          *dsl.CompiledPlan
		wantStructural  bool
		wantAddedDepts  []string
		wantRemovedDept []string
		wantStages      []service.StageChange
	}{
		{
			name:           "no departments in the base: every stage of the target is added",
			base:           w7cDeptPlan(),
			target:         w7cDeptPlan(w7cDept("a", taskStage("T1")), w7cDept("b", taskStage("T2"))),
			wantStructural: true,
			wantAddedDepts: []string{"a", "b"},
			wantStages: []service.StageChange{
				{NodeKey: "a/T1", Description: "stage added"},
				{NodeKey: "b/T2", Description: "stage added"},
			},
		},
		{
			name:            "no departments in the target: every stage of the base is removed",
			base:            w7cDeptPlan(w7cDept("a", taskStage("T1")), w7cDept("b", taskStage("T2"))),
			target:          w7cDeptPlan(),
			wantStructural:  true,
			wantRemovedDept: []string{"a", "b"},
			wantStages: []service.StageChange{
				{NodeKey: "a/T1", Description: "stage removed"},
				{NodeKey: "b/T2", Description: "stage removed"},
			},
		},
		{
			name:   "no departments in either plan: nothing changed",
			base:   w7cDeptPlan(),
			target: w7cDeptPlan(),
		},
		{
			name:   "several shared departments with the same stages: nothing changed",
			base:   w7cDeptPlan(w7cDept("a", taskStage("T1")), w7cDept("b", taskStage("T2"), taskStage("T3"))),
			target: w7cDeptPlan(w7cDept("b", taskStage("T2"), taskStage("T3")), w7cDept("a", taskStage("T1"))),
		},
		{
			name:           "a stage added in one of several shared departments",
			base:           w7cDeptPlan(w7cDept("a", taskStage("T1")), w7cDept("b", taskStage("T2"))),
			target:         w7cDeptPlan(w7cDept("a", taskStage("T1")), w7cDept("b", taskStage("T2"), taskStage("T9"))),
			wantStructural: true,
			wantStages:     []service.StageChange{{NodeKey: "b/T9", Description: "stage added"}},
		},
		{
			name:            "a department dropped together with its stages while another is kept",
			base:            w7cDeptPlan(w7cDept("a", taskStage("T1")), w7cDept("b", taskStage("T2"))),
			target:          w7cDeptPlan(w7cDept("a", taskStage("T1"))),
			wantStructural:  true,
			wantRemovedDept: []string{"b"},
			wantStages:      []service.StageChange{{NodeKey: "b/T2", Description: "stage removed"}},
		},
		{
			name:           "a stage moved to another department is removed from one and added to the other",
			base:           w7cDeptPlan(w7cDept("a", taskStage("T1")), w7cDept("b")),
			target:         w7cDeptPlan(w7cDept("a"), w7cDept("b", taskStage("T1"))),
			wantStructural: true,
			wantStages: []service.StageChange{
				{NodeKey: "b/T1", Description: "stage added"},
				{NodeKey: "a/T1", Description: "stage removed"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := w3aDiffStoredPlans(t, tc.base, tc.target)

			if tc.wantStructural {
				assert.Equal(t, service.ChangeTypeStructural, result.ChangeType)
				assert.False(t, result.Changes.MetadataOnly)
			} else {
				assert.Equal(t, service.ChangeTypeMetadataOnly, result.ChangeType)
				assert.True(t, result.Changes.MetadataOnly)
			}
			assert.Equal(t, tc.wantAddedDepts, result.Changes.AddedDepartments)
			assert.Equal(t, tc.wantRemovedDept, result.Changes.RemovedDepartments)
			assert.Equal(t, tc.wantStages, result.Changes.StageChanges)
			assert.Empty(t, result.Changes.StepChanges)
		})
	}
}

const w7cNotXML = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn:process id="Process_Open">`

func TestVersionService_Diff_AVersionWithNoUsablePlanAndUnparseableXML_IsAMalformedDocument(t *testing.T) {
	t.Parallel()
	unusable := "zzq not json"
	empty := ""
	storedPlan := marshalledPlan(t, buildPlan("a"))
	cases := []struct {
		name     string
		base     func(id, wf, tenant uuid.UUID) *domain.WorkflowVersion
		target   func(id, wf, tenant uuid.UUID) *domain.WorkflowVersion
		wantSide string
	}{
		{
			name: "draft base",
			base: func(id, wf, tenant uuid.UUID) *domain.WorkflowVersion { return w7cDraft(id, wf, tenant, w7cNotXML) },
			target: func(id, wf, tenant uuid.UUID) *domain.WorkflowVersion {
				return w7cDraft(id, wf, tenant, libraryCallerBPMN)
			},
			wantSide: "resolve base plan",
		},
		{
			name: "published base whose stored plan cannot be read",
			base: func(id, wf, tenant uuid.UUID) *domain.WorkflowVersion {
				v := w7cPublished(id, wf, tenant, w7cNotXML)
				v.CompiledPlanJSON = &unusable
				return v
			},
			target: func(id, wf, tenant uuid.UUID) *domain.WorkflowVersion {
				return w7cDraft(id, wf, tenant, libraryCallerBPMN)
			},
			wantSide: "resolve base plan",
		},
		{
			name: "published base whose stored plan is empty",
			base: func(id, wf, tenant uuid.UUID) *domain.WorkflowVersion {
				v := w7cPublished(id, wf, tenant, w7cNotXML)
				v.CompiledPlanJSON = &empty
				return v
			},
			target: func(id, wf, tenant uuid.UUID) *domain.WorkflowVersion {
				return w7cDraft(id, wf, tenant, libraryCallerBPMN)
			},
			wantSide: "resolve base plan",
		},
		{
			name: "draft target",
			base: func(id, wf, tenant uuid.UUID) *domain.WorkflowVersion {
				v := w7cPublished(id, wf, tenant, "<unused/>")
				v.CompiledPlanJSON = storedPlan
				return v
			},
			target:   func(id, wf, tenant uuid.UUID) *domain.WorkflowVersion { return w7cDraft(id, wf, tenant, w7cNotXML) },
			wantSide: "resolve target plan",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			modules := mocks.NewMockModuleVersionRepository(ctrl)
			svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: bpmn_compiler.New(), ModuleVersions: modules})
			tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(tc.base(baseID, wfID, tenantID), nil)
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(tc.target(targetID, wfID, tenantID), nil)

			result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)

			require.Error(t, err)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, domain.ErrMalformedBPMN)
			assert.False(t, errors.Is(err, domain.ErrNotFound), "an unparseable document is not a missing version")
			assert.Contains(t, err.Error(), tc.wantSide)
		})
	}
}

func w7cDraft(id, wf, tenant uuid.UUID, xml string) *domain.WorkflowVersion {
	return &domain.WorkflowVersion{ID: id, WorkflowID: wf, TenantID: tenant, Status: domain.VersionStatusDraft, BPMNXML: xml}
}

func w7cPublished(id, wf, tenant uuid.UUID, xml string) *domain.WorkflowVersion {
	n := int32(1)
	return &domain.WorkflowVersion{
		ID: id, WorkflowID: wf, TenantID: tenant, Status: domain.VersionStatusPublished, VersionNumber: &n, BPMNXML: xml,
	}
}
