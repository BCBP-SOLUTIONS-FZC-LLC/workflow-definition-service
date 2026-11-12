package service_test

import (
	"context"
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

type w5bLimits struct {
	limit *int64
	err   error
	asked []string
}

func (l *w5bLimits) WorkflowTemplateLimit(_ context.Context, planCode string) (*int64, error) {
	l.asked = append(l.asked, planCode)
	return l.limit, l.err
}

func w5bPlanWithAssignee() *dsl.CompiledPlan {
	return &dsl.CompiledPlan{
		Name: "w5b",
		Departments: []dsl.DepartmentDef{{
			ID:              "ops",
			IAMDepartmentID: uuid.New().String(),
			Stages: []dsl.StageDef{{
				Type:             "approve",
				Role:             "approver",
				DefaultAssignees: []string{uuid.Must(uuid.NewV7()).String()},
			}},
		}},
	}
}

func w5bCollab(plan *dsl.CompiledPlan) *dsl.CompiledCollaboration {
	return &dsl.CompiledCollaboration{
		MainPlan:      plan.Name,
		Plans:         []*dsl.CompiledPlan{plan},
		SchemaVersion: dsl.CurrentSchemaVersion,
	}
}

func TestVersionService_Publish_AStepOfTheTransactionFails_RollsBackAndTheVersionStaysDraft(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		failAt     string
		wantPrefix string
	}{
		{name: "the next version number cannot be read", failAt: "next", wantPrefix: "publish version: next version number: "},
		{name: "the version row cannot be marked published", failAt: "publish", wantPrefix: "publish version: "},
		{name: "the old assignee rows cannot be deleted", failAt: "delete", wantPrefix: "publish version: "},
		{name: "the new assignee rows cannot be inserted", failAt: "bulk", wantPrefix: "publish version: "},
		{name: "the workflow's active version cannot be moved", failAt: "active", wantPrefix: "publish version: "},
	}
	order := []string{"next", "publish", "delete", "bulk", "active"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			tx := &w5bTx{}
			workflows := mocks.NewMockWorkflowRepository(ctrl)
			versions := mocks.NewMockWorkflowVersionRepository(ctrl)
			assignees := mocks.NewMockAssigneeRepository(ctrl)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			svc := service.NewVersionService(service.VersionDeps{
				Transactor: tx, Workflows: workflows, Versions: versions, Assignees: assignees, Compiler: compiler,
			})
			tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			cause := errors.New("storage refused the " + tc.failAt + " step")
			draft := &domain.WorkflowVersion{
				ID: vID, WorkflowID: wfID, TenantID: tenantID,
				Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>", RecordVersion: 3,
			}

			versions.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
			compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
			compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(w5bCollab(w5bPlanWithAssignee()), nil)
			compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
			workflows.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ID: wfID}, nil)

			outcome := func(ctx context.Context, step string) error {
				if step == tc.failAt {
					return cause
				}
				tx.write(ctx, step)
				return nil
			}
			for _, step := range order {
				switch step {
				case "next":
					versions.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).
						DoAndReturn(func(ctx context.Context, _, _ uuid.UUID) (int32, error) {
							if err := outcome(ctx, "next"); err != nil {
								return 0, err
							}
							return 7, nil
						})
				case "publish":
					versions.EXPECT().Publish(gomock.Any(), tenantID, vID, int64(3), int32(7), gomock.Any(), "h", gomock.Any()).
						DoAndReturn(func(ctx context.Context, _, _ uuid.UUID, _ int64, _ int32, _, _ string, _ []domain.ModuleRef) error {
							return outcome(ctx, "publish")
						})
				case "delete":
					assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).
						DoAndReturn(func(ctx context.Context, _, _ uuid.UUID) error { return outcome(ctx, "delete") })
				case "bulk":
					assignees.EXPECT().BulkInsert(gomock.Any(), tenantID, vID, gomock.Any()).
						DoAndReturn(func(ctx context.Context, _, _ uuid.UUID, _ []*domain.NodeAssignee) error {
							return outcome(ctx, "bulk")
						})
				case "active":
					workflows.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).
						DoAndReturn(func(ctx context.Context, _, _ uuid.UUID, _ *uuid.UUID) error {
							return outcome(ctx, "active")
						})
				}
				if step == tc.failAt {
					break
				}
			}

			got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false)

			require.Error(t, err)
			assert.Nil(t, got)
			assert.ErrorIs(t, err, cause)
			assert.True(t, strings.HasPrefix(err.Error(), tc.wantPrefix), "err = %q", err)
			assert.Equal(t, 1, tx.retryStart, "the steps run in one retried transaction")
			assert.Equal(t, 1, tx.rollbacks, "the failing step leaves the transaction through its error")
			assert.Zero(t, tx.commits)
			if tc.failAt != "next" {
				assert.Empty(t, tx.persisted(), "nothing the earlier steps wrote survives")
			}
			assert.Equal(t, domain.VersionStatusDraft, draft.Status)
			assert.Nil(t, draft.VersionNumber)
		})
	}
}

func TestVersionService_Clone_AWriteOfTheTransactionFails_RollsBackAndKeepsTheCause(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		workflowErr  error
		versionErr   error
		wantVersions bool
		wrote        bool
	}{
		{
			name:        "the workflow insert collides with an existing template key",
			workflowErr: errors.Join(errors.New("insert workflow"), domain.ErrDuplicateTemplateKey),
		},
		{
			name:         "the version insert fails after the workflow row was written",
			versionErr:   errors.New("version insert refused"),
			wantVersions: true,
			wrote:        true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			tx := &w5bTx{}
			workflows := mocks.NewMockWorkflowRepository(ctrl)
			versions := mocks.NewMockWorkflowVersionRepository(ctrl)
			limits := &w5bLimits{}
			svc := service.NewVersionService(service.VersionDeps{
				PlanLimits: limits, Transactor: tx, Workflows: workflows, Versions: versions,
			})
			tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

			versions.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
				&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, BPMNXML: "<bpmn/>"}, nil)
			var createdWF *domain.Workflow
			workflows.EXPECT().Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, w *domain.Workflow) error {
					createdWF = w
					if tc.workflowErr != nil {
						return tc.workflowErr
					}
					tx.write(ctx, "workflow row")
					return nil
				})
			var createdVersion *domain.WorkflowVersion
			if tc.wantVersions {
				versions.EXPECT().Create(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, v *domain.WorkflowVersion) error {
						createdVersion = v
						return tc.versionErr
					})
			}

			gotWF, gotVersion, err := svc.Clone(context.Background(), tenantID, userID, wfID, vID, "enterprise",
				service.CloneReq{NewKey: "copy-key", NewName: "Copy"})

			require.Error(t, err)
			assert.Nil(t, gotWF)
			assert.Nil(t, gotVersion)
			assert.True(t, strings.HasPrefix(err.Error(), "clone version: "), "err = %q", err)
			if tc.workflowErr != nil {
				assert.ErrorIs(t, err, domain.ErrDuplicateTemplateKey)
			}
			if tc.versionErr != nil {
				assert.ErrorIs(t, err, tc.versionErr)
			}
			require.NotNil(t, createdWF)
			assert.Equal(t, tenantID, createdWF.TenantID)
			assert.Equal(t, "copy-key", createdWF.TemplateKey)
			assert.Equal(t, userID, createdWF.CreatedByUserID)
			assert.Equal(t, []string{"enterprise"}, limits.asked, "the caller's plan code is what is looked up")
			if tc.wantVersions {
				require.NotNil(t, createdVersion)
				assert.Equal(t, tenantID, createdVersion.TenantID)
				assert.Equal(t, userID, createdVersion.CreatedByUserID)
				assert.Equal(t, createdWF.ID, createdVersion.WorkflowID)
				assert.Equal(t, domain.VersionStatusDraft, createdVersion.Status)
			}
			assert.Equal(t, 1, tx.retryStart)
			assert.Equal(t, 1, tx.rollbacks, "the failing write leaves the transaction through its error")
			assert.Zero(t, tx.commits)
			if tc.wrote {
				assert.Empty(t, tx.persisted(), "the workflow row written first is rolled back with the rest")
			}
		})
	}
}

func TestVersionService_Clone_PlanLimitLookupFails_IsWrappedAndNothingIsWritten(t *testing.T) {
	t.Parallel()

	cause := errors.New("plan table unreachable")
	cases := []struct {
		name        string
		limits      *w5bLimits
		noSource    bool
		wantCause   error
		wantPrefix  string
		wantContain string
		wantUnavail bool
	}{
		{
			name:        "no limit source is configured",
			noSource:    true,
			wantContain: "no plan limit source is configured",
			wantUnavail: true,
		},
		{
			name:       "the limit source fails with an arbitrary error",
			limits:     &w5bLimits{err: cause},
			wantCause:  cause,
			wantPrefix: "resolve workflow template limit: ",
		},
		{
			name:        "the limit source reports an unknown plan as unavailable",
			limits:      &w5bLimits{err: domain.ErrUpstreamUnavailable},
			wantPrefix:  "resolve workflow template limit: ",
			wantUnavail: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			tx := &w5bTx{}
			versions := mocks.NewMockWorkflowVersionRepository(ctrl)
			deps := service.VersionDeps{
				Transactor: tx, Workflows: mocks.NewMockWorkflowRepository(ctrl), Versions: versions,
			}
			if !tc.noSource {
				deps.PlanLimits = tc.limits
			}
			svc := service.NewVersionService(deps)
			tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
			versions.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
				&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, BPMNXML: "<bpmn/>"}, nil)

			gotWF, gotVersion, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "gold",
				service.CloneReq{NewKey: "k", NewName: "n"})

			require.Error(t, err)
			assert.Nil(t, gotWF)
			assert.Nil(t, gotVersion)
			assert.Equal(t, tc.wantUnavail, errors.Is(err, domain.ErrUpstreamUnavailable))
			if tc.wantCause != nil {
				assert.ErrorIs(t, err, tc.wantCause)
			}
			if tc.wantPrefix != "" {
				assert.True(t, strings.HasPrefix(err.Error(), tc.wantPrefix), "err = %q", err)
			}
			if tc.wantContain != "" {
				assert.Contains(t, err.Error(), tc.wantContain)
			}
			if tc.limits != nil {
				assert.Equal(t, []string{"gold"}, tc.limits.asked, "the caller's plan code is what is looked up")
			}
			assert.Zero(t, tx.started, "no transaction is opened when the limit cannot be resolved")
		})
	}
}
