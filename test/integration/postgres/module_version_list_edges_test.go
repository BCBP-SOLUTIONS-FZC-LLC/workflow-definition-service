//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func moduleVersionListInsertArchived(
	t *testing.T, pool *pgcommon.Pool, moduleID uuid.UUID, tenantID *uuid.UUID,
	scope domain.CatalogScope, count int,
) []uuid.UUID {
	t.Helper()
	repo := postgres.NewModuleVersionRepo(pool)
	ids := make([]uuid.UUID, 0, count)
	for n := 1; n <= count; n++ {
		number := int32(n)
		v := newDraftModuleVersion(moduleID, tenantID, scope)
		v.Status = domain.VersionStatusArchived
		v.VersionNumber = &number
		if err := repo.Create(context.Background(), v); err != nil {
			t.Fatalf("create module version %d: %v", n, err)
		}
		ids = append(ids, v.ID)
	}
	return ids
}

func moduleVersionListIDs(versions []*domain.ModuleVersion) map[uuid.UUID]bool {
	ids := make(map[uuid.UUID]bool, len(versions))
	for _, v := range versions {
		ids[v.ID] = true
	}
	return ids
}

func TestModuleVersionRepo_ListByModule_PageBeyondLastPage_ReturnsNoRowsButTheTotal(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenantID := uuid.New()
	mod := newTestModule(t, ctx, pool, tenantID)
	moduleVersionListInsertArchived(t, pool, mod.ID, &tenantID, domain.ScopeTenant, 3)

	got, total, err := postgres.NewModuleVersionRepo(pool).ListByModule(ctx, tenantID, mod.ID, 99, 20)

	if err != nil {
		t.Fatalf("ListByModule: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 even beyond the last page", total)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("rows = %v, want an empty non-nil slice", got)
	}
}

func TestModuleVersionRepo_ListByModule_InvalidPaging_FallsBackToFirstPageOfTwenty(t *testing.T) {
	cases := []struct {
		name  string
		page  int
		limit int
	}{
		{"page zero", 0, 20},
		{"limit zero", 1, 0},
		{"both zero", 0, 0},
		{"both negative", -3, -5},
	}
	pool := newTestPool(t)
	ctx := context.Background()
	tenantID := uuid.New()
	mod := newTestModule(t, ctx, pool, tenantID)
	moduleVersionListInsertArchived(t, pool, mod.ID, &tenantID, domain.ScopeTenant, 25)
	repo := postgres.NewModuleVersionRepo(pool)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, total, err := repo.ListByModule(ctx, tenantID, mod.ID, tc.page, tc.limit)

			if err != nil {
				t.Fatalf("ListByModule: %v", err)
			}
			if total != 25 {
				t.Errorf("total = %d, want 25", total)
			}
			if len(got) != 20 {
				t.Errorf("rows = %d, want 20 (the default limit on the first page)", len(got))
			}
		})
	}
}

func TestModuleVersionRepo_ListByModule_TenantScopedVersionsOfAnotherTenant_AreHiddenByRLS(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	tenantA, tenantB, userA := uuid.New(), uuid.New(), uuid.New()
	otherModule := newTestModule(t, ctx, superPool, tenantB)
	moduleVersionListInsertArchived(t, superPool, otherModule.ID, &tenantB, domain.ScopeTenant, 2)
	repo := postgres.NewModuleVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA))

	cases := []struct {
		name     string
		callerID uuid.UUID
	}{
		{"asked under the owning tenant's id on the caller's pool", tenantB},
		{"asked under the caller's own id", tenantA},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, total, err := repo.ListByModule(ctx, tc.callerID, otherModule.ID, 1, 20)

			if err != nil {
				t.Fatalf("ListByModule: %v", err)
			}
			if total != 0 || len(got) != 0 {
				t.Errorf("saw total=%d rows=%d of another tenant's versions, want 0 and 0", total, len(got))
			}
		})
	}
}

func TestModuleVersionRepo_ListByModule_GlobalModule_IsListedForEveryTenant(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	moduleID := uuid.New()
	insertModule(t, dsn, moduleID, "global", nil)
	want := moduleVersionListInsertArchived(t, superPool, moduleID, nil, domain.ScopeGlobal, 2)

	cases := []struct {
		name   string
		tenant uuid.UUID
	}{
		{"first tenant", uuid.New()},
		{"second tenant", uuid.New()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := uuid.New()
			repo := postgres.NewModuleVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &tc.tenant, &user))

			got, total, err := repo.ListByModule(ctx, tc.tenant, moduleID, 1, 20)

			if err != nil {
				t.Fatalf("ListByModule: %v", err)
			}
			if total != int64(len(want)) || len(got) != len(want) {
				t.Fatalf("total=%d rows=%d, want %d each", total, len(got), len(want))
			}
			seen := moduleVersionListIDs(got)
			for _, id := range want {
				if !seen[id] {
					t.Errorf("global version %s missing from the list", id)
				}
			}
			for _, v := range got {
				if v.Scope != domain.ScopeGlobal || v.TenantID != nil {
					t.Errorf("version %s scope=%s tenant=%v, want global with no tenant", v.ID, v.Scope, v.TenantID)
				}
			}
		})
	}
}

func TestModuleVersionRepo_ListByModule_ClosedPool_ReturnsErrUpstreamUnavailable(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	pool, err := pgcommon.NewPool(context.Background(), pgcommon.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	pool.Close()

	got, total, err := postgres.NewModuleVersionRepo(pool).
		ListByModule(context.Background(), uuid.New(), uuid.New(), 1, 20)

	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Fatalf("err = %v, want ErrUpstreamUnavailable so the API answers 503", err)
	}
	if got != nil || total != 0 {
		t.Errorf("got %v rows, total %d; want nothing on failure", got, total)
	}
}

func moduleVersionListRolePool(
	t *testing.T, dsn string, tenantID uuid.UUID, grants func(role string) []string,
) *pgcommon.Pool {
	t.Helper()
	ctx := context.Background()
	role := "mvlist_" + strings.ReplaceAll(uuid.NewString(), "-", "")

	admin, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer admin.Close()
	statements := append([]string{
		fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD 'restricted' NOBYPASSRLS`, role),
	}, grants(role)...)
	err = admin.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		for _, stmt := range statements {
			if _, err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("%s: %w", stmt, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("prepare role: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgcommon.NewPool(context.Background(), pgcommon.Config{DSN: dsn})
		if err != nil {
			return
		}
		defer cleanup.Close()
		_ = cleanup.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
			_, _ = conn.Exec(ctx, fmt.Sprintf(`DROP OWNED BY %s`, role))
			_, _ = conn.Exec(ctx, fmt.Sprintf(`DROP ROLE IF EXISTS %s`, role))
			return nil
		})
	})

	before, after, _ := strings.Cut(dsn, "@")
	scheme, _, _ := strings.Cut(before, "://")
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{
		DSN: fmt.Sprintf("%s://%s:restricted@%s", scheme, role, after),
		GUCProvider: func(context.Context) (pgdomain.GUCSet, bool) {
			return pgdomain.GUCSet{TenantID: tenantID.String(), UserID: uuid.NewString()}, true
		},
	})
	if err != nil {
		t.Fatalf("open restricted pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestModuleVersionRepo_ListByModule_ListQueryFailsAfterCount_ReturnsUnclassifiedError(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	tenantID := uuid.New()
	mod := newTestModule(t, ctx, superPool, tenantID)
	moduleVersionListInsertArchived(t, superPool, mod.ID, &tenantID, domain.ScopeTenant, 2)
	restricted := moduleVersionListRolePool(t, dsn, tenantID, func(role string) []string {
		return []string{fmt.Sprintf(
			`GRANT SELECT (id, module_id, tenant_id, scope) ON workflow_module_version TO %s`, role)}
	})

	var counted int
	err := restricted.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx,
			`SELECT COUNT(*) FROM workflow_module_version WHERE module_id = $1`, mod.ID).Scan(&counted)
	})
	if err != nil || counted != 2 {
		t.Fatalf("precondition: count = %d, err = %v; want 2, nil", counted, err)
	}

	got, _, err := postgres.NewModuleVersionRepo(restricted).ListByModule(ctx, tenantID, mod.ID, 1, 20)

	if err == nil {
		t.Fatal("err = nil, want the database error")
	}
	if errors.Is(err, domain.ErrUpstreamUnavailable) || errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want an unclassified error that maps to 500", err)
	}
	if got != nil {
		t.Errorf("got %d rows, want nil on failure", len(got))
	}
}

func TestModuleVersionRepo_ListByModule_CountCancelledByTimeout_ReturnsErrUpstreamUnavailable(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	tenantID := uuid.New()
	mod := newTestModule(t, ctx, superPool, tenantID)
	moduleVersionListInsertArchived(t, superPool, mod.ID, &tenantID, domain.ScopeTenant, 1)
	restricted := moduleVersionListRolePool(t, dsn, tenantID, func(role string) []string {
		return []string{
			fmt.Sprintf(`GRANT SELECT ON workflow_module_version TO %s`, role),
			fmt.Sprintf(`ALTER ROLE %s SET statement_timeout = '300ms'`, role),
		}
	})

	var (
		got     []*domain.ModuleVersion
		total   int64
		listErr error
	)
	err := superPool.WithTx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `LOCK TABLE workflow_module_version IN ACCESS EXCLUSIVE MODE`); err != nil {
			return err
		}
		got, total, listErr = postgres.NewModuleVersionRepo(restricted).
			ListByModule(ctx, tenantID, mod.ID, 1, 20)
		return nil
	})
	if err != nil {
		t.Fatalf("hold table lock: %v", err)
	}

	if !errors.Is(listErr, domain.ErrUpstreamUnavailable) {
		t.Fatalf("err = %v, want ErrUpstreamUnavailable", listErr)
	}
	if got != nil || total != 0 {
		t.Errorf("got %v rows, total %d; want nothing on failure", got, total)
	}
}
