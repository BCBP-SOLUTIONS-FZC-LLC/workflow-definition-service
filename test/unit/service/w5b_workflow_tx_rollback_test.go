package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestWorkflowService_Archive_ActivePointerUpdateFails_RollsBackKeepsTheCacheAndLogsNoArchive(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	tx := &w5bTx{}
	log := &w5bLog{}
	workflows := mocks.NewMockWorkflowRepository(ctrl)
	versions := mocks.NewMockWorkflowVersionRepository(ctrl)
	cache := mocks.NewMockCacheStore(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: tx, Workflows: workflows, Versions: versions, Cache: cache, Log: log,
	})
	tenantID, userID, wfID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cause := errors.New("pointer write refused")

	workflows.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ID: wfID, ActiveVersionID: &activeID}, nil).Times(2)
	versions.EXPECT().Archive(gomock.Any(), tenantID, activeID).
		DoAndReturn(func(ctx context.Context, _, _ uuid.UUID) error {
			tx.write(ctx, "version archived")
			return nil
		})
	workflows.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, (*uuid.UUID)(nil)).Return(cause)

	err := svc.Archive(context.Background(), tenantID, userID, wfID)

	require.Error(t, err)
	assert.ErrorIs(t, err, cause)
	assert.True(t, strings.HasPrefix(err.Error(), "archive workflow: "), "err = %q", err)
	assert.Equal(t, 1, tx.retryStart)
	assert.Equal(t, 1, tx.rollbacks, "the failure leaves the transaction through its error")
	assert.Zero(t, tx.commits)
	assert.Empty(t, tx.persisted(), "the archived version row is rolled back so the version stays published")
	assert.NotContains(t, log.infos, "workflow archived")
}

func TestWorkflowService_Create_VersionInsertFails_RollsBackTheWorkflowRowAndWrapsTheError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	tx := &w5bTx{}
	log := &w5bLog{}
	workflows := mocks.NewMockWorkflowRepository(ctrl)
	versions := mocks.NewMockWorkflowVersionRepository(ctrl)
	limits := &w5bLimits{}
	svc := service.NewWorkflowService(service.WorkflowDeps{
		PlanLimits: limits, Transactor: tx, Workflows: workflows, Versions: versions, Log: log,
	})
	tenantID, userID := uuid.New(), uuid.New()
	cause := errors.New("version insert refused")

	var createdWF *domain.Workflow
	workflows.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, w *domain.Workflow) error {
			createdWF = w
			tx.write(ctx, "workflow row")
			return nil
		})
	var createdVersion *domain.WorkflowVersion
	versions.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, v *domain.WorkflowVersion) error {
			createdVersion = v
			return cause
		})

	gotWF, gotVersion, err := svc.Create(context.Background(), tenantID, userID, "invoice", "Invoice", "", "<bpmn/>", "enterprise")

	require.Error(t, err)
	assert.Nil(t, gotWF)
	assert.Nil(t, gotVersion)
	assert.ErrorIs(t, err, cause)
	assert.True(t, strings.HasPrefix(err.Error(), "create workflow: "), "err = %q", err)
	require.NotNil(t, createdWF)
	require.NotNil(t, createdVersion)
	assert.Equal(t, tenantID, createdWF.TenantID)
	assert.Equal(t, "invoice", createdWF.TemplateKey)
	assert.Equal(t, userID, createdWF.CreatedByUserID)
	assert.Equal(t, tenantID, createdVersion.TenantID)
	assert.Equal(t, userID, createdVersion.CreatedByUserID)
	assert.Equal(t, []string{"enterprise"}, limits.asked, "the caller's plan code is what is looked up")
	assert.Equal(t, createdWF.ID, createdVersion.WorkflowID)
	assert.Equal(t, 1, tx.retryStart)
	assert.Equal(t, 1, tx.rollbacks, "the failing insert leaves the transaction through its error")
	assert.Zero(t, tx.commits)
	assert.Empty(t, tx.persisted(), "no workflow row remains")
	assert.NotContains(t, log.infos, "workflow created")
}

func TestWorkflowService_Create_PlanLimitLookupFails_IsReportedAsGivenAndNothingIsPersisted(t *testing.T) {
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
			deps := service.WorkflowDeps{
				Transactor: tx,
				Workflows:  mocks.NewMockWorkflowRepository(ctrl),
				Versions:   mocks.NewMockWorkflowVersionRepository(ctrl),
			}
			if !tc.noSource {
				deps.PlanLimits = tc.limits
			}
			svc := service.NewWorkflowService(deps)

			gotWF, gotVersion, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "key", "name", "", "<bpmn/>", "gold")

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
			assert.Zero(t, tx.started, "no transaction is opened and the strict repositories see no call")
		})
	}
}
