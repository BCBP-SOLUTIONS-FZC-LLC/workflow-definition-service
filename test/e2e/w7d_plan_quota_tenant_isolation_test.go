//go:build integration

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func TestE2E_TemplateLimit_ATenantAtTheLimitDoesNotBlockAnotherTenantOnTheSamePlan(t *testing.T) {
	ctx := context.Background()
	pool := fixtures.NewTestPool(t)
	svcs := wireServices(t, pool, nil)
	workflows := postgres.NewWorkflowRepo(pool)
	atLimit, underLimit, userID := uuid.New(), uuid.New(), uuid.New()

	for i := range 5 {
		_, _, err := svcs.workflow.Create(ctx, atLimit, userID, fmt.Sprintf("full-%d", i), "Full", "", minimalBPMN, "starter")
		require.NoError(t, err, "template %d of the tenant that fills its plan", i)
	}
	_, _, err := svcs.workflow.Create(ctx, atLimit, userID, "full-over", "Over", "", minimalBPMN, "starter")
	require.True(t, errors.Is(err, domain.ErrPlanQuotaExceeded), "the tenant at its limit is refused: %v", err)

	wf, draft, err := svcs.workflow.Create(ctx, underLimit, userID, "other-first", "Other", "", minimalBPMN, "starter")

	require.NoError(t, err, "a tenant under the same limit is not refused because another tenant is full")
	assert.Equal(t, underLimit, wf.TenantID)
	assert.Equal(t, domain.VersionStatusDraft, draft.Status)
	_, otherCount, err := workflows.List(ctx, underLimit, port.WorkflowFilter{Page: 1, Limit: 100})
	require.NoError(t, err)
	assert.Equal(t, int64(1), otherCount)
	_, fullCount, err := workflows.List(ctx, atLimit, port.WorkflowFilter{Page: 1, Limit: 100})
	require.NoError(t, err)
	assert.Equal(t, int64(5), fullCount, "the refused create left the full tenant at its limit")
}
