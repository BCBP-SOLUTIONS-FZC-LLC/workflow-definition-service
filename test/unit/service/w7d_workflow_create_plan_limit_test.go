package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type w7dFailingLimits struct {
	err   error
	plans []string
}

func (f *w7dFailingLimits) WorkflowTemplateLimit(_ context.Context, planCode string) (*int64, error) {
	f.plans = append(f.plans, planCode)
	return nil, f.err
}

type w7dBlockedPlanReader struct {
	release chan struct{}
}

func (r *w7dBlockedPlanReader) ReadPlanTable(context.Context) (map[string]*int64, error) {
	<-r.release
	return nil, errors.New("zzq released without a table")
}

func w7dNewCreateService(ctrl *gomock.Controller, limits *w7dFailingLimits) *service.WorkflowService {
	return service.NewWorkflowService(service.WorkflowDeps{
		Transactor: mocks.NewMockTransactor(ctrl),
		Workflows:  mocks.NewMockWorkflowRepository(ctrl),
		Versions:   mocks.NewMockWorkflowVersionRepository(ctrl),
		PlanLimits: limits,
	})
}

func TestWorkflowService_Create_PlanLimitSourceFails_WrapsTheCauseAndWritesNothing(t *testing.T) {
	t.Parallel()
	cause := fmt.Errorf("%w: reading the plan table: zzq connection refused", domain.ErrUpstreamUnavailable)
	limits := &w7dFailingLimits{err: cause}
	svc := w7dNewCreateService(gomock.NewController(t), limits)

	wf, version, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "w7d-key", "W7D", "", "<definitions/>", "starter")

	require.Error(t, err)
	assert.Nil(t, wf)
	assert.Nil(t, version)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.ErrorIs(t, err, cause)
	assert.ErrorContains(t, err, "resolve workflow template limit: ")
	assert.Equal(t, []string{"starter"}, limits.plans, "the limit is read once, for the plan the caller sent")
}

func TestWorkflowService_Create_PlanTableReadOutlastsTheDeadline_IsUnavailableAndWritesNothing(t *testing.T) {
	t.Parallel()
	reader := &w7dBlockedPlanReader{release: make(chan struct{})}
	t.Cleanup(func() { close(reader.release) })
	limits := service.NewPlanLimitService(service.PlanLimitDeps{Reader: reader, TTL: time.Hour})
	ctrl := gomock.NewController(t)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: mocks.NewMockTransactor(ctrl),
		Workflows:  mocks.NewMockWorkflowRepository(ctrl),
		Versions:   mocks.NewMockWorkflowVersionRepository(ctrl),
		PlanLimits: limits,
	})
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	t.Cleanup(cancel)

	wf, version, err := svc.Create(ctx, uuid.New(), uuid.New(), "w7d-key", "W7D", "", "<definitions/>", "starter")

	require.Error(t, err)
	assert.Nil(t, wf)
	assert.Nil(t, version)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorContains(t, err, "resolve workflow template limit: ")
	assert.ErrorContains(t, err, "waiting for the plan table")
}
