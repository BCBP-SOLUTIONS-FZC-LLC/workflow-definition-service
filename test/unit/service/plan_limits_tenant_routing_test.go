package service_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

// twoTenantLimits is the real Entitlements limit service over a reader where
// tenant A may hold one workflow template and tenant B any number. A tenant the
// reader does not know (such as uuid.Nil) reads as unlimited, so a lookup made
// for the wrong tenant lets tenant A through and fails these tests.
func twoTenantLimits(t *testing.T, tenantA, tenantB uuid.UUID) *service.EntitlementsLimitService {
	t.Helper()
	one := int64(1)
	reader := newFakeEntitlementsReader()
	reader.set(tenantA, &one, nil)
	reader.set(tenantB, nil, nil)
	svc, err := service.NewEntitlementsLimitService(service.EntitlementsLimitDeps{Reader: reader, TTL: entTTL, FreshnessBound: entBound})
	require.NoError(t, err)
	return svc
}

func countsByTenant(a, b uuid.UUID) func(context.Context, uuid.UUID) (int64, error) {
	return func(_ context.Context, tenant uuid.UUID) (int64, error) {
		if tenant == a {
			return 1, nil
		}
		return 100, nil
	}
}

func TestWorkflowService_Create_UsesTheCallersTenantLimitFromTheRealEntitlementsService(t *testing.T) {
	tenantA, tenantB := uuid.New(), uuid.New()
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		PlanLimits: twoTenantLimits(t, tenantA, tenantB), Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo,
	})
	wfRepo.EXPECT().CountByTenant(gomock.Any(), gomock.Any()).DoAndReturn(countsByTenant(tenantA, tenantB)).AnyTimes()
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	_, _, errA := svc.Create(context.Background(), tenantA, uuid.New(), "key", "name", "", "<bpmn/>", "starter")
	wfB, _, errB := svc.Create(context.Background(), tenantB, uuid.New(), "key", "name", "", "<bpmn/>", "starter")

	require.ErrorIs(t, errA, domain.ErrPlanQuotaExceeded, "tenant A is at its limit of 1")
	require.NoError(t, errB, "tenant B is unlimited, whatever tenant A's limit is")
	assert.Equal(t, tenantB, wfB.TenantID)
}

func TestVersionService_Clone_UsesTheCallersTenantLimitFromTheRealEntitlementsService(t *testing.T) {
	tenantA, tenantB := uuid.New(), uuid.New()
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		PlanLimits: twoTenantLimits(t, tenantA, tenantB), Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo,
	})
	wfID, srcID := uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), srcID).DoAndReturn(
		func(_ context.Context, tenant, id uuid.UUID) (*domain.WorkflowVersion, error) {
			return &domain.WorkflowVersion{ID: id, WorkflowID: wfID, TenantID: tenant, Status: domain.VersionStatusPublished, BPMNXML: "<bpmn/>"}, nil
		}).AnyTimes()
	wfRepo.EXPECT().CountByTenant(gomock.Any(), gomock.Any()).DoAndReturn(countsByTenant(tenantA, tenantB)).AnyTimes()
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	req := service.CloneReq{NewKey: "copy", NewName: "Copy"}

	_, _, errA := svc.Clone(context.Background(), tenantA, uuid.New(), wfID, srcID, "starter", req)
	_, _, errB := svc.Clone(context.Background(), tenantB, uuid.New(), wfID, srcID, "starter", req)

	require.ErrorIs(t, errA, domain.ErrPlanQuotaExceeded)
	require.NoError(t, errB)
}

func TestVersionService_Promote_UsesTheCallersTenantLimitFromTheRealEntitlementsService(t *testing.T) {
	tenantA, tenantB := uuid.New(), uuid.New()
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		PlanLimits: twoTenantLimits(t, tenantA, tenantB), Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo,
	})
	wfID, verID := uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), verID).DoAndReturn(
		func(_ context.Context, tenant, id uuid.UUID) (*domain.WorkflowVersion, error) {
			return &domain.WorkflowVersion{ID: id, WorkflowID: wfID, TenantID: tenant, Status: domain.VersionStatusPublished, IsValid: true}, nil
		}).AnyTimes()
	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), wfID).Return(&domain.Workflow{ID: wfID}, nil).AnyTimes()
	vRepo.EXPECT().GetDraft(gomock.Any(), gomock.Any(), wfID).Return(nil, domain.ErrNotFound).AnyTimes()
	wfRepo.EXPECT().CountByTenant(gomock.Any(), gomock.Any()).DoAndReturn(countsByTenant(tenantA, tenantB)).AnyTimes()
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantB, wfID, &verID).Return(nil).Times(1)

	_, errA := svc.Promote(context.Background(), tenantA, uuid.New(), wfID, verID, "starter")
	_, errB := svc.Promote(context.Background(), tenantB, uuid.New(), wfID, verID, "starter")

	require.ErrorIs(t, errA, domain.ErrPlanQuotaExceeded)
	require.NoError(t, errB)
}

// recordingLimits wraps any PlanLimits fake and records the tenant of each
// lookup, so a test can assert it is the caller's.
type recordingLimits struct {
	inner port.PlanLimits

	mu      sync.Mutex
	tenants []uuid.UUID
}

func (l *recordingLimits) WorkflowTemplateLimit(ctx context.Context, tenantID uuid.UUID, planCode string) (*int64, error) {
	l.mu.Lock()
	l.tenants = append(l.tenants, tenantID)
	l.mu.Unlock()
	return l.inner.WorkflowTemplateLimit(ctx, tenantID, planCode)
}

func (l *recordingLimits) assertOnly(t *testing.T, want uuid.UUID) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	require.NotEmpty(t, l.tenants, "the limit was never looked up")
	for _, got := range l.tenants {
		assert.Equal(t, want, got, "the limit must be looked up for the caller's tenant")
	}
}

func TestPlanLimitsFakes_AreAskedForTheCallersTenantOnCreateCloneAndPromote(t *testing.T) {
	fakes := map[string]func() port.PlanLimits{
		"plan table fake":     stdPlanLimits,
		"transaction fake":    func() port.PlanLimits { return &w5bLimits{} },
		"spy":                 func() port.PlanLimits { return &w3cLimitsSpy{} },
		"create failure fake": func() port.PlanLimits { return &w7dFailingLimits{} },
	}
	for name, newFake := range fakes {
		t.Run(name+"/create", func(t *testing.T) {
			tenant := uuid.New()
			limits := &recordingLimits{inner: newFake()}
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewWorkflowService(service.WorkflowDeps{PlanLimits: limits, Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo})
			wfRepo.EXPECT().CountByTenant(gomock.Any(), tenant).Return(int64(0), nil).AnyTimes()
			wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
			vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

			_, _, err := svc.Create(context.Background(), tenant, uuid.New(), "key", "name", "", "<bpmn/>", "starter")

			require.NoError(t, err)
			limits.assertOnly(t, tenant)
		})
		t.Run(name+"/clone", func(t *testing.T) {
			tenant, wfID, srcID := uuid.New(), uuid.New(), uuid.New()
			limits := &recordingLimits{inner: newFake()}
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewVersionService(service.VersionDeps{PlanLimits: limits, Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo})
			vRepo.EXPECT().GetByID(gomock.Any(), tenant, srcID).Return(
				&domain.WorkflowVersion{ID: srcID, WorkflowID: wfID, TenantID: tenant, Status: domain.VersionStatusPublished}, nil)
			wfRepo.EXPECT().CountByTenant(gomock.Any(), tenant).Return(int64(0), nil).AnyTimes()
			wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
			vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

			_, _, err := svc.Clone(context.Background(), tenant, uuid.New(), wfID, srcID, "starter", service.CloneReq{NewKey: "k", NewName: "n"})

			require.NoError(t, err)
			limits.assertOnly(t, tenant)
		})
		t.Run(name+"/promote", func(t *testing.T) {
			tenant, wfID, verID := uuid.New(), uuid.New(), uuid.New()
			limits := &recordingLimits{inner: newFake()}
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewVersionService(service.VersionDeps{PlanLimits: limits, Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo})
			vRepo.EXPECT().GetByID(gomock.Any(), tenant, verID).Return(
				&domain.WorkflowVersion{ID: verID, WorkflowID: wfID, TenantID: tenant, Status: domain.VersionStatusPublished, IsValid: true}, nil)
			wfRepo.EXPECT().GetByID(gomock.Any(), tenant, wfID).Return(&domain.Workflow{ID: wfID}, nil).AnyTimes()
			vRepo.EXPECT().GetDraft(gomock.Any(), tenant, wfID).Return(nil, domain.ErrNotFound)
			wfRepo.EXPECT().CountByTenant(gomock.Any(), tenant).Return(int64(0), nil).AnyTimes()
			wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenant, wfID, &verID).Return(nil)

			_, err := svc.Promote(context.Background(), tenant, uuid.New(), wfID, verID, "starter")

			require.NoError(t, err)
			limits.assertOnly(t, tenant)
		})
	}
}
