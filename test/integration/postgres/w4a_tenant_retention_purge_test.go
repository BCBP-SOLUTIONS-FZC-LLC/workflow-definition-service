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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type w4aRetentionRow struct {
	CredentialsPurgedAt *time.Time
	PurgedAt            *time.Time
	Attempts            int
	NextAttemptAt       time.Time
	RecordVersion       int64
}

func w4aReadRetention(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID) w4aRetentionRow {
	t.Helper()
	var row w4aRetentionRow
	require.NoError(t, pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx,
			`SELECT credentials_purged_at, purged_at, attempts, next_attempt_at, record_version
			   FROM tenant_retention WHERE tenant_id = $1`, tenantID).
			Scan(&row.CredentialsPurgedAt, &row.PurgedAt, &row.Attempts, &row.NextAttemptAt, &row.RecordVersion)
	}))
	return row
}

func w4aCount(t *testing.T, pool *pgcommon.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, query, args...).Scan(&n)
	}))
	return n
}

func w4aSeedTenantRows(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID, workflows int) {
	t.Helper()
	require.NoError(t, pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
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
		}
		modID := uuid.New()
		if _, err := conn.Exec(ctx, `INSERT INTO workflow_module (id, tenant_id, scope, name, process_id) VALUES ($1, $2, 'tenant', 'm', $3)`,
			modID, tenantID, modID.String()); err != nil {
			return err
		}
		_, err := conn.Exec(ctx, `INSERT INTO workflow_template (id, tenant_id, scope, name, bpmn_xml) VALUES ($1, $2, 'tenant', 's', '<x/>')`,
			uuid.New(), tenantID)
		return err
	}))
}

func w4aTenantRowCounts(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, table := range []string{"workflow", "workflow_module", "workflow_template"} {
		counts[table] = w4aCount(t, pool, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tenantID)
	}
	return counts
}

func TestTenantRetentionRepo_PurgeBatch_DeletesFromOneTableInOrderPerCallThenEndsWithAnEmptyMap(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	repo := pgadapter.NewTenantRetentionRepo(fixtures.NewContextGUCRolePool(t, dsn))
	ctx := context.Background()
	tenant, bystander := uuid.New(), uuid.New()
	w4aSeedTenantRows(t, superPool, tenant, 3)
	w4aSeedTenantRows(t, superPool, bystander, 3)
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, repo.RecordOffboarded(ctx, tenant, at, at, at))

	steps := []struct {
		name       string
		wantDelete map[string]int64
		wantLeft   map[string]int
	}{
		{"first call takes a full batch of workflows and leaves the other tables alone",
			map[string]int64{"workflow": 2}, map[string]int{"workflow": 1, "workflow_module": 1, "workflow_template": 1}},
		{"second call takes the last workflow and still nothing else",
			map[string]int64{"workflow": 1}, map[string]int{"workflow": 0, "workflow_module": 1, "workflow_template": 1}},
		{"third call moves on to the modules",
			map[string]int64{"workflow_module": 1}, map[string]int{"workflow": 0, "workflow_module": 0, "workflow_template": 1}},
		{"fourth call takes the starter templates",
			map[string]int64{"workflow_template": 1}, map[string]int{"workflow": 0, "workflow_module": 0, "workflow_template": 0}},
		{"fifth call finds nothing and returns an empty map",
			map[string]int64{}, map[string]int{"workflow": 0, "workflow_module": 0, "workflow_template": 0}},
	}
	for _, step := range steps {
		deleted, err := repo.PurgeBatch(ctx, tenant, 1, 2)

		require.NoError(t, err, step.name)
		require.NotNil(t, deleted, step.name)
		assert.Equal(t, step.wantDelete, deleted, step.name)
		assert.Equal(t, step.wantLeft, w4aTenantRowCounts(t, superPool, tenant), step.name)
	}
	assert.Equal(t, map[string]int{"workflow": 3, "workflow_module": 1, "workflow_template": 1}, w4aTenantRowCounts(t, superPool, bystander),
		"another tenant's rows are not touched")
}

func TestTenantRetentionRepo_PurgeBatch_StaleVersionOrMissingRecord_IsRefusedAndDeletesNothing(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	repo := pgadapter.NewTenantRetentionRepo(fixtures.NewContextGUCRolePool(t, dsn))
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	tenant, noRecord := uuid.New(), uuid.New()
	w4aSeedTenantRows(t, superPool, tenant, 2)
	w4aSeedTenantRows(t, superPool, noRecord, 2)
	require.NoError(t, repo.RecordOffboarded(ctx, tenant, at, at, at))
	current, err := repo.MarkCredentialsPurged(ctx, tenant, 1, at)
	require.NoError(t, err)
	require.Greater(t, current, int64(1), "the write moved the version past the one a sweep read earlier")
	want := map[string]int{"workflow": 2, "workflow_module": 1, "workflow_template": 1}

	deleted, err := repo.PurgeBatch(ctx, tenant, 1, 10)

	require.ErrorIs(t, err, domain.ErrTenantRetentionChanged)
	assert.False(t, errors.Is(err, domain.ErrUpstreamUnavailable), "a stale version is not an outage: %v", err)
	assert.Empty(t, deleted)
	assert.Equal(t, want, w4aTenantRowCounts(t, superPool, tenant), "a stale batch deletes nothing from any table")

	_, err = repo.PurgeBatch(ctx, noRecord, 1, 10)
	require.ErrorIs(t, err, domain.ErrTenantRetentionChanged, "a tenant with no retention record is a changed record")
	assert.Equal(t, want, w4aTenantRowCounts(t, superPool, noRecord))

	deleted, err = repo.PurgeBatch(ctx, tenant, current, 10)
	require.NoError(t, err, "the same batch with the version read now goes through")
	assert.Equal(t, map[string]int64{"workflow": 2}, deleted)
}

func TestTenantRetentionRepo_MarkAndRecordWrites_WithAStaleVersion_AreRefusedAndLeaveTheRowUnchanged(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	repo := pgadapter.NewTenantRetentionRepo(pool)
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	credentialsVersion, err := func() (int64, error) {
		tenant := uuid.New()
		if err := repo.RecordOffboarded(ctx, tenant, at, at, at); err != nil {
			return 0, err
		}
		return repo.MarkCredentialsPurged(ctx, tenant, 1, at)
	}()
	require.NoError(t, err)
	assert.Equal(t, int64(2), credentialsVersion, "a successful write returns the version after it")

	writes := []struct {
		name  string
		write func(tenant uuid.UUID, version int64) error
	}{
		{"MarkCredentialsPurged", func(tenant uuid.UUID, version int64) error {
			_, err := repo.MarkCredentialsPurged(ctx, tenant, version, at.Add(time.Hour))
			return err
		}},
		{"MarkPurged", func(tenant uuid.UUID, version int64) error {
			_, err := repo.MarkPurged(ctx, tenant, version, at.Add(time.Hour))
			return err
		}},
		{"RecordFailure", func(tenant uuid.UUID, version int64) error {
			return repo.RecordFailure(ctx, tenant, version, 7, at.Add(48*time.Hour))
		}},
	}
	for _, tc := range writes {
		t.Run(tc.name, func(t *testing.T) {
			tenant := uuid.New()
			require.NoError(t, repo.RecordOffboarded(ctx, tenant, at, at, at))
			require.NoError(t, repo.RecordFailure(ctx, tenant, 1, 2, at.Add(time.Hour)))
			before := w4aReadRetention(t, pool, tenant)
			require.Greater(t, before.RecordVersion, int64(1))

			err := tc.write(tenant, before.RecordVersion-1)

			require.ErrorIs(t, err, domain.ErrTenantRetentionChanged)
			assert.Equal(t, before, w4aReadRetention(t, pool, tenant), "the refused write changed nothing, not even the version")
		})
	}
}

func TestRLSViolationRepo_PruneBefore_DeadlineAlreadyExceeded_IsUnavailable(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	repo := pgadapter.NewRLSViolationRepo(pool)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	n, err := repo.PruneBefore(ctx, time.Now())

	require.Error(t, err)
	assert.Zero(t, n)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable, "an expired request deadline reads as the database being unavailable: %v", err)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "the cause stays in the chain")
}
