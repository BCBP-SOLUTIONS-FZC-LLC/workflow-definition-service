//go:build integration

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type wfCreateAttempt struct {
	key string
	err error
}

type overlapWorkflowRepo struct {
	port.WorkflowRepository
	callers atomic.Int32
	both    chan struct{}
}

func (r *overlapWorkflowRepo) CountByTenant(ctx context.Context, tenantID uuid.UUID) (int64, error) {
	count, err := r.WorkflowRepository.CountByTenant(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	switch n := r.callers.Add(1); {
	case n == 2:
		close(r.both)
	case n < 2:
		select {
		case <-r.both:
		case <-time.After(30 * time.Second):
		}
	}
	return count, nil
}

func (r *overlapWorkflowRepo) bothTransactionsHadCounted() bool {
	select {
	case <-r.both:
		return true
	default:
		return false
	}
}

func wireOverlappingWorkflowService(
	t *testing.T, pool *pgcommon.Pool,
) (*service.WorkflowService, *overlapWorkflowRepo) {
	t.Helper()
	repo := &overlapWorkflowRepo{
		WorkflowRepository: pgadapter.NewWorkflowRepo(pool),
		both:               make(chan struct{}),
	}
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: pgadapter.NewTransactor(pool),
		Workflows:  repo,
		Versions:   pgadapter.NewWorkflowVersionRepo(pool),
		Compiler:   bpmn_compiler.New(),
		PlanLimits: service.NewPlanLimitService(service.PlanLimitDeps{Reader: staticPlanTable{}, TTL: time.Hour}),
	})
	return svc, repo
}

func wfCreateTogether(svc *service.WorkflowService, tenantID uuid.UUID, keys ...string) []wfCreateAttempt {
	results := make([]wfCreateAttempt, len(keys))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, key := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := svc.Create(context.Background(), tenantID, uuid.New(), key, "Concurrent", "", minimalBPMN, "starter")
			results[i] = wfCreateAttempt{key: key, err: err}
		}()
	}
	close(start)
	wg.Wait()
	return results
}

func TestE2E_CreateWorkflow_TwoSimultaneousCreatesOfOneKey_OneWinsOneIsADuplicate(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	svc, overlap := wireOverlappingWorkflowService(t, pool)
	tenantID := uuid.New()

	results := wfCreateTogether(svc, tenantID, "same-key", "same-key")

	require.True(t, overlap.bothTransactionsHadCounted(), "both creates were inside their transactions together")
	created, duplicates := 0, 0
	for _, r := range results {
		switch {
		case r.err == nil:
			created++
		case errors.Is(r.err, domain.ErrDuplicateTemplateKey):
			duplicates++
		default:
			t.Errorf("unexpected error: %v", r.err)
		}
	}
	assert.Equal(t, 1, created)
	assert.Equal(t, 1, duplicates)

	count, err := pgadapter.NewWorkflowRepo(pool).CountByTenant(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "exactly one workflow exists afterwards")
	_, err = pgadapter.NewWorkflowRepo(pool).GetByTemplateKey(context.Background(), tenantID, "same-key")
	require.NoError(t, err)
}

func TestE2E_CreateWorkflow_TwoSimultaneousCreatesOneBelowTheLimit_NeverExceedTheLimit(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	svcs := wireServices(t, pool, nil)
	svc, overlap := wireOverlappingWorkflowService(t, pool)
	tenantID := uuid.New()
	for i := range 4 {
		_, _, err := svcs.workflow.Create(context.Background(), tenantID, uuid.New(), fmt.Sprintf("seed-%d", i), "Seed", "", minimalBPMN, "starter")
		require.NoError(t, err)
	}

	results := wfCreateTogether(svc, tenantID, "race-a", "race-b")

	require.True(t, overlap.bothTransactionsHadCounted(), "both creates counted the workflows before either wrote")
	created, refused := 0, 0
	for _, r := range results {
		switch {
		case r.err == nil:
			created++
		case errors.Is(r.err, domain.ErrPlanQuotaExceeded):
			refused++
		default:
			t.Errorf("unexpected error for %s: %v", r.key, r.err)
		}
	}
	assert.Equal(t, 1, created, "only one of the two fits under the limit")
	assert.Equal(t, 1, refused, "the other is refused with the quota error")

	count, err := pgadapter.NewWorkflowRepo(pool).CountByTenant(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, int64(5), count, "the tenant ends at the limit, not above it")
}

func TestE2E_CreateWorkflow_AnArchivedWorkflowKeepsItsKeyReserved(t *testing.T) {
	ctx := context.Background()
	svcs := wireServices(t, fixtures.NewTestPool(t), nil)
	tenantID, userID := uuid.New(), uuid.New()

	wf, draft, err := svcs.workflow.Create(ctx, tenantID, userID, "archived-key", "Archived", "", minimalBPMN, "starter")
	require.NoError(t, err)
	published, err := svcs.version.Publish(ctx, tenantID, userID, wf.ID, draft.ID, false)
	require.NoError(t, err)
	_, err = svcs.version.Promote(ctx, tenantID, userID, wf.ID, published.ID, "enterprise")
	require.NoError(t, err)
	require.NoError(t, svcs.workflow.Archive(ctx, tenantID, userID, wf.ID))

	_, _, err = svcs.workflow.Create(ctx, tenantID, userID, "archived-key", "Reuse", "", minimalBPMN, "starter")

	assert.ErrorIs(t, err, domain.ErrDuplicateTemplateKey, "archiving does not release the key")
	_, _, err = svcs.workflow.Create(ctx, uuid.New(), userID, "archived-key", "Other tenant", "", minimalBPMN, "starter")
	assert.NoError(t, err, "another tenant may still use the key")
}
