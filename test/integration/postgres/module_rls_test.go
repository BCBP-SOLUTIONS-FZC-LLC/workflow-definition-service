package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func insertModule(t *testing.T, superDSN string, id uuid.UUID, scope string, tenantID *uuid.UUID) {
	t.Helper()
	pool, err := pgcommon.NewPool(context.Background(), pgcommon.Config{DSN: superDSN})
	if err != nil {
		t.Fatalf("insertModule: create pool: %v", err)
	}
	t.Cleanup(pool.Close)
	err = pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx,
			`INSERT INTO workflow_module (id, tenant_id, scope, name) VALUES ($1, $2, $3, 'm')`,
			id, tenantID, scope)
		return err
	})
	if err != nil {
		t.Fatalf("insertModule: %v", err)
	}
}

func countVisibleModules(t *testing.T, pool interface {
	WithConn(context.Context, func(context.Context, *pgxpool.Conn) error) error
}, id uuid.UUID) int {
	t.Helper()
	var count int
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT count(*) FROM workflow_module WHERE id = $1`, id).Scan(&count)
	})
	if err != nil {
		t.Fatalf("countVisibleModules: %v", err)
	}
	return count
}

func TestModuleRLS_OwnTenantAndGlobalVisible(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantA := uuid.New()
	tenantB := uuid.New()
	globalID, ownID, otherTenantID := uuid.New(), uuid.New(), uuid.New()
	insertModule(t, dsn, globalID, "global", nil)
	insertModule(t, dsn, ownID, "tenant", &tenantA)
	insertModule(t, dsn, otherTenantID, "tenant", &tenantB)

	userA := uuid.New()
	pool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA)

	if got := countVisibleModules(t, pool, globalID); got != 1 {
		t.Errorf("global module visible count = %d, want 1", got)
	}
	if got := countVisibleModules(t, pool, ownID); got != 1 {
		t.Errorf("own-tenant module visible count = %d, want 1", got)
	}
	if got := countVisibleModules(t, pool, otherTenantID); got != 0 {
		t.Errorf("other-tenant module visible count = %d, want 0", got)
	}
}

func TestModuleRLS_MissingGUCHidesTenantRowsOnly(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantA := uuid.New()
	globalID, ownID := uuid.New(), uuid.New()
	insertModule(t, dsn, globalID, "global", nil)
	insertModule(t, dsn, ownID, "tenant", &tenantA)

	pool := fixtures.NewRestrictedRolePool(t, dsn, false, nil, nil)

	if got := countVisibleModules(t, pool, globalID); got != 1 {
		t.Errorf("global module visible count with no GUC = %d, want 1", got)
	}
	if got := countVisibleModules(t, pool, ownID); got != 0 {
		t.Errorf("tenant module visible count with no GUC = %d, want 0", got)
	}
}

func TestModuleRLS_OrdinaryRoleCannotInsertGlobalScope(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	pool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)

	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx,
			`INSERT INTO workflow_module (id, tenant_id, scope, name) VALUES ($1, NULL, 'global', 'm')`,
			uuid.New())
		return err
	})
	if err == nil {
		t.Error("ordinary RLS-bound role should not be able to insert a scope=global row")
	}
}

func TestModuleRLS_BypassRoleCanInsertGlobalScope(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	pool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	id := uuid.New()
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx,
			`INSERT INTO workflow_module (id, tenant_id, scope, name) VALUES ($1, NULL, 'global', 'm')`,
			id)
		return err
	})
	if err != nil {
		t.Fatalf("BYPASSRLS role should be able to insert a scope=global row: %v", err)
	}
	if got := countVisibleModules(t, pool, id); got != 1 {
		t.Errorf("visible count after bypass insert = %d, want 1", got)
	}
}
