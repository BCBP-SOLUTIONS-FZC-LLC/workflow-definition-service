package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func TestPoolRolePosture(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	appPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
	ctx := context.Background()

	super, err := postgres.PoolRolePosture(ctx, superPool)
	if err != nil || !super.Superuser || !super.BypassesRLS() || !super.CanWrite {
		t.Errorf("superuser posture = %+v, %v; want a superuser that bypasses RLS and can write", super, err)
	}
	app, err := postgres.PoolRolePosture(ctx, appPool)
	if err != nil || app.BypassesRLS() || !app.CanWrite || app.Superuser {
		t.Errorf("restricted posture = %+v, %v; want a role subject to RLS that can write", app, err)
	}
	system, err := postgres.PoolRolePosture(ctx, systemPool)
	if err != nil || !system.BypassRLS || system.Superuser || !system.CanWrite {
		t.Errorf("BYPASSRLS posture = %+v, %v; want a non-superuser that bypasses RLS and can write", system, err)
	}
}

func TestPoolRolePosture_ARoleThatCannotWriteTheSystemTablesIsReported(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()
	role := "readonly_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	for _, stmt := range []string{
		"CREATE ROLE " + role + " LOGIN PASSWORD 'readonly' BYPASSRLS",
		"GRANT SELECT ON ALL TABLES IN SCHEMA public TO " + role,
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP OWNED BY "+role)
		_, _ = admin.Exec(context.Background(), "DROP ROLE IF EXISTS "+role)
	})
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: strings.Replace(dsn, "postgres:postgres@", role+":readonly@", 1)})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	posture, err := postgres.PoolRolePosture(ctx, pool)
	if err != nil || !posture.BypassRLS || posture.CanWrite {
		t.Errorf("read-only posture = %+v, %v; want BYPASSRLS without write access", posture, err)
	}
}

func TestPoolRolePosture_ASchemaThatIsNotMigratedYetIsNotAnError(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()
	name := "empty_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: strings.Replace(dsn, "/testdb", "/"+name, 1)})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	posture, err := postgres.PoolRolePosture(ctx, pool)
	if err != nil {
		t.Fatalf("PoolRolePosture on an empty database = %v: the server must report the role, not fail with a missing relation before the migrations ran", err)
	}
	if !posture.Superuser {
		t.Errorf("posture = %+v, want the bootstrap superuser", posture)
	}
}
