package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func requirePlanQuotaError(t *testing.T, err error, plan string, limit, count int64) {
	t.Helper()
	if !errors.Is(err, domain.ErrPlanQuotaExceeded) {
		t.Fatalf("err = %v, want ErrPlanQuotaExceeded", err)
	}
	var quota *domain.PlanQuotaError
	if !errors.As(err, &quota) {
		t.Fatalf("err = %v, want a *domain.PlanQuotaError", err)
	}
	if quota.Plan != plan || quota.Limit != limit || quota.Count != count {
		t.Errorf("quota error = {%q %d %d}, want {%q %d %d}", quota.Plan, quota.Limit, quota.Count, plan, limit, count)
	}
}

func TestWorkflowService_Create_QuotaRefusalCarriesPlanLimitAndCount(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl), Workflows: wfRepo})
	wfRepo.EXPECT().CountByTenant(gomock.Any(), gomock.Any()).Return(int64(7), nil)

	_, _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "key", "name", "", "<bpmn/>", "starter")

	requirePlanQuotaError(t, err, "starter", 5, 7)
}

func TestWorkflowService_Create_QuotaRefusalOfAFrozenPlanReportsALimitOfZero(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		PlanLimits: staticLimits{"frozen": limitOf(0)}, Transactor: txPassthrough(ctrl), Workflows: wfRepo,
	})
	wfRepo.EXPECT().CountByTenant(gomock.Any(), gomock.Any()).Return(int64(0), nil)

	_, _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "key", "name", "", "<bpmn/>", "frozen")

	requirePlanQuotaError(t, err, "frozen", 0, 0)
}

func TestVersionService_Clone_QuotaRefusalCarriesPlanLimitAndCount(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	svc := service.NewVersionService(service.VersionDeps{PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo})
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(5), nil)

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "starter", service.CloneReq{NewKey: "k", NewName: "n"})

	requirePlanQuotaError(t, err, "starter", 5, 5)
}

func TestVersionService_Promote_QuotaRefusalCarriesPlanLimitAndCount(t *testing.T) {
	svc, tx, wfRepo, vRepo, tenantID, wfID, vID := promoteReactivationFixture(t)
	promoteInTxRead(wfRepo, tenantID, wfID, &domain.Workflow{ID: wfID})
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(runTx)
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(nil, domain.ErrNotFound)
	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(6), nil)

	_, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "starter")

	requirePlanQuotaError(t, err, "starter", 5, 6)
}

func TestWorkflowService_Create_QuotaRefusalNamesThePlanInTheFormTheLimitIsLookedUpBy(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		PlanLimits: staticLimits{" Starter ": limitOf(5)}, Transactor: txPassthrough(ctrl), Workflows: wfRepo,
	})
	wfRepo.EXPECT().CountByTenant(gomock.Any(), gomock.Any()).Return(int64(5), nil)

	_, _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "key", "name", "", "<bpmn/>", " Starter ")

	requirePlanQuotaError(t, err, "starter", 5, 5)
}
