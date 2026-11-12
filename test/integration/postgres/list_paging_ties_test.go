//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

const tiedRowCount = 5

func ascendingIDs(n int) []uuid.UUID {
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.New()
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	return ids
}

func tieCreatedAt(t *testing.T, pool *pgcommon.Pool, table string, ids []uuid.UUID) {
	t.Helper()
	at := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, fmt.Sprintf(`UPDATE %s SET created_at = $1 WHERE id = ANY($2)`, table), at, ids)
		return err
	})
	require.NoError(t, err)
}

func TestLists_RowsCreatedAtTheSameInstant_PageInDescendingIDOrderWithoutRepeatsOrGaps(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	cases := []struct {
		name string
		seed func(t *testing.T, tenantID uuid.UUID, ids []uuid.UUID) func(page, limit int) ([]uuid.UUID, int64, error)
	}{
		{
			name: "modules",
			seed: func(t *testing.T, tenantID uuid.UUID, ids []uuid.UUID) func(int, int) ([]uuid.UUID, int64, error) {
				repo := postgres.NewModuleRepo(pool)
				for i, id := range ids {
					m := &domain.Module{
						ID: id, ProcessID: fmt.Sprintf("Process_%d_%s", i, id), TenantID: &tenantID,
						Scope: domain.ScopeTenant, Name: fmt.Sprintf("module %d", i),
					}
					require.NoError(t, repo.Create(ctx, m))
				}
				tieCreatedAt(t, pool, "workflow_module", ids)
				return func(page, limit int) ([]uuid.UUID, int64, error) {
					rows, total, err := repo.List(ctx, tenantID, port.ModuleFilter{Page: page, Limit: limit})
					got := make([]uuid.UUID, len(rows))
					for i, r := range rows {
						got[i] = r.ID
					}
					return got, total, err
				}
			},
		},
		{
			name: "module versions",
			seed: func(t *testing.T, tenantID uuid.UUID, ids []uuid.UUID) func(int, int) ([]uuid.UUID, int64, error) {
				mod := newTestModule(t, ctx, pool, tenantID)
				repo := postgres.NewModuleVersionRepo(pool)
				for i, id := range ids {
					number := int32(i + 1)
					v := &domain.ModuleVersion{
						ID: id, ModuleID: mod.ID, TenantID: &tenantID, Scope: domain.ScopeTenant,
						Status: domain.VersionStatusArchived, VersionNumber: &number,
						BPMNXML: "<bpmn/>", ProcessID: "P1", IsValid: true,
					}
					require.NoError(t, repo.Create(ctx, v))
				}
				tieCreatedAt(t, pool, "workflow_module_version", ids)
				return func(page, limit int) ([]uuid.UUID, int64, error) {
					rows, total, err := repo.ListByModule(ctx, tenantID, mod.ID, page, limit)
					got := make([]uuid.UUID, len(rows))
					for i, r := range rows {
						got[i] = r.ID
					}
					return got, total, err
				}
			},
		},
		{
			name: "workflow versions",
			seed: func(t *testing.T, tenantID uuid.UUID, ids []uuid.UUID) func(int, int) ([]uuid.UUID, int64, error) {
				wf := &domain.Workflow{
					ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
					TemplateKey: "ties-" + uuid.NewString(), Name: "Ties",
				}
				require.NoError(t, postgres.NewWorkflowRepo(pool).Create(ctx, wf))
				repo := postgres.NewWorkflowVersionRepo(pool)
				for i, id := range ids {
					number := int32(i + 1)
					v := newDraftVersion(wf.ID, tenantID)
					v.ID = id
					v.Status = domain.VersionStatusArchived
					v.VersionNumber = &number
					require.NoError(t, repo.Create(ctx, v))
				}
				tieCreatedAt(t, pool, "workflow_version", ids)
				return func(page, limit int) ([]uuid.UUID, int64, error) {
					rows, total, err := repo.ListByWorkflow(ctx, tenantID, wf.ID, page, limit)
					got := make([]uuid.UUID, len(rows))
					for i, r := range rows {
						got[i] = r.ID
					}
					return got, total, err
				}
			},
		},
		{
			name: "workflows",
			seed: func(t *testing.T, tenantID uuid.UUID, ids []uuid.UUID) func(int, int) ([]uuid.UUID, int64, error) {
				repo := postgres.NewWorkflowRepo(pool)
				for i, id := range ids {
					wf := &domain.Workflow{
						ID: id, TenantID: tenantID, CreatedByUserID: uuid.New(),
						TemplateKey: fmt.Sprintf("ties-%d", i), Name: fmt.Sprintf("workflow %d", i),
					}
					require.NoError(t, repo.Create(ctx, wf))
				}
				tieCreatedAt(t, pool, "workflow", ids)
				return func(page, limit int) ([]uuid.UUID, int64, error) {
					rows, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{Page: page, Limit: limit})
					got := make([]uuid.UUID, len(rows))
					for i, r := range rows {
						got[i] = r.ID
					}
					return got, total, err
				}
			},
		},
		{
			name: "starters",
			seed: func(t *testing.T, tenantID uuid.UUID, ids []uuid.UUID) func(int, int) ([]uuid.UUID, int64, error) {
				repo := postgres.NewStarterRepo(pool)
				for i, id := range ids {
					s := &domain.StarterTemplate{
						ID: id, TenantID: &tenantID, Scope: domain.ScopeTenant,
						Name: fmt.Sprintf("starter %d", i), Category: "general", BPMNXML: "<bpmn/>",
					}
					require.NoError(t, repo.Create(ctx, s))
				}
				tieCreatedAt(t, pool, "workflow_template", ids)
				return func(page, limit int) ([]uuid.UUID, int64, error) {
					rows, total, err := repo.List(ctx, tenantID, port.StarterFilter{Page: page, Limit: limit})
					got := make([]uuid.UUID, len(rows))
					for i, r := range rows {
						got[i] = r.ID
					}
					return got, total, err
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID := uuid.New()
			ids := ascendingIDs(tiedRowCount)
			want := slices.Clone(ids)
			slices.Reverse(want)
			list := tc.seed(t, tenantID, ids)

			var paged []uuid.UUID
			for page := 1; page <= 3; page++ {
				got, total, err := list(page, 2)
				require.NoError(t, err)
				require.Equal(t, int64(tiedRowCount), total)
				paged = append(paged, got...)
			}

			require.Equal(t, want, paged)
		})
	}
}
