package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

const coreSvcBPublishRecordVersion = 7

type coreSvcBPublishWire struct {
	ctrl       *gomock.Controller
	tx         *mocks.MockTransactor
	workflows  *mocks.MockWorkflowRepository
	versions   *mocks.MockWorkflowVersionRepository
	assignees  *mocks.MockAssigneeRepository
	compiler   *mocks.MockPlanCompiler
	membership *mocks.MockMembershipService
	svc        *service.VersionService

	tenantID, userID, wfID, vID uuid.UUID
	draft                       *domain.WorkflowVersion
}

func coreSvcBNewPublishWire(t *testing.T) *coreSvcBPublishWire {
	t.Helper()
	ctrl := gomock.NewController(t)
	w := &coreSvcBPublishWire{
		ctrl:       ctrl,
		tx:         mocks.NewMockTransactor(ctrl),
		workflows:  mocks.NewMockWorkflowRepository(ctrl),
		versions:   mocks.NewMockWorkflowVersionRepository(ctrl),
		assignees:  mocks.NewMockAssigneeRepository(ctrl),
		compiler:   mocks.NewMockPlanCompiler(ctrl),
		membership: mocks.NewMockMembershipService(ctrl),
		tenantID:   uuid.New(), userID: uuid.New(), wfID: uuid.New(), vID: uuid.New(),
	}
	w.draft = &domain.WorkflowVersion{
		ID: w.vID, WorkflowID: w.wfID, TenantID: w.tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>", RecordVersion: coreSvcBPublishRecordVersion,
	}
	w.svc = service.NewVersionService(service.VersionDeps{
		Transactor: w.tx, Workflows: w.workflows, Versions: w.versions,
		Assignees: w.assignees, Membership: w.membership, Compiler: w.compiler,
	})
	return w
}

func (w *coreSvcBPublishWire) expectCompile(collab *dsl.CompiledCollaboration) {
	w.versions.EXPECT().GetByID(gomock.Any(), w.tenantID, w.vID).Return(w.draft, nil)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	w.compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collab, nil)
}

// expectPublishToCommit expects everything after a successful compile and
// returns a pointer to the plan JSON the repository was asked to store.
func (w *coreSvcBPublishWire) expectPublishToCommit() *string {
	stored := new(string)
	w.compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("hash-1", nil)
	w.workflows.EXPECT().GetByID(gomock.Any(), w.tenantID, w.wfID).Return(&domain.Workflow{ID: w.wfID, TenantID: w.tenantID}, nil)
	w.tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	w.versions.EXPECT().NextVersionNumber(gomock.Any(), w.tenantID, w.wfID).Return(int32(1), nil)
	w.versions.EXPECT().Publish(gomock.Any(), w.tenantID, w.vID, int64(coreSvcBPublishRecordVersion), int32(1),
		gomock.Any(), "hash-1", gomock.Nil()).
		DoAndReturn(func(_ context.Context, _, _ uuid.UUID, _ int64, _ int32, plan, _ string, _ []domain.ModuleRef) error {
			*stored = plan
			return nil
		})
	w.assignees.EXPECT().DeleteByVersion(gomock.Any(), w.tenantID, w.vID).Return(nil)
	w.workflows.EXPECT().UpdateActiveVersion(gomock.Any(), w.tenantID, w.wfID, &w.vID).Return(nil)
	return stored
}

func (w *coreSvcBPublishWire) publish() (*domain.WorkflowVersion, error) {
	return w.svc.Publish(context.Background(), w.tenantID, w.userID, w.wfID, w.vID, false)
}

func coreSvcBAutomaticStages(n int) []dsl.StageDef {
	stages := make([]dsl.StageDef, n)
	for i := range stages {
		stages[i] = dsl.StageDef{Type: "send_task", NodeID: "S" + uuid.NewString()}
	}
	return stages
}

func TestVersionService_Publish_ExpandedPlanAtAndOverTheStageCap(t *testing.T) {
	cases := []struct {
		name    string
		stages  int
		wantErr bool
	}{
		{"exactly at the cap is published", domain.MaxPlanStages, false},
		{"one stage over the cap is refused", domain.MaxPlanStages + 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := coreSvcBNewPublishWire(t)
			plan := &dsl.CompiledPlan{Name: "main", Departments: []dsl.DepartmentDef{
				{ID: "Ops", Label: "Ops", Stages: coreSvcBAutomaticStages(tc.stages)},
			}}
			w.expectCompile(collabOf(plan))
			if !tc.wantErr {
				stored := w.expectPublishToCommit()

				got, err := w.publish()

				require.NoError(t, err)
				assert.Equal(t, domain.VersionStatusPublished, got.Status)
				assert.NotEmpty(t, *stored)
				return
			}

			got, err := w.publish()

			require.ErrorIs(t, err, dsl.ErrPlanTooLarge)
			assert.Nil(t, got)
			assert.True(t, strings.HasPrefix(err.Error(), "compile bpmn: expand calls: "), "got %q", err.Error())
		})
	}
}

func TestVersionService_Publish_HumanStageWithoutAssignees_ServiceDoesNotRecheckTheRole(t *testing.T) {
	w := coreSvcBNewPublishWire(t)
	plan := &dsl.CompiledPlan{Name: "main", Departments: []dsl.DepartmentDef{{
		ID: "Ops", Label: "Ops", IAMDepartmentID: uuid.NewString(),
		Stages: []dsl.StageDef{{Type: "review", NodeID: "Review_1", Role: "admin"}},
	}}}
	w.expectCompile(collabOf(plan))
	stored := w.expectPublishToCommit()

	got, err := w.publish()

	require.NoError(t, err)
	assert.Equal(t, domain.VersionStatusPublished, got.Status)
	assert.Contains(t, *stored, `"admin"`, "the service stores the compiled stage as it received it")
}

func coreSvcBHistogramCount(t *testing.T, o prometheus.Observer) uint64 {
	t.Helper()
	m, ok := o.(prometheus.Metric)
	require.True(t, ok, "observer is not a metric")
	var out dto.Metric
	require.NoError(t, m.Write(&out))
	return out.GetHistogram().GetSampleCount()
}

func TestVersionService_Publish_CountsTheOutcomeAndObservesLatencyOnSuccessAndFailure(t *testing.T) {
	registerServiceMetrics(t)
	txDown := errors.New("commit failed")
	cases := []struct {
		name       string
		arrange    func(w *coreSvcBPublishWire)
		wantStatus string
		wantErr    error
	}{
		{
			name: "a publish that commits counts ok",
			arrange: func(w *coreSvcBPublishWire) {
				w.expectCompile(collabOf(buildPlan("a")))
				w.expectPublishToCommit()
			},
			wantStatus: "ok",
		},
		{
			name: "a refusal in the pre-flight counts err",
			arrange: func(w *coreSvcBPublishWire) {
				w.draft.Status = domain.VersionStatusPublished
				w.versions.EXPECT().GetByID(gomock.Any(), w.tenantID, w.vID).Return(w.draft, nil)
			},
			wantStatus: "err",
			wantErr:    domain.ErrVersionNotDraft,
		},
		{
			name: "a failure inside the transaction counts err",
			arrange: func(w *coreSvcBPublishWire) {
				w.expectCompile(collabOf(buildPlan("a")))
				w.compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("hash-1", nil)
				w.workflows.EXPECT().GetByID(gomock.Any(), w.tenantID, w.wfID).Return(&domain.Workflow{ID: w.wfID, TenantID: w.tenantID}, nil)
				w.tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
				w.versions.EXPECT().NextVersionNumber(gomock.Any(), w.tenantID, w.wfID).Return(int32(1), nil)
				w.versions.EXPECT().Publish(gomock.Any(), w.tenantID, w.vID, int64(coreSvcBPublishRecordVersion), int32(1),
					gomock.Any(), "hash-1", gomock.Nil()).Return(txDown)
			},
			wantStatus: "err",
			wantErr:    txDown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := coreSvcBNewPublishWire(t)
			tc.arrange(w)
			okBefore := testutil.ToFloat64(observability.WFPublishTotal.WithLabelValues("ok"))
			errBefore := testutil.ToFloat64(observability.WFPublishTotal.WithLabelValues("err"))
			latencyBefore := coreSvcBHistogramCount(t, observability.WFPublishLatencySeconds)

			_, err := w.publish()

			wantOK, wantErr := 0.0, 0.0
			if tc.wantStatus == "ok" {
				require.NoError(t, err)
				wantOK = 1
			} else {
				require.ErrorIs(t, err, tc.wantErr)
				wantErr = 1
			}
			assert.Equal(t, okBefore+wantOK, testutil.ToFloat64(observability.WFPublishTotal.WithLabelValues("ok")))
			assert.Equal(t, errBefore+wantErr, testutil.ToFloat64(observability.WFPublishTotal.WithLabelValues("err")))
			assert.Equal(t, latencyBefore+1, coreSvcBHistogramCount(t, observability.WFPublishLatencySeconds))
		})
	}
}
