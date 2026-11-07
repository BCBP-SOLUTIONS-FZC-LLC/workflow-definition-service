package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

type w3cLimitsSpy struct{ calls int }

func (l *w3cLimitsSpy) WorkflowTemplateLimit(context.Context, string) (*int64, error) {
	l.calls++
	return nil, nil
}

func w3cPublishes(label string) float64 {
	return testutil.ToFloat64(observability.WFPublishTotal.WithLabelValues(label))
}

func w3cClones(label string) float64 {
	return testutil.ToFloat64(observability.WFCloneTotal.WithLabelValues(label))
}

func w3cPromotes(label string) float64 {
	return testutil.ToFloat64(observability.WFPromoteTotal.WithLabelValues(label))
}

func TestVersionService_Publish_WorkflowLookupFailure_IsWrappedAndSkipsEverythingAfter(t *testing.T) {
	w3cRegisterMetrics(t)
	cause := errors.New("workflow read down")
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()

	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(buildPlan("a")), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	var gotTenant, gotWF uuid.UUID
	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tid, id uuid.UUID) (*domain.Workflow, error) {
			gotTenant, gotWF = tid, id
			return nil, cause
		})
	svc := service.NewVersionService(service.VersionDeps{
		Workflows: wfRepo, Versions: vRepo, Compiler: compiler,
		Membership: mocks.NewMockMembershipService(ctrl),
		Transactor: mocks.NewMockTransactor(ctrl),
		Assignees:  mocks.NewMockAssigneeRepository(ctrl),
	})
	watch := w3cWatch(w3cPublishes)

	got, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)

	require.ErrorIs(t, err, cause)
	assert.EqualError(t, err, "get workflow: workflow read down")
	assert.Nil(t, got)
	assert.Equal(t, tenantID, gotTenant)
	assert.Equal(t, wfID, gotWF)
	assert.Equal(t, domain.VersionStatusDraft, draft.Status, "the draft is not marked published")
	watch.assertDelta(t, 0, 1)
}

func TestVersionService_Clone_SourceLookupFailure_IsWrappedAndNothingElseRuns(t *testing.T) {
	w3cRegisterMetrics(t)
	cases := []struct {
		name  string
		cause error
	}{
		{name: "source not found", cause: domain.ErrNotFound},
		{name: "store failure", cause: errors.New("connection reset")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
			ctrl := gomock.NewController(t)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			var gotTenant, gotVersion uuid.UUID
			vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, tid, id uuid.UUID) (*domain.WorkflowVersion, error) {
					gotTenant, gotVersion = tid, id
					return nil, tc.cause
				})
			limits := &w3cLimitsSpy{}
			svc := service.NewVersionService(service.VersionDeps{
				Versions: vRepo, PlanLimits: limits,
				Workflows:  mocks.NewMockWorkflowRepository(ctrl),
				Transactor: mocks.NewMockTransactor(ctrl),
			})
			watch := w3cWatch(w3cClones)

			wf, v, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise",
				service.CloneReq{NewKey: "copy", NewName: "Copy"})

			require.ErrorIs(t, err, tc.cause)
			assert.EqualError(t, err, "get source version: "+tc.cause.Error())
			assert.Nil(t, wf)
			assert.Nil(t, v)
			assert.Zero(t, limits.calls, "no plan limit lookup after a failed source read")
			assert.Equal(t, tenantID, gotTenant)
			assert.Equal(t, vID, gotVersion)
			watch.assertDelta(t, 0, 1)
		})
	}
}

func TestVersionService_Promote_LookupFailures_AreWrappedAndTheActiveVersionIsUntouched(t *testing.T) {
	w3cRegisterMetrics(t)
	cases := []struct {
		name         string
		versionErr   error
		workflowErr  error
		txWorkflow   error
		wantMessage  string
		wantCause    error
		wantUsesTx   bool
		wantWFLookup int
	}{
		{
			name: "version load fails", versionErr: errors.New("version read down"),
			wantMessage: "get version: version read down",
		},
		{
			name: "workflow load fails before the transaction", workflowErr: errors.New("workflow read down"),
			wantMessage: "get workflow: workflow read down", wantWFLookup: 1,
		},
		{
			name: "workflow load fails inside the transaction", txWorkflow: errors.New("tx workflow read down"),
			wantMessage: "promote version: get workflow: tx workflow read down", wantUsesTx: true, wantWFLookup: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			var tx *mocks.MockTransactor
			if tc.wantUsesTx {
				tx = txPassthrough(ctrl)
			} else {
				tx = mocks.NewMockTransactor(ctrl)
			}

			var versionTenant, versionID uuid.UUID
			vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, tid, id uuid.UUID) (*domain.WorkflowVersion, error) {
					versionTenant, versionID = tid, id
					if tc.versionErr != nil {
						return nil, tc.versionErr
					}
					return &domain.WorkflowVersion{
						ID: vID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusPublished, IsValid: true,
					}, nil
				})
			var lookups int
			var lookupTenants, lookupIDs []uuid.UUID
			if tc.wantWFLookup > 0 {
				wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, tid, id uuid.UUID) (*domain.Workflow, error) {
						lookups++
						lookupTenants, lookupIDs = append(lookupTenants, tid), append(lookupIDs, id)
						if tc.workflowErr != nil {
							return nil, tc.workflowErr
						}
						if lookups == 2 {
							return nil, tc.txWorkflow
						}
						return &domain.Workflow{ID: wfID, TenantID: tenantID, ActiveVersionID: &activeID}, nil
					}).Times(tc.wantWFLookup)
			}
			svc := service.NewVersionService(service.VersionDeps{Transactor: tx, Workflows: wfRepo, Versions: vRepo})
			watch := w3cWatch(w3cPromotes)

			got, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise")

			require.Error(t, err)
			assert.EqualError(t, err, tc.wantMessage)
			for _, cause := range []error{tc.versionErr, tc.workflowErr, tc.txWorkflow} {
				if cause != nil {
					assert.ErrorIs(t, err, cause)
				}
			}
			assert.Nil(t, got)
			assert.Equal(t, tenantID, versionTenant)
			assert.Equal(t, vID, versionID)
			assert.Equal(t, tc.wantWFLookup, lookups)
			for i := range lookupTenants {
				assert.Equal(t, tenantID, lookupTenants[i])
				assert.Equal(t, wfID, lookupIDs[i])
			}
			watch.assertDelta(t, 0, 1)
		})
	}
}

func TestVersionService_Export_LookupFailures_AreWrappedAndReturnNoXML(t *testing.T) {
	cases := []struct {
		name        string
		versionErr  error
		workflowErr error
		wantMessage string
	}{
		{name: "version load fails", versionErr: errors.New("version read down"), wantMessage: "get version: version read down"},
		{name: "version missing", versionErr: domain.ErrNotFound, wantMessage: "get version: " + domain.ErrNotFound.Error()},
		{name: "workflow load fails", workflowErr: errors.New("workflow read down"), wantMessage: "get workflow: workflow read down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).DoAndReturn(
				func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
					if tc.versionErr != nil {
						return nil, tc.versionErr
					}
					return &domain.WorkflowVersion{ID: vID, WorkflowID: wfID, BPMNXML: "<secret/>"}, nil
				})
			if tc.workflowErr != nil {
				wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(nil, tc.workflowErr)
			}
			svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

			xml, filename, err := svc.Export(context.Background(), tenantID, wfID, vID)

			assert.EqualError(t, err, tc.wantMessage)
			if tc.versionErr != nil {
				assert.ErrorIs(t, err, tc.versionErr)
			} else {
				assert.ErrorIs(t, err, tc.workflowErr)
			}
			assert.Empty(t, xml)
			assert.Empty(t, filename)
		})
	}
}

func TestVersionService_Diff_PlanResolutionFailure_NamesTheSideAndTheStep(t *testing.T) {
	cause := errors.New("compiler down")
	cases := []struct {
		name        string
		failOnBase  bool
		failAtCalls bool
		wantMessage string
	}{
		{name: "base module listing fails", failOnBase: true, failAtCalls: true, wantMessage: "resolve base plan: list module calls: compiler down"},
		{name: "base compile fails", failOnBase: true, wantMessage: "resolve base plan: compile bpmn: compiler down"},
		{name: "target module listing fails", failAtCalls: true, wantMessage: "resolve target plan: list module calls: compiler down"},
		{name: "target compile fails", wantMessage: "resolve target plan: compile bpmn: compiler down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			ctrl := gomock.NewController(t)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
				&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: "<base/>"}, nil)
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
				&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: "<target/>"}, nil)

			failing, other := "<target/>", "<base/>"
			if tc.failOnBase {
				failing, other = other, failing
			}
			expectSide := func(xml string, fail bool) {
				if fail && tc.failAtCalls {
					compiler.EXPECT().ModuleCalls(gomock.Any(), xml).Return(nil, nil, cause)
					return
				}
				compiler.EXPECT().ModuleCalls(gomock.Any(), xml).Return(nil, nil, nil)
				compiler.EXPECT().BundleModules(xml, gomock.Any()).Return(xml, nil)
				if fail {
					compiler.EXPECT().CompileDraft(gomock.Any(), xml).Return(nil, cause)
					return
				}
				compiler.EXPECT().CompileDraft(gomock.Any(), xml).Return(collabOf(buildPlan()), nil)
			}
			if tc.failOnBase {
				expectSide(failing, true)
			} else {
				expectSide(other, false)
				expectSide(failing, true)
			}
			svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

			got, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)

			require.ErrorIs(t, err, cause)
			assert.EqualError(t, err, tc.wantMessage)
			assert.Nil(t, got)
		})
	}
}

func TestVersionService_Nodes_StoredPlanThatDoesNotExpand_NamesTheVersionAndTheCause(t *testing.T) {
	twoPlansSameName, err := json.Marshal(dsl.CompiledCollaboration{
		MainPlan: "main", SchemaVersion: dsl.CurrentSchemaVersion,
		Plans: []*dsl.CompiledPlan{{Name: "main"}, {Name: "main"}},
	})
	require.NoError(t, err)

	cases := []struct {
		name        string
		stored      string
		wantAfterID string
	}{
		{
			name:        "call expansion fails",
			stored:      string(twoPlansSameName),
			wantAfterID: ": expand calls: ",
		},
		{
			name:        "the main plan is not among the plans",
			stored:      `{"main_plan":"x","plans":[]}`,
			wantAfterID: ": main plan \"x\" not among the compiled plans",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := int32(1)
			v := &domain.WorkflowVersion{
				ID: uuid.New(), WorkflowID: uuid.New(), TenantID: uuid.New(),
				Status: domain.VersionStatusPublished, VersionNumber: &n, CompiledPlanJSON: &tc.stored,
			}

			got, err := nodesService(t, v).Nodes(context.Background(), v.TenantID, v.WorkflowID, v.ID)

			require.Error(t, err)
			assert.Nil(t, got)
			assert.True(t, strings.HasPrefix(err.Error(), "version "+v.ID.String()+tc.wantAfterID), "message = %q", err.Error())
			for _, domainErr := range []error{
				domain.ErrVersionNotPublished, domain.ErrNotFound, domain.ErrMalformedBPMN, domain.ErrUpstreamUnavailable,
			} {
				assert.NotErrorIs(t, err, domainErr, "a stored plan that does not expand must not read as a client-visible condition")
			}
		})
	}
}
