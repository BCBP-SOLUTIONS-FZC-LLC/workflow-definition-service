//go:build integration

package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type w2bRetentionRow struct {
	returnedAt *time.Time
	version    int64
}

func w2bReadRetention(t *testing.T, pool *pgcommon.Pool, tenant uuid.UUID) w2bRetentionRow {
	t.Helper()
	var row w2bRetentionRow
	require.NoError(t, pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT returned_at, record_version FROM tenant_retention WHERE tenant_id = $1`, tenant).
			Scan(&row.returnedAt, &row.version)
	}))
	return row
}

func w2bBlockedBy(pool *pgcommon.Pool, holderPID int32) (int, error) {
	var n int
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))`, holderPID).Scan(&n)
	})
	return n, err
}

func TestTenantRetentionRepo_AReturnArrivingDuringAPurgeWaitsForItThenWins(t *testing.T) {
	superPool, superDSN := fixtures.NewTestPoolAndDSN(t)
	appPool := fixtures.NewContextGUCRolePool(t, superDSN)
	repo := pgadapter.NewTenantRetentionRepo(appPool)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	tenant := uuid.New()
	seedPurgeTenant(t, superPool, tenant, 2)
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	require.NoError(t, repo.RecordOffboarded(ctx, tenant, now.Add(-48*time.Hour), now.Add(-time.Hour), now.Add(-time.Hour)))
	due, err := repo.ListDue(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	staleVersion := due[0].RecordVersion

	locked := make(chan int32, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releasePurge := func() { releaseOnce.Do(func() { close(release) }) }
	defer releasePurge()
	purgeDone := make(chan error, 1)
	var purged int64
	go func() {
		txCtx := pgcommon.WithGUCSet(ctx, pgdomain.GUCSet{TenantID: tenant.String()})
		purgeDone <- pgcommon.RunInTx(txCtx, appPool, pgx.TxOptions{}, func(c context.Context, tx pgx.Tx) error {
			q := db.New(tx)
			if _, err := q.LockCurrentTenantRetention(c, db.LockCurrentTenantRetentionParams{TenantID: tenant, RecordVersion: staleVersion}); err != nil {
				return err
			}
			n, err := q.PurgeTenantWorkflows(c, db.PurgeTenantWorkflowsParams{TenantID: tenant, Limit: 10})
			if err != nil {
				return err
			}
			purged = n
			var pid int32
			if err := tx.QueryRow(c, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				return err
			}
			locked <- pid
			select {
			case <-release:
			case <-c.Done():
			}
			return nil
		})
	}()
	var holderPID int32
	select {
	case holderPID = <-locked:
	case err := <-purgeDone:
		require.FailNow(t, "the purge transaction ended before it held the lock", "%v", err)
	case <-ctx.Done():
		require.FailNow(t, "the purge transaction never took the lock")
	}

	returnDone := make(chan error, 1)
	go func() { returnDone <- repo.RecordReturned(ctx, tenant, now) }()

	require.Eventually(t, func() bool {
		n, err := w2bBlockedBy(superPool, holderPID)
		return err == nil && n > 0
	}, 30*time.Second, 5*time.Millisecond,
		"the return should wait on the purge transaction's lock")
	select {
	case err := <-returnDone:
		require.FailNow(t, "the return finished while the purge transaction was still open", "%v", err)
	default:
	}
	during := w2bReadRetention(t, superPool, tenant)
	assert.Nil(t, during.returnedAt, "the return is not visible before the purge transaction ends")
	assert.Equal(t, staleVersion, during.version)

	releasePurge()
	require.NoError(t, <-purgeDone)
	require.NoError(t, <-returnDone)

	after := w2bReadRetention(t, superPool, tenant)
	require.NotNil(t, after.returnedAt, "the return wins once the purge transaction has ended")
	assert.True(t, after.returnedAt.Equal(now))
	assert.Greater(t, after.version, staleVersion, "the return bumps the record version")

	_, err = repo.PurgeBatch(ctx, tenant, staleVersion, 10)
	require.ErrorIs(t, err, domain.ErrTenantRetentionChanged)
	require.ErrorIs(t, repo.Current(ctx, tenant, staleVersion), domain.ErrTenantRetentionChanged)

	assert.EqualValues(t, 2, purged)
	assert.Zero(t, countTenantRows(t, superPool, "workflow", tenant), "the batch that committed before the return stays deleted")
	assert.Equal(t, 1, countTenantRows(t, superPool, "workflow_module", tenant), "nothing after the return is purged")
	assert.Equal(t, 1, countTenantRows(t, superPool, "workflow_template", tenant))
}
