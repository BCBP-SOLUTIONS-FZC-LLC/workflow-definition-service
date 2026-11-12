//go:build integration

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func TestE2E_CloneWorkflow_TwoSimultaneousClonesOneBelowTheLimit_NeverExceedTheLimit(t *testing.T) {
	ctx := context.Background()
	pool := fixtures.NewTestPool(t)
	svcs := wireServices(t, pool, nil)
	tenantID, userID := uuid.New(), uuid.New()
	for i := range 3 {
		_, _, err := svcs.workflow.Create(ctx, tenantID, userID, fmt.Sprintf("seed-%d", i), "Seed", "", minimalBPMN, "starter")
		require.NoError(t, err)
	}
	source, draft, err := svcs.workflow.Create(ctx, tenantID, userID, "clone-source", "Source", "", minimalBPMN, "starter")
	require.NoError(t, err)
	published, err := svcs.version.Publish(ctx, tenantID, userID, source.ID, draft.ID, false)
	require.NoError(t, err)

	overlap := &overlapWorkflowRepo{WorkflowRepository: pgadapter.NewWorkflowRepo(pool), both: make(chan struct{})}
	cloner := service.NewVersionService(service.VersionDeps{
		Transactor: pgadapter.NewTransactor(pool),
		Workflows:  overlap,
		Versions:   pgadapter.NewWorkflowVersionRepo(pool),
		Assignees:  pgadapter.NewAssigneeRepo(pool),
		Compiler:   bpmn_compiler.New(),
		PlanLimits: service.NewPlanLimitService(service.PlanLimitDeps{Reader: staticPlanTable{}, TTL: time.Hour}),
	})

	errs := make([]error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, errs[i] = cloner.Clone(ctx, tenantID, userID, source.ID, published.ID, "starter",
				service.CloneReq{NewKey: fmt.Sprintf("clone-race-%d", i), NewName: "Clone"})
		}()
	}
	close(start)
	wg.Wait()

	require.True(t, overlap.bothTransactionsHadCounted(), "both clones counted the workflows before either wrote")
	created, refused := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			created++
		case errors.Is(e, domain.ErrPlanQuotaExceeded):
			refused++
		default:
			t.Errorf("unexpected clone error: %v", e)
		}
	}
	assert.Equal(t, 1, created, "only one of the two fits under the limit")
	assert.Equal(t, 1, refused, "the other is refused with the quota error")
	count, err := pgadapter.NewWorkflowRepo(pool).CountByTenant(ctx, tenantID)
	require.NoError(t, err)
	assert.Equal(t, int64(5), count, "the tenant ends at the limit, not above it")
}
