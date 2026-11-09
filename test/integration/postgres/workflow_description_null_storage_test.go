//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestWorkflowRepo_Create_EmptyDescription_IsStoredAsNullAndNonEmptyAsText(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	cases := []struct {
		name        string
		description string
		want        *string
	}{
		{name: "empty description is NULL", description: "", want: nil},
		{name: "non-empty description is kept as text", description: "approves expenses", want: descriptionNullStoragePtr("approves expenses")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID := uuid.New()
			wf := &domain.Workflow{
				ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
				TemplateKey: "descr-" + uuid.NewString(), Name: "X", Description: tc.description,
			}
			require.NoError(t, repo.Create(ctx, wf))

			var stored *string
			require.NoError(t, pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
				return conn.QueryRow(ctx, `SELECT description FROM workflow WHERE id = $1`, wf.ID).Scan(&stored)
			}))
			assert.Equal(t, tc.want, stored, "description column")

			got, err := repo.GetByID(ctx, tenantID, wf.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.description, got.Description, "read back")
		})
	}
}

func descriptionNullStoragePtr[T any](v T) *T { return &v }
