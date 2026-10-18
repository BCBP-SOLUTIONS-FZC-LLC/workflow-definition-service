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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

var purgeTables = []string{"workflow", "workflow_version", "workflow_node_assignee", "workflow_module", "workflow_module_version", "workflow_template"}

func countTenantRows(t *testing.T, pool *pgcommon.Pool, table string, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tenantID).Scan(&n)
	}))
	return n
}

func seedPurgeTenant(t *testing.T, superPool *pgcommon.Pool, tenantID uuid.UUID, workflows int) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, superPool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		for i := 0; i < workflows; i++ {
			wfID, vID := uuid.New(), uuid.New()
			if _, err := conn.Exec(ctx, `INSERT INTO workflow (id, tenant_id, created_by_user_id, template_key, name) VALUES ($1, $2, $3, $4, 'wf')`,
				wfID, tenantID, uuid.New(), "key-"+wfID.String()); err != nil {
				return err
			}
			if _, err := conn.Exec(ctx, `INSERT INTO workflow_version (id, workflow_id, tenant_id, status, bpmn_xml, version_number, created_by_user_id)
				VALUES ($1, $2, $3, 'PUBLISHED', '<x/>', 1, $4)`, vID, wfID, tenantID, uuid.New()); err != nil {
				return err
			}
			if _, err := conn.Exec(ctx, `UPDATE workflow SET active_version_id = $1 WHERE id = $2`, vID, wfID); err != nil {
				return err
			}
			if _, err := conn.Exec(ctx, `INSERT INTO workflow_node_assignee (id, tenant_id, workflow_version_id, node_key, user_id, department_id, role)
				VALUES ($1, $2, $3, 'a/b', $4, $5, 'reviewer')`, uuid.New(), tenantID, vID, uuid.New(), uuid.New()); err != nil {
				return err
			}
		}
		modID, modVer := uuid.New(), uuid.New()
		if _, err := conn.Exec(ctx, `INSERT INTO workflow_module (id, tenant_id, scope, name, process_id) VALUES ($1, $2, 'tenant', 'm', $3)`, modID, tenantID, modID.String()); err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, `INSERT INTO workflow_module_version (id, module_id, tenant_id, scope, status, bpmn_xml, process_id, version_number)
			VALUES ($1, $2, $3, 'tenant', 'PUBLISHED', '<x/>', $4, 1)`, modVer, modID, tenantID, modID.String()); err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, `UPDATE workflow_module SET active_version_id = $1 WHERE id = $2`, modVer, modID); err != nil {
			return err
		}
		_, err := conn.Exec(ctx, `INSERT INTO workflow_template (id, tenant_id, scope, name, bpmn_xml) VALUES ($1, $2, 'tenant', 's', '<x/>')`, uuid.New(), tenantID)
		return err
	}))
}

func TestTenantPurge_AsTheTenantUnderRLSDeletesADueTenantAndLeavesEveryoneElse(t *testing.T) {
	superPool, superDSN := fixtures.NewTestPoolAndDSN(t)
	appPool := fixtures.NewContextGUCRolePool(t, superDSN)
	ctx := context.Background()
	repo := pgadapter.NewTenantRetentionRepo(appPool)

	due, notDue, neverOffboarded := uuid.New(), uuid.New(), uuid.New()
	for _, tenant := range []uuid.UUID{due, notDue, neverOffboarded} {
		seedPurgeTenant(t, superPool, tenant, 3)
	}
	globalModule := uuid.New()
	require.NoError(t, superPool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		if _, err := conn.Exec(ctx, `INSERT INTO workflow_module (id, scope, name, process_id) VALUES ($1, 'global', 'g', $2)`, globalModule, globalModule.String()); err != nil {
			return err
		}
		_, err := conn.Exec(ctx, `INSERT INTO workflow_template (id, scope, name, bpmn_xml) VALUES ($1, 'global', 'g', '<x/>')`, uuid.New())
		return err
	}))
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	require.NoError(t, repo.RecordOffboarded(ctx, due, now.Add(-48*time.Hour), now.Add(-time.Hour)))
	require.NoError(t, repo.RecordOffboarded(ctx, notDue, now.Add(-48*time.Hour), now.Add(24*time.Hour)))

	recorded := map[string]int64{}
	purger := service.NewTenantPurger(service.TenantPurgerDeps{
		Retention: repo, Data: repo, Batch: 2, Interval: time.Hour,
		Now:    func() time.Time { return now },
		Record: func(table string, n int64) { recorded[table] += n },
		Failed: func() {},
	})

	require.NoError(t, purger.Tick(ctx))

	for _, table := range purgeTables {
		assert.Zero(t, countTenantRows(t, superPool, table, due), "%s rows of the due tenant", table)
		assert.NotZero(t, countTenantRows(t, superPool, table, notDue), "%s rows of a tenant whose period has not passed", table)
		assert.NotZero(t, countTenantRows(t, superPool, table, neverOffboarded), "%s rows of a tenant never offboarded", table)
	}
	assert.EqualValues(t, 3, recorded["workflow"])
	assert.EqualValues(t, 1, recorded["workflow_module"])
	assert.EqualValues(t, 1, recorded["workflow_template"])

	var globalModules, globalTemplates int
	require.NoError(t, superPool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM workflow_module WHERE scope = 'global'`).Scan(&globalModules); err != nil {
			return err
		}
		return conn.QueryRow(ctx, `SELECT count(*) FROM workflow_template WHERE scope = 'global'`).Scan(&globalTemplates)
	}))
	assert.Equal(t, 1, globalModules, "the global catalog is never purged")
	assert.Equal(t, 1, globalTemplates)

	var purgedAt *time.Time
	require.NoError(t, superPool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT purged_at FROM tenant_retention WHERE tenant_id = $1`, due).Scan(&purgedAt)
	}))
	require.NotNil(t, purgedAt)

	require.NoError(t, purger.Tick(ctx), "a second run finds nothing due")
}

func TestTenantRetentionRepo_ARedeliveredOffboardingKeepsTheFirstDates(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	repo := pgadapter.NewTenantRetentionRepo(pool)
	ctx := context.Background()
	tenant := uuid.New()
	first := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, repo.RecordOffboarded(ctx, tenant, first, first.Add(24*time.Hour)))
	require.NoError(t, repo.RecordOffboarded(ctx, tenant, first.Add(72*time.Hour), first.Add(96*time.Hour)))

	due, err := repo.ListDue(ctx, first.Add(25*time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{tenant}, due, "the first purge date still applies")
}
