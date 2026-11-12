//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"math"
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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

var w4aModuleEpoch = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func w4aCreateModule(t *testing.T, pool *pgcommon.Pool, tenantID *uuid.UUID, name string, createdAt time.Time) *domain.Module {
	t.Helper()
	scope := domain.ScopeTenant
	if tenantID == nil {
		scope = domain.ScopeGlobal
	}
	m := &domain.Module{ID: uuid.New(), TenantID: tenantID, Scope: scope, Name: name, ProcessID: "Process_" + uuid.NewString()}
	require.NoError(t, pgadapter.NewModuleRepo(pool).Create(context.Background(), m))
	require.NoError(t, pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, `UPDATE workflow_module SET created_at = $2 WHERE id = $1`, m.ID, createdAt)
		return err
	}))
	return m
}

func w4aModuleNames(modules []*domain.Module) []string {
	names := make([]string, len(modules))
	for i, m := range modules {
		names[i] = m.Name
	}
	return names
}

func TestModuleRepo_List_PagingOutOfRange_FallsBackToTheDefaultsAndNewestFirst(t *testing.T) {
	pool := newTestPool(t)
	repo := pgadapter.NewModuleRepo(pool)
	ctx := context.Background()
	tenantID := uuid.New()
	const modules = 25
	var newestFirst []string
	for i := 0; i < modules; i++ {
		m := w4aCreateModule(t, pool, &tenantID, fmt.Sprintf("M%02d", i), w4aModuleEpoch.Add(time.Duration(i)*time.Minute))
		newestFirst = append([]string{m.Name}, newestFirst...)
	}

	cases := []struct {
		name      string
		page      int
		limit     int
		wantNames []string
	}{
		{"page and limit zero give page one of twenty", 0, 0, newestFirst[:20]},
		{"negative page and negative limit give page one of twenty", -3, -7, newestFirst[:20]},
		{"page below one keeps the requested limit", 0, 5, newestFirst[:5]},
		{"limit below one on page two uses twenty per page", 2, 0, newestFirst[20:]},
		{"second page of ten", 2, 10, newestFirst[10:20]},
		{"a page far past the end is empty", 1_000_000, 20, []string{}},
		{"a page beyond the 32-bit offset range is empty", math.MaxInt32, 20, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, total, err := repo.List(ctx, tenantID, port.ModuleFilter{Page: tc.page, Limit: tc.limit})

			require.NoError(t, err)
			assert.EqualValues(t, modules, total, "the total is the real count whatever page was asked for")
			assert.Equal(t, tc.wantNames, w4aModuleNames(got))
		})
	}
}

func TestModuleRepo_List_ShowsGlobalPlusOwnModulesAndNeverAnotherTenantsOnes(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	superRepo := pgadapter.NewModuleRepo(superPool)
	tenantA, tenantB, userA := uuid.New(), uuid.New(), uuid.New()
	w4aCreateModule(t, superPool, nil, "global-one", w4aModuleEpoch.Add(time.Hour))
	w4aCreateModule(t, superPool, nil, "global-two", w4aModuleEpoch.Add(2*time.Hour))
	w4aCreateModule(t, superPool, &tenantA, "a-own", w4aModuleEpoch.Add(3*time.Hour))
	w4aCreateModule(t, superPool, &tenantB, "b-private", w4aModuleEpoch.Add(4*time.Hour))
	tenantAPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA)
	global, tenant := domain.ScopeGlobal, domain.ScopeTenant

	cases := []struct {
		name      string
		repo      *pgadapter.ModuleRepo
		caller    uuid.UUID
		filter    port.ModuleFilter
		wantNames []string
	}{
		{"unfiltered list is the globals and the caller's own", superRepo, tenantA, port.ModuleFilter{}, []string{"a-own", "global-two", "global-one"}},
		{"scope global narrows to the globals", superRepo, tenantA, port.ModuleFilter{Scope: &global}, []string{"global-two", "global-one"}},
		{"scope tenant narrows to the caller's own", superRepo, tenantA, port.ModuleFilter{Scope: &tenant}, []string{"a-own"}},
		{"another tenant's list holds the globals and its own only", superRepo, tenantB, port.ModuleFilter{}, []string{"b-private", "global-two", "global-one"}},
		{"a tenant with no modules sees only the globals", superRepo, uuid.New(), port.ModuleFilter{}, []string{"global-two", "global-one"}},
		{"scope tenant for a tenant with no modules is empty", superRepo, uuid.New(), port.ModuleFilter{Scope: &tenant}, []string{}},
		{"row-level security hides the other tenant's modules even when the query names that tenant", pgadapter.NewModuleRepo(tenantAPool), tenantB, port.ModuleFilter{}, []string{"global-two", "global-one"}},
		{"row-level security with scope tenant and the other tenant's id is empty", pgadapter.NewModuleRepo(tenantAPool), tenantB, port.ModuleFilter{Scope: &tenant}, []string{}},
		{"the caller's own list through row-level security is unchanged", pgadapter.NewModuleRepo(tenantAPool), tenantA, port.ModuleFilter{}, []string{"a-own", "global-two", "global-one"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, total, err := tc.repo.List(context.Background(), tc.caller, tc.filter)

			require.NoError(t, err)
			assert.EqualValues(t, len(tc.wantNames), total)
			assert.Equal(t, tc.wantNames, w4aModuleNames(got))
		})
	}
}
