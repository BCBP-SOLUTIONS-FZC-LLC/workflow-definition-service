package handler_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

// tenantRecordingLimits wraps a PlanLimits fake and records the tenant of every
// lookup so a test can assert it is the caller's tenant.
type tenantRecordingLimits struct {
	inner port.PlanLimits

	mu      sync.Mutex
	tenants []uuid.UUID
}

func (l *tenantRecordingLimits) WorkflowTemplateLimit(ctx context.Context, tenantID uuid.UUID, planCode string) (*int64, error) {
	l.mu.Lock()
	l.tenants = append(l.tenants, tenantID)
	l.mu.Unlock()
	return l.inner.WorkflowTemplateLimit(ctx, tenantID, planCode)
}

func (l *tenantRecordingLimits) assertOnly(t *testing.T, want uuid.UUID) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	require.NotEmpty(t, l.tenants, "the limit was never looked up")
	for _, got := range l.tenants {
		assert.Equal(t, want, got, "the limit must be looked up for the caller's tenant")
	}
}

func TestPlanLimitFakes_AreAskedForTheCallersTenantThroughTheHandlers(t *testing.T) {
	five := int64(5)
	fakes := map[string]func() port.PlanLimits{
		"plan table fake": func() port.PlanLimits { return w6dPlanLimits{"starter": &five} },
		"version fake":    func() port.PlanLimits { return &w7bLimits{limit: &five} },
	}
	for name, newFake := range fakes {
		t.Run(name+"/create", func(t *testing.T) {
			limits := &tenantRecordingLimits{inner: newFake()}
			ctrl := gomock.NewController(t)
			tx, wfRepo, vRepo := mocks.NewMockTransactor(ctrl), mocks.NewMockWorkflowRepository(ctrl), mocks.NewMockWorkflowVersionRepository(ctrl)
			tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
			wfRepo.EXPECT().CountByTenant(gomock.Any(), testTenantID).Return(int64(0), nil)
			wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
			vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
			svc := service.NewWorkflowService(service.WorkflowDeps{PlanLimits: limits, Transactor: tx, Workflows: wfRepo, Versions: vRepo})
			r := adminReq(http.MethodPost, "/api/v1/workflows", map[string]any{"key": "k", "name": "n", "bpmn_xml": "<definitions/>"})
			r.Header.Set("x-plan", "starter")

			w := do(newRouter(handler.New(handler.Services{Workflows: svc})), r)

			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			limits.assertOnly(t, testTenantID)
		})
		t.Run(name+"/clone", func(t *testing.T) {
			limits := &tenantRecordingLimits{inner: newFake()}
			ctrl := gomock.NewController(t)
			tx, wfRepo, vRepo := mocks.NewMockTransactor(ctrl), mocks.NewMockWorkflowRepository(ctrl), mocks.NewMockWorkflowVersionRepository(ctrl)
			tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
			vRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(newPublishedVersion(), nil)
			wfRepo.EXPECT().CountByTenant(gomock.Any(), testTenantID).Return(int64(0), nil)
			wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
			vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
			svc := service.NewVersionService(service.VersionDeps{PlanLimits: limits, Transactor: tx, Workflows: wfRepo, Versions: vRepo})
			r := adminReq(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/versions/"+testVerID.String()+"/clone",
				map[string]string{"new_key": "copy", "new_name": "Copy"})
			r.Header.Set("x-plan", "starter")

			w := do(newRouter(handler.New(handler.Services{Versions: svc})), r)

			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			limits.assertOnly(t, testTenantID)
		})
		t.Run(name+"/promote", func(t *testing.T) {
			limits := &tenantRecordingLimits{inner: newFake()}
			ctrl := gomock.NewController(t)
			tx, wfRepo, vRepo := mocks.NewMockTransactor(ctrl), mocks.NewMockWorkflowRepository(ctrl), mocks.NewMockWorkflowVersionRepository(ctrl)
			tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
			vRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(newPublishedVersion(), nil).AnyTimes()
			wfRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(&domain.Workflow{ID: testWFID}, nil).AnyTimes()
			vRepo.EXPECT().GetDraft(gomock.Any(), testTenantID, testWFID).Return(nil, domain.ErrNotFound)
			wfRepo.EXPECT().CountByTenant(gomock.Any(), testTenantID).Return(int64(0), nil)
			wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), testTenantID, testWFID, gomock.Any()).Return(nil)
			svc := service.NewVersionService(service.VersionDeps{PlanLimits: limits, Transactor: tx, Workflows: wfRepo, Versions: vRepo})
			r := adminReq(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/versions/"+testVerID.String()+"/promote", nil)
			r.Header.Set("x-plan", "starter")

			w := do(newRouter(handler.New(handler.Services{Versions: svc})), r)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			limits.assertOnly(t, testTenantID)
		})
	}
}
