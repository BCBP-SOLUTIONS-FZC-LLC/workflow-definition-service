//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
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
	require.NoError(t, repo.RecordOffboarded(ctx, due, now.Add(-48*time.Hour), now.Add(-time.Hour), now.Add(-time.Hour)))
	require.NoError(t, repo.RecordOffboarded(ctx, notDue, now.Add(-48*time.Hour), now.Add(24*time.Hour), now.Add(24*time.Hour)))

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

	require.NoError(t, repo.RecordOffboarded(ctx, tenant, first, first.Add(24*time.Hour), first.Add(48*time.Hour)))
	require.NoError(t, repo.RecordOffboarded(ctx, tenant, first.Add(72*time.Hour), first.Add(96*time.Hour), first.Add(120*time.Hour)))

	due, err := repo.ListDue(ctx, first.Add(25*time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, []port.TenantPurgeDue{{TenantID: tenant, Credentials: true, RecordVersion: 1}}, due, "only the credentials are due at 25 hours, on the first dates")

	due, err = repo.ListDue(ctx, first.Add(49*time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, []port.TenantPurgeDue{{TenantID: tenant, Credentials: true, Artifacts: true, RecordVersion: 1}}, due)

	version, err := repo.MarkCredentialsPurged(ctx, tenant, 1, first.Add(50*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(2), version, "the trigger counts the write")
	due, err = repo.ListDue(ctx, first.Add(51*time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, []port.TenantPurgeDue{{TenantID: tenant, Artifacts: true, RecordVersion: 2}}, due, "a purged part is not due again")

	_, err = repo.MarkPurged(ctx, tenant, 2, first.Add(52*time.Hour))
	require.NoError(t, err)
	due, err = repo.ListDue(ctx, first.Add(53*time.Hour), 10)
	require.NoError(t, err)
	assert.Empty(t, due)
}

func TestTenantRetentionRepo_HasRowsSeesOnlyTheTenantsOwnRows(t *testing.T) {
	superPool, superDSN := fixtures.NewTestPoolAndDSN(t)
	appPool := fixtures.NewContextGUCRolePool(t, superDSN)
	repo := pgadapter.NewTenantRetentionRepo(appPool)
	ctx := context.Background()
	withRows, empty := uuid.New(), uuid.New()
	seedPurgeTenant(t, superPool, withRows, 1)

	has, err := repo.HasRows(ctx, withRows)
	require.NoError(t, err)
	assert.True(t, has)
	has, err = repo.HasRows(ctx, empty)
	require.NoError(t, err)
	assert.False(t, has)
}

func TestTenantRetentionRepo_AReturnStopsEveryOlderOffboarding(t *testing.T) {
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	at := func(hours int) time.Time { return base.Add(time.Duration(hours) * time.Hour) }
	tests := []struct {
		name    string
		events  func(ctx context.Context, repo *pgadapter.TenantRetentionRepo, tenant uuid.UUID) error
		wantDue bool
	}{
		{
			name: "an offboarding redelivered after the return",
			events: func(ctx context.Context, repo *pgadapter.TenantRetentionRepo, tenant uuid.UUID) error {
				return errors.Join(
					repo.RecordOffboarded(ctx, tenant, at(0), at(1), at(2)),
					repo.RecordReturned(ctx, tenant, at(10)),
					repo.RecordOffboarded(ctx, tenant, at(0), at(1), at(2)))
			},
		},
		{
			name: "an offboarding that arrives after the return that undid it",
			events: func(ctx context.Context, repo *pgadapter.TenantRetentionRepo, tenant uuid.UUID) error {
				return errors.Join(
					repo.RecordReturned(ctx, tenant, at(10)),
					repo.RecordOffboarded(ctx, tenant, at(0), at(1), at(2)))
			},
		},
		{
			name: "a return older than the offboarding is a late delivery",
			events: func(ctx context.Context, repo *pgadapter.TenantRetentionRepo, tenant uuid.UUID) error {
				return errors.Join(
					repo.RecordOffboarded(ctx, tenant, at(5), at(6), at(7)),
					repo.RecordReturned(ctx, tenant, at(4)))
			},
			wantDue: true,
		},
		{
			name: "an offboarding after the return records afresh",
			events: func(ctx context.Context, repo *pgadapter.TenantRetentionRepo, tenant uuid.UUID) error {
				return errors.Join(
					repo.RecordOffboarded(ctx, tenant, at(0), at(1), at(2)),
					repo.RecordReturned(ctx, tenant, at(10)),
					repo.RecordOffboarded(ctx, tenant, at(20), at(21), at(22)))
			},
			wantDue: true,
		},
	}
	pool := fixtures.NewTestPool(t)
	repo := pgadapter.NewTenantRetentionRepo(pool)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			tenant := uuid.New()
			require.NoError(t, tt.events(ctx, repo, tenant))

			due, err := repo.ListDue(ctx, at(100), 1000)
			require.NoError(t, err)
			var found bool
			for _, d := range due {
				found = found || d.TenantID == tenant
			}
			assert.Equal(t, tt.wantDue, found)
		})
	}
}

func TestTenantRetentionRepo_APurgeBatchStopsOnceTheTenantReturned(t *testing.T) {
	superPool, superDSN := fixtures.NewTestPoolAndDSN(t)
	appPool := fixtures.NewContextGUCRolePool(t, superDSN)
	repo := pgadapter.NewTenantRetentionRepo(appPool)
	ctx := context.Background()
	tenant := uuid.New()
	seedPurgeTenant(t, superPool, tenant, 2)
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	require.NoError(t, repo.RecordOffboarded(ctx, tenant, now.Add(-48*time.Hour), now.Add(-time.Hour), now.Add(-time.Hour)))
	due, err := repo.ListDue(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, due, 1)

	require.NoError(t, repo.RecordReturned(ctx, tenant, now))

	_, err = repo.PurgeBatch(ctx, tenant, due[0].RecordVersion, 10)
	require.ErrorIs(t, err, domain.ErrTenantRetentionChanged)
	require.ErrorIs(t, repo.Current(ctx, tenant, due[0].RecordVersion), domain.ErrTenantRetentionChanged)
	assert.Equal(t, 2, countTenantRows(t, superPool, "workflow", tenant), "a returned tenant keeps its workflows")
}

func TestTenantRetentionRepo_AFailedTenantWaitsForItsNextAttempt(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	repo := pgadapter.NewTenantRetentionRepo(pool)
	ctx := context.Background()
	failing, other := uuid.New(), uuid.New()
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, repo.RecordOffboarded(ctx, failing, at, at, at))
	require.NoError(t, repo.RecordOffboarded(ctx, other, at.Add(time.Minute), at.Add(time.Minute), at.Add(time.Minute)))
	now := at.Add(time.Hour)

	require.NoError(t, repo.RecordFailure(ctx, failing, 1, 1, now.Add(24*time.Hour)))

	due, err := repo.ListDue(ctx, now, 1)
	require.NoError(t, err)
	assert.Equal(t, []port.TenantPurgeDue{{TenantID: other, Credentials: true, Artifacts: true, RecordVersion: 1}}, due,
		"the failing tenant no longer takes the only slot")
	due, err = repo.ListDue(ctx, now.Add(25*time.Hour), 10)
	require.NoError(t, err)
	assert.Contains(t, due, port.TenantPurgeDue{TenantID: failing, Credentials: true, Artifacts: true, Attempts: 1, RecordVersion: 2})
}

func TestTenantRetentionRepo_AWriteWithAStaleVersionIsRefused(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	repo := pgadapter.NewTenantRetentionRepo(pool)
	ctx := context.Background()
	tenant := uuid.New()
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, repo.RecordOffboarded(ctx, tenant, at, at, at))

	version, err := repo.MarkCredentialsPurged(ctx, tenant, 1, at)
	require.NoError(t, err)

	_, err = repo.MarkPurged(ctx, tenant, 1, at)
	require.ErrorIs(t, err, domain.ErrTenantRetentionChanged)
	require.ErrorIs(t, repo.RecordFailure(ctx, tenant, 1, 1, at), domain.ErrTenantRetentionChanged)
	_, err = repo.MarkPurged(ctx, uuid.New(), 1, at)
	require.ErrorIs(t, err, domain.ErrTenantRetentionChanged, "a cleared row is a changed row")
	_, err = repo.MarkPurged(ctx, tenant, version, at)
	require.NoError(t, err)
}
