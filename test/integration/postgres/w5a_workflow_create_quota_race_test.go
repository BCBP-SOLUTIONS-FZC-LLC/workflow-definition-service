//go:build integration

package postgres_test

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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type w5aLimits map[string]int64

func (l w5aLimits) WorkflowTemplateLimit(_ context.Context, planCode string) (*int64, error) {
	limit, ok := l[planCode]
	if !ok {
		return nil, domain.ErrUpstreamUnavailable
	}
	return &limit, nil
}

type w5aRendezvousWorkflows struct {
	port.WorkflowRepository
	calls   atomic.Int32
	arrived chan struct{}
	release chan struct{}
}

func (r *w5aRendezvousWorkflows) CountByTenant(ctx context.Context, tenantID uuid.UUID) (int64, error) {
	count, err := r.WorkflowRepository.CountByTenant(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	if r.calls.Add(1) > 2 {
		return count, nil
	}
	r.arrived <- struct{}{}
	select {
	case <-r.release:
		return count, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func TestWorkflowService_Create_TwoConcurrentCreatesOneBelowTheLimit_OneCommitsAndTheOtherIsRefusedAfterARetry(t *testing.T) {
	pool := newTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	realWorkflows := postgres.NewWorkflowRepo(pool)
	rendezvous := &w5aRendezvousWorkflows{
		WorkflowRepository: realWorkflows,
		arrived:            make(chan struct{}, 2),
		release:            make(chan struct{}),
	}
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: postgres.NewTransactor(pool),
		Workflows:  rendezvous,
		Versions:   postgres.NewWorkflowVersionRepo(pool),
		PlanLimits: w5aLimits{"capped": 2},
	})
	tenantID := uuid.New()
	publishRaceSeedDraft(t, pool, tenantID, "seed", "<definitions/>")

	type result struct {
		wf  *domain.Workflow
		err error
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wf, _, err := svc.Create(ctx, tenantID, uuid.New(), fmt.Sprintf("race-%d", i), "Race", "", "<definitions/>", "capped")
			results[i] = result{wf, err}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	for range 2 {
		select {
		case <-rendezvous.arrived:
		case <-done:
			for _, r := range results {
				if r.err != nil {
					t.Fatalf("a create ended before both counted the tenant's workflows: %v", r.err)
				}
			}
			t.Fatal("both creates ended before both counted the tenant's workflows")
		case <-ctx.Done():
			t.Fatal("the two creates never both counted the tenant's workflows")
		}
	}
	close(rendezvous.release)
	<-done

	var winners, losers []result
	for _, r := range results {
		if r.err == nil {
			winners = append(winners, r)
		} else {
			losers = append(losers, r)
		}
	}
	require.Len(t, winners, 1, "exactly one create commits: %+v", results)
	require.Len(t, losers, 1)
	var quota *domain.PlanQuotaError
	require.True(t, errors.As(losers[0].err, &quota), "the loser fails with the quota error, got %v", losers[0].err)
	assert.ErrorIs(t, losers[0].err, domain.ErrPlanQuotaExceeded)
	assert.EqualValues(t, 2, quota.Limit)
	assert.EqualValues(t, 2, quota.Count, "the loser's retry saw the winner's workflow")
	assert.Nil(t, losers[0].wf)
	assert.EqualValues(t, 3, rendezvous.calls.Load(), "two first attempts and one serialization retry")

	total, err := realWorkflows.CountByTenant(ctx, tenantID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total, "the seed workflow and the winner's; the limit holds")
	stored, err := realWorkflows.GetByID(ctx, tenantID, winners[0].wf.ID)
	require.NoError(t, err)
	assert.Equal(t, winners[0].wf.TemplateKey, stored.TemplateKey)
}
