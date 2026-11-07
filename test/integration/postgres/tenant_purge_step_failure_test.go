//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func TestTenantRetentionRepo_PurgeBatch_AFailingModuleDelete_NamesTheTableAndLeavesTheTenantIntact(t *testing.T) {
	superPool := fixtures.NewTestPool(t)
	repo := pgadapter.NewTenantRetentionRepo(superPool)
	ctx := context.Background()
	tenant := uuid.New()
	seedPurgeTenant(t, superPool, tenant, 0)
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, repo.RecordOffboarded(ctx, tenant, at, at, at))

	exec := func(sql string) {
		t.Helper()
		require.NoError(t, superPool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
			_, err := conn.Exec(ctx, sql)
			return err
		}))
	}
	exec(`CREATE FUNCTION purge_step_probe_fn() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'module delete blocked'; END $$;
		CREATE TRIGGER purge_step_probe_trg BEFORE DELETE ON workflow_module
			FOR EACH ROW EXECUTE FUNCTION purge_step_probe_fn();`)

	deleted, err := repo.PurgeBatch(ctx, tenant, 1, 10)

	require.Error(t, err)
	assert.ErrorContains(t, err, "purge workflow_module: ")
	assert.ErrorContains(t, err, "module delete blocked", "the cause stays in the chain")
	assert.NotErrorIs(t, err, domain.ErrTenantRetentionChanged)
	assert.Empty(t, deleted)
	assert.Equal(t, 1, countTenantRows(t, superPool, "workflow_module", tenant))
	assert.Equal(t, 1, countTenantRows(t, superPool, "workflow_template", tenant), "the failed batch rolled back as a whole")

	exec(`DROP TRIGGER purge_step_probe_trg ON workflow_module`)
	deleted, err = repo.PurgeBatch(ctx, tenant, 1, 10)
	require.NoError(t, err, "the failed batch left the retention row at the version the caller holds")
	assert.Equal(t, map[string]int64{"workflow_module": 1}, deleted)
	assert.Zero(t, countTenantRows(t, superPool, "workflow_module", tenant))
}
