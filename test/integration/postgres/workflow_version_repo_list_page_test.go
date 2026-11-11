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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestWorkflowVersionRepo_ListByWorkflow_SecondPageOfOne_ReturnsTheSecondNewestVersion(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
		TemplateKey: "sgd-page-" + uuid.NewString(), Name: "Paged",
	}
	require.NoError(t, postgres.NewWorkflowRepo(pool).Create(ctx, wf))

	repo := postgres.NewWorkflowVersionRepo(pool)
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]uuid.UUID, 0, 3)
	for n := int32(1); n <= 3; n++ {
		number := n
		v := newDraftVersion(wf.ID, tenantID)
		v.Status = domain.VersionStatusArchived
		v.VersionNumber = &number
		require.NoError(t, repo.Create(ctx, v))
		ids = append(ids, v.ID)
	}
	setCreatedAt := func(id uuid.UUID, at time.Time) {
		err := pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
			_, err := conn.Exec(ctx, `UPDATE workflow_version SET created_at = $2 WHERE id = $1`, id, at)
			return err
		})
		require.NoError(t, err)
	}
	setCreatedAt(ids[0], base.Add(1*time.Hour))
	setCreatedAt(ids[1], base.Add(3*time.Hour))
	setCreatedAt(ids[2], base.Add(2*time.Hour))

	got, total, err := repo.ListByWorkflow(ctx, tenantID, wf.ID, 2, 1)

	require.NoError(t, err)
	assert.Equal(t, int64(3), total, "total counts every version, not the page")
	require.Len(t, got, 1)
	assert.Equal(t, ids[2], got[0].ID, "newest is version 2, the second newest is version 3")
	require.NotNil(t, got[0].VersionNumber)
	assert.Equal(t, int32(3), *got[0].VersionNumber)
}
