//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
)

func damageModuleRefs(t *testing.T, pool *pgcommon.Pool, id uuid.UUID, raw string) {
	t.Helper()
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, `UPDATE workflow_version SET module_refs = $2::jsonb WHERE id = $1`, id, raw)
		return err
	})
	require.NoError(t, err)
}

func TestWorkflowVersionRepo_GetByIDAndGetDraft_StoredModuleRefsOfTheWrongShape_ReturnAnErrorNotNoModules(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	wf := w4cWorkflow(t, pool, uuid.New())
	v := newDraftVersion(wf.ID, wf.TenantID)
	require.NoError(t, repo.Create(ctx, v))
	damageModuleRefs(t, pool, v.ID, `{"module_id":"not-a-list"}`)

	_, err := repo.GetByID(ctx, wf.TenantID, v.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module refs")

	_, err = repo.GetDraft(ctx, wf.TenantID, wf.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module refs")
}
