package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

type w3bWire struct {
	tx        *mocks.MockTransactor
	workflows *mocks.MockWorkflowRepository
	versions  *mocks.MockWorkflowVersionRepository
	assignees *mocks.MockAssigneeRepository
	compiler  *mocks.MockPlanCompiler
	svc       *service.VersionService

	tenantID, wfID, vID uuid.UUID
}

func w3bNewWire(t *testing.T) *w3bWire {
	t.Helper()
	ctrl := gomock.NewController(t)
	w := &w3bWire{
		tx:        mocks.NewMockTransactor(ctrl),
		workflows: mocks.NewMockWorkflowRepository(ctrl),
		versions:  mocks.NewMockWorkflowVersionRepository(ctrl),
		assignees: mocks.NewMockAssigneeRepository(ctrl),
		compiler:  mocks.NewMockPlanCompiler(ctrl),
		tenantID:  uuid.New(), wfID: uuid.New(), vID: uuid.New(),
	}
	w.svc = service.NewVersionService(service.VersionDeps{
		Transactor: w.tx, Workflows: w.workflows, Versions: w.versions,
		Assignees: w.assignees, Compiler: w.compiler,
	})
	return w
}

func (w *w3bWire) draft(xml string) *domain.WorkflowVersion {
	return &domain.WorkflowVersion{
		ID: w.vID, WorkflowID: w.wfID, TenantID: w.tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: xml, RecordVersion: 3,
	}
}

func (w *w3bWire) expectDraftBundled() {
	w.versions.EXPECT().GetByID(gomock.Any(), w.tenantID, w.vID).Return(w.draft("<bpmn/>"), nil)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
}

func (w *w3bWire) expectCompiledAndHashed(plan *dsl.CompiledPlan) {
	w.expectDraftBundled()
	w.compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(plan), nil)
	w.compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
}

func (w *w3bWire) publish(force bool) (*domain.WorkflowVersion, error) {
	return w.svc.Publish(context.Background(), w.tenantID, uuid.New(), w.wfID, w.vID, force)
}

func (w *w3bWire) expectCommit(assigneesExpected func()) {
	w.versions.EXPECT().NextVersionNumber(gomock.Any(), w.tenantID, w.wfID).Return(int32(2), nil)
	w.tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	w.versions.EXPECT().Publish(gomock.Any(), w.tenantID, w.vID, int64(3), int32(2), gomock.Any(), "h", gomock.Any()).Return(nil)
	w.assignees.EXPECT().DeleteByVersion(gomock.Any(), w.tenantID, w.vID).Return(nil)
	if assigneesExpected != nil {
		assigneesExpected()
	}
	w.workflows.EXPECT().UpdateActiveVersion(gomock.Any(), w.tenantID, w.wfID, &w.vID).Return(nil)
}

func w3bActiveBaseline(w *w3bWire) uuid.UUID {
	activeID := uuid.New()
	w.workflows.EXPECT().GetByID(gomock.Any(), w.tenantID, w.wfID).Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
	return activeID
}

func TestVersionService_Publish_CompilerFailureIsWrappedAndNothingIsWritten(t *testing.T) {
	w := w3bNewWire(t)
	cause := errors.New("compiler rejected the model")
	w.expectDraftBundled()
	w.compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(nil, cause)

	got, err := w.publish(false)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorIs(t, err, cause)
	assert.True(t, strings.HasPrefix(err.Error(), "compile bpmn: "), "err = %q", err)
}

func TestVersionService_Publish_MainPlanNamingNoPlanIsReportedAsACompileFailure(t *testing.T) {
	w := w3bNewWire(t)
	collab := collabOf(buildPlan("a"))
	collab.MainPlan = "absent"
	w.expectDraftBundled()
	w.compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collab, nil)

	got, err := w.publish(false)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, strings.HasPrefix(err.Error(), "compile bpmn: "), "err = %q", err)
	assert.Contains(t, err.Error(), `main plan "absent" not among the compiled plans`)
}

func TestVersionService_Publish_HashFailureIsWrappedAndStopsBeforeTheWorkflowLookup(t *testing.T) {
	w := w3bNewWire(t)
	cause := errors.New("hash backend down")
	w.expectDraftBundled()
	w.compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(buildPlan("a")), nil)
	w.compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("", cause)

	got, err := w.publish(false)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorIs(t, err, cause)
	assert.True(t, strings.HasPrefix(err.Error(), "hash bpmn: "), "err = %q", err)
}

func TestVersionService_Publish_ActiveVersionThatIsMalformedBPMN_ForceDecidesTheOutcome(t *testing.T) {
	cases := []struct {
		name  string
		force bool
	}{
		{name: "without force the publish is refused as divergent", force: false},
		{name: "with force the publish goes ahead", force: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := w3bNewWire(t)
			w.expectCompiledAndHashed(buildPlan("a"))
			activeID := w3bActiveBaseline(w)
			w.versions.EXPECT().GetByID(gomock.Any(), w.tenantID, activeID).Return(&domain.WorkflowVersion{
				ID: activeID, WorkflowID: w.wfID, TenantID: w.tenantID,
				Status: domain.VersionStatusPublished, BPMNXML: "<active/>",
			}, nil)
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<active/>").Return(nil, nil, domain.ErrMalformedBPMN)
			if tc.force {
				w.expectCommit(nil)
			}

			got, err := w.publish(tc.force)

			if tc.force {
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, domain.VersionStatusPublished, got.Status)
				return
			}
			assert.Nil(t, got)
			require.ErrorIs(t, err, domain.ErrStructuralDivergence)
			assert.Contains(t, err.Error(), "active version no longer compiles")
		})
	}
}

func TestVersionService_Publish_ActiveVersionThatCannotBeCompiledForAnotherReason_IsNeverDivergentEvenWithForce(t *testing.T) {
	compilerDown := errors.New("compiler unreachable")
	cases := []struct {
		name   string
		expect func(w *w3bWire)
	}{
		{
			name: "module listing fails",
			expect: func(w *w3bWire) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<active/>").Return(nil, nil, compilerDown)
			},
		},
		{
			name: "compiling the stored model fails",
			expect: func(w *w3bWire) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<active/>").Return(nil, nil, nil)
				w.compiler.EXPECT().BundleModules("<active/>", gomock.Any()).Return("<active/>", nil)
				w.compiler.EXPECT().CompileDraft(gomock.Any(), "<active/>").Return(nil, compilerDown)
			},
		},
	}
	for _, tc := range cases {
		for _, force := range []bool{false, true} {
			name := tc.name + "/force=false"
			if force {
				name = tc.name + "/force=true"
			}
			t.Run(name, func(t *testing.T) {
				w := w3bNewWire(t)
				w.expectCompiledAndHashed(buildPlan("a"))
				activeID := w3bActiveBaseline(w)
				w.versions.EXPECT().GetByID(gomock.Any(), w.tenantID, activeID).Return(&domain.WorkflowVersion{
					ID: activeID, WorkflowID: w.wfID, TenantID: w.tenantID,
					Status: domain.VersionStatusPublished, BPMNXML: "<active/>",
				}, nil)
				tc.expect(w)

				got, err := w.publish(force)

				assert.Nil(t, got)
				require.Error(t, err)
				assert.ErrorIs(t, err, compilerDown)
				assert.NotErrorIs(t, err, domain.ErrStructuralDivergence)
				assert.Contains(t, err.Error(), "compile active version for divergence check")
			})
		}
	}
}

func TestVersionService_Publish_ActiveVersionLoadFailureIsWrapped(t *testing.T) {
	w := w3bNewWire(t)
	cause := errors.New("active row unreadable")
	w.expectCompiledAndHashed(buildPlan("a"))
	activeID := w3bActiveBaseline(w)
	w.versions.EXPECT().GetByID(gomock.Any(), w.tenantID, activeID).Return(nil, cause)

	got, err := w.publish(true)

	assert.Nil(t, got)
	require.Error(t, err)
	assert.ErrorIs(t, err, cause)
	assert.Contains(t, err.Error(), "get active version for divergence check")
}

func w3bPublishWithAssignees(t *testing.T, plan *dsl.CompiledPlan, onBulkInsert func([]*domain.NodeAssignee)) {
	t.Helper()
	w := w3bNewWire(t)
	w.expectCompiledAndHashed(plan)
	w.workflows.EXPECT().GetByID(gomock.Any(), w.tenantID, w.wfID).Return(&domain.Workflow{}, nil)
	w.expectCommit(func() {
		if onBulkInsert == nil {
			return
		}
		w.assignees.EXPECT().BulkInsert(gomock.Any(), w.tenantID, w.vID, gomock.Any()).DoAndReturn(
			func(_ context.Context, _, _ uuid.UUID, rows []*domain.NodeAssignee) error {
				onBulkInsert(rows)
				return nil
			})
	})

	got, err := w.publish(false)

	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestVersionService_Publish_PlanWithOnlyAutomaticStagesStoresNoAssignees(t *testing.T) {
	plan := &dsl.CompiledPlan{
		Name: "test",
		Departments: []dsl.DepartmentDef{{
			ID: "ops", IAMDepartmentID: uuid.New().String(),
			Stages: []dsl.StageDef{
				{NodeID: "s1", Type: "send_task", Role: "reviewer", DefaultAssignees: []string{uuid.New().String()}},
				{NodeID: "s2", Type: "receive_task", Role: "reviewer", DefaultAssignees: []string{uuid.New().String()}},
				{NodeID: "s3", Type: "review", ConnectorType: "rest", Role: "reviewer", DefaultAssignees: []string{uuid.New().String()}},
			},
		}},
	}

	w3bPublishWithAssignees(t, plan, nil)
}

func w3bStoredPlanJSON(raw string) *string { return &raw }

func w3bMarshalCollab(t *testing.T, collab *dsl.CompiledCollaboration) *string {
	t.Helper()
	b, err := json.Marshal(collab)
	require.NoError(t, err)
	return w3bStoredPlanJSON(string(b))
}

func TestVersionService_Diff_StoredPlanThatCannotBeUsedIsRecompiledFromTheBPMN(t *testing.T) {
	storedElsewhere := marshalledPlan(t, buildPlan("stored-only"))
	missingMain := collabOf(buildPlan("stored-only"))
	missingMain.MainPlan = "absent"
	missingMainJSON := w3bMarshalCollab(t, missingMain)

	cases := []struct {
		name   string
		stored *string
	}{
		{name: "empty string", stored: w3bStoredPlanJSON("")},
		{name: "valid JSON with no plans", stored: w3bStoredPlanJSON("{}")},
		{name: "valid JSON whose main plan names no plan", stored: missingMainJSON},
		{name: "a stored plan is used when it is usable", stored: storedElsewhere},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := w3bNewWire(t)
			baseID, targetID := uuid.New(), uuid.New()
			w.versions.EXPECT().GetByID(gomock.Any(), w.tenantID, baseID).Return(
				&domain.WorkflowVersion{ID: baseID, WorkflowID: w.wfID, TenantID: w.tenantID, BPMNXML: "<base/>", CompiledPlanJSON: tc.stored}, nil)
			w.versions.EXPECT().GetByID(gomock.Any(), w.tenantID, targetID).Return(
				&domain.WorkflowVersion{ID: targetID, WorkflowID: w.wfID, TenantID: w.tenantID, BPMNXML: "<target/>", CompiledPlanJSON: tc.stored}, nil)
			usable := tc.stored == storedElsewhere
			if !usable {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<base/>").Return(nil, nil, nil)
				w.compiler.EXPECT().BundleModules("<base/>", gomock.Any()).Return("<base/>", nil)
				w.compiler.EXPECT().CompileDraft(gomock.Any(), "<base/>").Return(collabOf(buildPlan("compiled-base")), nil)
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<target/>").Return(nil, nil, nil)
				w.compiler.EXPECT().BundleModules("<target/>", gomock.Any()).Return("<target/>", nil)
				w.compiler.EXPECT().CompileDraft(gomock.Any(), "<target/>").Return(collabOf(buildPlan("compiled-target")), nil)
			}

			res, err := w.svc.Diff(context.Background(), w.tenantID, w.wfID, baseID, targetID)

			require.NoError(t, err)
			if usable {
				assert.Equal(t, service.ChangeTypeMetadataOnly, res.ChangeType)
				return
			}
			assert.Equal(t, service.ChangeTypeStructural, res.ChangeType)
			assert.Equal(t, []string{"compiled-target"}, res.Changes.AddedDepartments)
			assert.Equal(t, []string{"compiled-base"}, res.Changes.RemovedDepartments)
		})
	}
}

func TestVersionService_Clone_QuotaBoundaryAroundTheLimit(t *testing.T) {
	cases := []struct {
		name    string
		count   int64
		refused bool
	}{
		{name: "one below the limit is allowed", count: 4, refused: false},
		{name: "at the limit is refused", count: 5, refused: true},
		{name: "one above the limit is refused", count: 6, refused: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewVersionService(service.VersionDeps{
				PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo,
			})
			tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
				&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, BPMNXML: "<source/>"}, nil)
			wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(tc.count, nil)
			var createdWF *domain.Workflow
			var createdV *domain.WorkflowVersion
			if !tc.refused {
				wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, wf *domain.Workflow) error { createdWF = wf; return nil })
				vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, v *domain.WorkflowVersion) error { createdV = v; return nil })
			}

			newWF, newV, err := svc.Clone(context.Background(), tenantID, userID, wfID, vID, "starter",
				service.CloneReq{NewKey: "k", NewName: "n"})

			if tc.refused {
				require.ErrorIs(t, err, domain.ErrPlanQuotaExceeded)
				assert.Nil(t, newWF)
				assert.Nil(t, newV)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, createdWF)
			require.NotNil(t, createdV)
			assert.Same(t, createdWF, newWF)
			assert.Same(t, createdV, newV)
			assert.Equal(t, tenantID, createdWF.TenantID)
			assert.Equal(t, userID, createdWF.CreatedByUserID)
			assert.Equal(t, "k", createdWF.TemplateKey)
			assert.Equal(t, createdWF.ID, createdV.WorkflowID)
			assert.NotEqual(t, wfID, createdV.WorkflowID)
			assert.Equal(t, tenantID, createdV.TenantID)
			assert.Equal(t, userID, createdV.CreatedByUserID)
			assert.Equal(t, domain.VersionStatusDraft, createdV.Status)
			assert.Equal(t, "<source/>", createdV.BPMNXML)
		})
	}
}

func TestVersionService_Diff_VersionOfAnotherWorkflowIsNotFoundBeforeAnythingIsCompiled(t *testing.T) {
	cases := []struct {
		name                   string
		baseOther, targetOther bool
	}{
		{name: "base belongs elsewhere", baseOther: true},
		{name: "target belongs elsewhere", targetOther: true},
		{name: "both belong elsewhere", baseOther: true, targetOther: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := w3bNewWire(t)
			baseID, targetID := uuid.New(), uuid.New()
			baseWF, targetWF := w.wfID, w.wfID
			if tc.baseOther {
				baseWF = uuid.New()
			}
			if tc.targetOther {
				targetWF = uuid.New()
			}
			w.versions.EXPECT().GetByID(gomock.Any(), w.tenantID, baseID).Return(
				&domain.WorkflowVersion{ID: baseID, WorkflowID: baseWF, TenantID: w.tenantID, BPMNXML: "<base/>"}, nil)
			w.versions.EXPECT().GetByID(gomock.Any(), w.tenantID, targetID).Return(
				&domain.WorkflowVersion{ID: targetID, WorkflowID: targetWF, TenantID: w.tenantID, BPMNXML: "<target/>"}, nil)

			res, err := w.svc.Diff(context.Background(), w.tenantID, w.wfID, baseID, targetID)

			assert.Nil(t, res)
			assert.ErrorIs(t, err, domain.ErrNotFound)
		})
	}
}

func TestVersionService_List_RepositoryFailureIsWrappedAndReturnsNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})
	tenantID, wfID := uuid.New(), uuid.New()
	cause := errors.New("list query failed")
	vRepo.EXPECT().ListByWorkflow(gomock.Any(), tenantID, wfID, 2, 15).
		Return([]*domain.WorkflowVersion{{ID: uuid.New()}}, int64(9), cause)

	got, count, err := svc.List(context.Background(), tenantID, wfID, 2, 15)

	assert.Nil(t, got)
	assert.Zero(t, count)
	require.Error(t, err)
	assert.ErrorIs(t, err, cause)
	assert.True(t, strings.HasPrefix(err.Error(), "list versions: "), "err = %q", err)
}

func TestVersionService_Publish_DraftLookupFailureIsWrappedAndNothingIsCompiled(t *testing.T) {
	cases := []struct {
		name  string
		cause error
	}{
		{name: "database error", cause: errors.New("db down")},
		{name: "unknown version id", cause: domain.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := w3bNewWire(t)
			w.versions.EXPECT().GetByID(gomock.Any(), w.tenantID, w.vID).Return(nil, tc.cause)

			got, err := w.publish(false)

			assert.Nil(t, got)
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.cause)
			assert.True(t, strings.HasPrefix(err.Error(), "get draft version: "), "err = %q", err)
		})
	}
}

func TestVersionService_Publish_ModuleResolutionFailureIsReturnedWithoutFurtherWrapping(t *testing.T) {
	cause := errors.New("module registry down")
	cases := []struct {
		name   string
		expect func(w *w3bWire)
		want   string
	}{
		{
			name: "module listing fails",
			expect: func(w *w3bWire) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, cause)
			},
			want: "list module calls: " + cause.Error(),
		},
		{
			name: "bundling fails",
			expect: func(w *w3bWire) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
				w.compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("", cause)
			},
			want: "bundle modules: " + cause.Error(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := w3bNewWire(t)
			w.versions.EXPECT().GetByID(gomock.Any(), w.tenantID, w.vID).Return(w.draft("<bpmn/>"), nil)
			tc.expect(w)

			got, err := w.publish(false)

			assert.Nil(t, got)
			require.Error(t, err)
			assert.ErrorIs(t, err, cause)
			var invalid *domain.ValidationFailedError
			assert.False(t, errors.As(err, &invalid))
			assert.Equal(t, tc.want, err.Error())
		})
	}
}
