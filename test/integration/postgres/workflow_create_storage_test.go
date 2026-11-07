//go:build integration

package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestWorkflowRepo_Create_StoresTheDescriptionAsNullOnlyWhenItIsEmpty(t *testing.T) {
	cases := []struct {
		name        string
		description string
		wantNull    bool
	}{
		{name: "an omitted description is NULL", description: "", wantNull: true},
		{name: "a description of 2000 characters is stored as given", description: strings.Repeat("d", 2000), wantNull: false},
	}
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			wf := &domain.Workflow{
				ID: uuid.New(), TenantID: uuid.New(), CreatedByUserID: uuid.New(),
				TemplateKey: "description-null", Name: "Description", Description: tc.description,
			}
			require.NoError(t, repo.Create(ctx, wf))

			var isNull bool
			var stored *string
			err := pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
				return conn.QueryRow(ctx,
					`SELECT description IS NULL, description FROM workflow WHERE id = $1`, wf.ID).Scan(&isNull, &stored)
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantNull, isNull)
			if !tc.wantNull {
				require.NotNil(t, stored)
				assert.Equal(t, tc.description, *stored)
			}
			got, err := repo.GetByID(ctx, wf.TenantID, wf.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.description, got.Description, "reading back a NULL description gives the empty string")
		})
	}
}

func TestWorkflowRepo_Create_StoresKeyAndNameVerbatim(t *testing.T) {
	cases := []struct {
		name   string
		key    string
		wfName string
	}{
		{name: "SQL metacharacters", key: "'; DROP TABLE workflow;--", wfName: `Robert'); DROP TABLE workflow_version;--`},
		{name: "markup", key: "<img src=x onerror=alert(1)>", wfName: "<script>alert('x')</script>"},
		{name: "255 multibyte characters, over 255 bytes", key: strings.Repeat("é", 255), wfName: strings.Repeat("é", 255)},
	}
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			tenantID := uuid.New()
			wf := &domain.Workflow{
				ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
				TemplateKey: tc.key, Name: tc.wfName,
			}
			require.NoError(t, repo.Create(ctx, wf))

			got, err := repo.GetByTemplateKey(ctx, tenantID, tc.key)
			require.NoError(t, err)
			assert.Equal(t, wf.ID, got.ID)
			assert.Equal(t, tc.key, got.TemplateKey)
			assert.Equal(t, tc.wfName, got.Name)

		})
	}
}

func TestWorkflowRepo_Create_TemplateKeyIsUniquePerTenantNotAcrossTenants(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()
	tenantA, tenantB := uuid.New(), uuid.New()
	newWF := func(tenantID uuid.UUID) *domain.Workflow {
		return &domain.Workflow{
			ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
			TemplateKey: "shared-key", Name: "Shared",
		}
	}
	inA := newWF(tenantA)
	require.NoError(t, repo.Create(ctx, inA))

	inB := newWF(tenantB)
	require.NoError(t, repo.Create(ctx, inB), "the same key under another tenant is accepted")
	assert.ErrorIs(t, repo.Create(ctx, newWF(tenantA)), domain.ErrDuplicateTemplateKey, "the same key in the same tenant is still refused")

	gotA, err := repo.GetByTemplateKey(ctx, tenantA, "shared-key")
	require.NoError(t, err)
	assert.Equal(t, inA.ID, gotA.ID)
	gotB, err := repo.GetByTemplateKey(ctx, tenantB, "shared-key")
	require.NoError(t, err)
	assert.Equal(t, inB.ID, gotB.ID)
}
