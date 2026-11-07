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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func sgDInsertArchivedVersions(
	t *testing.T, pool *pgcommon.Pool, moduleID uuid.UUID, tenantID *uuid.UUID, scope domain.CatalogScope, firstNumber, count int,
) []uuid.UUID {
	t.Helper()
	repo := postgres.NewModuleVersionRepo(pool)
	ids := make([]uuid.UUID, 0, count)
	for i := 0; i < count; i++ {
		number := int32(firstNumber + i)
		v := &domain.ModuleVersion{
			ID: uuid.New(), ModuleID: moduleID, TenantID: tenantID, Scope: scope,
			Status: domain.VersionStatusArchived, VersionNumber: &number, BPMNXML: "<bpmn/>", ProcessID: "P1", IsValid: true,
		}
		require.NoError(t, repo.Create(context.Background(), v))
		ids = append(ids, v.ID)
	}
	return ids
}

func sgDSetModuleVersionCreatedAt(t *testing.T, pool *pgcommon.Pool, id uuid.UUID, at time.Time) {
	t.Helper()
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, `UPDATE workflow_module_version SET created_at = $2 WHERE id = $1`, id, at)
		return err
	})
	require.NoError(t, err)
}

func sgDVersionIDs(versions []*domain.ModuleVersion) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(versions))
	for _, v := range versions {
		ids = append(ids, v.ID)
	}
	return ids
}

func TestModuleVersionRepo_ListByModule_PagesNewestFirstWithTheTotalOfAllRows(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenantID := uuid.New()
	mod := newTestModule(t, ctx, pool, tenantID)
	ids := sgDInsertArchivedVersions(t, pool, mod.ID, &tenantID, domain.ScopeTenant, 1, 7)
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	shuffled := []int{3, 0, 6, 1, 5, 2, 4}
	for hours, idx := range shuffled {
		sgDSetModuleVersionCreatedAt(t, pool, ids[idx], base.Add(time.Duration(hours)*time.Hour))
	}
	newestFirst := make([]uuid.UUID, 0, len(ids))
	for i := len(shuffled) - 1; i >= 0; i-- {
		newestFirst = append(newestFirst, ids[shuffled[i]])
	}
	repo := postgres.NewModuleVersionRepo(pool)

	cases := []struct {
		name  string
		page  int
		limit int
		want  []uuid.UUID
	}{
		{name: "first page of five", page: 1, limit: 5, want: newestFirst[:5]},
		{name: "second page of five holds the two oldest", page: 2, limit: 5, want: newestFirst[5:]},
		{name: "everything on one page, newest first", page: 1, limit: 20, want: newestFirst},
		{name: "page past the end", page: 3, limit: 5, want: []uuid.UUID{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, total, err := repo.ListByModule(ctx, tenantID, mod.ID, tc.page, tc.limit)

			require.NoError(t, err)
			assert.Equal(t, int64(7), total, "total counts every row, not the page")
			assert.Equal(t, tc.want, sgDVersionIDs(got))
		})
	}
}
