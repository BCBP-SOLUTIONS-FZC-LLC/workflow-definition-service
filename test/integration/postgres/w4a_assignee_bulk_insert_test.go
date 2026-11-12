//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestAssigneeRepo_BulkInsert_ABatchWithAFailingRow_ReturnsTheDatabaseErrorAndLeavesNoNewRow(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenantID := uuid.New()
	wf := &domain.Workflow{ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(), TemplateKey: "bulk-failing-row", Name: "X"}
	require.NoError(t, pgadapter.NewWorkflowRepo(pool).Create(ctx, wf))
	version := newDraftVersion(wf.ID, tenantID)
	require.NoError(t, pgadapter.NewWorkflowVersionRepo(pool).Create(ctx, version))
	repo := pgadapter.NewAssigneeRepo(pool)

	keeper, newcomer := uuid.New(), uuid.New()
	taken := &domain.NodeAssignee{ID: uuid.New(), NodeKey: "Task_Existing", UserID: keeper, Role: "approver"}
	require.NoError(t, repo.BulkInsert(ctx, tenantID, version.ID, []*domain.NodeAssignee{taken}))

	batch := []*domain.NodeAssignee{
		{ID: uuid.New(), NodeKey: "Task_First", UserID: newcomer, Role: "approver"},
		{ID: taken.ID, NodeKey: "Task_Clash", UserID: newcomer, Role: "reviewer"},
		{ID: uuid.New(), NodeKey: "Task_Last", UserID: newcomer, Role: "reviewer"},
	}

	err := repo.BulkInsert(ctx, tenantID, version.ID, batch)

	require.Error(t, err)
	assert.Zero(t, w4aCount(t, pool, `SELECT count(*) FROM workflow_node_assignee WHERE user_id = $1`, newcomer),
		"the rows before and after the failing one are not stored either")
	stored, err := repo.ListByUser(ctx, tenantID, keeper)
	require.NoError(t, err)
	require.Len(t, stored, 1, "the row that was already there is untouched")
	assert.Equal(t, "Task_Existing", stored[0].NodeKey)
	assert.Equal(t, "approver", stored[0].Role)
}
