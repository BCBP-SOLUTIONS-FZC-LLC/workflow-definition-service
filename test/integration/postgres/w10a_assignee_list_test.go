//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func w10aSeedVersionWithID(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID, versionID uuid.UUID) *domain.WorkflowVersion {
	t.Helper()
	ctx := context.Background()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
		TemplateKey: "w10a-" + uuid.NewString(), Name: "W10a",
	}
	require.NoError(t, pgadapter.NewWorkflowRepo(pool).Create(ctx, wf))
	v := newDraftVersion(wf.ID, tenantID)
	v.ID = versionID
	require.NoError(t, pgadapter.NewWorkflowVersionRepo(pool).Create(ctx, v))
	return v
}

func w10aAssigneeIDs(rows []*domain.NodeAssignee) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestAssigneeRepo_ListByUser_RowsInSeveralVersions_ReturnsEveryRowOfThatUserInTheTenantOrderedByVersionThenNode(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	repo := pgadapter.NewAssigneeRepo(superPool)
	ctx := context.Background()

	tenantA, tenantB, userID, otherUser, callerUser := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	versionIDs := ascendingIDs(2)
	first := w10aSeedVersionWithID(t, superPool, tenantA, versionIDs[0])
	second := w10aSeedVersionWithID(t, superPool, tenantA, versionIDs[1])
	foreign := w10aSeedVersionWithID(t, superPool, tenantB, uuid.New())

	secondTaskC := &domain.NodeAssignee{ID: uuid.New(), NodeKey: "Task_C", UserID: userID, Role: "approver"}
	firstTaskB := &domain.NodeAssignee{ID: uuid.New(), NodeKey: "Task_B", UserID: userID, Role: "reviewer"}
	firstTaskA := &domain.NodeAssignee{ID: uuid.New(), NodeKey: "Task_A", UserID: userID, Role: "approver"}
	someoneElse := &domain.NodeAssignee{ID: uuid.New(), NodeKey: "Task_A_other", UserID: otherUser, Role: "approver"}
	foreignRow := &domain.NodeAssignee{ID: uuid.New(), NodeKey: "Task_Foreign", UserID: userID, Role: "approver"}
	require.NoError(t, repo.BulkInsert(ctx, tenantA, second.ID, []*domain.NodeAssignee{secondTaskC}))
	require.NoError(t, repo.BulkInsert(ctx, tenantA, first.ID, []*domain.NodeAssignee{firstTaskB, someoneElse, firstTaskA}))
	require.NoError(t, repo.BulkInsert(ctx, tenantB, foreign.ID, []*domain.NodeAssignee{foreignRow}))

	t.Run("all rows of the user in the tenant, none of another user or tenant", func(t *testing.T) {
		got, err := repo.ListByUser(ctx, tenantA, userID)

		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{firstTaskA.ID, firstTaskB.ID, secondTaskC.ID}, w10aAssigneeIDs(got),
			"every row of the user across both versions, in version id then node key order")
		assert.Equal(t, []uuid.UUID{first.ID, first.ID, second.ID}, []uuid.UUID{got[0].WorkflowVersionID, got[1].WorkflowVersionID, got[2].WorkflowVersionID})
		assert.Equal(t, []string{"approver", "reviewer", "approver"}, []string{got[0].Role, got[1].Role, got[2].Role})
		for _, row := range got {
			assert.Equal(t, tenantA, row.TenantID)
			assert.Equal(t, userID, row.UserID)
		}
	})

	t.Run("the same user id in another tenant lists only that tenant's row", func(t *testing.T) {
		got, err := repo.ListByUser(ctx, tenantB, userID)

		require.NoError(t, err)
		assert.Equal(t, []uuid.UUID{foreignRow.ID}, w10aAssigneeIDs(got))
	})

	t.Run("a user with one row gets only that row", func(t *testing.T) {
		got, err := repo.ListByUser(ctx, tenantA, otherUser)

		require.NoError(t, err)
		assert.Equal(t, []uuid.UUID{someoneElse.ID}, w10aAssigneeIDs(got))
	})

	t.Run("a caller bound to tenant A asking with tenant B's id sees nothing, with tenant A's id sees all three", func(t *testing.T) {
		callerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &callerUser)
		callerRepo := pgadapter.NewAssigneeRepo(callerPool)

		own, err := callerRepo.ListByUser(ctx, tenantA, userID)
		require.NoError(t, err)
		assert.Len(t, own, 3, "control: the restricted pool reads the caller's own tenant")

		other, err := callerRepo.ListByUser(ctx, tenantB, userID)
		require.NoError(t, err)
		assert.Empty(t, other, "row-level security hides the other tenant's rows even when its id is passed")
	})
}
