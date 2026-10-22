package fixtures

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
)

func NewRestrictedRolePool(t *testing.T, superDSN string, bypassRLS bool, tenantID, userID *uuid.UUID) *pgcommon.Pool {
	t.Helper()
	ctx := context.Background()

	admin, err := sql.Open("pgx", superDSN)
	if err != nil {
		t.Fatalf("fixtures.NewRestrictedRolePool: open admin connection: %v", err)
	}
	defer func() { _ = admin.Close() }()

	roleName := "restricted_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	bypassClause := "NOBYPASSRLS"
	if bypassRLS {
		bypassClause = "BYPASSRLS"
	}
	if _, err := admin.ExecContext(ctx, fmt.Sprintf(
		`CREATE ROLE %s LOGIN PASSWORD 'restricted' %s`, roleName, bypassClause,
	)); err != nil {
		t.Fatalf("fixtures.NewRestrictedRolePool: create role: %v", err)
	}
	if _, err := admin.ExecContext(ctx, fmt.Sprintf(
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %s`, roleName,
	)); err != nil {
		t.Fatalf("fixtures.NewRestrictedRolePool: grant table privileges: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), fmt.Sprintf(`DROP OWNED BY %s`, roleName))
		_, _ = admin.ExecContext(context.Background(), fmt.Sprintf(`DROP ROLE IF EXISTS %s`, roleName))
	})

	roleDSN := restrictedRoleDSN(superDSN, roleName)
	cfg := pgcommon.Config{DSN: roleDSN}
	if !bypassRLS && tenantID != nil && userID != nil {
		cfg.GUCProvider = func(context.Context) (pgdomain.GUCSet, bool) {
			return pgdomain.GUCSet{TenantID: tenantID.String(), UserID: userID.String()}, true
		}
	}
	pool, err := pgcommon.NewPool(ctx, cfg)
	if err != nil {
		t.Fatalf("fixtures.NewRestrictedRolePool: create pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func restrictedRoleDSN(superDSN, roleName string) string {
	before, after, _ := strings.Cut(superDSN, "@")
	scheme, _, _ := strings.Cut(before, "://")
	return fmt.Sprintf("%s://%s:restricted@%s", scheme, roleName, after)
}

func NewContextGUCRolePool(t *testing.T, superDSN string) *pgcommon.Pool {
	t.Helper()
	ctx := context.Background()

	admin, err := sql.Open("pgx", superDSN)
	if err != nil {
		t.Fatalf("fixtures.NewContextGUCRolePool: open admin connection: %v", err)
	}
	defer func() { _ = admin.Close() }()

	roleName := "ctxguc_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	for _, stmt := range []string{
		fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD 'restricted' NOBYPASSRLS`, roleName),
		fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %s`, roleName),
	} {
		if _, err := admin.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("fixtures.NewContextGUCRolePool: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), fmt.Sprintf(`DROP OWNED BY %s`, roleName))
		_, _ = admin.ExecContext(context.Background(), fmt.Sprintf(`DROP ROLE IF EXISTS %s`, roleName))
	})

	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: restrictedRoleDSN(superDSN, roleName), GUCProvider: pgcommon.GUCSetFromContext})
	if err != nil {
		t.Fatalf("fixtures.NewContextGUCRolePool: create pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
