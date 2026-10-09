package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

const sampledChecks = `SELECT count(*) FROM generate_series(1, 5000) WHERE rls_check_tenant(gen_random_uuid(), 'workflow')`

func violationCount(t *testing.T, dsn, violationType string) int {
	t.Helper()
	admin, err := pgcommon.NewPool(context.Background(), pgcommon.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("violationCount: create pool: %v", err)
	}
	t.Cleanup(admin.Close)
	var n int
	err = admin.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT count(*) FROM rls_violation_log WHERE violation_type = $1`, violationType).Scan(&n)
	})
	if err != nil {
		t.Fatalf("violationCount: %v", err)
	}
	return n
}

func runSampledChecks(t *testing.T, pool *pgcommon.Pool) {
	t.Helper()
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		var visible int
		return conn.QueryRow(ctx, sampledChecks).Scan(&visible)
	})
	if err != nil {
		t.Fatalf("runSampledChecks: %v", err)
	}
}

func TestRLSViolationLog_CrossTenantAccessIsSampled(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	pool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)

	runSampledChecks(t, pool)

	if got := violationCount(t, dsn, "cross_tenant_access"); got == 0 {
		t.Error("cross_tenant_access rows = 0 after 5000 foreign-tenant checks, want a sample")
	}
	if got := violationCount(t, dsn, "missing_or_invalid_guc"); got != 0 {
		t.Errorf("missing_or_invalid_guc rows = %d with the GUC set, want 0", got)
	}
}

func TestRLSViolationLog_MissingGUCIsSampled(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	pool := fixtures.NewRestrictedRolePool(t, dsn, false, nil, nil)

	runSampledChecks(t, pool)

	if got := violationCount(t, dsn, "missing_or_invalid_guc"); got == 0 {
		t.Error("missing_or_invalid_guc rows = 0 after 5000 checks with no tenant GUC, want a sample")
	}
}

func TestRLSViolationLog_OwnTenantRowsAreNeverLogged(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	pool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)

	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		var visible int
		return conn.QueryRow(ctx, `SELECT count(*) FROM generate_series(1, 5000) WHERE rls_check_tenant($1, 'workflow')`, tenantID).Scan(&visible)
	})
	if err != nil {
		t.Fatalf("own-tenant checks: %v", err)
	}

	for _, kind := range []string{"cross_tenant_access", "missing_or_invalid_guc"} {
		if got := violationCount(t, dsn, kind); got != 0 {
			t.Errorf("%s rows = %d for own-tenant rows, want 0", kind, got)
		}
	}
}
