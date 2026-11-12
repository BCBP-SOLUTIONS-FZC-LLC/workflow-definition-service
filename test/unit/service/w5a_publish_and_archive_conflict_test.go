package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestVersionService_Publish_ADraftEditedAfterPreFlight_FailsWithTheOptimisticLockErrorAndWritesNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Versions: vRepo, Assignees: assignees, Compiler: compiler,
	})
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>", RecordVersion: 7,
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(buildPlan()), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(4), nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	var sawRecordVersion int64
	var sawVersionNumber int32
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ uuid.UUID, recordVersion int64, versionNumber int32, _, _ string, _ []domain.ModuleRef) error {
			sawRecordVersion, sawVersionNumber = recordVersion, versionNumber
			return domain.ErrDraftConcurrency
		})

	got, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)

	assert.Nil(t, got)
	require.ErrorIs(t, err, domain.ErrDraftConcurrency)
	assert.Contains(t, err.Error(), "publish version")
	assert.Equal(t, int64(7), sawRecordVersion, "the publish is conditional on the record version read before the edit")
	assert.Equal(t, int32(4), sawVersionNumber)
}

func TestModuleService_Archive_TheActiveVersionClearedBetweenTheCheckAndTheTransaction_ReturnsNoActiveVersionAndArchivesNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)
	tenantID, userID, moduleID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	gomock.InOrder(
		m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
			Return(&domain.Module{ID: moduleID, Scope: domain.ScopeTenant, ActiveVersionID: &activeID}, nil),
		m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
			Return(&domain.Module{ID: moduleID, Scope: domain.ScopeTenant, ActiveVersionID: nil}, nil),
	)

	err := svc.Archive(context.Background(), tenantID, userID, moduleID)

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNoActiveVersion)
	assert.Contains(t, err.Error(), "archive module")
	assert.False(t, errors.Is(err, domain.ErrNotFound))
}
