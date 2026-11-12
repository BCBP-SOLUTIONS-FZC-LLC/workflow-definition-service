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

// txAwareLimits records, for every lookup, whether it ran inside a transaction
// of the w5bTx fake and which tenant it was asked for.
type txAwareLimits struct {
	limit    *int64
	err      error
	inTx     []bool
	tenants  []uuid.UUID
	planCode []string
}

func (l *txAwareLimits) WorkflowTemplateLimit(ctx context.Context, tenantID uuid.UUID, planCode string) (*int64, error) {
	l.inTx = append(l.inTx, ctx.Value(w5bTxKey{}) != nil)
	l.tenants = append(l.tenants, tenantID)
	l.planCode = append(l.planCode, planCode)
	return l.limit, l.err
}

// promoteRaceFixture builds a Promote where the workflow had an active version
// at the first read and none inside the transaction, as after a concurrent archive.
func promoteRaceFixture(t *testing.T, limits *txAwareLimits) (*service.VersionService, *w5bTx, *mocks.MockWorkflowRepository, *mocks.MockWorkflowVersionRepository, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctrl := gomock.NewController(t)
	tx := &w5bTx{}
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo, PlanLimits: limits,
	})
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	other := uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, IsValid: true}, nil)
	gomock.InOrder(
		wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ID: wfID, ActiveVersionID: &other}, nil),
		wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ID: wfID}, nil).AnyTimes(),
	)
	return svc, tx, wfRepo, vRepo, tenantID, wfID, vID
}

func TestVersionService_Promote_ConcurrentArchiveClearsTheActiveVersion_ResolvesTheLimitOutsideTheTransaction(t *testing.T) {
	limit := int64(5)
	limits := &txAwareLimits{limit: &limit}
	svc, tx, wfRepo, vRepo, tenantID, wfID, vID := promoteRaceFixture(t, limits)
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(nil, domain.ErrNotFound)
	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(1), nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	got, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "starter")

	require.NoError(t, err)
	assert.Equal(t, vID, got.ID)
	assert.Equal(t, []bool{false}, limits.inTx, "the limit is looked up once, and never inside a transaction")
	assert.Equal(t, []uuid.UUID{tenantID}, limits.tenants)
	assert.Equal(t, 2, tx.started, "the transaction runs again once, with the limit in hand")
	assert.Equal(t, 1, tx.commits)
	assert.Equal(t, 1, tx.rollbacks, "the first run stops before it writes anything")
}

func TestVersionService_Promote_ConcurrentArchiveAndTheLimitSourceFails_NoTransactionIsRerun(t *testing.T) {
	limits := &txAwareLimits{err: errors.New("entitlements down")}
	svc, tx, _, _, tenantID, wfID, vID := promoteRaceFixture(t, limits)

	_, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "starter")

	require.ErrorContains(t, err, "entitlements down")
	assert.Equal(t, []bool{false}, limits.inTx)
	assert.Equal(t, 1, tx.started)
}

func TestVersionService_Promote_ConcurrentArchiveAtTheLimit_IsRefusedAfterTheRerun(t *testing.T) {
	limit := int64(1)
	limits := &txAwareLimits{limit: &limit}
	svc, _, wfRepo, vRepo, tenantID, wfID, vID := promoteRaceFixture(t, limits)
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(nil, domain.ErrNotFound)
	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(1), nil)

	_, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "starter")

	require.ErrorIs(t, err, domain.ErrPlanQuotaExceeded)
	assert.Equal(t, []bool{false}, limits.inTx)
}

func TestVersionService_Promote_NoActiveVersionAtTheFirstRead_ResolvesTheLimitOnceBeforeTheTransaction(t *testing.T) {
	limit := int64(5)
	limits := &txAwareLimits{limit: &limit}
	ctrl := gomock.NewController(t)
	tx := &w5bTx{}
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo, PlanLimits: limits,
	})
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, IsValid: true}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ID: wfID}, nil).Times(2)
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(nil, domain.ErrNotFound)
	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(1), nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	_, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "starter")

	require.NoError(t, err)
	assert.Equal(t, []bool{false}, limits.inTx)
	assert.Equal(t, 1, tx.started)
}
